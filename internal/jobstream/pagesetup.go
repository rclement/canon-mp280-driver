// SPDX-License-Identifier: GPL-2.0-only

package jobstream

import (
	"encoding/binary"
	"errors"
	"fmt"

	"canon-mp280-driver/internal/printdb"
)

const (
	userPaperSizeCode = 0x100 // database paper-size code of a custom size
	// formatWithSeparatedDB is the lowest table-format number that stores the
	// page-setup commands in the "separated" group-8 records (the MP280's is 18).
	formatWithSeparatedDB = 8
	maxSetupBytes         = 250 // room for the native command bytes of a page
)

// Group-8 records (sub ids): 2000 holds resolution and ink depth, 2001 the
// literal ESC ( d, t, c commands.
const (
	subSettingsRecord = 2000
	subCommandRecord  = 2001
)

// pageParams are the printer-side values derived from Settings: the option
// values the resource tables are keyed by, plus a few words the lookups
// produce.
type pageParams struct {
	q printdb.ResourceQuery // option values by id (media, quality, cartridge, ...)

	model         int16
	width, height int64 // custom paper size, 1/100 mm
	b41           int16 // extra key nibble of the group-8 records (0x7f: unused)

	// inkA and inkB identify the cartridge combination the group-8 record
	// selected; GetComSystem maps them back to a cartridge id.
	inkA, inkB int16
	// dpi[i] is the (x, y) resolution of table 1018 for resolution index i.
	dpi [2][2]int16
}

// PageSetup is the resolved page-setup for a job's Settings.
type PageSetup struct {
	// Commands are the bytes between the first-page prefix and the raster
	// layer: ESC ( d, t, c, p, l, u.
	Commands []byte
	// Geometry is the margin and size information (also inside Commands).
	Geometry Geometry

	// Database codes of the choices, in the form the raster stage needs.
	Media, Quality, Gray, Banner, MarginType int

	// XDPI and YDPI are the printer resolution of the page.
	XDPI, YDPI int
	// InkA and InkB identify the cartridge combination (see pageParams).
	InkA, InkB int

	params pageParams
}

// Resolve configures a job: it validates the settings and
// computes the page-setup commands from the printer database.
func Resolve(t *printdb.Table, s Settings) (*PageSetup, error) {
	if err := checkFormat(t); err != nil {
		return nil, err
	}
	u, err := s.translate()
	if err != nil {
		return nil, err
	}
	p, err := commonParams(t, u)
	if err != nil {
		return nil, err
	}
	cmds, geo, err := nativeCommands(t, p)
	if err != nil {
		return nil, err
	}
	if err := s.validateColour(); err != nil {
		return nil, err
	}
	return &PageSetup{
		Commands: cmds, Geometry: geo,
		Media: int(p.q[printdb.OptMedia]), Quality: int(p.q[printdb.OptQuality]),
		Gray: int(p.q[printdb.OptGrayscale]), Banner: int(p.q[printdb.OptBanner]),
		MarginType: int(p.q[printdb.OptMarginType]),
		XDPI:       int(p.dpi[0][0]), YDPI: int(p.dpi[0][1]),
		InkA: int(p.inkA), InkB: int(p.inkB),
		params: p,
	}, nil
}

// ErrDatabase is wrapped by errors from inconsistent printer tables.
var ErrDatabase = errors.New("jobstream: printer database")

// checkFormat verifies the database is one this package can drive (format
// 8 and up, as the MP280's 18) and that its basic tables are present.
func checkFormat(t *printdb.Table) error {
	r, err := t.Lookup(printdb.TableFormat, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if len(r.Data) < 32 || int32(binary.LittleEndian.Uint32(r.Data[28:])) > 0 ||
		(r.Status != 0 && r.Status != 2) {
		return fmt.Errorf("%w: bad format table", ErrDatabase)
	}
	all, err := t.Lookup(printdb.TableAllOptions, nil)
	if err != nil || all.Count == 0 || all.Status != 0 {
		return fmt.Errorf("%w: option table missing", ErrDatabase)
	}
	f, err := t.FormatType()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDatabase, err)
	}
	if f < formatWithSeparatedDB {
		return fmt.Errorf("%w: table format %d not supported", ErrDatabase, f)
	}
	return nil
}

// commonParams computes the common parameters for the separated-database
// formats: it fills the option values and looks up the dither class and the
// resolutions.
func commonParams(t *printdb.Table, u DBChoices) (pageParams, error) {
	var p pageParams
	p.model = int16(u.Model)
	p.width, p.height = u.Width, u.Height
	q := &p.q
	q[printdb.OptMedia] = int16(u.Media)
	q[printdb.OptGrayscale] = int16(u.Gray)
	q[printdb.OptBanner] = int16(u.Banner)
	q[printdb.OptCartridge] = int16(u.Cartridge)
	q[printdb.OptPaperSize] = int16(u.PaperSize)
	q[printdb.OptSupply] = int16(u.Supply)
	q[printdb.OptQuality] = int16(u.Quality)
	q[printdb.OptQualityLevel] = 0
	q[printdb.OptMarginType] = int16(u.Margin)
	q[printdb.OptDuplex] = int16(u.Duplex)
	q[printdb.OptPaperGap] = int16(u.Gap)
	q[printdb.OptSlot41] = 0x7f
	q[printdb.OptSlot43] = 99
	q[printdb.OptSlot44], q[printdb.OptSlot45], q[printdb.OptSlot46] = 0, 0, 0
	p.b41 = 0x7f
	if u.Slot43 != 0 {
		q[printdb.OptSlot43] = int16(u.Slot43)
	}
	if u.Model == 0xfd {
		p.b41 = 0
	}
	if u.Model == 0xbb || u.Model == 0xbf {
		q[printdb.OptSlot43] = 0x32
	}

	// Dither class from the halftoning code.
	var dq printdb.ResourceQuery
	dq[printdb.OptDither] = int16(u.Dither)
	r, err := t.Lookup(printdb.TableDitherClass, &dq)
	if err != nil || r.Count != 1 || len(r.Data) < 2 {
		return p, fmt.Errorf("%w: no dither class for halftoning %d", ErrDatabase, u.Dither)
	}
	if r.Status != 0 {
		return p, fmt.Errorf("%w: dither class status %d", ErrDatabase, r.Status)
	}
	q[printdb.OptDitherClass] = int16(binary.LittleEndian.Uint16(r.Data))

	// Resolution: the group-8 settings record picks two resolution indices,
	// table 1018 turns each into (x, y) dpi.
	fake, err := substituted(t, p)
	if err != nil {
		return p, err
	}
	rec, err := separatedRecord(t, subSettingsRecord, fake)
	if err != nil {
		return p, err
	}
	if len(rec) < 8 {
		return p, fmt.Errorf("%w: short settings record", ErrDatabase)
	}
	w := printdb.Int16s(rec[:8])
	p.inkA, p.inkB = w[0], w[1]
	for i := 0; i < 2; i++ {
		var rq printdb.ResourceQuery
		rq[printdb.OptDither] = int16(u.Dither)
		rq[printdb.OptResolution] = w[2+i]
		rr, err := t.Lookup(printdb.TableResolution, &rq)
		if err != nil || rr.Count != 1 || rr.Status != 0 || len(rr.Data) < 4 {
			return p, fmt.Errorf("%w: no resolution for index %d", ErrDatabase, w[2+i])
		}
		d := printdb.Int16s(rr.Data[:4])
		p.dpi[i] = [2]int16{d[0], d[1]}
	}
	return p, nil
}

// substituted returns the parameters after the model's field substitutions
// (table 1037), which the group-8 lookups are keyed by. The MP280's table
// only holds the "no substitution" marker.
func substituted(t *printdb.Table, p pageParams) (pageParams, error) {
	f := p
	r, err := t.Lookup(printdb.TableSubstitution, &p.q)
	if err != nil {
		return f, nil // no substitution table: nothing to apply
	}
	pairs := printdb.Int16s(r.Data)
	if len(pairs) < 2*r.Count {
		return f, fmt.Errorf("%w: short substitution table", ErrDatabase)
	}
	if r.Count == 1 && pairs[0] == -0x8000 && pairs[1] == -0x8000 {
		return f, nil
	}
	for i := 0; i < r.Count; i++ {
		id, val := pairs[2*i], pairs[2*i+1]
		switch printdb.OptionID(id) {
		case printdb.OptMedia, printdb.OptQuality, printdb.OptQualityLevel, printdb.OptSupply, printdb.OptPaperSize,
			printdb.OptBanner, printdb.OptCartridge, printdb.OptGrayscale, printdb.OptDitherClass,
			printdb.OptMarginType, printdb.OptDuplex, printdb.OptPaperGap, printdb.OptSlot41,
			printdb.OptSlot43, printdb.OptSlot44, printdb.OptSlot45, printdb.OptSlot46:
			f.q[id] = val
		default:
			return f, fmt.Errorf("%w: substitution of unknown field %d", ErrDatabase, id)
		}
	}
	return f, nil
}

// separatedRecord looks up the group-8 record for the parameters. The records
// are keyed by two u32 words that pack the option values:
//
//	key1  bits 0-4 quality code, 5-13 dither class, 14-15 grayscale,
//	      16-20 cartridge, 21-22 margin type, 23-24 duplex, 25-28 extra nibble
//	key2  media code
//
// A missing key means the combination is not printable.
func separatedRecord(db *printdb.Table, sub int, p pageParams) ([]byte, error) {
	e, err := db.Entry(printdb.GroupOptions, sub)
	if err != nil || len(e) < 16 {
		return nil, fmt.Errorf("%w: group 8 record %d unavailable", ErrDatabase, sub)
	}
	n := binary.LittleEndian.Uint32(e)
	if binary.LittleEndian.Uint32(e[4:])&0xffff != 0 {
		return nil, fmt.Errorf("%w: unknown record structure", ErrDatabase)
	}
	hdr := (2 + uint64(n) - 1) * 4
	if hdr+8 > uint64(len(e)) {
		return nil, fmt.Errorf("%w: group 8 record %d truncated", ErrDatabase, sub)
	}
	nrec := uint64(binary.LittleEndian.Uint32(e[hdr:]))
	rlen := uint64(binary.LittleEndian.Uint32(e[hdr+4:]))

	var k1 uint32
	if uint16(p.b41) != 0x7f {
		k1 = uint32(uint16(p.b41)) << 25
	}
	k1 &= 0x1e000000
	field := func(id printdb.OptionID, mask uint32, shift uint) uint32 {
		return (uint32(int32(p.q[id])) & mask) << shift
	}
	k1 |= field(printdb.OptDuplex, 3, 23) | field(printdb.OptMarginType, 3, 21) | field(printdb.OptCartridge, 0x1f, 16) |
		field(printdb.OptGrayscale, 3, 14) | field(printdb.OptDitherClass, 0x1ff, 5) | field(printdb.OptQuality, 0x1f, 0)
	k2 := uint32(int32(p.q[printdb.OptMedia]))

	for i := uint64(0); i < nrec; i++ {
		o := hdr + 8 + 4*rlen*i
		if o+4*rlen > uint64(len(e)) {
			return nil, fmt.Errorf("%w: group 8 record %d truncated", ErrDatabase, sub)
		}
		if binary.LittleEndian.Uint32(e[o:]) == k1 && binary.LittleEndian.Uint32(e[o+4:]) == k2 {
			return e[o+8 : o+4*rlen], nil
		}
	}
	return nil, fmt.Errorf("%w: unprintable combination (group 8 record %d)", ErrDatabase, sub)
}

// nativeCommands builds the page-setup command bytes: the literal ESC ( d, t,
// c commands of the group-8 record, the ESC ( p geometry, then the byte strings
// of table 1027 (ESC ( l, ESC ( u).
func nativeCommands(t *printdb.Table, p pageParams) ([]byte, Geometry, error) {
	fake, err := substituted(t, p)
	if err != nil {
		return nil, Geometry{}, err
	}
	rec, err := separatedRecord(t, subCommandRecord, fake)
	if err != nil {
		return nil, Geometry{}, err
	}
	if len(rec) == 0 || rec[0] > 0x7f || int(rec[0]) >= len(rec) {
		return nil, Geometry{}, fmt.Errorf("%w: bad command record", ErrDatabase)
	}
	out := append([]byte(nil), rec[1:1+int(rec[0])]...)

	geo, err := PageGeometry(t, &p.q, p.width, p.height)
	if err != nil {
		return nil, Geometry{}, err
	}
	out = append(out, 0x1b, '(', 'p', byte(len(geo.EscP)), 0)
	out = append(out, geo.EscP[:]...)

	list, err := t.Lookup(printdb.TableSetupStrings, &p.q)
	if err != nil || list.Count == 0 || list.Status != 0 {
		return nil, Geometry{}, fmt.Errorf("%w: no setup string list", ErrDatabase)
	}
	for _, id := range printdb.Int16s(list.Data) {
		if id == 0 {
			break
		}
		s, err := t.Lookup(int(id), &p.q)
		if err != nil || s.Count == 0 || s.Status != 0 || len(s.Data) == 0 {
			return nil, Geometry{}, fmt.Errorf("%w: setup string table %d", ErrDatabase, id)
		}
		n := int(s.Data[0])
		if len(out)+n > maxSetupBytes || 1+n > len(s.Data) {
			return nil, Geometry{}, fmt.Errorf("%w: setup string too long", ErrDatabase)
		}
		out = append(out, s.Data[1:1+n]...)
	}
	// Table 1010 must have an entry for the combination.
	ex, err := t.Lookup(printdb.TableExists, &p.q)
	if err != nil || ex.Count == 0 || ex.Status != 0 {
		return nil, Geometry{}, fmt.Errorf("%w: combination missing from table 1010", ErrDatabase)
	}
	return out, geo, nil
}

// Margins is the printable area of a paper size in 1/100 mm.
type Margins struct {
	Top, Left     int64 // distance from the paper edge to the printable area
	Width, Length int64 // size of the printable area
}

// Geometry is the page geometry of a paper size: the margins plus the 46-byte
// ESC ( p payload that tells the printer the paper and print-area size in
// device units.
type Geometry struct {
	Margins Margins
	// EscP is the payload of ESC ( p: u16 a0..a3 (printable length/width in
	// 1/60 inch ...), u32 b0, u16 b1 (resolution), u32 b2..b9 (left and top
	// margin, printable and paper size in pixels), all big endian.
	EscP [46]byte
}

// PageGeometry looks up the geometry of q's paper size. For a custom size
// (code 0x100) width and height (1/100 mm) must lie inside the model's limits
// and the record is recomputed from them.
func PageGeometry(t *printdb.Table, q *printdb.ResourceQuery, width, height int64) (Geometry, error) {
	var g Geometry
	user := q[printdb.OptPaperSize] == userPaperSizeCode
	if user {
		blob, err := t.Lookup(printdb.TableFormat, q)
		if err != nil || len(blob.Data) < 24 {
			return g, fmt.Errorf("%w: no size limits", ErrDatabase)
		}
		lim := printdb.Int32s(blob.Data[8:24])
		if width < int64(lim[0]) || width > int64(lim[1]) || height < int64(lim[2]) || height > int64(lim[3]) {
			return g, fmt.Errorf("%w: custom size %dx%d outside %v", ErrInvalidSetting, width, height, lim)
		}
	}
	rec, err := t.Lookup(geometryTable, q)
	if err != nil || rec.Count != 1 || (rec.Status != 0 && rec.Status != 2) || len(rec.Data) < 23*4 {
		return g, fmt.Errorf("%w: no geometry for paper size %d", ErrDatabase, q[printdb.OptPaperSize])
	}
	r64 := make([]int64, 23)
	for i, v := range printdb.Int32s(rec.Data[:23*4]) {
		r64[i] = int64(v)
	}
	r := r64
	a := [4]uint64{uint64(uint16(r[9])), uint64(uint16(r[10])), uint64(uint16(r[11])), uint64(uint16(r[12]))}
	var b [10]uint64
	b[0] = uint64(uint32(r[13]))
	b[1] = uint64(uint16(r[14]))
	for i := 2; i < 10; i++ {
		b[i] = uint64(uint32(r[13+i]))
	}
	g.Margins = Margins{Top: r[7] - r[3], Left: r[6] - r[2], Width: r[4], Length: r[5]}
	if user {
		m := r[6] - r[2]
		tb := r[7] - r[3]
		w4 := min(width-2*m, r[4])
		h5 := height - (tb + r[8])
		u6 := uint64(uint16(ceilDiv(r[4]*6, 0xfe)))
		a[0] = uint64(uint16(ceilDiv(h5*6, 0xfe)))
		a[2] = uint64(uint16(ceilDiv(w4*6, 0xfe)))
		if u6 < a[2] {
			a[2] = u6
		}
		dpi := r[14]
		b[2], b[3] = uint64(uint32(r[15])), uint64(uint32(r[16]))
		b[4] = uint64(uint32(ceilDiv(w4*dpi, hundredthsMMPerInch)))
		b[5] = uint64(uint32(ceilDiv(h5*dpi, hundredthsMMPerInch)))
		b[6], b[7] = uint64(uint32(r[19])), uint64(uint32(r[20]))
		b[8] = uint64(uint32(ceilDiv(width*dpi, hundredthsMMPerInch)))
		b[9] = uint64(uint32(ceilDiv(height*dpi, hundredthsMMPerInch)))
		g.Margins = Margins{Top: tb, Left: m, Width: w4, Length: h5}
	}
	w := g.EscP[:0]
	for _, v := range a {
		w = binary.BigEndian.AppendUint16(w, uint16(v))
	}
	w = binary.BigEndian.AppendUint32(w, uint32(b[0]))
	w = binary.BigEndian.AppendUint16(w, uint16(b[1]))
	for _, v := range b[2:] {
		w = binary.BigEndian.AppendUint32(w, uint32(v))
	}
	return g, nil
}

// geometryTable is the id the geometry lookup starts at (table 1003, which
// indirects to the normal or borderless record table).
const geometryTable = printdb.TableGeometry

// hundredthsMMPerInch is 25.4 mm in 1/100 mm.
const hundredthsMMPerInch = 2540

// ceilDiv returns "x/d + 1 unless x is a multiple of d": the number of
// whole units needed, counting an exact multiple once.
func ceilDiv(x, d int64) int64 {
	n := x/d + 1
	if x%d == 0 {
		n--
	}
	return n
}

// Common is the part of the page setup that depends only on the choices: the
// cartridge-combination words and the printer resolution.
type Common struct {
	// InkA and InkB identify the cartridge combination of the printer's
	// settings record.
	InkA, InkB int
	// XDPI and YDPI are the printer resolution of the page.
	XDPI, YDPI int
}

// ResolveCommon runs the common-parameter step for database-valued choices.
// The front end uses it to learn the cartridge and resolution a selection
// implies before it lays the page out.
func ResolveCommon(t *printdb.Table, c DBChoices) (Common, error) {
	if err := checkFormat(t); err != nil {
		return Common{}, err
	}
	p, err := commonParams(t, c)
	if err != nil {
		return Common{}, err
	}
	return Common{InkA: int(p.inkA), InkB: int(p.inkB), XDPI: int(p.dpi[0][0]), YDPI: int(p.dpi[0][1])}, nil
}
