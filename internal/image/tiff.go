// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"encoding/binary"
	"io"
	"math"
)

// TIFF reader: the original driver opens the file with libtiff, accepts uncompressed RGB
// and gray files (photometric 0, 1, 2) and treats every sample as one byte.
// This is a small independent reader of classic TIFF (not BigTIFF) that
// reproduces what the original driver's use of libtiff observably does.

type tiffEntry struct {
	typ   uint16
	count uint32
	data  []byte // value bytes (already fetched)
}

type tiffPage struct {
	rs           io.ReadSeeker
	bo           binary.ByteOrder
	fileSize     int64
	width, hght  int64
	spp, bps     int
	planar       int
	fillOrder2   bool
	swap         int // bytes per sample to byte-swap (big-endian file, 16 bit samples): 2, 4 or 8
	scanline     int64
	rowsPerStrip int64
	offsets      []uint64
	counts       []uint64
	tiled        bool
	undefined    bool // a row was short, missing or read past a scanline: the original result is unrelated memory there
}

var tiffTypeSize = map[uint16]int64{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4, 16: 8, 17: 8, 18: 8}

func (e *tiffEntry) uints(bo binary.ByteOrder) []uint64 {
	var out []uint64
	switch e.typ {
	case 1, 2, 6, 7:
		for _, b := range e.data {
			out = append(out, uint64(b))
		}
	case 3, 8:
		for i := 0; i+2 <= len(e.data); i += 2 {
			out = append(out, uint64(bo.Uint16(e.data[i:])))
		}
	case 4, 9, 13:
		for i := 0; i+4 <= len(e.data); i += 4 {
			out = append(out, uint64(bo.Uint32(e.data[i:])))
		}
	}
	return out
}

// float returns the first value of a rational or integer entry.
func (e *tiffEntry) float(bo binary.ByteOrder) (float64, bool) {
	switch e.typ {
	case 5:
		if len(e.data) < 8 {
			return 0, false
		}
		num, den := bo.Uint32(e.data), bo.Uint32(e.data[4:])
		if den == 0 {
			return 0, true
		}
		return float64(num) / float64(den), true
	case 11:
		if len(e.data) < 4 {
			return 0, false
		}
		return float64(math.Float32frombits(bo.Uint32(e.data))), true
	case 12:
		if len(e.data) < 8 {
			return 0, false
		}
		return math.Float64frombits(bo.Uint64(e.data)), true
	}
	if u := e.uints(bo); len(u) > 0 {
		return float64(u[0]), true
	}
	return 0, false
}

func openTIFF(rs io.ReadSeeker) (*Page, error) {
	size, err := rs.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, unreadable("seek: %v", err)
	}
	readAt := func(off int64, n int64) ([]byte, bool) {
		if off < 0 || n < 0 || off+n > size {
			return nil, false
		}
		b := make([]byte, n)
		if _, err := rs.Seek(off, io.SeekStart); err != nil {
			return nil, false
		}
		if _, err := io.ReadFull(rs, b); err != nil {
			return nil, false
		}
		return b, true
	}
	hdr, ok := readAt(0, 8)
	if !ok {
		return nil, unreadable("TIFF header truncated")
	}
	var bo binary.ByteOrder
	switch string(hdr[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, unreadable("not a TIFF file")
	}
	if bo.Uint16(hdr[2:]) != 42 {
		return nil, unreadable("TIFF version %d not supported", bo.Uint16(hdr[2:]))
	}
	ifdOff := int64(bo.Uint32(hdr[4:]))
	cb, ok := readAt(ifdOff, 2)
	if !ok {
		return nil, unreadable("TIFF directory outside file")
	}
	count := int64(bo.Uint16(cb))
	if count == 0 {
		return nil, unreadable("TIFF directory empty")
	}
	raw, ok := readAt(ifdOff+2, count*12)
	if !ok {
		return nil, unreadable("TIFF directory truncated")
	}
	tags := map[uint16]*tiffEntry{}
	deferred := map[uint16][]byte{} // strip offsets and byte counts: fetched once the strip count is known
	for i := int64(0); i < count; i++ {
		e := raw[i*12 : i*12+12]
		tag, typ, cnt := bo.Uint16(e), bo.Uint16(e[2:]), bo.Uint32(e[4:])
		sz, known := tiffTypeSize[typ]
		if !known {
			return nil, unreadable("TIFF tag %d has unknown data type %d", tag, typ)
		}
		// The tags the checks depend on must be well formed (an integer type,
		// one value, lying inside the file); a defect in any other tag only makes
		// libtiff ignore that tag.
		critical := tiffCriticalTags[tag]
		if critical && !tiffIntegerTypes[typ] {
			return nil, unreadable("TIFF tag %d has type %d", tag, typ)
		}
		if tag == 273 || tag == 279 {
			if !tiffIntegerTypes[typ] {
				return nil, unreadable("TIFF tag %d has type %d", tag, typ)
			}
			deferred[tag] = e
			continue
		}
		if critical && tag != 258 && cnt != 1 {
			return nil, unreadable("TIFF tag %d has %d values", tag, cnt)
		}
		total := sz * int64(cnt)
		var data []byte
		if total <= 4 {
			data = e[8 : 8+total]
		} else {
			data, ok = readAt(int64(bo.Uint32(e[8:])), total)
			if !ok {
				if critical {
					return nil, unreadable("TIFF tag %d value outside file", tag)
				}
				continue
			}
		}
		if _, dup := tags[tag]; dup {
			continue
		}
		tags[tag] = &tiffEntry{typ: typ, count: cnt, data: data}
	}
	uint1 := func(tag uint16) (uint64, bool) {
		if e := tags[tag]; e != nil {
			if u := e.uints(bo); len(u) > 0 {
				return u[0], true
			}
		}
		return 0, false
	}
	p := &tiffPage{rs: rs, bo: bo, fileSize: size, spp: 1, bps: 1, planar: 1}
	w, okw := uint1(256)
	h, okh := uint1(257)
	if !okw || !okh {
		return nil, unreadable("TIFF without image size")
	}
	p.width, p.hght = int64(w), int64(h)
	if v, ok := uint1(277); ok {
		p.spp = int(v)
	}
	if e := tags[258]; e != nil {
		u := e.uints(bo)
		// The values must agree; a short list is used for every sample, extra values are ignored.
		for _, v := range u[:min(len(u), p.spp)] {
			if v != u[0] {
				return nil, unreadable("TIFF with different bits per sample")
			}
		}
		p.bps = int(u[0])
	}
	if v, ok := uint1(284); ok {
		if v != 1 && v != 2 {
			return nil, unreadable("TIFF planar configuration %d", v)
		}
		p.planar = int(v)
	}
	if v, ok := uint1(266); ok && v == 2 {
		p.fillOrder2 = true
	}
	compression := uint64(1)
	if v, ok := uint1(259); ok {
		compression = v
	}
	// The checks, in the original order.
	photometric, ok := uint1(262)
	if !ok {
		return nil, unreadable("TIFF without photometric interpretation")
	}
	if photometric == 3 && tags[320] == nil {
		photometric = 2 // libtiff cannot use a palette image without colour map
	}
	unit := uint64(2)
	if v, ok := uint1(296); ok && v >= 1 && v <= 3 {
		unit = v
	}
	var xres, yres float32
	if e := tags[282]; e != nil {
		if f, ok := e.float(bo); ok {
			xres = float32(f)
		}
	}
	if e := tags[283]; e != nil {
		if f, ok := e.float(bo); ok {
			yres = float32(f)
		}
	}
	switch unit {
	case 1:
		xres, yres = 0, 0
	case 3:
		xres = float32(float64(xres) * 2.54)
		yres = float32(float64(yres) * 2.54)
	}
	if compression != 1 {
		return nil, unreadable("TIFF is compressed")
	}
	gray := false
	switch photometric {
	case 0, 1:
		gray = true
	case 2:
	default:
		return nil, unreadable("TIFF photometric %d not supported", photometric)
	}
	if p.width == 0 || p.hght == 0 || p.spp == 0 || p.bps == 0 {
		return nil, unreadable("TIFF with empty geometry")
	}

	// Strip layout (what libtiff derives from the directory).
	p.tiled = tags[322] != nil || tags[323] != nil
	if !p.tiled {
		if deferred[273] == nil {
			return nil, unreadable("TIFF without strip offsets")
		}
		rps := int64(math.MaxUint32)
		if v, ok := uint1(278); ok {
			if v == 0 {
				return nil, unreadable("TIFF rows per strip is zero")
			}
			rps = int64(v)
		}
		if rps > p.hght {
			rps = p.hght
		}
		p.rowsPerStrip = rps
		nstrips := (p.hght + rps - 1) / rps
		if p.planar == 2 {
			nstrips *= int64(p.spp)
		}
		// libtiff reads only as many strip offsets and byte counts as the image has
		// strips (a longer list is cut, its far end need not lie inside the file).
		fetch := func(tag uint16) ([]uint64, error) {
			e := deferred[tag]
			if e == nil {
				return nil, nil
			}
			typ, cnt := bo.Uint16(e[2:]), int64(bo.Uint32(e[4:]))
			cnt = min(cnt, nstrips)
			total := tiffTypeSize[typ] * cnt
			var data []byte
			if total <= 4 {
				data = e[8 : 8+total]
			} else if data, ok = readAt(int64(bo.Uint32(e[8:])), total); !ok {
				return nil, unreadable("TIFF tag %d value outside file", tag)
			}
			return (&tiffEntry{typ: typ, count: uint32(cnt), data: data}).uints(bo), nil
		}
		var err error
		if p.offsets, err = fetch(273); err != nil {
			return nil, err
		}
		if p.counts, err = fetch(279); err != nil {
			return nil, err
		}
		cnts := deferred[279]
		// libtiff accepts lists that are too short (the missing strips have offset 0
		// and size 0, which cannot be read) or too long.
		for int64(len(p.offsets)) < nstrips {
			p.offsets = append(p.offsets, 0)
		}
		p.offsets = p.offsets[:nstrips]
		if cnts == nil {
			if nstrips != 1 {
				return nil, unreadable("TIFF without strip byte counts")
			}
			p.counts = []uint64{uint64(max(0, size-int64(p.offsets[0])))}
		}
		for int64(len(p.counts)) < nstrips {
			p.counts = append(p.counts, 0)
		}
		p.counts = p.counts[:nstrips]
	}
	p.scanline = (p.width*int64(p.spp)*int64(p.bps) + 7) / 8
	if p.planar == 2 {
		p.scanline = (p.width*int64(p.bps) + 7) / 8
	}
	if p.bo == binary.BigEndian && (p.bps == 16 || p.bps == 32 || p.bps == 64) {
		p.swap = p.bps / 8
	}
	p.chop()

	info := Info{Width: p.width, Height: p.hght, BytesPerPixel: p.spp, Gray: gray, XRes: float64(xres), YRes: float64(yres)}
	return &Page{Info: info, r: p}, nil
}

// chop mimics libtiff's splitting of a single large uncompressed strip into
// strips of about 8 KiB, which decides how much of a damaged file stays readable.
func (p *tiffPage) chop() {
	if p.tiled || len(p.offsets) != 1 {
		return
	}
	rowBytes := p.scanline
	var rows, stripBytes int64
	switch {
	case rowBytes > 8192:
		rows, stripBytes = 1, rowBytes
	case rowBytes > 0:
		rows = 8192 / rowBytes
		stripBytes = rowBytes * rows
	default:
		return
	}
	if rows >= p.rowsPerStrip {
		return
	}
	n := (p.hght + rows - 1) / rows
	off, remain := p.offsets[0], p.counts[0]
	p.offsets, p.counts = make([]uint64, n), make([]uint64, n)
	for i := int64(0); i < n; i++ {
		c := remain
		if c > uint64(stripBytes) {
			c = uint64(stripBytes)
		}
		p.offsets[i], p.counts[i] = off, c
		off += c
		remain -= c
	}
	p.rowsPerStrip = rows
}

func (p *tiffPage) readRow(dst []byte, x, y, n int64) error {
	// the original driver tests the result of TIFFReadScanline with "!", which is never true for
	// its failure value -1, so a scanline that cannot be read is not an error: the
	// row simply contains whatever the buffer held. libtiff zero-fills it when a
	// strip has too little data; other failures leave unrelated memory. This
	// reader delivers zeros in every such case and records that the result is
	// not specified (see undefined).
	line := make([]byte, p.scanline)
	p.fillScanline(line, y)
	bpp := int64(p.spp)
	// the original driver copies width*bpp bytes from byte x*bpp of the scanline without regard
	// to the scanline's real length; bytes past its end are undefined there and
	// read as zero here.
	for i := int64(0); i < n*bpp; i++ {
		var v byte
		if j := x*bpp + i; j >= 0 && j < int64(len(line)) {
			v = line[j]
		} else {
			p.undefined = true
		}
		dst[i] = v
	}
	return nil
}

func (p *tiffPage) flush() error { return nil }

func reverseBits(b byte) byte {
	b = b>>4 | b<<4
	b = (b&0xCC)>>2 | (b&0x33)<<2
	return (b&0xAA)>>1 | (b&0x55)<<1
}

// fillScanline reads scanline y into line (len = scanline size). Whatever is
// missing stays zero and sets p.undefined.
func (p *tiffPage) fillScanline(line []byte, y int64) {
	if p.tiled || y < 0 || y >= p.hght {
		p.undefined = true
		return
	}
	strip := y / p.rowsPerStrip
	inStrip := y % p.rowsPerStrip
	if strip >= int64(len(p.offsets)) {
		p.undefined = true
		return
	}
	off, cnt := int64(p.offsets[strip]), int64(p.counts[strip])
	start := inStrip * p.scanline
	avail := min(cnt-start, p.fileSize-(off+start), p.scanline)
	rowsInStrip := min(p.rowsPerStrip, p.hght-strip*p.rowsPerStrip)
	if cnt <= 0 || avail < p.scanline || cnt < rowsInStrip*p.scanline {
		p.undefined = true
	}
	if avail <= 0 {
		return
	}
	if _, err := p.rs.Seek(off+start, io.SeekStart); err != nil {
		p.undefined = true
		return
	}
	n, _ := io.ReadFull(p.rs, line[:avail])
	if int64(n) < avail {
		p.undefined = true
	}
	if p.fillOrder2 {
		for i := range line {
			line[i] = reverseBits(line[i])
		}
	}
	if s := p.swap; s > 1 {
		for i := 0; i+s <= len(line); i += s {
			for a, b := i, i+s-1; a < b; a, b = a+1, b-1 {
				line[a], line[b] = line[b], line[a]
			}
		}
	}
}

// tiffCriticalTags are the tags whose damage makes libtiff refuse the file.
var tiffCriticalTags = map[uint16]bool{256: true, 257: true, 258: true, 259: true, 262: true, 277: true, 278: true, 284: true}

// tiffIntegerTypes are the field types libtiff converts to integers.
var tiffIntegerTypes = map[uint16]bool{1: true, 3: true, 4: true, 6: true, 8: true, 9: true}
