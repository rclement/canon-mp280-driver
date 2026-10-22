// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// FixedWeightDiffuser is error diffusion with fixed weights 7, 5, 3, 1 (out
// of 16) and no parameter tree. The MP280 uses it only for the yellow ink of
// Photo Paper Pro Platinum at quality 1. The quantiser is a 512-entry table
// built at construction; the scan direction of each row comes from a 4x4
// table selected by the ink id.
type FixedWeightDiffuser struct {
	bpp, dirRow int
	tab         [512]fixedEntry
	eb          []int16
}

type fixedEntry struct {
	w     [4]int16 // diffusion amounts: next pixel, then three line buffer taps
	level uint8
}

var fixedDirections = [4][4]uint8{{0, 0, 1, 1}, {0, 1, 1, 0}, {1, 0, 0, 1}, {1, 1, 0, 0}}

// NewFixedWeightDiffuser builds the entity for the ink with the given id
// (0 C, 1 M, 2 Y, 3 K), which only selects the scan direction pattern.
func NewFixedWeightDiffuser(inkID, bitsPerPixel, levels, width int) (*FixedWeightDiffuser, error) {
	if err := checkPacking(bitsPerPixel, levels, width); err != nil {
		return nil, err
	}
	if levels > 8 {
		return nil, fmt.Errorf("halftone: unsupported level count %d", levels)
	}
	f := &FixedWeightDiffuser{bpp: bitsPerPixel, dirRow: inkID & 3}
	d := 0x100 - levels
	step := (d / 2) / (levels - 1)
	top := -step + 0x1ff
	rem := d % (levels*2 - 2)
	if rem != 0 {
		top = -step + 0x1fe
	}
	base, next, lvl := 0, step, 0
	for i := range f.tab {
		var e int
		if int16(step+0xff) < int16(i) {
			e, lvl, base = 0, 0, i
			if top < i {
				e, base, lvl = i-0x200, 0x200, 0
			}
		} else {
			if next < i {
				e = step + next
				base = e + 1
				nn := 2*step + next
				next = nn + 1
				if rem != 0 {
					rem--
					base = e + 2
					next = nn + 2
				}
				lvl++
			}
			e = i - base
		}
		for k, w := range [4]int{7, 5, 3, 1} {
			v := min(max(w*e, -0x8000), 0x7fff)
			u := uint16(v)
			if k == 2 {
				u &= 0xfffe
			}
			f.tab[i].w[k] = int16(u)
		}
		f.tab[i].level = uint8(lvl)
	}
	f.eb = make([]int16, paddedWidth(width)+8)
	return f, nil
}

// Reset clears the error line buffer.
func (f *FixedWeightDiffuser) Reset() { clear(f.eb) }

// Row halftones pixels [start,end) of in into the packed plane out; see
// SingleInkDiffuser.Row for the conventions.
func (f *FixedWeightDiffuser) Row(in []uint16, out []byte, start, end, line int) error {
	if end <= start {
		return nil
	}
	m := uint(f.bpp)
	ppb := 8 / f.bpp
	if err := checkRun(start, end, ppb, len(in), len(out), len(f.eb), f.bpp); err != nil {
		return err
	}
	eb := f.eb
	var a, c2, c1 int32
	var r1, r2, ob uint32
	if fixedDirections[f.dirRow][line&3] == 0 { // right to left
		for x := end - 1; x >= start; x-- {
			acc := uint32(in[x]) + r1 + uint32((int32(eb[x+2])+a)<<4)
			r1, r2 = r2, acc&0xff
			e := &f.tab[int(int32(acc)>>8)&0x1ff]
			eb[x+4] = int16(int32(e.w[3]) + int32(int16(c2)))
			c2 = int32(e.w[2]) + c1
			c1 = int32(e.w[1])
			a = int32(e.w[0])
			ob = ob>>m | uint32(e.level)<<(8-m)
			if (end-1-x)%ppb == ppb-1 {
				out[x/ppb] = byte(ob)
				ob = 0
			}
		}
		eb[start+3] = int16(c2)
		eb[start+2] = int16(c1)
		return nil
	}
	for x := start; x < end; x++ {
		acc := uint32(in[x]) + r1 + uint32((int32(eb[x+2])+a)<<4)
		r1, r2 = r2, acc&0xff
		e := &f.tab[int(int32(acc)>>8)&0x1ff]
		eb[x] = int16(int32(e.w[3]) + int32(int16(c2)))
		c2 = int32(e.w[2]) + c1
		c1 = int32(e.w[1])
		a = int32(e.w[0])
		ob = ob<<m | uint32(e.level)
		if (x-start)%ppb == ppb-1 {
			out[x/ppb] = byte(ob)
			ob = 0
		}
	}
	eb[end] = int16(c2)
	eb[end+1] = int16(c1)
	return nil
}
