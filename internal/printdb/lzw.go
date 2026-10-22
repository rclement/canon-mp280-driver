// SPDX-License-Identifier: GPL-2.0-only

package printdb

import "errors"

// Decompress expands the database's LZW stream: TIFF-style variable width codes
// packed MSB first, width 9..12 bits, 0x100 = clear table, 0x101 = end of
// data, first dictionary entry 0x102, width grows one code early. Running out
// of input acts as an end code.
func Decompress(in []byte) ([]byte, error) {
	const (
		clearCode = 0x100
		endCode   = 0x101
		firstCode = 0x102
	)
	var (
		prefix [4096]uint16
		suffix [4096]byte
		stack  [4200]byte
	)
	out := make([]byte, 0, len(in)*4)
	var acc uint32
	bits, ip := 0, 0
	width, next, prev := 9, firstCode, -1
	var first byte
	for {
		for bits < width && ip < len(in) {
			acc = acc<<8 | uint32(in[ip])
			ip++
			bits += 8
		}
		var code int
		if bits < width {
			code = endCode
		} else {
			bits -= width
			code = int(acc>>uint(bits)) & (1<<uint(width) - 1)
			acc &= 1<<uint(bits) - 1
		}
		if code == endCode {
			return out, nil
		}
		if code == clearCode {
			width, next, prev = 9, firstCode, -1
			continue
		}
		sp := 0
		if prev < 0 {
			if code >= firstCode {
				return nil, errors.New("printdb: LZW: dictionary code after clear")
			}
			first = byte(code)
			stack[sp] = first
			sp++
		} else {
			c := code
			if code > next {
				return nil, errors.New("printdb: LZW: code beyond dictionary")
			}
			if code == next {
				stack[sp] = first
				sp++
				c = prev
			}
			for c >= firstCode {
				stack[sp] = suffix[c]
				sp++
				c = int(prefix[c])
			}
			stack[sp] = byte(c)
			sp++
			first = stack[sp-1]
			if next > 0xfff {
				return nil, errors.New("printdb: LZW: dictionary overflow")
			}
			prefix[next] = uint16(prev)
			suffix[next] = first
			next++
			if next >= 1<<uint(width)-1 && width < 12 {
				width++
			}
		}
		for sp > 0 {
			sp--
			out = append(out, stack[sp])
		}
		prev = code
	}
}
