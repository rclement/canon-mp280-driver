// SPDX-License-Identifier: GPL-2.0-only

package colour

import (
	"canon-mp280-driver/internal/printdb"
	"errors"
	"fmt"
)

// Gamma codes of the colour library.
const (
	GammaDefault = 0
	Gamma10      = 10
	Gamma14      = 14
	Gamma18      = 18 // job default
	Gamma22      = 22
)

// NodeSetForGamma returns which of the five node lists of a front LUT a gamma
// code selects. Gamma is the only thing that changes the front LUT: the same
// grid values are re-interpreted at different node positions.
func NodeSetForGamma(code int) int {
	switch code {
	case Gamma18:
		return 3
	case Gamma10:
		return 1
	case Gamma22:
		return 4
	case 32:
		return 0
	}
	return 2
}

// Converter turns 8-bit RGB pixels into 16-bit ink amounts (0..65280) for the
// halftoner.
type Converter struct {
	mode    Mode
	gray    bool
	adjust  bool
	sliders SliderTables // colour jobs with sliders
	front   *lutEngine
	back    *lutEngine
	tone    []ToneCurve // one per ink of the mode

	// grayTab maps a gray byte to a 12-bit gray; grayInk maps that to inks.
	grayTab [256]uint16
	grayInk [][]uint16 // [ink][0..4080]

	haveLast bool
	lastKey  uint32
	lastInk  [4]uint16
}

// Config describes a converter.
type Config struct {
	Mode   Mode
	Gray   bool // grayscale session (--grayscale)
	Gamma  int  // BJ gamma code; 0 means the library default (node set 0)
	Adjust Adjust
}

// ErrNoBackLUT is returned for a colour session on a mode without a back LUT.
var ErrNoBackLUT = errors.New("colour: mode has no back LUT")

// NewConverter prepares the tables for a job. A gray session precomputes the
// 4081-entry gray->ink table so that conversion is two lookups per pixel.
func NewConverter(db *printdb.Table, cfg Config) (*Converter, error) {
	if err := cfg.Adjust.Validate(); err != nil {
		return nil, err
	}
	m := cfg.Mode
	c := &Converter{mode: m, gray: cfg.Gray, adjust: !cfg.Adjust.IsZero()}
	gamma := cfg.Gamma
	if gamma == 0 {
		gamma = 32
	}
	nodeSet := NodeSetForGamma(gamma)
	ids := make([]int, len(m.Inks))
	for i, in := range m.Inks {
		ids[i] = in.Ink
	}
	for _, id := range ids {
		samples, max, err := toneSamples(db, m.ToneSub, id)
		if err != nil {
			return nil, fmt.Errorf("tone curve %d/ink %d: %w", m.ToneSub, id, err)
		}
		tc, err := NewToneCurve(samples, max)
		if err != nil {
			return nil, err
		}
		c.tone = append(c.tone, tc)
	}
	frontIn, frontOut, backIn := 8, 12, 12
	if cfg.Gray {
		frontIn, frontOut, backIn = 12, 13, 13
	}
	var err error
	if c.front, err = frontLUT(db, m.FrontLUT, nodeSet, frontIn, frontOut); err != nil {
		return nil, fmt.Errorf("front LUT %d: %w", m.FrontLUT, err)
	}
	if m.BackLUT != 0 {
		if c.back, err = backLUT(db, m.BackLUT, ids, backIn, m.BackBits); err != nil {
			return nil, fmt.Errorf("back LUT %d: %w", m.BackLUT, err)
		}
	} else if !cfg.Gray {
		return nil, ErrNoBackLUT
	}
	if cfg.Gray {
		return c, c.buildGray(cfg.Adjust)
	}
	if c.adjust {
		if c.sliders, err = NewSliderTables(cfg.Adjust, 8); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// buildGray computes the gray byte -> 12-bit gray table and the gray -> ink
// table. The gray ramp (j, j, j) goes through the front LUT, then the back LUT
// (photo media) or, for the K-only plain-paper modes, a luma conversion that
// inverts it to a black amount.
func (c *Converter) buildGray(a Adjust) error {
	var tone []uint16
	var bal SliderTables
	if c.adjust {
		g, err := NewGrayTables(a, 8, 12)
		if err != nil {
			return err
		}
		bal, tone = g.Balance, g.Tone
	}
	for g := 0; g < 256; g++ {
		if c.adjust {
			c.grayTab[g] = tone[Luma(bal[0][g], bal[1][g], bal[2][g], 8, 12)]
		} else {
			c.grayTab[g] = uint16(16 * g)
		}
	}
	n := len(c.mode.Inks)
	c.grayInk = make([][]uint16, n)
	for i := range c.grayInk {
		c.grayInk[i] = make([]uint16, 4081)
	}
	var f [3]uint16
	var b [4]uint16
	for j := 0; j <= 4080; j++ {
		jj := uint16(j)
		c.front.Apply([3]uint16{jj, jj, jj}, f[:])
		if c.back != nil {
			c.back.Apply(f, b[:n])
		} else {
			sum := (77*uint32(f[0]) + 151*uint32(f[1]) + 28*uint32(f[2])) << 1
			b[0] = uint16(16320 - ((sum + 0x80) >> 8))
		}
		for i := 0; i < n; i++ {
			c.grayInk[i][j] = c.tone[i].Apply(b[i], c.mode.BackBits)
		}
	}
	return nil
}

// Inks returns the number of ink channels produced per pixel.
func (c *Converter) Inks() int { return len(c.mode.Inks) }

// Pixel converts one RGB pixel; out must have room for Inks() values.
func (c *Converter) Pixel(r, g, b uint8, out []uint16) {
	if c.gray {
		j := c.grayTab[(5*int(r)+9*int(g)+2*int(b))>>4]
		for i := range c.grayInk {
			out[i] = c.grayInk[i][j]
		}
		return
	}
	key := uint32(r) | uint32(g)<<8 | uint32(b)<<16
	if !c.haveLast || key != c.lastKey {
		in := [3]uint16{uint16(r), uint16(g), uint16(b)}
		if c.adjust {
			in = [3]uint16{c.sliders[0][r], c.sliders[1][g], c.sliders[2][b]}
		}
		var f [3]uint16
		var bk [4]uint16
		c.front.Apply(in, f[:])
		c.back.Apply(f, bk[:len(c.tone)])
		for i := range c.tone {
			c.lastInk[i] = c.tone[i].Apply(bk[i], c.mode.BackBits)
		}
		c.lastKey, c.haveLast = key, true
	}
	copy(out, c.lastInk[:len(c.tone)])
}

// Row converts n pixels starting at pixel x0 of an RGB row (3 bytes per pixel,
// indexed by absolute pixel) into ink[i][x]. Other pixels are left untouched.
func (c *Converter) Row(rgb []byte, x0, n int, ink [][]uint16) {
	var px [4]uint16
	for x := x0; x < x0+n; x++ {
		c.Pixel(rgb[3*x], rgb[3*x+1], rgb[3*x+2], px[:])
		for i := range c.tone {
			ink[i][x] = px[i]
		}
	}
}
