// SPDX-License-Identifier: GPL-2.0-only

package colour

import "fmt"

// SliderTables are the three per-channel look-up tables that implement the
// colour sliders. Table k maps an input channel value (R, G, B for k = 0, 1, 2)
// to the adjusted value; each has (255<<(bits-8))+1 entries.
type SliderTables [3][]uint16

// sliderValue converts a 30-bit fixed-point result to an output value with
// `bits` bits: round((x>>6) * 255 / 2^(30-6-(bits-8)...)), exactly as the
// original: ((x>>6)*255 + scale/2) / scale with scale = 2^30 >> (bits-2).
func sliderValue(x uint32, bits int) uint16 {
	scale := uint64(fixedOne) >> uint(bits-2)
	return uint16((uint64(x>>6)*0xff + scale>>1) / scale)
}

// unitFraction returns i/max as a 30-bit fixed-point value; the top of the
// range is exactly 1.0.
func unitFraction(i, max uint32) uint32 {
	if i < max {
		return fixRatio(i, max) >> 2
	}
	return fixedOne
}

// NewSliderTables builds the colour slider tables for inputs of `bits` bits
// (8 for the print path): T_c[i] = round(255 * density(contrast(balance_c(i/255)))).
// All arithmetic is the original's 30-bit fixed point, so entries can differ
// by one from an IEEE double evaluation.
func NewSliderTables(a Adjust, bits int) (SliderTables, error) {
	var t SliderTables
	if bits < 8 || bits > 16 {
		return t, fmt.Errorf("colour: unsupported table width %d bits", bits)
	}
	if err := a.Validate(); err != nil {
		return t, err
	}
	max := uint32(0xff) << uint(bits-8)
	bal := a.balances()
	for k := range t {
		t[k] = make([]uint16, max+1)
		for i := uint32(0); i <= max; i++ {
			x := unitFraction(i, max)
			x = balanceCurve(x, bal[k])
			x = contrastCurve(x, a.Contrast)
			x = densityCurve(x, a.Density)
			t[k][i] = sliderValue(x, bits)
		}
	}
	return t, nil
}

// GrayTables are the tables of a grayscale job. Balance holds the per-channel
// balance-only tables applied to the RGB input (bitsIn wide); Tone is the
// contrast+density curve applied to the luma (bitsOut wide).
type GrayTables struct {
	Balance SliderTables
	Tone    []uint16
}

// NewGrayTables builds the grayscale slider tables: balance-only tables on the
// input bits and a contrast+density table on the converted gray value
// (bitsIn 8, bitsOut 12 in the print path).
func NewGrayTables(a Adjust, bitsIn, bitsOut int) (GrayTables, error) {
	var g GrayTables
	if bitsIn < 8 || bitsIn > 16 || bitsOut < bitsIn || bitsOut > 16 {
		return g, fmt.Errorf("colour: unsupported gray table widths %d/%d", bitsIn, bitsOut)
	}
	if err := a.Validate(); err != nil {
		return g, err
	}
	maxIn := uint32(0xff) << uint(bitsIn-8)
	bal := a.balances()
	for k := range g.Balance {
		g.Balance[k] = make([]uint16, maxIn+1)
		for i := uint32(0); i <= maxIn; i++ {
			g.Balance[k][i] = sliderValue(balanceCurve(unitFraction(i, maxIn), bal[k]), bitsIn)
		}
	}
	maxOut := uint32(0xff) << uint(bitsOut-8)
	g.Tone = make([]uint16, maxOut+1)
	for j := uint32(0); j <= maxOut; j++ {
		x := unitFraction(j, maxOut)
		x = contrastCurve(x, a.Contrast)
		x = densityCurve(x, a.Density)
		g.Tone[j] = sliderValue(x, bitsOut)
	}
	return g, nil
}

// Luma converts an RGB triple of bitsIn-bit channels to one gray value of
// bitsOut bits with the printer's weights (77, 151, 28)/256, rounded to
// nearest.
func Luma(r, g, b uint16, bitsIn, bitsOut int) uint16 {
	v := (int32(r)*0x4d + int32(g)*0x97 + int32(b)*0x1c) << uint(bitsOut-bitsIn)
	t := v + 0x80
	if t < 0 {
		t = v + 0x17f
	}
	return uint16(uint32(t) >> 8)
}
