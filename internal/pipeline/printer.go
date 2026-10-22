// SPDX-License-Identifier: GPL-2.0-only

// Package pipeline is the row-by-row driver of a print job: it pulls the rows
// of a page from a PageSource, converts them from RGB to ink amounts (package
// colour), halftones them into dot planes (package halftone), packs the planes
// into printer raster commands (package raster) and wraps everything in the
// job and page commands (package jobstream).
//
// It reproduces what the original front end `cif` does after it has placed and
// scaled the image together with the pixel path
// behind it.

package pipeline

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"

	"canon-mp280-driver/internal/colour"
	"canon-mp280-driver/internal/halftone"
	"canon-mp280-driver/internal/raster"
)

// Printer prints one job. It is not safe for concurrent use.
type Printer struct {
	cfg  Config
	mode pageMode
	conv *colour.Converter

	cache     *pixelCache
	page      *halftone.Page
	pageWidth int

	// Scratch rows, reused for every row of the job.
	prevRGB []byte
	haveRGB bool
	inks    [][]uint16 // converted ink amounts per ink, indexed by input pixel
	scaled  [][]uint16 // same at the output grid when the mode doubles pixels
	planes  []raster.Plane
	values  [][]uint16
	out     []byte // bytes waiting to be written
}

// New prepares the job described by cfg. It selects the print mode and
// loads the colour tables, so a combination the pipeline cannot print fails
// here, before any output exists.
func New(cfg Config) (*Printer, error) {
	if cfg.Database == nil || cfg.Job == nil {
		return nil, errNoJob
	}
	mode, err := modeFor(cfg.Settings, cfg.Job.Setup())
	if err != nil {
		return nil, err
	}
	conv, err := newConverter(cfg.Database, mode)
	if err != nil {
		return nil, err
	}
	return &Printer{cfg: cfg, mode: mode, conv: conv, cache: new(pixelCache)}, nil
}

// storedPage is the bytes of one page, kept when copies or reverse printing
// need to replay it.
type storedPage []byte

// Print writes the whole job to w. next returns the pages one after the
// other and io.EOF after the last one. Like the original front end it always
// closes the job (footer, EndJob) once it has opened it; if a page fails, the
// part of that page already produced stays in the stream, no copies are
// replayed, and the error is returned after the job has been closed.
func (p *Printer) Print(w io.Writer, next func() (PageSource, error)) error {
	bw := bufio.NewWriterSize(w, 1<<20)
	head, err := p.cfg.Job.Begin()
	if err != nil {
		return err
	}
	if _, err := bw.Write(head); err != nil {
		return err
	}

	copies := max(p.cfg.Copies, 1)
	keep := copies > 1 || p.cfg.ReversePrint
	var stored []storedPage
	var printErr error
	for printErr == nil {
		src, err := next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				printErr = err
			}
			break
		}
		var page storedPage
		var sink io.Writer = bw
		if p.cfg.ReversePrint {
			sink = nil // pages are held back and written last to first
		}
		page, err = p.printPage(sink, src, keep)
		if err != nil {
			printErr = err
			if p.cfg.ReversePrint {
				// Nothing was written for the page; the original driver would have
				// written nothing either.
				page = nil
			}
			break
		}
		if keep {
			stored = append(stored, page)
		}
		if copies > 1 && !p.cfg.Collate && !p.cfg.ReversePrint {
			// The page has been written once; replay the other copies now.
			for i := 1; i < copies; i++ {
				if _, err := bw.Write(page); err != nil {
					return err
				}
			}
		}
	}
	if printErr == nil {
		if err := p.replay(bw, stored, copies); err != nil {
			return err
		}
	}
	tail, err := p.cfg.Job.End()
	if err != nil {
		return errors.Join(printErr, err)
	}
	if _, err := bw.Write(tail); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return printErr
}

// replay writes the stored pages after the last page was produced: the
// remaining copies of collated documents, or everything for reverse printing.
func (p *Printer) replay(w io.Writer, pages []storedPage, copies int) error {
	write := func(b storedPage) error { _, err := w.Write(b); return err }
	switch {
	case p.cfg.ReversePrint && p.cfg.Collate:
		for c := 0; c < copies; c++ {
			for i := len(pages) - 1; i >= 0; i-- {
				if err := write(pages[i]); err != nil {
					return err
				}
			}
		}
	case p.cfg.ReversePrint:
		for i := len(pages) - 1; i >= 0; i-- {
			for c := 0; c < copies; c++ {
				if err := write(pages[i]); err != nil {
					return err
				}
			}
		}
	case p.cfg.Collate:
		for c := 1; c < copies; c++ {
			for _, pg := range pages {
				if err := write(pg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// printPage produces one page. The bytes go to sink as they are produced
// (nil: only collected); with keep they are also returned.
func (p *Printer) printPage(sink io.Writer, src PageSource, keep bool) (storedPage, error) {
	var stored storedPage
	emit := func(force bool) error {
		if len(p.out) == 0 || (!force && len(p.out) < 1<<16) {
			return nil
		}
		if keep {
			stored = append(stored, p.out...)
		}
		if sink != nil {
			if _, err := sink.Write(p.out); err != nil {
				return err
			}
		}
		p.out = p.out[:0]
		return nil
	}

	geo := src.Geometry()
	if err := geo.validate(); err != nil {
		return nil, err
	}
	hdr, err := p.cfg.Job.StartPage()
	if err != nil {
		return nil, err
	}
	p.out = append(p.out[:0], hdr...)
	failed := true
	defer func() {
		if failed {
			// Keep the job state machine usable so the job can still be
			// closed; the page footer is not written.
			_, _ = p.cfg.Job.EndPage()
			p.out = p.out[:0]
		}
	}()

	if err := p.startPage(geo); err != nil {
		return nil, err
	}
	enc, err := raster.New(p.mode.rasterArgs)
	if err != nil {
		return nil, err
	}
	if enc.NumPlanes() != len(p.mode.mode.Inks) {
		return nil, fmt.Errorf("pipeline: mode has %d inks but the raster stage %d planes", len(p.mode.mode.Inks), enc.NumPlanes())
	}

	for y := 0; y < geo.Height; y++ {
		row, err := src.Row(y)
		if err != nil {
			_ = emit(true)
			return nil, err
		}
		vpos := geo.TopSkip + y
		if err := p.produceRow(enc, geo, vpos, row); err != nil {
			_ = emit(true)
			return nil, err
		}
		if err := emit(false); err != nil {
			return nil, err
		}
	}
	p.out = enc.AppendFlush(p.out)
	ftr, err := p.cfg.Job.EndPage()
	if err != nil {
		failed = false
		return nil, err
	}
	failed = false
	p.out = append(p.out, ftr...)
	if err := emit(true); err != nil {
		return nil, err
	}
	return stored, nil
}

// startPage sizes the buffers and starts the halftoner for a page. Every page
// starts with cleared error diffusion state and ink buffers (also across
// the pages of a multi page job); the entities are only rebuilt when the page
// width changes.
func (p *Printer) startPage(geo Geometry) error {
	scale := 1
	if p.mode.mode.Width == 9600 {
		// Pro Platinum at quality 1 prints 1200 dpi: every input pixel is
		// replicated 2x2 on the output grid.
		scale = 2
	}
	width := geo.Width * scale
	if p.page == nil || p.pageWidth != width {
		cfg, err := halftoneConfig(p.cfg.Database, p.mode.mode, width)
		if err != nil {
			return err
		}
		if p.page, err = halftone.NewPage(cfg); err != nil {
			return err
		}
		p.pageWidth = width
	} else {
		p.page.Start()
	}
	n := len(p.mode.mode.Inks)
	p.inks = growRows(p.inks, n, geo.Width+16)
	if scale > 1 {
		p.scaled = growRows(p.scaled, n, width+16)
	}
	if cap(p.planes) < n {
		p.planes = make([]raster.Plane, n)
	}
	p.planes = p.planes[:n]
	p.prevRGB = slices.Grow(p.prevRGB[:0], 3*geo.Width)[:3*geo.Width]
	p.haveRGB = false
	return nil
}

func growRows(rows [][]uint16, n, size int) [][]uint16 {
	if len(rows) != n {
		rows = make([][]uint16, n)
	}
	for i := range rows {
		if cap(rows[i]) < size {
			rows[i] = make([]uint16, size)
		}
		rows[i] = rows[i][:size]
	}
	return rows
}

// produceRow handles input row vpos: white margins, colour conversion,
// halftoning of the one or two output rows it covers, raster encoding.
func (p *Printer) produceRow(enc *raster.Encoder, geo Geometry, vpos int, row Row) error {
	if len(row.Pixels) < 3*geo.Width {
		return fmt.Errorf("pipeline: source row %d has %d bytes, want %d", vpos-geo.TopSkip, len(row.Pixels), 3*geo.Width)
	}
	pixels := row.Pixels[:3*geo.Width]
	p.page.BeginInputRow(vpos)
	if row.White {
		// A white row only does bookkeeping (above); it produces no raster.
		return nil
	}
	left, n := row.LeftWhite, geo.Width-row.LeftWhite-row.RightWhite
	if left < 0 || n <= 0 {
		return nil
	}

	// A source often repeats a row (vertical scale-up); converting it again
	// would give the same inks.
	same := p.haveRGB && string(pixels) == string(p.prevRGB)
	scale := p.pageWidth / geo.Width
	if !same {
		copy(p.prevRGB, pixels)
		p.haveRGB = true
		p.cache.convertRow(p.conv, pixels, left, n, p.inks)
		if scale > 1 {
			for i := range p.inks {
				for x := left; x < left+n; x++ {
					for k := 0; k < scale; k++ {
						p.scaled[i][x*scale+k] = p.inks[i][x]
					}
				}
			}
		}
	}
	src, lo := p.inks, left
	if scale > 1 {
		src, lo = p.scaled, left*scale
	}
	hi := lo + n*scale
	values := p.values[:0]
	for i := range src {
		values = append(values, src[i][lo:hi])
	}
	p.values = values
	for k := 0; k < scale; k++ {
		outRow := vpos*scale + k
		res, err := p.page.Row(outRow, lo, hi, values)
		if err != nil {
			return err
		}
		for i := range p.planes {
			p.planes[i] = raster.Plane{Data: res.Planes[i], Start: res.Ranges[i].Start, End: res.Ranges[i].End}
		}
		if p.out, err = enc.AppendRow(p.out, outRow, p.planes); err != nil {
			return err
		}
	}
	return nil
}
