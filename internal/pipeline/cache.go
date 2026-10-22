// SPDX-License-Identifier: GPL-2.0-only

package pipeline

import "canon-mp280-driver/internal/colour"

// pixelCache remembers the ink amounts of recently converted colours. The
// conversion (two 3D look-up tables and a tone curve per ink) is a pure
// function of the pixel, and real images repeat colours a lot, so a small
// direct-mapped cache removes most of the work. It never changes a result.
type pixelCache struct {
	keys [1 << 16]uint32 // pixel value + 1 (0: empty slot)
	inks [1 << 16][4]uint16
}

// convertRow converts n pixels of an RGB row starting at pixel x0 into
// ink[i][x] (indexed by absolute pixel), like colour.Converter.Row.
func (c *pixelCache) convertRow(conv *colour.Converter, rgb []byte, x0, n int, ink [][]uint16) {
	nink := len(ink)
	for x := x0; x < x0+n; x++ {
		r, g, b := rgb[3*x], rgb[3*x+1], rgb[3*x+2]
		key := uint32(r) | uint32(g)<<8 | uint32(b)<<16
		slot := (key * 0x9E3779B1) >> 16
		if c.keys[slot] != key+1 {
			conv.Pixel(r, g, b, c.inks[slot][:nink])
			c.keys[slot] = key + 1
		}
		v := &c.inks[slot]
		for i := 0; i < nink; i++ {
			ink[i][x] = v[i]
		}
	}
}
