// SPDX-License-Identifier: GPL-2.0-only

// Package layout places an image on the printer page the way the original front end
// does: it computes the position code and the per-page geometry
// and row preparation (resolution, fit, cut to the printable area,
// horizontal resampling, mirroring, white-margin detection).
//
// Compute turns the image size, the options and the paper's printable area
// into a Plan; Page then delivers the page's rows (a pipeline.PageSource).

package layout

import (
	"canon-mp280-driver/internal/image"
	"canon-mp280-driver/internal/jobstream"
	"canon-mp280-driver/internal/options"
)

// DefaultResolution is the dpi the original driver assumes for an image without one.
const DefaultResolution = 120

// Media types whose rows are mirrored (back print film, T-shirt transfer).
const (
	mediaBackPrintFilm = 2
	mediaTShirt        = 9
)

// MirrorsMedia reports whether pages on this media type (front-end media
// number) are printed mirrored.
func MirrorsMedia(media int) bool { return media == mediaBackPrintFilm || media == mediaTShirt }

// Input is everything the position code looks at.
type Input struct {
	Width, Height int64   // image size in pixels
	XRes, YRes    float64 // image resolution as the reader reports it, 0 = none

	ImageRes  int // --imageres, 0 = off
	BBox      *[4]int
	Fit       options.FitMode
	Percent   int
	Location  options.Location
	Extension int

	PrinterXRes, PrinterYRes float64 // dpi of the input grid (ComSystem XDPI/YDPI)

	// Paper geometry in 1/100 mm.
	PaperWidth, PaperLength int64
	TopMargin, LeftMargin   int64
	AreaWidth, AreaLength   int64

	Borderless bool
	PaperSize  int // front-end paper size id, for the over-margin query
	Duplex     int
	OverMargin *options.Database
}

// InputFor builds the Input from the option analysis and the image.
func InputFor(s *options.Settings, db *options.Database, com options.ComSystem, img image.Info) Input {
	return Input{
		Width: img.Width, Height: img.Height, XRes: img.XRes, YRes: img.YRes,
		ImageRes: s.ImageRes, BBox: s.BBox, Fit: s.Fit, Percent: s.Percent, Location: s.Location, Extension: s.Extension,
		PrinterXRes: float64(com.XDPI), PrinterYRes: float64(com.YDPI),
		PaperWidth: s.Paper.Width, PaperLength: s.Paper.Height,
		TopMargin: s.Paper.Margins.Top, LeftMargin: s.Paper.Margins.Left,
		AreaWidth: s.Paper.Margins.Width, AreaLength: s.Paper.Margins.Length,
		Borderless: s.Device.MarginType == jobstream.MarginBorderless,
		PaperSize:  s.Device.PaperSize, Duplex: s.Device.Duplex, OverMargin: db,
	}
}

// Plan is the placement result in pixels, rounded as the original driver rounds.
type Plan struct {
	MaskWidth, MaskHeight     int64 // source pixels used
	WidthOffset, HeightOffset int64 // first source column and row used
	OutWidth                  int64 // scaled image width (clamped to PageWidth-LeftSkip)
	OutHeight                 int64 // rows of image delivered
	LeftSkip, TopSkip         int64
	PageWidth                 int64
}

func roundUp(f float64) int64 {
	d := int64(f)
	if f == float64(d) {
		return d
	}
	return int64(f + 1)
}

// Compute is the position code of a page. An error means the original job
// ends here (over-margin lookup failure).
func Compute(in Input) (Plan, error) {
	imgW, imgH := float64(in.Width), float64(in.Height)

	// Image resolution. the original driver uses the x resolution for
	// y whenever the image has a y resolution.
	var imgX, imgY float64
	switch {
	case in.ImageRes != 0:
		imgX, imgY = float64(in.ImageRes), float64(in.ImageRes)
	default:
		imgX, imgY = in.XRes, in.XRes
		if in.XRes == 0 {
			imgX = DefaultResolution
		}
		if in.YRes == 0 {
			imgY = DefaultResolution
		}
	}
	prnX, prnY := in.PrinterXRes, in.PrinterYRes

	prnWidth, prnHeight := float64(in.AreaWidth), float64(in.AreaLength)
	paperW, paperL := float64(in.PaperWidth), float64(in.PaperLength)
	topM, leftM := float64(in.TopMargin), float64(in.LeftMargin)
	rightM := float64(in.PaperWidth - in.AreaWidth - in.LeftMargin)
	bottomM := float64(in.PaperLength - in.AreaLength - in.TopMargin)

	curW, curH := prnWidth, prnHeight
	var widthOff, heightOff, leftSkip, topSkip float64

	if in.Borderless {
		l, r, t, b, err := in.OverMargin.OverMargin(in.Extension, in.PaperSize, in.Duplex)
		if err != nil {
			return Plan{}, err
		}
		top, left, right, bottom := topM, leftM, rightM, bottomM
		if l < 0 {
			f := float64(in.Extension) / 3.0
			top, left, bottom, right = top*f, left*f, bottom*f, right*f
		} else {
			top, left, bottom, right = float64(-t), float64(-l), float64(-b), float64(-r)
		}
		curW = paperW - left - right
		curH = paperL - top - bottom
		// the original driver scales the top difference with the x resolution and the left one
		// with the y resolution.
		topSkip += (top - topM) * prnX / 2540.0
		leftSkip += (left - leftM) * prnY / 2540.0
	}

	if in.BBox != nil {
		bb := in.BBox
		left := roundUp(float64(bb[0]) * imgX / 72.0)
		bottom := roundUp(float64(bb[1]) * imgY / 72.0)
		right := int64(float64(bb[2]) * imgX / 72.0)
		top := int64(float64(bb[3]) * imgY / 72.0)
		w, h := right-left, top-bottom
		if w > 0 && h > 0 && right <= in.Width && top <= in.Height {
			widthOff = float64(left)
			heightOff = float64(in.Height - top)
			imgW, imgH = float64(w), float64(h)
		}
	}

	areaW, areaH := curW/2540.0, curH/2540.0
	var outW, outH float64
	switch in.Fit {
	case options.FitShort:
		outW, outH = areaW*prnX, areaH*prnY
		alpha := outW / imgW
		if tmp := alpha * imgH; tmp > outH {
			outW = outH / imgH * imgW
		} else {
			outH = tmp
		}
	case options.FitLong:
		outW, outH = areaW*prnX, areaH*prnY
		alpha := outW / imgW
		if tmp := alpha * imgH; tmp < outH {
			outW = outH / imgH * imgW
		} else {
			outH = tmp
		}
	default:
		outW = imgW / imgX * prnX
		outH = imgH / imgY * prnY
	}
	if in.Percent != 0 {
		outW = outW * float64(in.Percent) / 100.0
		outH = outH * float64(in.Percent) / 100.0
	}
	pw, ph := areaW*prnX, areaH*prnY
	if in.Location == options.Center {
		lskip, tskip := (pw-outW)/2.0, (ph-outH)/2.0
		if lskip < 0 {
			widthOff += lskip / outW * imgW * -1.0
		} else {
			leftSkip += lskip
		}
		if tskip < 0 {
			heightOff += tskip / outH * imgH * -1.0
		} else {
			topSkip += tskip
		}
	}
	maskW, maskH := imgW, imgH
	if outW > pw {
		maskW = imgW * pw / outW
		outW = pw
	}
	if outH > ph {
		maskH = imgH * ph / outH
		outH = ph
	}
	pageW := prnX * (prnWidth / 2540.0)

	p := Plan{
		MaskWidth: roundUp(maskW), MaskHeight: roundUp(maskH),
		WidthOffset: int64(widthOff), HeightOffset: int64(heightOff),
		OutWidth: roundUp(outW), OutHeight: roundUp(outH),
		LeftSkip: int64(leftSkip), TopSkip: int64(topSkip), PageWidth: int64(pageW),
	}
	if p.OutWidth+p.LeftSkip > p.PageWidth {
		p.OutWidth = p.PageWidth - p.LeftSkip
	}
	return p, nil
}
