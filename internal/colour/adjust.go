// SPDX-License-Identifier: GPL-2.0-only

package colour

import (
	"errors"
	"fmt"
)

// Adjust holds the five user colour sliders of the print dialog. Each runs
// from -50 to +50; 0 is neutral. (The MP280 does not expose the black balance
// or tone sliders; the job layer rejects them.)
//
// The sliders are applied to the 8-bit RGB input before any look-up table:
// the cyan balance acts on red, magenta on green and yellow on blue.
type Adjust struct {
	BalanceCyan    int // acts on the red channel
	BalanceMagenta int // acts on the green channel
	BalanceYellow  int // acts on the blue channel
	Density        int // overall lightness/darkness of the print
	Contrast       int // S-curve around mid-grey
}

// ErrSliderRange is returned for a slider outside -50..50.
var ErrSliderRange = errors.New("colour: slider outside -50..50")

// IsZero reports whether every slider is neutral. The printer library then
// installs no converter at all, so the pipeline must skip the tables too.
func (a Adjust) IsZero() bool { return a == Adjust{} }

// Validate checks that all sliders are inside -50..50.
func (a Adjust) Validate() error {
	for name, v := range map[string]int{
		"balance_c": a.BalanceCyan, "balance_m": a.BalanceMagenta, "balance_y": a.BalanceYellow,
		"density": a.Density, "contrast": a.Contrast,
	} {
		if v < -50 || v > 50 {
			return fmt.Errorf("%w: %s=%d", ErrSliderRange, name, v)
		}
	}
	return nil
}

func (a Adjust) balances() [3]int {
	return [3]int{a.BalanceCyan, a.BalanceMagenta, a.BalanceYellow}
}

// balanceCurve is the colour balance stage on a 30-bit fixed-point value:
// x^(1+b/100) for b > 0, x^(100/(100-b)) for b < 0.
func balanceCurve(x uint32, b int) uint32 {
	if b == 0 {
		return x
	}
	if b > 0 {
		e := fixRatio(uint32(b), 100)
		return fixPow(x, 30, (e>>2)+fixedOne, 30)
	}
	e := fixRatio(100, uint32(100-b))
	return fixPow(x, 30, e>>2, 30)
}

// sliderExponent returns the exponent shared by the contrast and density
// curves as a 30-bit fixed-point number: 2.0 for v >= 50, 50/(50-v) for
// v <= 0 and 1+v/50 in between.
func sliderExponent(v int) uint32 {
	e := uint32(0x80000000)
	if v < 50 {
		if v < 1 {
			e = fixRatio(50, uint32(50-v)) >> 2
		} else {
			e = (fixRatio(uint32(v), 50) >> 2) + fixedOne
		}
	}
	return e
}

// contrastCurve is the S-shaped contrast stage on a 30-bit value.
func contrastCurve(x uint32, c int) uint32 {
	if c == 0 {
		return x
	}
	e := sliderExponent(c)
	if x < 0x20000001 {
		return fixPow(x*2, 30, e, 30) >> 1
	}
	return fixedOne - fixPow((fixedOne-x)*2, 30, e, 30)>>1
}

// normalised is a value split into an odd-bit mantissa and exponent, the
// representation the original uses to feed fixMul. Only densityCurve needs it.
type normalised struct {
	mantissa uint64
	bits     int32 // significant bits kept in the mantissa
	exp      int32 // binary exponent relative to a 30-bit fraction
}

// densityCurve is the density stage on a 30-bit value. For d >= -25 it is a
// power curve; for d <= -26 it blends x^(2/3) towards white by t=(-25-d)/100,
// computed with the original's mantissa/exponent multiply.
func densityCurve(x uint32, d int) uint32 {
	if d == 0 {
		return x
	}
	if d > -26 {
		return fixPow(x, 30, sliderExponent(d), 30)
	}
	t := fixRatio(uint32(-25-d), 100)
	root := fixRatio(50, 75)
	root = fixPow(x, 30, root>>2, 30) // x^(2/3)
	scale := fixedOne - t>>2          // (1 - t)

	a := normalise(root)
	b := normalise(scale)
	r := uint32(fixMul(uint32(a.mantissa), uint32(a.bits), uint32(b.mantissa), uint32(b.bits)))
	n1 := b.bits - 0x20 + a.bits
	n2 := b.exp + a.exp
	if n1 > 0 {
		n2 = b.exp + a.exp + n1
	}
	if n2 < -0x1e {
		r >>= (uint32(-int32(uint8(n2))) - 0x1e) & 0x1f
	} else {
		r <<= (uint32(uint8(n2)) + 0x1e) & 0x1f
	}
	return r + t>>2
}

// normalise reproduces the bit-scan prologue of the original density code:
// it strips trailing zeros from v, then locates the top set bit by probing
// downwards. The odd two-bits-per-step probing is kept because the final
// bit count depends on it for values with an even number of leading zeros.
func normalise(v uint32) normalised {
	var n normalised
	m := uint64(v)
	if v == 0 {
		return n
	}
	n.exp = -0x1e
	bits := int32(0x20)
	if v&1 == 0 {
		bits = 0x20
		for {
			m >>= 1
			bits--
			if m&1 != 0 {
				break
			}
		}
		n.exp = 2 - bits
		if int32(m) == 0 || bits == 0 {
			return normalised{}
		}
	}
	probe := uint64(1) << ((uint32(uint8(bits)) - 1) & 0x3f)
	mask := uint32(probe)
	n.bits = bits
	for uint32(m)&mask == 0 {
		probe = (probe >> 1) & 0x7fffffff
		bits--
		mask = uint32(probe)
		n.bits = bits
	}
	n.mantissa = m
	return n
}
