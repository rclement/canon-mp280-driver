// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"canon-mp280-driver/internal/printdb"
	"errors"
	"fmt"

	"canon-mp280-driver/internal/jobstream"
)

// supplyOptions asks which options are valid for a paper source (table 1046
// and its sub-tables) and returns them with the custom-size limits of that
// source.
func (d *Database) supplyOptions(in []triple) (out []triple, limits [4]int64, err error) {
	if d.format < 7 {
		return nil, limits, errors.New("options: paper-source table needs format 7")
	}
	var q printdb.ResourceQuery
	for _, t := range in {
		if t.id == 5 && (t.val == 0x1f || t.val == 0x1d || t.val == 0x24) {
			return nil, limits, errors.New("options: unsupported paper source")
		}
		q[t.id] = t.val
	}
	seq, err := d.tables.Lookup(printdb.TableSupplySizes, &q)
	if err != nil || seq.Count == 0 || seq.Status != 0 {
		return nil, limits, errors.New("options: paper-source table missing")
	}
	ids := printdb.Int16s(seq.Data)
	for k := 0; k < len(ids) && k < 20 && ids[k] != 0; k++ {
		r, err := d.tables.Lookup(int(ids[k]), &q)
		if err != nil || r.Count == 0 || r.Status != 0 {
			return nil, limits, fmt.Errorf("options: paper-source sub-table %d", ids[k])
		}
		if int(ids[k]) == printdb.TableSupplyLimits {
			for i, v := range printdb.Int32s(r.Data[:16]) {
				limits[i] = int64(v)
			}
			continue
		}
		out = append(out, triples(r.Data, r.Count)...)
	}
	return out, limits, nil
}

// SizeRangeError is returned for a custom paper size outside the printer's
// limits.
type SizeRangeError struct {
	WidthBad, HeightBad bool
	Limits              [4]int64 // min/max width, min/max length (1/100 mm)
}

func (e *SizeRangeError) Error() string {
	switch {
	case e.WidthBad && e.HeightBad:
		return "paper width and height out of range"
	case e.WidthBad:
		return fmt.Sprintf("invalid paperwidth value (integer: %d - %d [1/100mm])", e.Limits[0], e.Limits[1])
	}
	return fmt.Sprintf("invalid paperheight value (integer: %d - %d [1/100mm])", e.Limits[2], e.Limits[3])
}

// PaperSizeOf returns the paper size of a front-end paper size id in 1/100 mm.
func PaperSizeOf(id int) (w, h int64, ok bool) {
	for _, p := range paperSizes {
		if p[0] == int64(id) {
			return p[1], p[2], true
		}
	}
	return 0, 0, false
}

// MarginInfo is the answer to a paper-size query.
type MarginInfo struct {
	Limits        [4]int64 // custom size limits that apply
	Width, Height int64    // paper size, 1/100 mm
	jobstream.Margins
}

// Margin returns the printable area of the currently selected paper size. For
// a custom size customW and customH (1/100 mm) are checked against the limits.
func (d *Database) Margin(customW, customH int64) (MarginInfo, error) {
	m := MarginInfo{Limits: d.limits}
	if d.format > 6 {
		_, lim, err := d.supplyOptions([]triple{{5, 7, 1}})
		if err != nil {
			return m, err
		}
		m.Limits = lim
	}
	var q printdb.ResourceQuery
	cur := func(id int16) int16 { return d.current(id) }
	orZero := func(v int16) int16 {
		if v == -1 {
			return 0
		}
		return v
	}
	size := cur(10)
	q[1], q[5], q[10] = cur(1), cur(5), size
	q[0x13] = cur(0x13)
	q[0x23] = orZero(cur(0x23))
	q[0x26] = orZero(cur(0x26))
	q[0x2c], q[0x2d] = 0, 0
	if size == 0x100 {
		var bad SizeRangeError
		bad.Limits = m.Limits
		bad.WidthBad = customW < m.Limits[0] || customW > m.Limits[1]
		bad.HeightBad = customH < m.Limits[2] || customH > m.Limits[3]
		if bad.WidthBad || bad.HeightBad {
			return m, &bad
		}
		m.Width, m.Height = customW, customH
	} else {
		w, h, ok := PaperSizeOf(int(fromTableValue(10, size)))
		if !ok {
			return m, errors.New("options: unknown paper size")
		}
		m.Width, m.Height = w, h
	}
	g, err := jobstream.PageGeometry(d.tables, &q, m.Width, m.Height)
	if err != nil {
		return m, err
	}
	m.Margins = g.Margins
	return m, nil
}

// Device is the selection the front end hands to ComSystem, in the front-end
// vocabulary.
type Device struct {
	Model       int // front-end model id (1370)
	Ink         int // cartridge option value
	Media       int
	PaperSize   int
	Supply      int
	Dither      int // halftoning option value (0 pattern .. 3 error diffusion)
	Gray        int
	Quality     int
	Banner      int
	MarginType  int
	PaperGap    int
	Duplex      int
	InkSettings int // cartridge-settings option value (6 = not applicable)

	PaperWidth, PaperHeight int64 // custom size, 1/100 mm
}

// ComSystem is the printer-side interpretation of a Device.
type ComSystem struct {
	InkType     int // cartridge as the job interface names it (1001..1006)
	XDPI, YDPI  int // printer resolution
	InkSettings int // cartridge settings as the job interface names them
}

// ComSystem works out the cartridge type and printer resolution implied by a
// selection.
func (d *Database) ComSystem(dev Device) (ComSystem, error) {
	all, err := d.enumAll()
	if err != nil {
		return ComSystem{}, err
	}
	model, _ := tableModel(dev.Model)
	t := func(obj Object, v int) int { return int(toTableValue(int16(obj), int16(v))) }
	c := jobstream.DBChoices{
		Model:     model,
		Media:     t(ObjMedia, dev.Media),
		Quality:   t(ObjQuality, dev.Quality),
		Supply:    t(ObjSupply, dev.Supply),
		PaperSize: t(ObjPaperSize, dev.PaperSize),
		Dither:    t(ObjDither, dev.Dither),
		Banner:    t(ObjBanner, dev.Banner),
		Cartridge: t(ObjCartridge, dev.Ink),
		Gray:      t(ObjGrayscale, dev.Gray),
		Margin:    t(ObjMarginType, dev.MarginType),
		Gap:       t(ObjPaperGap, dev.PaperGap),
		Duplex:    t(ObjDuplex, dev.Duplex),
		Width:     dev.PaperWidth,
		Height:    dev.PaperHeight,
	}
	if c.PaperSize != 0x100 {
		c.Width, c.Height = 0, 0
	}
	for _, tr := range all {
		if tr.id == 0x2b && tr.val == 0x32 {
			c.Slot43 = 0x32
			break
		}
	}
	com, err := jobstream.ResolveCommon(d.tables, c)
	if err != nil {
		return ComSystem{}, err
	}
	cs := ComSystem{XDPI: com.XDPI, YDPI: com.YDPI}
	cs.InkType = -1
	for _, r := range cartridgeIDs {
		if int(r[1]) == com.InkA && int(r[2]) == com.InkB {
			cs.InkType = int(r[0])
			break
		}
	}
	if cs.InkType == -1 {
		cs.InkType = c.Cartridge + 1000
	}
	cs.InkSettings = 0x14
	for _, p := range inkTypeMap {
		if int(p[0]) == dev.InkSettings {
			cs.InkSettings = int(p[1])
			break
		}
	}
	return cs, nil
}

// OverMargin returns how far the print area may extend beyond the paper
// (left, right, top, bottom in 1/100 mm; -1 where the model does not say) for
// borderless printing at the given extension level.
func (d *Database) OverMargin(extension, paperSize, duplex int) (left, right, top, bottom int, err error) {
	left, right, top, bottom = -1, -1, -1, -1
	if d.format < 6 {
		return
	}
	var q printdb.ResourceQuery
	q[0x26] = toTableValue(int16(ObjDuplex), int16(duplex))
	q[10] = toTableValue(int16(ObjPaperSize), int16(paperSize))
	q[0x32] = toTableValue(int16(ObjOverMargin), int16(extension))
	r, err := d.tables.Lookup(printdb.TableOverMargin, &q)
	if err != nil || r.Count == 0 || r.Status != 0 {
		return -1, -1, -1, -1, errors.New("options: over-margin table")
	}
	for _, t := range triples(r.Data, r.Count) {
		switch t.id {
		case 0x33:
			left = int(t.val)
		case 0x34:
			right = int(t.val)
		case 0x35:
			top = int(t.val)
		case 0x36:
			bottom = int(t.val)
		default:
			return -1, -1, -1, -1, errors.New("options: unknown over-margin item")
		}
	}
	return
}

// confirm turns the kind of an item into 1 (usable), 0 (not usable) or -1.
func confirm(kind int16) int {
	switch {
	case usable(kind):
		return 1
	case kind == kindUnavailable:
		return 0
	}
	return 0
}

// ConfirmSupplySize reports whether the selected paper size can be fed from
// the selected paper source: 1 yes, 0 no.
func (d *Database) ConfirmSupplySize() int {
	size := d.current(10)
	sup := d.current(5)
	switch sup {
	case 0x1f:
		sup = 0x1c
	case 0x24:
		sup = 7
	}
	list, _, err := d.supplyOptions([]triple{{5, sup, 1}})
	if err != nil {
		return -1
	}
	for _, t := range list {
		if t.id == 10 && t.val == size {
			return confirm(t.kind)
		}
	}
	// The original reads the entry past the list, which is zero (unavailable).
	return 0
}

// ConfirmBlackAdjustMedia reports whether the selected media allows the
// black-and-white tone adjustment.
func (d *Database) ConfirmBlackAdjustMedia() bool {
	all, err := d.enumAll()
	if err != nil {
		return false
	}
	mv := d.current(1)
	for _, t := range all {
		if t.id == 0x44 && t.val == mv {
			return true
		}
	}
	return false
}

// ConfirmCartridgeMedia reports whether the current media and margin type can
// be printed with the black-only cartridge setting: 1 yes, 0 no, -1 error.
func (d *Database) ConfirmCartridgeMedia() int {
	in, _ := d.dependencyInput(1, -1, true)
	out, err := d.dependencies(in, true)
	if err != nil {
		return -1
	}
	mv := d.current(0x23)
	for _, t := range out {
		if t.id == 0x23 && t.val == mv {
			if t.kind == kindChanged || t.kind > kindEnableQuiet || t.kind < 0 {
				return -1
			}
			return confirm(t.kind)
		}
	}
	return 0
}
