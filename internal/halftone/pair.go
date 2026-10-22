// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// PairDiffuser halftones cyan and magenta together. Each ink runs the same
// error accumulator as SingleInkDiffuser, but the dot decision is joint: both
// quantiser inputs are mapped onto cells of a 2-D grid and a table gives the
// pair of dot levels, so the two inks avoid printing on top of each other. A
// threshold shift common to both inks depends on their summed density.
//
// The pair always runs over the union of both inks' runs and reads both input
// buffers there, including cells outside an ink's own run (see Page).
type PairDiffuser struct {
	bpp   int
	width int
	ch    [2]pairChannel
	// commonThreshold is indexed by (vA+vB)>>8 and shifts both thresholds.
	commonThreshold [512]int8
	gridW           int     // cells along ink A; joint index = cellA + cellB*gridW
	joint           []uint8 // 2 dot levels (A, B) per grid cell
}

type pairChannel struct {
	par    *diffusionParams
	lo, hi int        // clamp range of the 128-offset quantiser input
	recon  [16]int16  // reconstruction value per dot level
	cell   [512]uint8 // quantiser input + 128 -> grid cell
	eb     []int16
}

// NewPairDiffuser builds the entity from the primary tree (schema
// "HTEDSEPTBL001", 8 entries, parameters of ink A plus the joint tables) and
// the secondary tree (6 entries, parameters of ink B).
func NewPairDiffuser(primary, secondary []byte, bitsPerPixel, levels, width int) (*PairDiffuser, error) {
	if err := checkPacking(bitsPerPixel, levels, width); err != nil {
		return nil, err
	}
	pt, err := openTree(primary)
	if err != nil {
		return nil, err
	}
	st, err := openTree(secondary)
	if err != nil {
		return nil, err
	}
	if nm, err := pt.name(0); err != nil || nm != "HTEDSEPTBL001" || pt.count() != 8 || st.count() != 6 {
		return nil, fmt.Errorf("%w: not a primary/secondary HTEDSEPTBL001 pair", errTree)
	}
	p := &PairDiffuser{bpp: bitsPerPixel, width: width}
	for k, t := range []tree{pt, st} {
		n, err := p.ch[k].load(t, width)
		if err != nil {
			return nil, err
		}
		if n < levels {
			return nil, fmt.Errorf("%w: quantiser has %d levels, need %d", errTree, n, levels)
		}
	}
	thr, err := pt.sub(6)
	if err != nil {
		return nil, err
	}
	if z, err := thr.scalar(1); err != nil || z != 2 {
		return nil, fmt.Errorf("%w: joint threshold table", errTree)
	}
	v, err := thr.bytesN(3, 512)
	if err != nil {
		return nil, err
	}
	for i, b := range v {
		p.commonThreshold[i] = int8(b)
	}
	grid, err := pt.sub(7)
	if err != nil {
		return nil, err
	}
	if n, err := grid.scalar(1); err != nil || n != 2 {
		return nil, fmt.Errorf("%w: joint grid", errTree)
	}
	dims, err := grid.bytesN(3, 2)
	if err != nil {
		return nil, err
	}
	bnd, err := grid.int16s(4)
	if err != nil {
		return nil, err
	}
	jt, _, _, err := grid.array(5)
	if err != nil {
		return nil, err
	}
	if len(bnd) != int(dims[0])+int(dims[1]) || dims[0] == 0 || dims[1] == 0 || len(jt) < int(dims[0])*int(dims[1])*2 {
		return nil, fmt.Errorf("%w: joint grid sizes", errTree)
	}
	p.gridW = int(dims[0])
	off := 0
	for k := 0; k < 2; k++ {
		nb := int(dims[k])
		if err := p.ch[k].buildCells(bnd[off : off+nb]); err != nil {
			return nil, err
		}
		off += nb
	}
	for _, c := range jt {
		if int(c) >= levels {
			return nil, fmt.Errorf("%w: joint table level %d >= %d", errTree, c, levels)
		}
	}
	p.joint = append([]uint8(nil), jt...)
	return p, nil
}

// load reads the noise, coefficient and quantiser sub-trees of one ink and
// returns the quantiser's number of levels.
func (c *pairChannel) load(t tree, width int) (int, error) {
	if _, err := t.scalar(2); err != nil {
		return 0, err
	}
	noise, err := t.sub(3)
	if err != nil {
		return 0, err
	}
	coef, err := t.sub(4)
	if err != nil {
		return 0, err
	}
	quant, err := t.sub(5)
	if err != nil {
		return 0, err
	}
	if c.par, err = loadDiffusionParams(noise, coef); err != nil {
		return 0, err
	}
	n, err := quant.scalar(1)
	if err != nil {
		return 0, err
	}
	q, err := quant.int16s(3)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > 8 || len(q) != 2*n+1 {
		return 0, fmt.Errorf("%w: pair quantiser", errTree)
	}
	c.lo, c.hi = int(q[0])+128, int(q[2*n])+128
	if c.lo < 0 || c.hi > 511 || c.lo > c.hi {
		return 0, fmt.Errorf("%w: pair quantiser range", errTree)
	}
	for i := 0; i < n; i++ {
		c.recon[i] = q[2*i+1]
	}
	c.eb = make([]int16, paddedWidth(width)+8)
	return n, nil
}

// buildCells fills the lookup from quantiser input (+128) to grid cell: the
// nearest cell boundary, ties going to the upper cell.
func (c *pairChannel) buildCells(b []int16) error {
	pos := 0
	put := func(cell int) error {
		if pos >= 512 {
			return fmt.Errorf("%w: grid boundaries outside -128..383", errTree)
		}
		c.cell[pos] = uint8(cell)
		pos++
		return nil
	}
	for v := -128; v < int(b[0]); v++ {
		if err := put(0); err != nil {
			return err
		}
	}
	for j := 0; j+1 < len(b); j++ {
		b0, b1 := int(b[j]), int(b[j+1])
		for v := b0; v < b1; v++ {
			cell := j + 1
			if v-b0 < b1-v {
				cell = j
			}
			if err := put(cell); err != nil {
				return err
			}
		}
	}
	for v := int(b[len(b)-1]); v < 384; v++ {
		if err := put(len(b) - 1); err != nil {
			return err
		}
	}
	if pos != 512 {
		return fmt.Errorf("%w: grid boundaries do not cover -128..383", errTree)
	}
	return nil
}

// Reset clears both error line buffers.
func (p *PairDiffuser) Reset() {
	for k := range p.ch {
		clear(p.ch[k].eb)
	}
}

// Row halftones the union run [start,end) of both inks. in[k] (k = 0 for A,
// 1 for B) is indexed by absolute pixel; every cell of the run is read,
// whatever the ink's own range is. out[k] receives the packed levels over the
// whole union run. line is the absolute output row; the scan direction
// follows the pattern right, right, left, left by line&3.
func (p *PairDiffuser) Row(in [2][]uint16, out [2][]byte, start, end, line int) error {
	if end <= start {
		return nil
	}
	m := uint(p.bpp)
	ppb := 8 / p.bpp
	for k := range p.ch {
		if err := checkRun(start, end, ppb, len(in[k]), len(out[k]), len(p.ch[k].eb), p.bpp); err != nil {
			return err
		}
	}
	leftToRight := line&3 >= 2
	var noise [2][]byte
	for k := range p.ch {
		par := p.ch[k].par
		noise[k] = par.noise[(line%par.noiseH)*par.noiseW:]
	}
	noiseW := p.ch[0].par.noiseW
	var a, c2, c1 [2]int32
	var r1, r2, ob, vp [2]uint32

	first, step, last := end-1, -1, start-1
	if leftToRight {
		first, step, last = start, 1, end
	}
	for k := range vp {
		vp[k] = uint32(in[k][first])
	}
	col := start % noiseW
	if !leftToRight {
		col = end % noiseW
	}
	n := 0
	for x := first; x != last; x += step {
		var v [2]uint32
		var t [2]int32
		for k := range p.ch {
			c := &p.ch[k]
			v[k] = uint32(in[k][x])
			acc := v[k] + r1[k] + uint32((int32(c.eb[x+2])+a[k])<<c.par.shift)
			r1[k], r2[k] = r2[k], acc&0xff
			t[k] = int32(acc) >> 8
		}
		base := int32(p.commonThreshold[(v[0]+v[1])>>8])
		if !leftToRight {
			if col == 0 {
				col = noiseW
			}
			col--
		}
		var cellIdx [2]int
		for k := range p.ch {
			c := &p.ch[k]
			s := int(t[k] + 0x80 - (base + int32(int8(noise[k][col]))))
			s = min(max(s, c.lo), c.hi)
			cellIdx[k] = int(c.cell[s])
		}
		if leftToRight {
			if col++; col >= noiseW {
				col = 0
			}
		}
		ji := (cellIdx[0] + cellIdx[1]*p.gridW) * 2
		for k := range p.ch {
			c := &p.ch[k]
			var level uint8
			if v[k] != 0 {
				level = p.joint[ji+k]
			}
			t[k] -= int32(c.recon[level])
			cls := (vp[k] + v[k]) >> 9
			w := &c.par.weights[cls]
			store := int16(int32(w[3])*t[k] + int32(int16(c2[k])))
			if leftToRight {
				c.eb[x] = store
			} else {
				c.eb[x+4] = store
			}
			c2[k] = int32(w[2])*t[k] + c1[k]
			c1[k] = int32(w[1]) * t[k]
			a[k] = int32(w[0]) * t[k]
			if leftToRight {
				ob[k] = ob[k]<<m | uint32(level)
			} else {
				ob[k] = ob[k]>>m | uint32(level)<<(8-m)
			}
			vp[k] = v[k]
		}
		if n%ppb == ppb-1 {
			for k := range p.ch {
				out[k][x/ppb] = byte(ob[k])
				ob[k] = 0
			}
		}
		n++
	}
	for k := range p.ch {
		eb := p.ch[k].eb
		if leftToRight {
			eb[end] = int16(c2[k])
			eb[end+1] = int16(c1[k])
		} else {
			eb[start+3] = int16(c2[k])
			eb[start+2] = int16(c1[k])
		}
	}
	return nil
}
