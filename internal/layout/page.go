// SPDX-License-Identifier: GPL-2.0-only

package layout

import (
	"errors"
	"fmt"

	"canon-mp280-driver/internal/image"
	"canon-mp280-driver/internal/pipeline"
)

// ErrDegenerate is returned for a plan the original driver cannot execute (it divides by
// zero or crashes there): no output rows or no output columns for a source
// that has pixels.
var ErrDegenerate = errors.New("layout: degenerate placement")

// Page delivers the rows of one placed image; it implements
// pipeline.PageSource. Row i of the page maps to a source row through the
// same remainder accumulator the original driver uses (vertical stepping), and a source
// row is read once however often it is repeated.
type Page struct {
	img    *image.Page
	plan   Plan
	mirror bool
	bpp    int

	src, buf []byte // source pixels, page row (PageWidth*bpp)
	rgb      []byte // RGB24 view when bpp != 3
	quotient int64
	rest     int64
	total    int64
	curPos   int64
	prevPos  int64
	next     int
}

// NewPage prepares the rows of img placed by plan. mirror reverses every
// row (see MirrorsMedia).
func NewPage(img *image.Page, plan Plan, mirror bool) (*Page, error) {
	if plan.OutHeight <= 0 || plan.OutWidth <= 0 || plan.PageWidth <= 0 || plan.MaskWidth < 0 {
		return nil, ErrDegenerate
	}
	bpp := img.BytesPerPixel
	pg := &Page{img: img, plan: plan, mirror: mirror, bpp: bpp, prevPos: -1,
		src: make([]byte, plan.MaskWidth*int64(bpp)+1), buf: make([]byte, plan.PageWidth*int64(bpp))}
	pg.quotient, pg.rest = plan.MaskHeight/plan.OutHeight, plan.MaskHeight%plan.OutHeight
	if bpp != 3 {
		pg.rgb = make([]byte, plan.PageWidth*3)
	}
	return pg, nil
}

// Geometry implements pipeline.PageSource.
func (p *Page) Geometry() pipeline.Geometry {
	return pipeline.Geometry{Width: int(p.plan.PageWidth), Height: int(p.plan.OutHeight), TopSkip: int(p.plan.TopSkip)}
}

// Row implements pipeline.PageSource; rows must be requested in order.
func (p *Page) Row(y int) (pipeline.Row, error) {
	if y != p.next {
		return pipeline.Row{}, fmt.Errorf("layout: row %d requested, expected %d", y, p.next)
	}
	p.next++
	pl := p.plan
	if p.curPos != p.prevPos {
		p.prevPos = p.curPos
		if err := p.img.ReadRow(p.src, pl.WidthOffset, p.curPos+pl.HeightOffset, pl.MaskWidth); err != nil {
			return pipeline.Row{}, err
		}
		for i := range p.buf {
			p.buf[i] = 0xff
		}
		off := min(pl.LeftSkip*int64(p.bpp), int64(len(p.buf)))
		hExtend(p.src, p.buf[off:], int(pl.MaskWidth), int(pl.OutWidth), p.bpp)
		if p.mirror {
			mirrorRow(p.buf, int(pl.PageWidth), p.bpp)
		}
	}
	row := p.scan()
	p.total += p.rest
	p.curPos += p.quotient
	if (2*p.total+p.rest)>>1 >= pl.OutHeight {
		p.total -= pl.OutHeight
		p.curPos++
	}
	return row, nil
}

// scan finds the white margins of the page row: pixels are compared three bytes
// at a time with a stride of bpp bytes (bytes past the row read as zero).
func (p *Page) scan() pipeline.Row {
	w := int(p.plan.PageWidth)
	at := func(i int) byte {
		if i >= 0 && i < len(p.buf) {
			return p.buf[i]
		}
		return 0
	}
	white := func(x int) bool {
		o := x * p.bpp
		return at(o) == 0xff && at(o+1) == 0xff && at(o+2) == 0xff
	}
	out := pipeline.Row{Pixels: p.buf}
	if p.bpp != 3 {
		copy(p.rgb, p.buf)
		for i := len(p.buf); i < len(p.rgb); i++ {
			p.rgb[i] = 0
		}
		out.Pixels = p.rgb
	}
	left := 0
	for left < w && white(left) {
		left++
	}
	if left == w {
		out.White = true
		return out
	}
	out.LeftWhite = left
	// From the right the original driver tests the last three bytes of each pixel slot.
	for right := 0; right < w; right++ {
		o := len(p.buf) - 1 - right*p.bpp
		if at(o) != 0xff || at(o-1) != 0xff || at(o-2) != 0xff {
			out.RightWhite = right
			break
		}
	}
	return out
}

// hExtend resamples src pixels to dst pixels by repeating or
// dropping pixels with a remainder accumulator. Only 1, 3, 4, 6 and 7
// components are handled; other pixel sizes leave out untouched.
func hExtend(in, out []byte, srcW, dstW, comp int) {
	switch comp {
	case 1, 3, 4, 6, 7:
	default:
		return
	}
	if dstW <= 0 {
		return
	}
	quotient, rest := srcW/dstW, srcW%dstW
	total, prev, cur := 0, -1, 0
	px := make([]byte, comp)
	for x := 0; x < dstW; x++ {
		if cur != prev {
			prev = cur
			for c := range px {
				px[c] = 0
				if j := cur*comp + c; j < len(in) {
					px[c] = in[j]
				}
			}
		}
		if (x+1)*comp > len(out) {
			return
		}
		copy(out[x*comp:], px)
		total += rest
		cur += quotient
		if (2*total+rest)>>1 >= dstW {
			total -= dstW
			cur++
		}
	}
}

func mirrorRow(buf []byte, w, bpp int) {
	if bpp > 4 {
		return
	}
	for i, j := 0, w-1; i < j; i, j = i+1, j-1 {
		for c := 0; c < bpp; c++ {
			buf[i*bpp+c], buf[j*bpp+c] = buf[j*bpp+c], buf[i*bpp+c]
		}
	}
}
