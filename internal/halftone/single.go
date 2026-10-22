// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// SingleInkDiffuser is serpentine error diffusion for one ink (yellow and
// black on the MP280). Each pixel's quantisation error is spread to the next
// pixel and to three cells of a line buffer that the following row reads.
// Even rows run right to left, odd rows left to right. Weights and the
// threshold offset depend on the local intensity class
// (previous + current pixel) >> 9, so highlights and shadows diffuse
// differently. The state is the 16-bit error line buffer, persistent across
// rows until Reset.
type SingleInkDiffuser struct {
	bpp   int
	par   *diffusionParams
	vth   [256]int8 // threshold offset per intensity class
	quant singleQuantiser
	eb    []int16 // error line buffer, padded width + 8
}

// NewSingleInkDiffuser builds the entity from its parameter tree (schema
// "HTEDCOFTBL000"). width is the row width in pixels.
func NewSingleInkDiffuser(treeData []byte, bitsPerPixel, levels, width int) (*SingleInkDiffuser, error) {
	if err := checkPacking(bitsPerPixel, levels, width); err != nil {
		return nil, err
	}
	t, err := openTree(treeData)
	if err != nil {
		return nil, err
	}
	if nm, err := t.name(0); err != nil || nm != "HTEDCOFTBL000" || t.count() != 4 {
		return nil, fmt.Errorf("%w: not an HTEDCOFTBL000 tree", errTree)
	}
	noise, err := t.sub(1)
	if err != nil {
		return nil, err
	}
	coef, err := t.sub(2)
	if err != nil {
		return nil, err
	}
	thr, err := t.sub(3)
	if err != nil {
		return nil, err
	}
	s := &SingleInkDiffuser{bpp: bitsPerPixel, quant: newSingleQuantiser(levels)}
	if s.par, err = loadDiffusionParams(noise, coef); err != nil {
		return nil, err
	}
	v, err := thr.bytesN(2, 256)
	if err != nil {
		return nil, err
	}
	for i, b := range v {
		s.vth[i] = int8(b)
	}
	s.eb = make([]int16, paddedWidth(width)+8)
	return s, nil
}

// Reset clears the error line buffer.
func (s *SingleInkDiffuser) Reset() { clear(s.eb) }

// Row halftones pixels [start,end) of in (indexed by absolute pixel) into the
// packed plane out. start and end must be multiples of the pixels per byte.
// line is the absolute output row. Only the bytes of the run are written.
func (s *SingleInkDiffuser) Row(in []uint16, out []byte, start, end, line int) error {
	if end <= start {
		return nil
	}
	m := uint(s.bpp)
	ppb := 8 / s.bpp
	if err := checkRun(start, end, ppb, len(in), len(out), len(s.eb), s.bpp); err != nil {
		return err
	}
	par := s.par
	noise := par.noise[(line%par.noiseH)*par.noiseW:]
	eb := s.eb
	var a, c2, c1 int32
	var r1, r2, ob uint32
	if line&1 == 0 { // right to left
		vp := uint32(in[end-1])
		col := end % par.noiseW
		for x := end - 1; x >= start; x-- {
			v := uint32(in[x])
			acc := v + r1 + uint32((int32(eb[x+2])+a)<<par.shift)
			r1, r2 = r2, acc&0xff
			t := int32(acc) >> 8
			if col == 0 {
				col = par.noiseW
			}
			col--
			cls := (vp + v) >> 9
			o := 0
			if v != 0 {
				o = int(t-(int32(s.vth[cls])+int32(int8(noise[col])))) & 0x3ff
			}
			t -= int32(s.quant.recon[o])
			c := &par.weights[cls]
			eb[x+4] = int16(int32(c[3])*t + int32(int16(c2)))
			c2 = int32(c[2])*t + c1
			c1 = int32(c[1]) * t
			a = int32(c[0]) * t
			ob = ob>>m | uint32(s.quant.level[o])<<(8-m)
			if (end-1-x)%ppb == ppb-1 {
				out[x/ppb] = byte(ob)
				ob = 0
			}
			vp = v
		}
		eb[start+3] = int16(c2)
		eb[start+2] = int16(c1)
		return nil
	}
	vp := uint32(in[start])
	col := start % par.noiseW
	for x := start; x < end; x++ {
		v := uint32(in[x])
		acc := v + r1 + uint32((int32(eb[x+2])+a)<<par.shift)
		r1, r2 = r2, acc&0xff
		t := int32(acc) >> 8
		cls := (vp + v) >> 9
		o := 0
		if v != 0 {
			o = int(t-(int32(s.vth[cls])+int32(int8(noise[col])))) & 0x3ff
		}
		if col++; col >= par.noiseW {
			col = 0
		}
		t -= int32(s.quant.recon[o])
		c := &par.weights[cls]
		eb[x] = int16(int32(c[3])*t + int32(int16(c2)))
		c2 = int32(c[2])*t + c1
		c1 = int32(c[1]) * t
		a = int32(c[0]) * t
		ob = ob<<m | uint32(s.quant.level[o])
		if (x-start)%ppb == ppb-1 {
			out[x/ppb] = byte(ob)
			ob = 0
		}
		vp = v
	}
	eb[end] = int16(c2)
	eb[end+1] = int16(c1)
	return nil
}

func paddedWidth(w int) int { return (w + 7) &^ 7 }

func checkPacking(bpp, levels, width int) error {
	if bpp != 1 && bpp != 2 && bpp != 4 {
		return fmt.Errorf("halftone: unsupported bits per pixel %d", bpp)
	}
	if levels < 2 || levels > 1<<bpp {
		return fmt.Errorf("halftone: %d levels do not fit %d bits per pixel", levels, bpp)
	}
	if width <= 0 {
		return fmt.Errorf("halftone: invalid row width %d", width)
	}
	return nil
}

func checkRun(start, end, ppb, inLen, outLen, ebLen, bpp int) error {
	if start < 0 || start%ppb != 0 || end%ppb != 0 {
		return fmt.Errorf("halftone: run [%d,%d) not aligned to %d pixels", start, end, ppb)
	}
	if end > inLen || end*bpp/8 > outLen || end+4 > ebLen {
		return fmt.Errorf("halftone: run [%d,%d) exceeds the row buffers", start, end)
	}
	return nil
}
