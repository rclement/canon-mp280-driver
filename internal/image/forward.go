// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"fmt"
)

// forwardPage is the row cache shared by the PPM and PNG readers
// (streaming formats): the
// image is read forward in blocks of rstep rows into one buffer; a requested
// row either lies in the current block or the reader reads on, discarding
// whole blocks, until the block holding the row is loaded.
type forwardPage struct {
	width, height int64 // the image's size (the original driver keeps copies as C "short" below)
	rowLen        int64 // bytes per row
	rstep         int64 // rows per block
	top           int64 // first row of the current block
	read          int64 // rows read so far , for flush
	blk           *block
	fail          error // set by load when the decoder fails (PNG): later requests fail

	// load reads rows rows of pixel data into the start of the block buffer and
	// reports how many complete rows it got.
	load func(rows int64) int64
	// skip discards rows rows of pixel data (used by flush).
	skip func(rows int64)
}

func (p *forwardPage) readRow(dst []byte, x, y, n int64) error {
	// the original driver holds width and height in C shorts here, so images larger than
	// 32767 pixels in either direction fail every row request.
	imgWidth, imgLength := int64(int16(p.width)), int64(int16(p.height))
	if y > imgLength || n > imgWidth {
		return fmt.Errorf("%w: row request outside image", ErrUnreadable)
	}
	if y < p.top {
		return fmt.Errorf("%w: row %d before current block", ErrUnreadable, y)
	}
	top := p.top
	skip := y - (top + p.rstep)
	if skip >= 0 {
		block := int64(int16(skip/p.rstep + 1))
		top += block * p.rstep
		if top+p.rstep > imgLength {
			laststep := imgLength - top
			for i := int64(0); i < block-1; i++ {
				p.load(p.rstep)
				p.read += p.rstep
			}
			if laststep > 0 {
				p.load(laststep)
			}
			p.read += laststep
		} else {
			for i := int64(0); i < block; i++ {
				p.load(p.rstep)
				p.read += p.rstep
			}
		}
	}
	p.top = top
	if p.fail != nil {
		return p.fail
	}
	copy(dst, p.blk.get((y-top)*p.rowLen+x*3, n*3))
	return nil
}

func (p *forwardPage) flush() error {
	if remain := p.height - p.read; remain > 0 {
		p.skip(remain)
		p.read = p.height
	}
	return nil
}
