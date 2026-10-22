// SPDX-License-Identifier: GPL-2.0-only

// Package raster turns already-halftoned ink-plane scanlines into the raster
// part of the MP280 printer stream.
//
// The printer receives the page as horizontal bands of 8 raster rows (16 for
// one rare setting, see Encoder). For
// every band and every ink plane the stream carries one "ESC ( F" command whose
// payload is 8 PackBits-style lines, each terminated by 0x80. Blank bands are
// not sent at all: an "ESC ( e" command tells the printer how many to skip.
// "ESC ( L" selects which ink planes the following "ESC ( F" commands belong to.
//
// The package reproduces, byte for byte, what the original output module
// produces between the page's "ESC ( b" command and the last
// "ESC ( F" command. Everything before and after (job and page set-up, the
// page-end commands) belongs to the job-stream package.
//
// Usage: build an Encoder from the page's Params, call AppendRow for every
// non-skipped raster row in ascending order, then AppendFlush once at the end
// of the page. Each call appends the bytes that are ready to the slice it is
// given.
package raster

import (
	"errors"
	"fmt"
)

// MaxPlanes is the largest number of ink planes the encoder handles (the
// layout of the printer interface); the MP280 uses at most four (C, M, Y, K).
const MaxPlanes = 7

// Raster rows per band. Quality code 0 combined with media 0x33 selects the
// tall band; every other combination uses the standard one.
const (
	standardBandRows = 8
	tallBandRows     = 16
)

// Params are the page settings that select the plane mode and bit depths.
// Field values are the printer database codes.
type Params struct {
	// Media is the media-type code (plain 0, postcard 7, envelope 8,
	// high resolution 0x10, glossy 0x16, ...).
	Media int16
	// Quality is the print-quality code (5 = quality 2, 10 = quality 3,
	// 15 = quality 4).
	Quality int16
	// Grayscale is non-zero for grayscale printing (black ink only, mode 1).
	Grayscale int16
	// Banner is 1 for banner printing, which is not supported.
	Banner int16
	// HeadType is the print-head code (4 on the MP280 selects head index 0).
	HeadType int16
	// Borderless is 1 for borderless printing.
	Borderless int16
	// Flag74 is the parameter-block word at +0x74, which influences the bit
	// depth of the plain-paper quality-3 planes.
	Flag74 int16
}

// Plane is one scanline of one ink plane.
//
// Data holds the pixels MSB first, BitsPerPixel(plane) bits each; the value of
// a pixel is its dot level. A nil Data or Start == End is a blank line. End is
// the number of valid pixels, Start the first valid pixel, and Tail the number
// of pixels at the end to ignore. Bytes outside [Start, End-Tail) are only
// inspected when looking for the first and last non-zero byte, so callers
// should keep them zero.
type Plane struct {
	Data  []byte
	End   int
	Start int
	Tail  int
}

// Encoder holds the state of one page being encoded.
type Encoder struct {
	bandRows   int // raster rows per band: 8, or 16 for quality code 0 on media 0x33
	mode       int // plane mode: 1 = K, 2/3/4 = CMY, 5 = CMYK
	numPlanes  int
	kPlane     int // index of the black plane, -1 if none
	basePlane  int // plane whose band number drives band changes
	bpp        [MaxPlanes]int
	planes     [MaxPlanes]planeState
	headerDone bool
	lastSelect int // last emitted colour selection: 0 none, 1 K only, 2 colour set
	skip       int // blank bands waiting to be announced with ESC ( e
}

// planeState is the per-ink-plane band buffering.
type planeState struct {
	shift int // log2 of pixels per byte: 3, 2 or 1 for 1, 2 or 4 bits per pixel

	prevSlot int // row-in-band of the last row written, -1 when the band is empty
	curBand  int // band currently being collected

	first, last int // first and last non-zero byte of the current row
	slot        int // row % rows per band
	band        int // row / rows per band
	blank       bool

	band8 []byte // band buffer: ESC ( F header followed by lines
	temp  []byte // new band's first row, parked until the old band is emitted
	done  bool   // flush found this plane empty
}

// New creates an Encoder for a page. It fails for settings outside what the
// MP280 uses (banner mode, interleaved planes, unknown plane modes).
func New(p Params) (*Encoder, error) {
	if p.Banner == 1 {
		return nil, errors.New("raster: banner mode is not supported")
	}
	head := printHead(p.HeadType)
	mode := planeMode(p, head)
	e := &Encoder{mode: mode, kPlane: -1, bandRows: standardBandRows}
	if qualityIndex(p.Quality) == 4 && mediaIndex(p.Media) == 10 {
		e.bandRows = tallBandRows
	}
	switch mode {
	case 1:
		e.numPlanes, e.kPlane = 1, 0
	case 2, 3, 4:
		e.numPlanes = 3
	case 5:
		e.numPlanes, e.kPlane = 4, 3
	default:
		return nil, fmt.Errorf("raster: unsupported plane mode %d", mode)
	}
	if mode == 5 {
		e.basePlane = 3
	}
	for i := 0; i < e.numPlanes; i++ {
		depth := bitDepth(p, i, head)
		ps := &e.planes[i]
		switch depth {
		case 1:
			ps.shift = 3
		case 2:
			ps.shift = 2
		case 4:
			ps.shift = 1
		default:
			return nil, fmt.Errorf("raster: unsupported bit depth %d for plane %d", depth, i)
		}
		if interleaveType(p, i, head) != 0 {
			return nil, fmt.Errorf("raster: interleaved plane %d is not supported", i)
		}
		e.bpp[i] = depth
		ps.resetRow()
		ps.band8 = newBandBuffer()
		ps.temp = newBandBuffer()
	}
	return e, nil
}

// Mode returns the plane mode: 1 = K only, 2/3/4 = CMY, 5 = CMYK.
func (e *Encoder) Mode() int { return e.mode }

// NumPlanes returns the number of ink planes AppendRow expects. Planes are in
// the order of the "ESC ( L" colour string (C, M, Y, K).
func (e *Encoder) NumPlanes() int { return e.numPlanes }

// BitsPerPixel returns the pixel depth (1, 2 or 4) of a plane, or -1 if the
// plane does not exist.
func (e *Encoder) BitsPerPixel(plane int) int {
	if plane < 0 || plane >= e.numPlanes {
		return -1
	}
	return e.bpp[plane]
}

// Command prefixes.
var (
	cmdPageRaster = []byte{0x1b, 0x28, 0x62, 0x01, 0x00, 0x01} // ESC ( b: raster follows
	cmdIntensity  = []byte{0x1b, 0x28, 0x49, 0x01, 0x00, 0x01} // ESC ( I
)

// appendBandHeight emits ESC ( J, the number of raster rows per band.
func (e *Encoder) appendBandHeight(dst []byte) []byte {
	return append(dst, 0x1b, 0x28, 0x4a, 0x01, 0x00, byte(e.bandRows))
}

func newBandBuffer() []byte { return []byte{0x1b, 0x28, 0x46, 0, 0} } // ESC ( F, length 0

func (p *planeState) resetRow() {
	p.prevSlot, p.curBand = -1, 0
	p.first, p.last, p.slot, p.band, p.blank = 0, 0, -1, 0, true
}

// appendLine adds one encoded line (including its 0x80 terminator) to a band
// buffer and updates the payload length in the ESC ( F header.
func appendLine(buf []byte, line ...byte) []byte {
	buf = append(buf, line...)
	n := int(buf[3]) | int(buf[4])<<8
	n += len(line)
	buf[3], buf[4] = byte(n), byte(n>>8)
	return buf
}

func appendBlankLine(buf []byte) []byte { return appendLine(buf, 0x80) }

// AppendRow encodes raster row `row` (absolute row on the page, ascending
// across calls) and appends any bytes that become ready to dst. Output appears
// only when the row starts a new band, because the previous band is then
// complete. planes must hold NumPlanes entries.
//
// On error the bytes of this call are discarded but the encoder keeps its
// updated state, exactly as the original module does.
func (e *Encoder) AppendRow(dst []byte, row int, planes []Plane) ([]byte, error) {
	if len(planes) < e.numPlanes {
		return dst, fmt.Errorf("raster: got %d planes, need %d", len(planes), e.numPlanes)
	}
	mark := len(dst)
	if !e.headerDone {
		dst = append(dst, cmdPageRaster...)
		dst = append(dst, cmdIntensity...)
		dst = e.appendBandHeight(dst)
		e.headerDone = true
	}
	if err := e.scanRow(row, planes); err != nil {
		return dst[:mark], err
	}
	base := &e.planes[e.basePlane]
	switch {
	case base.band == base.curBand:
		e.appendToBand(planes, false)
		e.finishRow(0)
	case base.prevSlot == -1:
		// First row of the page, or first row after a flush: nothing to emit.
		e.skip = base.band
		for i := 0; i < e.numPlanes; i++ {
			p := &e.planes[i]
			p.prevSlot, p.curBand = -1, p.band
		}
		e.appendToBand(planes, false)
		e.finishRow(1)
	default:
		dst = e.changeBand(dst, planes)
		e.finishRow(2)
	}
	return dst, nil
}

// scanRow finds, for every plane, the non-zero byte range of the new row.
func (e *Encoder) scanRow(row int, in []Plane) error {
	for i := 0; i < e.numPlanes; i++ {
		p := &e.planes[i]
		if in[i].Data == nil || in[i].End == in[i].Start {
			p.first, p.last, p.blank = 0, 0, true
		} else {
			first, last, blank, err := nonZeroRange(in[i], p.shift)
			if err != nil {
				return err
			}
			p.first, p.last, p.blank = first, last, blank
		}
		p.slot = row % e.bandRows
		p.band = row / e.bandRows
	}
	return nil
}

// nonZeroRange returns the first and last non-zero byte of the valid window
// of a scanline, and whether the line is blank. The byte range is taken from
// whole bytes, but the line is only non-blank if a pixel inside the window
// itself is non-zero.
func nonZeroRange(pl Plane, shift int) (first, last int, blank bool, err error) {
	if pl.Start < 0 {
		return 0, 0, false, errors.New("raster: negative start")
	}
	l := pl.Start >> shift
	r := (pl.End - pl.Tail - 1) >> shift
	if r >= len(pl.Data) {
		return 0, 0, false, fmt.Errorf("raster: plane data too short (%d bytes, need %d)", len(pl.Data), r+1)
	}
	i := l
	for ; i <= r; i++ {
		if pl.Data[i] != 0 {
			break
		}
	}
	if i > r {
		return 0, 0, true, nil
	}
	l = i
	for r > l && pl.Data[r] == 0 {
		r--
	}
	bpp := 8 >> shift
	count := pl.End - pl.Start - pl.Tail
	if count < 0 {
		return 0, 0, false, errors.New("raster: tail longer than the valid window")
	}
	for px := pl.Start; px < pl.Start+count; px++ {
		if pixelValue(pl.Data, px, bpp) != 0 {
			return l, r, false, nil
		}
	}
	return 0, 0, true, nil
}

func pixelValue(data []byte, px int, bpp int) byte {
	bit := px * bpp
	sh := 8 - bpp - bit&7
	return data[bit>>3] >> uint(sh) & (1<<uint(bpp) - 1)
}

// appendToBand writes the current row of every plane into its band buffer,
// padding skipped rows with blank lines. When intoTemp is set the old band is
// first padded to full height and the row goes to the parking buffer.
func (e *Encoder) appendToBand(in []Plane, intoTemp bool) {
	for i := 0; i < e.numPlanes; i++ {
		p := &e.planes[i]
		target := &p.band8
		from := p.prevSlot + 1
		if intoTemp {
			for k := p.prevSlot + 1; k < e.bandRows; k++ {
				p.band8 = appendBlankLine(p.band8)
			}
			from = 0
			target = &p.temp
		}
		for k := from; k < p.slot; k++ {
			*target = appendBlankLine(*target)
		}
		*target = p.appendRowLine(*target, in[i])
	}
}

// appendRowLine encodes the plane's current row as one line.
func (p *planeState) appendRowLine(buf []byte, in Plane) []byte {
	if p.blank {
		return appendBlankLine(buf)
	}
	line := make([]byte, 0, (p.last-p.first+1)*2+p.first/64*2+8)
	if p.first != 0 {
		line = appendZeroRun(line, p.first)
	}
	line = appendPackBits(line, in.Data[p.first:p.last+1])
	line = append(line, 0x80)
	return appendLine(buf, line...)
}

// appendZeroRun encodes n zero bytes (n >= 1) as PackBits repeat codes.
func appendZeroRun(dst []byte, n int) []byte {
	for n-0x80 > 0 {
		dst = append(dst, 0x81, 0)
		n -= 0x80
	}
	return append(dst, byte(1-n), 0)
}

// appendPackBits is the printer's PackBits variant: standard control codes (n < 0x80
// is n+1 literal bytes, n > 0x80 repeats the next byte 257-n times), greedy
// run detection, runs over 128 split into 0x81 chunks, a run of two inside a
// literal folded into it while the literal stays under 128 bytes, a longer run
// closing the literal. A single byte after a repeat starts a new literal.
func appendPackBits(dst []byte, in []byte) []byte {
	lit := -1 // index in dst of the open literal's control byte
	for n := len(in); n > 0; {
		c := in[len(in)-n]
		n--
		run := 1
		for n > 0 && in[len(in)-n] == c {
			n--
			run++
		}
		switch {
		case lit < 0 || run > 2:
			lit = -1
			for ; run > 128; run -= 128 {
				dst = append(dst, 0x81, c)
			}
			if run < 2 {
				lit = len(dst)
				dst = append(dst, 0, c)
			} else {
				dst = append(dst, byte(1-run), c)
			}
		case run == 1:
			v := dst[lit]
			dst[lit] = v + 1
			dst = append(dst, c)
			if v == 0x7e {
				lit = -1
			}
		default: // run == 2 inside a literal
			if int(dst[lit]+1)+2 < 0x80 {
				dst[lit] += 2
				dst = append(dst, c, c)
			} else {
				lit = len(dst)
				dst = append(dst, 1, c, c)
			}
		}
	}
	return dst
}

// changeBand handles a row that belongs to a later band than the pending one:
// the old band is padded and emitted, any fully blank bands in between become
// an ESC ( e skip, and the new row is parked until finishRow moves it in.
func (e *Encoder) changeBand(dst []byte, in []Plane) []byte {
	e.appendToBand(in, true)
	base := &e.planes[e.basePlane]
	newBand, old := base.band, base.curBand
	limit := min(newBand, old+1)
	for cur := old; cur < limit; cur++ {
		dst = e.emitBand(dst)
	}
	if n := newBand - limit; n > 0 {
		dst = appendSkip(dst, n)
	}
	return dst
}

func appendSkip(dst []byte, n int) []byte {
	for ; n > 0x7fff; n -= 0x7fff {
		dst = append(dst, 0x1b, 0x28, 0x65, 0x02, 0x00, 0x7f, 0xff)
	}
	return append(dst, 0x1b, 0x28, 0x65, 0x02, 0x00, byte(n>>8), byte(n))
}

// emitBand outputs the collected band: pending skip, colour selection when it
// changed, then the ESC ( F buffer of every plane involved. Bands without any
// dot are not output; they only extend the pending skip.
func (e *Encoder) emitBand(dst []byte) []byte {
	state := 0
	for i := 0; i < e.numPlanes; i++ {
		p := &e.planes[i]
		length := int(p.band8[3]) | int(p.band8[4])<<8
		if length != e.bandRows && length != 0 && !p.done {
			if i == e.kPlane {
				if state == 0 {
					state = 1
				}
			} else {
				state = 2
			}
		} else {
			p.band8 = p.band8[:5]
			p.band8[3], p.band8[4] = 0, 0
		}
	}
	for i := 0; i < e.numPlanes; i++ {
		e.planes[i].done = false
	}
	if state == 0 {
		e.skip++
		return dst
	}
	if e.skip > 0 {
		dst = appendSkip(dst, e.skip)
		e.skip = 0
	}
	if e.lastSelect != state {
		dst = e.appendColourSelect(dst, state)
	}
	e.lastSelect = state
	for i := 0; i < e.numPlanes; i++ {
		if state == 1 && i != e.kPlane {
			continue
		}
		dst = append(dst, e.planes[i].band8...)
	}
	return dst
}

// appendColourSelect emits "ESC ( L": state 1 selects black only, state 2 the
// full ink set of the mode.
func (e *Encoder) appendColourSelect(dst []byte, state int) []byte {
	if state == 1 {
		return append(dst, 0x1b, 0x28, 0x4c, 0x01, 0x00, 'K')
	}
	switch e.mode {
	case 2:
		return append(dst, 0x1b, 0x28, 0x4c, 0x03, 0x00, 'C', 'M', 'Y')
	case 3, 4:
		return append(dst, 0x1b, 0x28, 0x4c, 0x03, 0x00, 0xc3, 0xcd, 0xd9)
	case 5:
		return append(dst, 0x1b, 0x28, 0x4c, 0x04, 0x00, 'C', 'M', 'Y', 'K')
	}
	return dst
}

// finishRow completes a row. kind is 0 (same band), 1 (first row of a band
// with nothing pending) or 2 (band change: the parked row becomes the band).
func (e *Encoder) finishRow(kind int) {
	if kind == 2 {
		base := &e.planes[e.basePlane]
		if base.curBand < min(base.curBand+1, base.band) {
			for i := 0; i < e.numPlanes; i++ {
				e.planes[i].band8 = newBandBuffer()
			}
		}
		for i := 0; i < e.numPlanes; i++ {
			p := &e.planes[i]
			p.band8 = append(p.band8[:0], p.temp...)
			p.temp = newBandBuffer()
		}
	}
	for i := 0; i < e.numPlanes; i++ {
		p := &e.planes[i]
		p.prevSlot, p.curBand = p.slot, p.band
	}
}

// AppendFlush ends the page: the partially filled last band is padded with
// blank lines and emitted. A page without any row produces only the ESC ( I
// and ESC ( J set-up.
func (e *Encoder) AppendFlush(dst []byte) []byte {
	if !e.headerDone {
		dst = append(dst, cmdIntensity...)
		dst = e.appendBandHeight(dst)
		for i := 0; i < e.numPlanes; i++ {
			e.planes[i].resetRow()
		}
	}
	for i := 0; i < e.numPlanes; i++ {
		p := &e.planes[i]
		blanks := 0
		for k := p.prevSlot + 1; k < e.bandRows; k++ {
			p.band8 = appendBlankLine(p.band8)
			blanks++
		}
		p.prevSlot = e.bandRows - 1
		p.done = blanks == e.bandRows
	}
	cur := e.planes[e.basePlane].curBand
	for e.planes[e.basePlane].curBand < cur+1 {
		dst = e.emitBand(dst)
		for i := 0; i < e.numPlanes; i++ {
			e.planes[i].curBand++
		}
	}
	return dst
}
