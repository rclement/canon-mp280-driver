// SPDX-License-Identifier: GPL-2.0-only

package colour

// Fixed-point helpers of the colour adjustment.
//
// The original driver computes the balance / contrast / density curves in 30-bit fixed
// point (1.0 == 1<<30) with its own integer power routine. The results differ
// from an IEEE double evaluation in roughly 1 of 11,000 table entries, so the
// routines keep their exact intermediate truncations, including the
// odd normalisation loops and the (x & 31) shift-count masking that the
// original implementation performs. Do not "clean up" the arithmetic: the
// tables must match the printer driver bit for bit.

// fixedOne is 1.0 in the 30-bit fixed-point format used by the slider curves.
const fixedOne = 0x40000000

// fixRatio returns the 32-bit binary fraction a/b, i.e. floor(a*2^32/b) with
// the remainder step of a binary long division. If a >= b, a is first reduced
// modulo b.
func fixRatio(a, b uint32) uint32 {
	if b < a {
		a %= b
	}
	var q uint32
	for n := 0; n < 32; n++ {
		t := a << 1
		q <<= 1
		carry := int32(a) < 0
		a = t
		if b < t || carry {
			a = t - b
			q |= 1
		}
	}
	return q
}

// fixSqrt16 returns the square root of p with 16 fractional bits
// (floor(sqrt(p)*65536) for 32-bit p).
func fixSqrt16(p uint32) uint32 {
	var r3, r5 uint32
	mask := uint32(0xc0000000)
	shift := 0x1e
	for {
		var v uint32
		if p&mask != 0 {
			v = (p & mask) >> uint(shift&0x1f)
		}
		r3 *= 2
		r5 = v + r5*4
		if r3+1 <= r5 {
			r5 -= r3 + 1
			r3 += 2
		}
		shift -= 2
		mask >>= 2
		if shift == -2 {
			break
		}
	}
	for n := 0; n < 0xe; n++ {
		r3 *= 2
		r5 *= 4
		if r3+1 <= r5 {
			r5 -= r3 + 1
			r3 += 2
		}
	}
	if r5&0xc0000000 == 0 {
		r3 *= 2
		r5 *= 4
		if r3+1 <= r5 {
			r5 -= r3 + 1
			r3 += 2
		}
	} else {
		r5 -= r3 >> 1
		if r5&0xc0000000 != 0 {
			return 0
		}
		r5 = r5*4 - 1
		r3 = r3*2 + 2
	}
	if r5&0xc0000000 == 0 {
		if int32(r3) >= 0 {
			r3 *= 2
			if r3+1 <= r5*4 {
				r3 += 2
			}
			r3 >>= 1
		}
		return r3
	}
	if int32(r5) >= 0 && int32(r3) < 0 {
		r3 *= 2
		if r3+1 <= r5*4 {
			r3 += 2
		}
		return (r3 >> 1) | 0x80000000
	}
	return r3 + 1
}

// fixMul multiplies two floating mantissas p1 (with p2 significant bits) and
// p3 (with p4 bits) keeping the top 32 bits of the product, splitting the
// operands to avoid overflow. It returns -1 when a bit count exceeds 32.
func fixMul(p1, p2, p3, p4 uint32) int32 {
	if p2 >= 0x21 || p4 >= 0x21 {
		return -1
	}
	sum := int32(p2 + p4)
	over := sum - 0x20
	if over <= 0 {
		return int32(p1 * p3)
	}
	l := int64(int32(p4*uint32(over)+uint32(sum/2))) / int64(sum)
	u6 := uint32(l)
	u5 := uint32(over) - u6
	b2, b3 := uint8(l), uint8(u5)
	u8 := p1 >> (b3 & 0x1f)
	u9 := p3 >> (b2 & 0x1f)
	p1 = ((1 << (b3 & 0x1f)) - 1) & p1
	p3 = ((1 << (b2 & 0x1f)) - 1) & p3
	var r int32
	if u6 < u5 {
		u5 = u8 * p3
		u6 = ((p1 * p3 >> (b2 & 0x1f)) + p1*u9) >> (uint8(b3-b2) & 0x1f)
		if ^u6 < u5 {
			r = int32((uint32(1) << ((0x20 - uint32(b2)) & 0x1f)) + ((u6 + u5) >> (b2 & 0x1f)))
		} else {
			r = int32((u6 + u5) >> (b2 & 0x1f))
		}
	} else {
		u6 = ((p1 * p3 >> (b3 & 0x1f)) + p3*u8) >> (uint8(b2-b3) & 0x1f)
		if ^u6 < p1*u9 {
			r = int32(uint32(1) << ((0x20 - uint32(b3)) & 0x1f))
		}
		r += int32((u6 + p1*u9) >> (b3 & 0x1f))
	}
	return r + int32(u8*u9)
}

// powState is the shared state of fixPow; keeping it in one struct lets the
// goto-based control flow of the original be expressed as a small state
// machine without changing the order of the arithmetic.
type powState int

const (
	powShiftDown powState = iota // strip trailing zero bits of the base mantissa
	powNormBase                  // normalise the base mantissa to its top bit
	powCheckAcc                  // accumulator empty? enter the combine loop
	powOuter                     // restart accumulation
	powInner                     // square the base, multiply into the accumulator if the exponent bit is set
	powAccShift                  // strip trailing zero bits of the accumulator
	powDone                      // integer part finished
)

// fixPow computes x^y in fixed point: x = xm / 2^xfrac, y = ym / 2^yfrac,
// result with xfrac fraction bits. The integer part of y is applied by square
// and multiply on (mantissa, binary exponent) pairs, the fractional bits of y
// by repeated square roots.
func fixPow(xm uint32, xfrac int32, ym uint32, yfrac uint32) uint32 {
	var (
		u8, u2, u3 uint64
		u14, u6    uint32
		u1, u7     uint32
		i5         int32
		i9, i10    int32
		i11, i12   int32
		i13        int32
	)
	p3 := ym
	u8 = uint64(xm)
	if xm == 0 {
		return 0
	}
	if yfrac > 0x20 {
		return 0xffffffff
	}
	if yfrac == 0x20 {
		u3 = 0
	} else {
		b := uint8(yfrac)
		u3 = uint64(p3 >> (b & 0x1f))
		p3 = (((1 << (b & 0x1f)) - 1) & p3) << ((0x20 - uint32(b)) & 0x1f)
	}
	i11 = 0
	i10 = 1
	u14 = 1
	i13 = 0x20
	i9 = -xfrac

	if int32(u3) != 0 {
		state := powShiftDown
	loop:
		for {
			switch state {
			case powShiftDown:
				if u8&1 == 0 {
					i5 = 0
					for {
						u8 >>= 1
						i5++
						if u8&1 != 0 {
							break
						}
					}
					i9 += i5
					i13 -= i5
					if int32(u8) != 0 {
						state = powNormBase
					} else {
						u8, i9, i13 = 0, 0, 0
						state = powCheckAcc
					}
				} else {
					state = powNormBase
				}
			case powNormBase:
				if 0 < i13 {
					u2 = 1 << ((uint32(uint8(i13)) - 1) & 0x3f)
					u6 = uint32(u2)
					for uint32(u8)&u6 == 0 {
						u2 = (u2 >> 1) & 0x7fffffff
						i13--
						u6 = uint32(u2)
					}
				} else {
					u8, i9, i13 = 0, 0, 0
				}
				state = powCheckAcc
			case powCheckAcc:
				if u14 != 0 {
					state = powAccShift
				} else {
					state = powOuter
				}
			case powOuter:
				u14 = 0
				i11 = 0
				i5 = 0
				state = powInner
			case powInner:
				u6 = uint32(u8)
				i10 = i5
				if u3&1 != 0 {
					i10 = i13 + i5
					i12 = i10 - 0x20
					if i12 < 1 {
						u14 = u14 * u6
						i11 += i9
					} else {
						u14 = uint32(fixMul(u14, uint32(i5), uint32(u8), uint32(i13)))
						i10 = 0x20
						i11 = i11 + i9 + i12
					}
				}
				i13 *= 2
				u3 >>= 1
				i5 = (i13 - 0x20) / 2
				if 0 < i5 {
					u6 >>= uint(i5 & 0x1f)
					i9 += i5
					i13 = 0x20
				}
				u8 = uint64(u6 * u6)
				i9 *= 2
				if int32(u3) == 0 {
					break loop
				}
				if u6*u6 != 0 {
					state = powShiftDown
					continue
				}
				i9 = 0
				i13 = 0
				if u14 == 0 {
					state = powOuter
					continue
				}
				state = powAccShift
			case powAccShift:
				i5 = 0
				if u14&1 == 0 {
					for {
						u6 = u14
						u14 = u6 >> 1
						i5++
						if u14&1 != 0 {
							break
						}
					}
					i11 += i5
					i10 -= i5
					if u6>>1 == 0 {
						state = powOuter
						continue
					}
				}
				if i10 < 1 {
					state = powOuter
					continue
				}
				u2 = 1 << ((uint32(uint8(i10)) - 1) & 0x3f)
				u6 = uint32(u2)
				i5 = i10
				for u14&u6 == 0 {
					u2 = (u2 >> 1) & 0x7fffffff
					i5--
					u6 = uint32(u2)
				}
				state = powInner
			}
		}
	}

	// Fractional part of the exponent: repeated square roots of the base.
	u6 = 0x20 - uint32(xfrac)
	u8 = uint64(xm)
	i9 = 0x20
	zero := false
	if int32(xm) >= 0 {
		i9 = 0
		for {
			i9++
			u1 = uint32(int32(u8) * 2)
			u8 = uint64(u1)
			if int32(u1) < 0 {
				break
			}
		}
		u6 -= uint32(i9)
		i9 = 0x20 - i9
		if i9 < 1 {
			u8, u2 = 0, 0
			zero = true
		}
	}
	if !zero {
		u2 = uint64(u6)
		u3 = 1 << ((0x20 - uint32(uint8(i9))) & 0x3f)
		u6 = uint32(u3)
		for uint32(u8)&u6 == 0 {
			u6 = uint32(int32(u3) * 2)
			u3 = uint64(u6)
		}
	}
	for {
		if p3 == 0 {
			xfrac += i11
			if uint32(xfrac+0x1f) > 0x3e {
				return 0
			}
			if xfrac < 0 {
				u14 >>= uint32(-int32(uint8(xfrac))) & 0x1f
			} else {
				u14 <<= uint32(uint8(xfrac)) & 0x1f
			}
			return u14
		}
		u7 = uint32(u8)
		if u2&1 != 0 {
			u7 = uint32(u8 >> 1)
			u2 = uint64(uint32(int32(u2) + 1))
		}
		u1 = fixSqrt16(u7)
		u8 = uint64(u1)
		u6 = uint32((int32(u2>>0x1f) + int32(u2)) >> 1)
		if u1 == 0 {
			u8, u2, i9 = 0, 0, 0
		} else {
			i9 = 0x20
			bad := false
			if int32(u1) >= 0 {
				i13 = 0
				for {
					i13++
					u1 = uint32(int32(u8) * 2)
					u8 = uint64(u1)
					if int32(u1) < 0 {
						break
					}
				}
				i9 = 0x20 - i13
				u6 -= uint32(i13)
				if i9 < 1 {
					u8, u2, i9 = 0, 0, 0
					bad = true
				}
			}
			if !bad {
				u2 = uint64(u6)
				u3 = 1 << ((0x20 - uint32(uint8(i9))) & 0x3f)
				u6 = uint32(u3)
				for uint32(u8)&u6 == 0 {
					u6 = uint32(int32(u3) * 2)
					u3 = uint64(u6)
					i9--
				}
			}
		}
		if int32(p3) < 0 {
			reset := false
			if u14 == 0 {
				reset = true
			} else {
				i13 = 0
				if u14&1 == 0 {
					for {
						u6 = u14
						u14 = u6 >> 1
						i13++
						if u14&1 != 0 {
							break
						}
					}
					i11 += i13
					i10 -= i13
					if u6>>1 == 0 {
						reset = true
					}
				}
				if !reset && i10 < 1 {
					reset = true
				}
				if !reset {
					u3 = 1 << ((uint32(uint8(i10)) - 1) & 0x3f)
					u6 = uint32(u3)
					i13 = i10
					for u14&u6 == 0 {
						u3 = (u3 >> 1) & 0x7fffffff
						i13--
						u6 = uint32(u3)
					}
				}
			}
			if reset {
				u14, i11, i13 = 0, 0, 0
			}
			i10 = i9 + i13
			i5 = i10 - 0x20
			if i5 < 1 {
				u14 = u14 * (uint32(u8) >> ((0x20 - uint32(uint8(i9))) & 0x1f))
				i11 += int32(u2) - i9
			} else {
				u14 = uint32(fixMul(u14, uint32(i13), uint32(u8)>>((0x20-uint32(uint8(i9)))&0x1f), uint32(i9)))
				i10 = 0x20
				i11 = i11 + i5 + (int32(u2) - i9)
			}
		}
		p3 *= 2
	}
}
