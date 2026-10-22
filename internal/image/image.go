// SPDX-License-Identifier: GPL-2.0-only

// Package image reads the input documents of the original front end: PPM
// (P6), BMP (24 bit), PNG and TIFF, with the exact acceptance rules and
// quirks of the original reader.
//
// The package is not a general image library. Its job is to hand the layout
// stage the pixel rows the original reader would hand to the layout code, and
// to fail where the original reader fails. Rows are delivered one at a time from a
// sliding block of at most one mebibyte, so large images are never loaded
// whole (BMP and TIFF need random access to their input).
//

package image

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// Format is the file format of a document, using the front end's numbering.
type Format int

// Supported formats. The numbers are the front end's format ids.
const (
	FormatTIFF Format = 10
	FormatBMP  Format = 13
	FormatPPM  Format = 14
	FormatPNG  Format = 15
)

// ErrUnreadable is wrapped by every error of this package. When opening the
// document or its first page fails, the original front end still writes the job
// header and trailer: a job without pages.
var ErrUnreadable = errors.New("image: unreadable input")

func unreadable(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnreadable, fmt.Sprintf(format, a...))
}

// blockSize is the block size: pixel data is read in blocks of up to 1 MiB.
const blockSize = 0x100000

// Info describes one page.
type Info struct {
	Width, Height int64 // pixels
	// BytesPerPixel is 3 for PPM, PNG and BMP; for TIFF it is the number of
	// samples per pixel (1, 3 or 4 occur in practice) because the original driver treats every
	// sample as one byte.
	BytesPerPixel int
	// Gray is set for TIFF files with a gray photometric interpretation (the
	// reader's "output type"). the original driver does not use it afterwards.
	Gray bool
	// XRes and YRes are the resolution in dpi as the reader reports it; 0
	// means the file gives none (the original driver then assumes 120 dpi).
	XRes, YRes float64
}

// pageReader is implemented by the per-format page state.
type pageReader interface {
	readRow(dst []byte, x, y, n int64) error
	flush() error
}

// Page is one image of a document.
type Page struct {
	Info
	r pageReader
}

// ReadRow copies n pixels of image row y,
// starting at pixel column x, into dst (n*BytesPerPixel bytes).
//
// The formats that stream (PPM, PNG, BMP) keep one block of rows; rows must be
// requested in non-decreasing order, and a row before the current block is an
// error, as in the original code. Like the original, the copy may run past the end of the
// requested row into the following bytes of the block when x+n exceeds the
// image width; bytes beyond the block read as zero.
func (p *Page) ReadRow(dst []byte, x, y, n int64) error {
	if n < 0 || int64(len(dst)) < n*int64(p.BytesPerPixel) {
		return fmt.Errorf("%w: destination too small", ErrUnreadable)
	}
	return p.r.readRow(dst, x, y, n)
}

// Flush skips the pixel data of this page that
// has not been read yet, so that the next page of a PPM stream can be found.
func (p *Page) Flush() error { return p.r.flush() }

// Document is an opened input, ready to deliver pages.
type Document struct {
	format Format
	stdin  bool
	br     *bufio.Reader // PPM, PNG: sequential input
	rs     io.ReadSeeker // BMP, TIFF: random access
	first  bool
	prev   *Page
	closer io.Closer
}

// Format returns the detected format.
func (d *Document) Format() Format { return d.format }

// Close releases the document. It does not close the reader given to Open.
func (d *Document) Close() error { return nil }

// OpenStdin opens a standard-input document. the original driver accepts only PPM there:
// a stream of "P6" images, each of which is a page.
func OpenStdin(r io.Reader) (*Document, error) {
	br := bufio.NewReaderSize(r, 64*1024)
	sig, err := br.Peek(2)
	if err != nil || sig[0] != 'P' || sig[1] != '6' {
		return nil, unreadable("not a PPM stream")
	}
	_, _ = br.Discard(2)
	return &Document{format: FormatPPM, stdin: true, br: br, first: true}, nil
}

// Open opens a document given as a file argument. The format is chosen from
// the first eight bytes exactly like the original driver does (only two bytes are compared
// for TIFF, BMP and PPM, eight for PNG; a shorter file is unreadable).
// If r is not an io.ReadSeeker, BMP and TIFF inputs are read into memory.
func Open(r io.Reader) (*Document, error) {
	var head [8]byte
	var rs io.ReadSeeker
	if s, ok := r.(io.ReadSeeker); ok {
		rs = s
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return nil, unreadable("seek: %v", err)
		}
		if _, err := io.ReadFull(rs, head[:]); err != nil {
			return nil, unreadable("file shorter than 8 bytes")
		}
		if _, err := rs.Seek(0, io.SeekStart); err != nil {
			return nil, unreadable("seek: %v", err)
		}
	} else {
		br := bufio.NewReaderSize(r, 64*1024)
		h, err := br.Peek(8)
		if err != nil {
			return nil, unreadable("file shorter than 8 bytes")
		}
		copy(head[:], h)
		r = br
	}
	d := &Document{first: true}
	switch {
	case (head[0] == 'I' && head[1] == 'I') || (head[0] == 'M' && head[1] == 'M'):
		d.format = FormatTIFF
	case head[0] == 'B' && head[1] == 'M':
		d.format = FormatBMP
	case head[0] == 'P' && head[1] == '6':
		d.format = FormatPPM
	case bytes.Equal(head[:], []byte("\x89PNG\r\n\x1a\n")):
		d.format = FormatPNG
	default:
		return nil, unreadable("not a supported image format")
	}
	if d.format == FormatBMP || d.format == FormatTIFF {
		if rs == nil {
			all, err := io.ReadAll(r)
			if err != nil {
				return nil, unreadable("read: %v", err)
			}
			rs = bytes.NewReader(all)
		}
		d.rs = rs
		return d, nil
	}
	if rs != nil {
		r = rs
	}
	d.br = bufio.NewReaderSize(r, 64*1024)
	skip := 2
	if d.format == FormatPNG {
		skip = 8
	}
	_, _ = d.br.Discard(skip)
	return d, nil
}

// NextPage returns the next page: (nil, nil) when the document has no more
// pages, an error wrapping ErrUnreadable when the next page cannot be read.
// Only PPM streams from standard input have more than one page; the other
// formats (and PPM files) deliver one page and then (nil, nil), the way
// the original loop stops after the first page of a file.
func (d *Document) NextPage() (*Page, error) {
	if d.prev != nil {
		_ = d.prev.Flush()
		d.prev = nil
	}
	var p *Page
	var err error
	switch d.format {
	case FormatPPM:
		if !d.first && !d.stdin {
			return nil, nil
		}
		p, err = d.nextPPM()
	case FormatPNG:
		if !d.first {
			return nil, nil
		}
		d.first = false
		p, err = openPNG(d.br)
	case FormatBMP:
		if !d.first {
			return nil, nil
		}
		d.first = false
		p, err = openBMP(d.rs)
	case FormatTIFF:
		if !d.first {
			return nil, nil
		}
		d.first = false
		p, err = openTIFF(d.rs)
	}
	if err != nil || p == nil {
		return nil, err
	}
	d.prev = p
	return p, nil
}

// block is the sliding window of pixel data of the streaming readers: one
// buffer of blockSize bytes in which rows follow each other.
type block struct {
	buf []byte
}

func newBlock() *block { return &block{buf: make([]byte, blockSize)} }

// get returns n bytes at offset off; bytes outside the buffer read as zero.
func (b *block) get(off, n int64) []byte {
	out := make([]byte, n)
	for i := int64(0); i < n; i++ {
		if j := off + i; j >= 0 && j < int64(len(b.buf)) {
			out[i] = b.buf[j]
		}
	}
	return out
}

// fill reads up to count items of size bytes into the start of the buffer
// and returns the number of complete items, like fread. Bytes of an
// incomplete last item are stored too; bytes that are not read keep their
// old value.
func (b *block) fill(r io.Reader, off, size, count int64) int64 {
	if size <= 0 || count <= 0 || off < 0 || off+size*count > int64(len(b.buf)) {
		return 0
	}
	n, _ := io.ReadFull(r, b.buf[off:off+size*count])
	return int64(n) / size
}
