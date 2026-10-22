// SPDX-License-Identifier: GPL-2.0-only

package halftone

import (
	"fmt"
	"math/bits"
)

// diffusionParams are the per-ink parameters shared by the single-ink and the
// joint-pair error diffusion entities: a tiled noise matrix that perturbs the
// quantiser threshold, and a table of four diffusion weights selected by the
// local intensity class.
type diffusionParams struct {
	noiseW, noiseH int
	noise          []byte // noiseH rows of noiseW signed bytes
	// weights[class] = {next pixel, line buffer tap 1, 2, 3} (each /2^k, see shift).
	weights [256][4]uint8
	// shift scales the carried error back to accumulator units (8 - log2 of the weight sum).
	shift uint
}

// loadDiffusionParams reads a noise sub-tree (schema HTNOISE000) and a
// coefficient sub-tree (schema HTEDCOF000).
func loadDiffusionParams(noise, coef tree) (*diffusionParams, error) {
	w, err := noise.scalar(1)
	if err != nil {
		return nil, err
	}
	h, err := noise.scalar(2)
	if err != nil {
		return nil, err
	}
	d, es, n, err := noise.array(4)
	if err != nil {
		return nil, err
	}
	if w <= 0 || h <= 0 || es != 1 || n != w*h {
		return nil, fmt.Errorf("%w: noise matrix %dx%d does not match its data", errTree, w, h)
	}
	p := &diffusionParams{noiseW: w, noiseH: h, noise: d}

	pal, es, n, err := coef.array(4)
	if err != nil {
		return nil, err
	}
	if es != 1 || n < 4 || n%4 != 0 {
		return nil, fmt.Errorf("%w: weight palette", errTree)
	}
	idx, err := coef.bytesN(5, 256)
	if err != nil {
		return nil, err
	}
	sum := uint(pal[0]) + uint(pal[1]) + uint(pal[2]) + uint(pal[3])
	if sum == 0 || bits.TrailingZeros(sum) > 8 {
		return nil, fmt.Errorf("%w: weight sum %d", errTree, sum)
	}
	p.shift = uint(8 - bits.TrailingZeros(sum))
	for class := range p.weights {
		row := int(idx[class])
		if 4*row+4 > n {
			return nil, fmt.Errorf("%w: palette row %d out of range", errTree, row)
		}
		copy(p.weights[class][:], pal[4*row:4*row+4])
	}
	return p, nil
}

// singleQuantiser maps the 10-bit residual index to a dot level and to the
// reconstruction value subtracted from the accumulator.
type singleQuantiser struct {
	level [1024]uint8
	recon [1024]int16
}

// newSingleQuantiser builds the quantiser for `levels` dot levels. The input
// range -255..255 is split into 2*levels-2 equal steps; even boundaries
// separate levels, odd ones are the level's reconstruction value. Residual
// indices above 639 are negative accumulator values (wrapped) and give level 0.
func newSingleQuantiser(levels int) singleQuantiser {
	var q singleQuantiser
	d := 2*levels - 2
	s := make([]int, 2*levels+1)
	acc := d>>1 - 255
	for i := range s {
		s[i] = int(int16(acc / d))
		acc += 255
	}
	imax := 2*levels - 1
	for i := 0; i < 1024; i++ {
		k := 0
		if i <= 639 {
			k = imax
			if i < s[imax] {
				k = 1
				for k != imax && !(s[k] <= i && i < s[k+1]) {
					k++
				}
			}
		}
		q.level[i] = uint8(k / 2)
		q.recon[i] = int16(s[(k/2)*2+1])
	}
	return q
}
