// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// Page adds to Engine what the surrounding pipeline does around the
// halftoner: one persistent input row buffer per ink, the rules that derive
// each ink's non-white run from it, and the error-state reset rules.
//
// Each ink has a buffer of paddedWidth(width) cells that lives for the whole
// page and is only partly rewritten by every row. A row delivers converted
// ink amounts for its span [lo,hi); cells outside the span keep whatever
// earlier rows of the page left there. That matters because the joint C/M
// entity reads both buffers over the union of the two runs, so a stale cell
// outside an ink's own run changes the dots of the other ink. Buffers are
// cleared at the start of every page.
type Page struct {
	eng       *Engine
	width     int
	padded    int
	buf       [][]uint16
	planes    [][]byte
	ranges    []Range
	lastInput int
}

// Result is the outcome of Page.Row. Slices are owned by the Page and are
// overwritten by the next call.
type Result struct {
	// Planes[k] is ink k's packed dot plane for the row (MSB = leftmost pixel);
	// only the bytes of Ranges[k] are meaningful, all others are zero.
	Planes [][]byte
	Ranges []Range
}

// NewPage builds a Page for cfg and starts the first page.
func NewPage(cfg Config) (*Page, error) {
	eng, err := NewEngine(cfg)
	if err != nil {
		return nil, err
	}
	p := &Page{eng: eng, width: cfg.Width, padded: paddedWidth(cfg.Width)}
	for _, ink := range cfg.Inks {
		p.buf = append(p.buf, make([]uint16, p.padded+8))
		p.planes = append(p.planes, make([]byte, (p.padded*ink.BitsPerPixel+7)/8+8))
	}
	p.ranges = make([]Range, len(cfg.Inks))
	p.Start()
	return p, nil
}

// Start begins a new page: error state and all ink buffers are cleared.
func (p *Page) Start() {
	p.eng.Reset()
	for _, b := range p.buf {
		clear(b)
	}
	p.lastInput = -1
}

// BeginInputRow must be called once per input row, in order, before the
// output rows derived from it. When input rows were never delivered since the
// previous call (the top skip of a page) the error state is cleared. White
// rows that are delivered do not count as skipped.
func (p *Page) BeginInputRow(row int) {
	if p.lastInput+1 < row {
		p.eng.Reset()
	}
	p.lastInput = row
}

// Buffer returns ink k's persistent row buffer (paddedWidth(width) cells).
func (p *Page) Buffer(k int) []uint16 { return p.buf[k][:p.padded] }

// Row halftones output row line (absolute, from the page top). The row's
// non-white span is [lo,hi) in pixels; values[k][x-lo] is the converted
// amount of ink k at pixel x (a nil slice means all zero). A row with an
// empty span changes nothing and returns empty ranges.
func (p *Page) Row(line, lo, hi int, values [][]uint16) (Result, error) {
	n := len(p.buf)
	if len(values) != n {
		return Result{}, fmt.Errorf("halftone: row has %d inks, mode has %d", len(values), n)
	}
	for k := range p.ranges {
		p.ranges[k] = Range{}
	}
	res := Result{Planes: p.planes, Ranges: p.ranges}
	if hi <= lo {
		return res, nil
	}
	if lo < 0 || hi > p.width {
		return Result{}, fmt.Errorf("halftone: span [%d,%d) outside row width %d", lo, hi, p.width)
	}
	for k, v := range values {
		if v != nil && len(v) < hi-lo {
			return Result{}, fmt.Errorf("halftone: ink %d has %d values for span of %d", k, len(v), hi-lo)
		}
		b := p.buf[k]
		if v == nil {
			clear(b[lo:hi])
		} else {
			copy(b[lo:hi], v)
		}
		first, last := -1, -1
		for x := lo; x < hi; x++ {
			if b[x] != 0 {
				if first < 0 {
					first = x
				}
				last = x
			}
		}
		if first < 0 {
			clear(b[:lo]) // an ink without dots still wipes the cells left of the span
			continue
		}
		r := Range{Start: first &^ 7, End: min((last+8)&^7, p.padded)}
		p.ranges[k] = r
		// Only the rounding pads may reach outside the span; they are cleared.
		if r.Start < lo {
			clear(b[r.Start:lo])
		}
		if r.End > hi {
			clear(b[hi:r.End])
		}
	}
	for _, pl := range p.planes {
		clear(pl)
	}
	if err := p.eng.Row(line, p.buf, p.ranges, p.planes); err != nil {
		return Result{}, err
	}
	return res, nil
}
