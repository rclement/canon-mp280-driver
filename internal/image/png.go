// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bufio"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// PNG reader. the original driver uses libpng with: no interlacing, 16-bit samples stripped
// to their high byte, alpha channels and tRNS discarded (never composited),
// palette and low bit depths expanded to 8 bits, gray replicated to RGB;
// resolution and gamma chunks are ignored. Errors are those libpng treats as
// fatal (critical chunks, bad IHDR, damaged compressed data); problems in
// ancillary chunks are ignored (libpng release builds only warn about them).

const pngMaxDimension = 1000000 // libpng's default user limit

type pngChunkReader struct {
	br      *bufio.Reader
	name    [4]byte
	remain  uint32
	crc     uint32
	started bool
}

// header reads the next chunk header.
func (c *pngChunkReader) header() error {
	var h [8]byte
	if _, err := io.ReadFull(c.br, h[:]); err != nil {
		return fmt.Errorf("%w: PNG chunk header: %v", ErrUnreadable, err)
	}
	n := binary.BigEndian.Uint32(h[:4])
	if n > 0x7fffffff {
		return unreadable("PNG chunk length out of range")
	}
	copy(c.name[:], h[4:])
	for _, b := range c.name {
		if !(b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z') {
			return unreadable("PNG invalid chunk type")
		}
	}
	c.remain = n
	c.crc = crc32.ChecksumIEEE(h[4:])
	c.started = true
	return nil
}

func (c *pngChunkReader) read(p []byte) (int, error) {
	if uint32(len(p)) > c.remain {
		p = p[:c.remain]
	}
	n, err := io.ReadFull(c.br, p)
	c.crc = crc32.Update(c.crc, crc32.IEEETable, p[:n])
	c.remain -= uint32(n)
	if err != nil {
		return n, unreadable("PNG chunk data truncated")
	}
	return n, nil
}

// finish skips the rest of the chunk and reports whether its CRC matched.
func (c *pngChunkReader) finish() (crcOK bool, err error) {
	if c.remain > 0 {
		var buf [512]byte
		for c.remain > 0 {
			if _, err := c.read(buf[:min(uint32(len(buf)), c.remain)]); err != nil {
				return false, err
			}
		}
	}
	var t [4]byte
	if _, err := io.ReadFull(c.br, t[:]); err != nil {
		return false, unreadable("PNG chunk CRC truncated")
	}
	return binary.BigEndian.Uint32(t[:]) == c.crc, nil
}

func (c *pngChunkReader) critical() bool { return c.name[0]&0x20 == 0 }

// idatReader presents the concatenated IDAT chunks as a byte stream. It
// verifies a chunk's CRC when the decompressor asks for the byte after the
// chunk, and fails if the next chunk is not IDAT (libpng: "Not enough image
// data").
type idatReader struct {
	c   *pngChunkReader
	err error
}

func (r *idatReader) ReadByte() (byte, error) {
	if r.err != nil {
		return 0, r.err
	}
	for r.c.remain == 0 {
		ok, err := r.c.finish()
		if err == nil && !ok {
			err = unreadable("PNG IDAT CRC error")
		}
		if err == nil {
			err = r.c.header()
		}
		if err == nil && r.c.name != [4]byte{'I', 'D', 'A', 'T'} {
			err = unreadable("PNG not enough image data")
		}
		if err != nil {
			r.err = err
			return 0, err
		}
	}
	var b [1]byte
	if _, err := r.c.read(b[:]); err != nil {
		r.err = err
		return 0, err
	}
	return b[0], nil
}

func (r *idatReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = b
	return 1, nil
}

type pngDecoder struct {
	zr       io.Reader
	idat     *idatReader
	width    int64
	depth    int
	colour   int
	channels int
	palette  [256][3]byte
	rowBytes int
	bpp      int // bytes per pixel for filtering (at least 1)
	prev     []byte
	cur      []byte
	rowsLeft int64
	err      error
}

func openPNG(br *bufio.Reader) (*Page, error) {
	c := &pngChunkReader{br: br}
	var d pngDecoder
	var haveIHDR, havePLTE bool
	var height int64
	for {
		if err := c.header(); err != nil {
			return nil, err
		}
		name := string(c.name[:])
		if !haveIHDR && name != "IHDR" {
			return nil, unreadable("PNG: missing IHDR")
		}
		if name == "IDAT" {
			break
		}
		switch name {
		case "IHDR":
			if haveIHDR || c.remain != 13 {
				return nil, unreadable("PNG: invalid IHDR")
			}
			var h [13]byte
			if _, err := c.read(h[:]); err != nil {
				return nil, err
			}
			if ok, err := c.finish(); err != nil || !ok {
				return nil, unreadable("PNG: IHDR CRC")
			}
			w, ht := binary.BigEndian.Uint32(h[0:]), binary.BigEndian.Uint32(h[4:])
			depth, colour := int(h[8]), int(h[9])
			if w == 0 || ht == 0 || w > pngMaxDimension || ht > pngMaxDimension {
				return nil, unreadable("PNG: bad image size")
			}
			if h[10] != 0 || h[11] != 0 || h[12] > 1 {
				return nil, unreadable("PNG: bad compression, filter or interlace method")
			}
			switch colour {
			case 0:
				if depth != 1 && depth != 2 && depth != 4 && depth != 8 && depth != 16 {
					return nil, unreadable("PNG: bad bit depth")
				}
				d.channels = 1
			case 2, 4, 6:
				if depth != 8 && depth != 16 {
					return nil, unreadable("PNG: bad bit depth")
				}
				d.channels = map[int]int{2: 3, 4: 2, 6: 4}[colour]
			case 3:
				if depth != 1 && depth != 2 && depth != 4 && depth != 8 {
					return nil, unreadable("PNG: bad bit depth")
				}
				d.channels = 1
			default:
				return nil, unreadable("PNG: bad colour type")
			}
			if h[12] != 0 {
				return nil, unreadable("PNG: interlaced images are not supported")
			}
			d.width, height, d.depth, d.colour = int64(w), int64(ht), depth, colour
			haveIHDR = true
			continue
		case "IEND":
			return nil, unreadable("PNG: no image data")
		case "PLTE":
			if havePLTE {
				return nil, unreadable("PNG: duplicate PLTE")
			}
			if d.colour == 3 {
				if c.remain%3 != 0 || c.remain > 768 || c.remain == 0 {
					return nil, unreadable("PNG: invalid palette")
				}
				var buf [768]byte
				n := c.remain
				if _, err := c.read(buf[:n]); err != nil {
					return nil, err
				}
				if ok, err := c.finish(); err != nil || !ok {
					return nil, unreadable("PNG: PLTE CRC")
				}
				for i := 0; i < int(n)/3; i++ {
					copy(d.palette[i][:], buf[3*i:3*i+3])
				}
				havePLTE = true
				continue
			}
		}
		// All other chunks: skip. A CRC error is fatal only for critical chunks.
		if c.critical() && name != "PLTE" {
			return nil, unreadable("PNG: unknown critical chunk %q", name)
		}
		ok, err := c.finish()
		if err != nil {
			return nil, err
		}
		if !ok && c.critical() {
			return nil, unreadable("PNG: %s CRC error", name)
		}
	}
	if d.colour == 3 && !havePLTE {
		return nil, unreadable("PNG: missing PLTE before IDAT")
	}
	d.idat = &idatReader{c: c}
	zr, err := zlib.NewReader(d.idat)
	if err != nil {
		return nil, unreadable("PNG: zlib header: %v", err)
	}
	d.zr = zr
	bitsPerPixel := d.channels * d.depth
	d.rowBytes = int((d.width*int64(bitsPerPixel) + 7) / 8)
	d.bpp = (bitsPerPixel + 7) / 8
	d.prev = make([]byte, d.rowBytes)
	d.cur = make([]byte, d.rowBytes)
	d.rowsLeft = height

	rowLen := d.width * 3
	rstep := int64(blockSize) / rowLen
	if rstep > height {
		rstep = height
	}
	if rstep <= 0 {
		return nil, unreadable("PNG rows too long")
	}
	blk := newBlock()
	pg := &forwardPage{width: d.width, height: height, rowLen: rowLen, rstep: rstep, blk: blk}
	pg.load = func(rows int64) int64 {
		var got int64
		for ; got < rows && d.rowsLeft > 0 && pg.fail == nil; got++ {
			if err := d.nextRow(blk.buf[got*rowLen : (got+1)*rowLen]); err != nil {
				pg.fail = err
				break
			}
		}
		return got
	}
	pg.skip = func(rows int64) {}
	if pg.load(rstep); pg.fail != nil {
		return nil, pg.fail
	}
	pg.read = rstep
	return &Page{Info: Info{Width: d.width, Height: height, BytesPerPixel: 3}, r: pg}, nil
}

// nextRow decodes one image row into out (RGB, 3*width bytes).
func (d *pngDecoder) nextRow(out []byte) error {
	var ft [1]byte
	if _, err := io.ReadFull(d.zr, ft[:]); err != nil {
		return unreadable("PNG: image data: %v", errorOrEOF(err))
	}
	if _, err := io.ReadFull(d.zr, d.cur); err != nil {
		return unreadable("PNG: image data: %v", errorOrEOF(err))
	}
	d.rowsLeft--
	if err := unfilter(ft[0], d.cur, d.prev, d.bpp); err != nil {
		return err
	}
	d.toRGB(d.cur, out)
	d.cur, d.prev = d.prev, d.cur
	if d.rowsLeft == 0 {
		return d.finishStream()
	}
	return nil
}

func errorOrEOF(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("not enough image data")
	}
	return err
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func unfilter(ft byte, cur, prev []byte, bpp int) error {
	switch ft {
	case 0:
	case 1:
		for i := bpp; i < len(cur); i++ {
			cur[i] += cur[i-bpp]
		}
	case 2:
		for i := range cur {
			cur[i] += prev[i]
		}
	case 3:
		for i := range cur {
			var l int
			if i >= bpp {
				l = int(cur[i-bpp])
			}
			cur[i] += byte((l + int(prev[i])) / 2)
		}
	case 4:
		for i := range cur {
			var a, c int
			if i >= bpp {
				a, c = int(cur[i-bpp]), int(prev[i-bpp])
			}
			b := int(prev[i])
			p := a + b - c
			pa, pb, pc := abs(p-a), abs(p-b), abs(p-c)
			var pred int
			switch {
			case pa <= pb && pa <= pc:
				pred = a
			case pb <= pc:
				pred = b
			default:
				pred = c
			}
			cur[i] += byte(pred)
		}
	default:
		return unreadable("PNG: bad adaptive filter value")
	}
	return nil
}

// sample returns the i-th sample of the row scaled to 8 bits for gray
// (the value a bit-depth expansion gives) or raw (palette index) as asked.
func (d *pngDecoder) sample(row []byte, i int, scale bool) byte {
	switch d.depth {
	case 8:
		return row[i]
	case 16:
		return row[2*i] // high byte
	}
	bits := d.depth
	per := 8 / bits
	v := (row[i/per] >> (8 - bits*(i%per+1))) & (1<<bits - 1)
	if scale {
		return v * (255 / (1<<bits - 1))
	}
	return v
}

func (d *pngDecoder) toRGB(row, out []byte) {
	w := int(d.width)
	for x := 0; x < w; x++ {
		var r, g, b byte
		switch d.colour {
		case 0:
			r = d.sample(row, x, true)
			g, b = r, r
		case 4:
			r = d.sample(row, 2*x, true)
			g, b = r, r
		case 2:
			r, g, b = d.sample(row, 3*x, true), d.sample(row, 3*x+1, true), d.sample(row, 3*x+2, true)
		case 6:
			r, g, b = d.sample(row, 4*x, true), d.sample(row, 4*x+1, true), d.sample(row, 4*x+2, true)
		case 3:
			p := d.palette[d.sample(row, x, false)]
			r, g, b = p[0], p[1], p[2]
		}
		out[3*x], out[3*x+1], out[3*x+2] = r, g, b
	}
}

// finishStream is what libpng does after the last row (png_read_finish_IDAT):
// it swallows the rest of the compressed stream, then finishes the current
// IDAT chunk, which verifies that chunk's CRC. Damage found here is fatal.
func (d *pngDecoder) finishStream() error {
	if _, err := io.Copy(io.Discard, d.zr); err != nil {
		return unreadable("PNG: end of image data: %v", errorOrEOF(err))
	}
	ok, err := d.idat.c.finish()
	if err != nil {
		return err
	}
	if !ok {
		return unreadable("PNG IDAT CRC error")
	}
	return nil
}
