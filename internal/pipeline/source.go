// SPDX-License-Identifier: GPL-2.0-only

package pipeline

import "fmt"

// Geometry describes one page as the front end hands it to the pipeline: the
// image has already been placed on the page and scaled to the printer's input
// grid, so a source row corresponds one to one to a row of the page (times the
// scale factor of the mode, see Printer).
type Geometry struct {
	// Width is the number of pixels of every row, the width of the whole page
	// (not of the image) on the input grid. Pixels left and right of the image
	// are white (0xFF).
	Width int
	// Height is the number of image rows the source delivers.
	Height int
	// TopSkip is the number of blank page rows above the first image row. They
	// are never delivered, which matters to the halftoner (its error state is
	// cleared when input rows are skipped).
	TopSkip int
}

// Length is the number of rows of the page up to and including the last image
// row.
func (g Geometry) Length() int { return g.TopSkip + g.Height }

func (g Geometry) validate() error {
	if g.Width <= 0 || g.Height < 0 || g.TopSkip < 0 {
		return fmt.Errorf("pipeline: invalid page geometry %+v", g)
	}
	return nil
}

// Row is one row of a page as the source delivers it.
type Row struct {
	// Pixels is Geometry().Width pixels of three bytes (R, G, B), padded with
	// 0xFF outside the image. The slice is only read before the next call to
	// PageSource.Row, so a source may reuse one buffer.
	Pixels []byte
	// White is true when every pixel is pure white (255,255,255); the row then
	// only does bookkeeping and produces no raster.
	White bool
	// LeftWhite and RightWhite count the pure white pixels at both ends of a
	// row that is not white. Only the pixels between them are converted.
	LeftWhite, RightWhite int
}

// PageSource supplies the rows of one page. It is the seam between the image
// and layout stages (reading, placing, cropping, scaling) and the colour,
// halftone and raster stages.
type PageSource interface {
	// Geometry returns the page layout. It is called once, before any row.
	Geometry() Geometry
	// Row returns image row y (0 <= y < Height), called once for each y in
	// ascending order.
	Row(y int) (Row, error)
}
