// SPDX-License-Identifier: GPL-2.0-only

package printdb

// Resource tables: the "resource tables" of the printer database
// (group 12 of cnb_*.tbl). These small tables describe a
// printer model as data: which options exist, which combinations are allowed,
// the page-geometry records, the byte strings of some setup commands.
//
// A table is addressed by a numeric id (1000 and up) and answered against a
// ResourceResourceQuery: an array of option values indexed by a key slot. A slot is the same
// number as the option id (Opt* constants), so a ResourceQuery is simply "the current
// value of every option".
//
// Table layout (all little endian). The payload starts with a 48-byte header
// of u16 words:
//
//	+0  type    0 = indirection, 1 = raw blob, 2 = first matching record,
//	            3 = all matching records
//	+2  status  0 normal, 2 also normal for some tables, others are errors
//	+4  keys    number of key columns per record
//	+6  elems   data elements per record
//	+8  size    bytes per data element
//	+10 records number of records
//	+16 key slots: keys x u16, the ResourceQuery index compared against each column
//	    (for blobs, a u32 at +16 holds the blob size instead)
//
// followed (offset 48) by the records: keys x s16 key values (0x8000 matches
// anything) and elems x size bytes of data. An indirection table's single
// matching record holds the id of the table to continue with.

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// OptionID is the number of an option in the resource tables. The numbers are the
// database's own object ids, which differ from the option ids the front end uses
// (see package options).
type OptionID int

// Option ids used as ResourceQuery slots.
const (
	OptMedia         OptionID = 1    // media type
	OptQuality       OptionID = 3    // print-quality code (0, 5, 10, 15, 20, 30)
	OptQualityLevel  OptionID = 4    // always 0 for this printer
	OptSupply        OptionID = 5    // paper source
	OptPaperSize     OptionID = 10   // paper size code, 0x100 = user defined
	OptDitherPattern OptionID = 0x11 // halftoning choice offered in the UI
	OptBanner        OptionID = 0x13
	OptCartridge     OptionID = 0x14
	OptGrayscale     OptionID = 0x15
	OptDitherClass   OptionID = 0x16 // dither class derived from the halftoning choice
	OptMarginType    OptionID = 0x23 // 0 normal, 1 borderless
	OptDuplex        OptionID = 0x26
	OptPaperGap      OptionID = 0x27
	OptSlot41        OptionID = 0x29
	OptSlot43        OptionID = 0x2b // 99 for this printer
	OptSlot44        OptionID = 0x2c
	OptSlot45        OptionID = 0x2d
	OptSlot46        OptionID = 0x2e
	OptChangedID     OptionID = 25 // slot used by the dependency engine: option the user changed
	OptChangedValue  OptionID = 26 // ... and the value it was given
	OptResolution    OptionID = 27 // resolution index (tables 1018)
	OptDither        OptionID = 28 // halftoning code (2560 pattern, 2595 error diffusion)
)

// Table numbers (the database calls them 1000 + n).
const (
	TableFormat        = 1000 // blob: format type and custom-size limits
	TableAllOptions    = 1001 // every selectable (option, value, kind)
	TableDefaults      = 1002
	TableGeometry      = 1003 // selects the page-geometry table
	TableExists        = 1010 // existence check only
	TableResolution    = 1018 // resolution index -> dpi
	TableDitherClass   = 1019
	TableDependencies  = 1020 // option dependency engine entry
	TableSetupStrings  = 1027 // list of byte-string tables (ESC ( l, ESC ( u)
	TableSubstitution  = 1037 // field substitutions applied before the group-8 lookup
	TableOverMargin    = 1043
	TableSupplySizes   = 1046
	TableSupplyLimits  = 1048
	TableDuplexSizes   = 1055
	maxIndirectionHops = 16
)

// QuerySize is the number of slots in a ResourceQuery.
const QuerySize = 256

// ResourceQuery holds the value of each option by slot. Unused slots are zero.
type ResourceQuery [QuerySize]int16

// ResourceResult is the outcome of a table lookup.
type ResourceResult struct {
	Status int16  // header status word
	Count  int    // matching records (types 2 and 3)
	Data   []byte // blob, or the concatenated data of the matching records
}

// ErrNoMatch is returned when a table has no matching record.
var ErrNoMatch = errors.New("printdb: no matching record")

// Lookup finds table id and answers it for q (nil behaves as the zero ResourceQuery).
func (t *Table) Lookup(id int, q *ResourceQuery) (ResourceResult, error) {
	if q == nil {
		q = new(ResourceQuery)
	}
	for hop := 0; hop < maxIndirectionHops; hop++ {
		p, err := t.Entry(GroupModes, id)
		if err != nil {
			return ResourceResult{}, err
		}
		if len(p) < 48 {
			return ResourceResult{}, fmt.Errorf("printdb: table %d truncated", id)
		}
		typ := binary.LittleEndian.Uint16(p)
		status := int16(binary.LittleEndian.Uint16(p[2:]))
		if typ == 1 {
			n := int(binary.LittleEndian.Uint32(p[16:]))
			if 48+n > len(p) {
				return ResourceResult{}, fmt.Errorf("printdb: blob %d truncated", id)
			}
			return ResourceResult{Status: status, Data: p[48 : 48+n]}, nil
		}
		data, count, err := matchRecords(p, q, id)
		if err != nil {
			return ResourceResult{}, err
		}
		if typ == 0 {
			if count != 1 {
				return ResourceResult{}, fmt.Errorf("%w: table %d", ErrNoMatch, id)
			}
			id = int(binary.LittleEndian.Uint16(data))
			continue
		}
		return ResourceResult{Status: status, Count: count, Data: data}, nil
	}
	return ResourceResult{}, fmt.Errorf("printdb: indirection loop at table %d", id)
}

func matchRecords(p []byte, q *ResourceQuery, id int) (data []byte, count int, err error) {
	typ := binary.LittleEndian.Uint16(p)
	nkeys := int(binary.LittleEndian.Uint16(p[4:]))
	dsize := int(binary.LittleEndian.Uint16(p[6:])) * int(binary.LittleEndian.Uint16(p[8:]))
	nrec := int(binary.LittleEndian.Uint16(p[10:]))
	rsize := 2*nkeys + dsize
	if 16+2*nkeys > 48 || 48+nrec*rsize > len(p) {
		return nil, 0, fmt.Errorf("printdb: table %d malformed", id)
	}
	for r := 0; r < nrec; r++ {
		rec := p[48+r*rsize:]
		ok := true
		for k := 0; k < nkeys && ok; k++ {
			kv := int16(binary.LittleEndian.Uint16(rec[2*k:]))
			if kv == -0x8000 {
				continue
			}
			slot := int(binary.LittleEndian.Uint16(p[16+2*k:]))
			var have int16
			if slot < QuerySize {
				have = q[slot]
			}
			ok = kv == have
		}
		if !ok {
			continue
		}
		data = append(data, rec[2*nkeys:rsize]...)
		count++
		if typ == 0 || typ == 2 {
			break
		}
	}
	return data, count, nil
}

// FormatType returns the table-format number of the database (18 for the MP280).
func (t *Table) FormatType() (int, error) {
	r, err := t.Lookup(TableFormat, nil)
	if err != nil {
		return 0, err
	}
	if r.Status != 0 && r.Status != 2 {
		return 0, fmt.Errorf("printdb: format table status %d", r.Status)
	}
	if len(r.Data) < 26 {
		return 0, errors.New("printdb: format table too short")
	}
	return int(binary.LittleEndian.Uint16(r.Data[24:])), nil
}

// Int16s decodes little-endian s16 values.
func Int16s(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

// Int32s decodes little-endian s32 values.
func Int32s(b []byte) []int32 {
	out := make([]int32, len(b)/4)
	for i := range out {
		out[i] = int32(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
}
