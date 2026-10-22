// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"fmt"
	"io"
)

// bmpPage is the BMP reader. Only 24-bit files whose pixel data starts at
// offset 54 are accepted. The stored rows are bottom-up and the original driver reads the
// file backwards in blocks: the block buffer holds file rows in file order, so
// image row y of the block sits at index (rstep-1-(y-top)).
type bmpPage struct {
	rs                 io.ReadSeeker
	width, height      int64
	rowLen, rstep, top int64
	blk                *block
}

func le32(b []byte) int64 {
	return int64(int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24))
}

func openBMP(rs io.ReadSeeker) (*Page, error) {
	if _, err := rs.Seek(2, io.SeekStart); err != nil { // the "BM" signature
		return nil, unreadable("seek: %v", err)
	}
	var hdr [12]byte
	if _, err := io.ReadFull(rs, hdr[:]); err != nil {
		return nil, unreadable("BMP file header truncated")
	}
	fileSize, offBits := le32(hdr[0:]), le32(hdr[8:])
	var inf [40]byte
	if _, err := io.ReadFull(rs, inf[:]); err != nil {
		return nil, unreadable("BMP info header truncated")
	}
	width, height := le32(inf[4:]), le32(inf[8:])
	bitCount := int16(uint16(inf[0x0E]) | uint16(inf[0x0F])<<8)
	xppm, yppm := le32(inf[0x18:]), le32(inf[0x1C:])
	if bitCount != 24 || offBits != 54 {
		return nil, unreadable("BMP is not a 24 bit file with data at offset 54")
	}
	// The row length is derived from the file size field, not from the width.
	if height == 0 {
		return nil, unreadable("BMP height is zero")
	}
	rowLen := (fileSize - offBits) / height
	if rowLen <= 0 {
		return nil, unreadable("BMP row length %d", rowLen)
	}
	rstep := int64(blockSize) / rowLen
	if rstep <= 0 {
		return nil, unreadable("BMP rows too long")
	}
	pg := &bmpPage{rs: rs, width: width, height: height, rowLen: rowLen, rstep: rstep, blk: newBlock()}
	// Read the first block: the file's last rstep rows (the top of the image).
	off := rowLen * (height - rstep)
	if height-rstep < 0 {
		off = 0
	}
	if _, err := rs.Seek(54+off, io.SeekStart); err != nil {
		return nil, unreadable("seek: %v", err)
	}
	if pg.blk.fill(rs, 0, rowLen, rstep) == 0 {
		return nil, unreadable("BMP has no complete pixel row")
	}
	yres := float64(yppm) * 0.0254
	info := Info{Width: width, Height: height, BytesPerPixel: 3, XRes: float64(xppm) * 0.0254, YRes: yres}
	return &Page{Info: info, r: pg}, nil
}

func (p *bmpPage) readRow(dst []byte, x, y, n int64) error {
	if y > p.height {
		return fmt.Errorf("%w: BMP row %d beyond height", ErrUnreadable, y)
	}
	if n > p.width {
		return fmt.Errorf("%w: BMP row request wider than image", ErrUnreadable)
	}
	top := p.top
	if y < top {
		return fmt.Errorf("%w: BMP row %d before current block", ErrUnreadable, y)
	}
	if skip := y - (top + p.rstep); skip >= 0 {
		block := int64(int16(skip/p.rstep + 1))
		top += block * p.rstep
		if top+p.rstep > p.height {
			laststep := p.height - top
			for i := int64(0); i < block-1; i++ {
				p.blk.fill(p.rs, 0, p.rowLen, p.rstep)
			}
			// the remaining rows are the first rows of the file
			_, _ = p.rs.Seek(54, io.SeekStart)
			if laststep > 0 && laststep <= p.rstep {
				p.blk.fill(p.rs, (p.rstep-laststep)*p.rowLen, p.rowLen, laststep)
			}
		} else {
			_, _ = p.rs.Seek(p.rowLen*(p.height-p.rstep-top)+54, io.SeekStart)
			p.blk.fill(p.rs, 0, p.rowLen, p.rstep)
		}
	}
	var pos int64
	if p.height < p.rstep {
		pos = p.height - y + top - 1
	} else {
		pos = p.rstep - y + top - 1
	}
	p.top = top
	src := p.blk.get(pos*p.rowLen+x*3, n*3)
	for i := int64(0); i < n; i++ { // BGR to RGB
		dst[3*i], dst[3*i+1], dst[3*i+2] = src[3*i+2], src[3*i+1], src[3*i]
	}
	return nil
}

func (p *bmpPage) flush() error { return nil }
