// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// OrderedDither is threshold-matrix halftoning, used for the "dither
// pattern" option (--halftoning pattern). A 104x104 matrix of 12-bit
// thresholds tiles the page; the dot level is a function of the pixel value
// and the threshold at (row mod 104, column mod 104). There is no error
// state, and runs may start and end in the middle of a byte.
type OrderedDither struct {
	bpp, levels int
	h           int
	period      int      // row length of thr: lcm(matrix width, pixels per byte)
	thr         []uint16 // h rows of period entries
	scale       int
	lvl         []uint8 // level lookup for bpp > 1
}

// NewOrderedDither builds the entity from its tree (schema "HTDTM000").
func NewOrderedDither(treeData []byte, bitsPerPixel, levels int) (*OrderedDither, error) {
	if bitsPerPixel != 1 && bitsPerPixel != 2 && bitsPerPixel != 4 {
		return nil, fmt.Errorf("halftone: unsupported bits per pixel %d", bitsPerPixel)
	}
	if levels < 2 || levels > 15 {
		return nil, fmt.Errorf("halftone: unsupported level count %d", levels)
	}
	t, err := openTree(treeData)
	if err != nil {
		return nil, err
	}
	if nm, err := t.name(0); err != nil || nm != "HTDTM000" || t.count() != 8 {
		return nil, fmt.Errorf("%w: not an HTDTM000 tree", errTree)
	}
	w, err := t.scalar(1)
	if err != nil {
		return nil, err
	}
	h, err := t.scalar(2)
	if err != nil {
		return nil, err
	}
	d, es, n, err := t.array(7)
	if err != nil {
		return nil, err
	}
	if w <= 0 || h <= 0 || es != 2 || n != w*h {
		return nil, fmt.Errorf("%w: threshold matrix", errTree)
	}
	o := &OrderedDither{bpp: bitsPerPixel, levels: levels, h: h}
	ppb := 8 / bitsPerPixel
	g := gcd(w, ppb)
	o.period = w
	if g != ppb {
		o.period = ppb / g * w
	}
	// Quantiser steps over 0..4080: boundaries at even indices, level values at odd.
	nq, dd := 2*levels+1, 2*levels-2
	s := make([]int, nq)
	acc := dd>>1 - 0xff0
	for i := range s {
		s[i] = acc / dd
		acc += 0xff0
	}
	o.scale = s[3]
	o.thr = make([]uint16, o.period*h)
	for r := 0; r < h; r++ {
		for rep := 0; rep < o.period/w; rep++ {
			for x := 0; x < w; x++ {
				t := int(d[2*(r*w+x)]) | int(d[2*(r*w+x)+1])<<8
				v := (t*o.scale + 0x7f8) / 0xff0
				if bitsPerPixel == 1 {
					v = v<<4 | 15 // compare directly with the 16-bit ink value
				}
				o.thr[r*o.period+rep*w+x] = uint16(v)
			}
		}
	}
	if bitsPerPixel > 1 {
		o.lvl = make([]uint8, o.scale+0xff1)
		cls, idx, bound := 0, 1, o.scale
		for i := range o.lvl {
			if bound < i {
				cls++
				bound = o.scale + 0xff0
				if idx != levels-1 {
					idx++
					bound = s[2*idx+1]
				}
			}
			o.lvl[i] = uint8(cls)
		}
	}
	return o, nil
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Row halftones pixels [start,end) of in (indexed by absolute pixel) for the
// absolute output row line. Only the bits of those pixels in out are changed,
// so out must be zero beforehand.
func (o *OrderedDither) Row(in []uint16, out []byte, start, end, line int) error {
	if end <= start {
		return nil
	}
	ppb := 8 / o.bpp
	if start < 0 || end > len(in) || (end*o.bpp+7)/8 > len(out) {
		return fmt.Errorf("halftone: run [%d,%d) exceeds the row buffers", start, end)
	}
	mask := byte(1<<o.bpp - 1)
	row := o.thr[(line%o.h)*o.period:]
	col := start % o.period
	for x := start; x < end; x++ {
		var lv byte
		if o.bpp == 1 {
			if row[col] < in[x] {
				lv = 1
			}
		} else {
			i := min(o.scale+int(in[x]>>4)-int(row[col]), len(o.lvl)-1)
			lv = o.lvl[max(i, 0)]
		}
		sh := (ppb - 1 - x%ppb) * o.bpp
		out[x/ppb] = out[x/ppb]&^(mask<<sh) | lv<<sh
		if col++; col == o.period {
			col = 0
		}
	}
	return nil
}
