// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bufio"
	"io"
)

// nextPPM reads the header of the next P6 image of the stream .
func (d *Document) nextPPM() (*Page, error) {
	br := d.br
	if d.first {
		d.first = false
	} else {
		// A following image starts with "P6"; no more bytes means the end of the document.
		sig := make([]byte, 2)
		if n, _ := io.ReadFull(br, sig); n < 2 {
			return nil, nil
		}
		if sig[0] != 'P' || sig[1] != '6' {
			return nil, unreadable("bad PPM signature of next page")
		}
	}
	// One arbitrary byte follows "P6" (normally the newline). the original driver compares it
	// as a signed char with EOF, so a 0xFF byte counts as end of file.
	skip, err := br.ReadByte()
	if err != nil || skip == 0xFF {
		return nil, unreadable("PPM header ends after signature")
	}
	width, length, maxval, ok := readPPMHeader(br)
	if !ok || width == 0 || length == 0 {
		return nil, unreadable("PPM size missing or zero")
	}
	if maxval != 255 {
		return nil, unreadable("PPM maximum value is %d, not 255", maxval)
	}
	const bpp = 3
	if width < 0 || length < 0 || width > blockSize {
		return nil, unreadable("PPM size %dx%d not supported", width, length)
	}
	rowLen := width * bpp
	rstep := int64(blockSize) / rowLen
	if rstep > length {
		rstep = length
	}
	if rstep <= 0 {
		return nil, unreadable("PPM rows too long")
	}
	blk := newBlock()
	if blk.fill(br, 0, rowLen, rstep) == 0 {
		return nil, unreadable("PPM has no complete pixel row")
	}
	pg := &forwardPage{width: width, height: length, rowLen: rowLen, rstep: rstep, read: rstep, blk: blk}
	pg.load = func(rows int64) int64 { return blk.fill(br, 0, rowLen, rows) }
	pg.skip = func(rows int64) { discard(br, rows*rowLen) }
	return &Page{Info: Info{Width: width, Height: length, BytesPerPixel: bpp}, r: pg}, nil
}

func discard(br *bufio.Reader, n int64) {
	for n > 0 {
		k := n
		if k > 1<<30 {
			k = 1 << 30
		}
		got, err := br.Discard(int(k))
		n -= int64(got)
		if err != nil {
			return
		}
	}
}

// readPPMHeader parses the header: after the signature and one skipped
// byte it reads
//
//	[# comment line] W H LF maxval LF
//
// with C stdio and atol semantics. The quirks that
// follow (every space starts a new token, only the first and the last token
// of the size line count, a 0xFF byte ends a loop like EOF, ...).
func readPPMHeader(br *bufio.Reader) (width, height, maxval int64, ok bool) {
	// Loops end at EOF, which is compared with a signed char, so a
	// 0xFF byte ends them too.
	next := func() (byte, bool) {
		b, err := br.ReadByte()
		if err != nil || b == 0xFF {
			return 0, false
		}
		return b, true
	}
	var wid, hgt []byte
	toHeight := false // writing into the height token
	first, err := br.ReadByte()
	if err != nil {
		first = 0xFF
	}
	if first == '#' {
		// Skip one comment line. If the file ends inside it, the original driver's pointer is
		// still NULL when it is used: treat as unreadable.
		sawAny := false
		for {
			c, more := next()
			if !more {
				break
			}
			sawAny = true
			if c == 0x0A {
				break
			}
		}
		if !sawAny {
			return 0, 0, 0, false
		}
	} else {
		wid = append(wid, first)
	}
	for {
		c, more := next()
		if !more || c == 0x0A {
			break
		}
		if c == ' ' {
			toHeight = true
			hgt = hgt[:0]
			continue
		}
		if toHeight {
			hgt = append(hgt, c)
		} else {
			wid = append(wid, c)
		}
	}
	var mx []byte
	for {
		c, more := next()
		if !more || c == 0x0A {
			break
		}
		mx = append(mx, c)
	}
	return atol(wid), atol(hgt), atol(mx), true
}

// atol is C's atol (strtol base 10): leading white space, an optional sign,
// digits; anything else ends the number; overflow saturates.
func atol(s []byte) int64 {
	i := 0
	for i < len(s) && (s[i] == ' ' || (s[i] >= 9 && s[i] <= 13)) {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	const maxPos = int64(1<<63 - 1)
	var v uint64
	over := false
	for ; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		if over {
			continue
		}
		d := uint64(s[i] - '0')
		if v > (uint64(maxPos)+1-d)/10 {
			over = true
			continue
		}
		v = v*10 + d
	}
	if neg {
		if over || v > uint64(maxPos)+1 {
			return -maxPos - 1
		}
		return -int64(v)
	}
	if over || v > uint64(maxPos) {
		return maxPos
	}
	return int64(v)
}
