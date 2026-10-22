// SPDX-License-Identifier: GPL-2.0-only

package colour

import (
	"fmt"
	"math/bits"
)

// ToneCurve is the per-ink tone reproduction curve ("TCS") applied to the
// back-LUT output just before halftoning. It linearises the ink response:
// 257 knots over the input range, each a 16-bit ink amount where 65280
// (0xff00) means full coverage.
type ToneCurve struct {
	knots [257]uint16
}

// NewToneCurve builds a curve from the 256 stored samples of a tone-curve
// table. max is the largest sample value of the table (4080 = 12-bit curves
// for the MP280); samples are scaled so that max becomes 65280, and the last
// knot repeats the 255th so interpolation at full scale stays in range.
func NewToneCurve(samples [256]uint16, max int) (ToneCurve, error) {
	var c ToneCurve
	if max != 0xff0 && max != 0xff00 {
		return c, fmt.Errorf("colour: unsupported tone curve maximum %d", max)
	}
	q := max / 255
	if q&(q-1) != 0 {
		return c, fmt.Errorf("colour: tone curve maximum %d is not 255 << n", max)
	}
	shift := uint(8 - bits.TrailingZeros(uint(q)))
	for i, s := range samples {
		c.knots[i] = s << shift
	}
	c.knots[256] = c.knots[255]
	return c, nil
}

// Apply maps a back-LUT output of the given bit width (14 on the MP280, i.e.
// 0..16320) to the 16-bit ink amount (0..65280) by linear interpolation
// between knots.
func (c *ToneCurve) Apply(v uint16, width int) uint16 {
	sh := uint(width - 8)
	i := uint(v) >> sh
	f := uint32(v) & (1<<sh - 1)
	cur, nxt := uint32(c.knots[i]), uint32(c.knots[i+1])
	delta := int32(f*(nxt-cur)) >> sh
	return uint16(cur + uint32(int32(int16(delta))))
}
