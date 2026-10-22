// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"

	"canon-mp280-driver/internal/printdb"
)

// entry is one selectable value of one option. The database keeps the entries
// of an option next to each other; the last entry of a block is where a value
// that has no entry of its own (a free value) is stored.
type entry struct {
	obj, val int16
	isDef    bool // default value
	isCur    bool // currently selected
	isTmp    bool // selected by the caller, not yet applied
	disabled bool // not selectable in the current context
}

// Kinds of an (option, value) triple in the printer tables.
const (
	kindUnavailable = 0
	kindSelect      = 1
	kindEnable      = 2
	kindChanged     = 3 // input only: the item the user changed
	kindSelectQuiet = 4
	kindEnableQuiet = 5
)

// triple is (table option id, table value, kind).
type triple struct{ id, val, kind int16 }

// usable reports whether a combination of this kind may be printed.
func usable(kind int16) bool {
	return kind == kindSelect || kind == kindEnable || kind == kindSelectQuiet || kind == kindEnableQuiet
}

// dummyObjects are table object ids that the database holds with value 0 only
// to keep the object numbering complete.
var dummyObjects = []int16{11, 12, 13, 14, 15, 29, 30, 31, 32, 33, 67}

// Database is the option database of one printer model: every selectable
// value of every option, which one is the default, which is selected, and
// the dependency rules that keep the selection consistent.
type Database struct {
	tables     *printdb.Table
	format     int // table format number (18 for the MP280)
	formatType int // front-end format type (11 for the MP280)
	entries    []entry

	limits [4]int64 // custom paper size limits: min/max width, min/max length (1/100 mm)
}

// ErrNoModel is returned when the model id is not in the name table.
var ErrNoModel = errors.New("options: printer model not found")

// tableModel converts a front-end model id to the database model number.
func tableModel(id int) (int, bool) {
	if id > 999 {
		return id - 1000, true
	}
	v, ok := modelIDMap.forward(int16(id))
	return int(v), ok
}

// databaseFile reads the model's name table (cnbpname<N>.tbl) and returns the
// file name of its option database (e.g. cnb_3700.tbl).
func databaseFile(dir string, model int) (string, error) {
	nt, err := printdb.Load(filepath.Join(dir, fmt.Sprintf("cnbpname%d.tbl", model)))
	if err != nil {
		return "", err
	}
	p, err := nt.Entry(printdb.GroupOptions, model+1000)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoModel, err)
	}
	if len(p) < 0x14 || p[0] != 1 || p[1] != 0 || p[2] != 0 || p[3] != 0 {
		return "", fmt.Errorf("%w: bad name record", ErrNoModel)
	}
	name := p[4:20]
	for i, c := range name {
		if c == 0 {
			name = name[:i]
			break
		}
	}
	return string(name) + ".tbl", nil
}

// Open loads the printer database of a model from the table directory and
// builds the option database with its defaults and initial dependencies
// applied. modelID is the front-end model id (1370 for the MP280).
func Open(dir string, modelID int) (*Database, error) {
	model, ok := tableModel(modelID)
	if !ok {
		return nil, fmt.Errorf("%w: id %d", ErrNoModel, modelID)
	}
	file, err := databaseFile(dir, model)
	if err != nil {
		return nil, err
	}
	t, err := printdb.Load(filepath.Join(dir, file))
	if err != nil {
		return nil, err
	}
	return New(t)
}

// New builds the option database from loaded resource tables.
func New(t *printdb.Table) (*Database, error) {
	d, err := newBare(t)
	if err != nil {
		return nil, err
	}
	if err := d.defaults(); err != nil {
		return nil, err
	}
	if err := d.menulink(true); err != nil {
		return nil, err
	}
	return d, nil
}

// newBare builds the entries without defaults or dependency processing.
func newBare(t *printdb.Table) (*Database, error) {
	d := &Database{tables: t}
	var err error
	if d.format, err = t.FormatType(); err != nil {
		return nil, err
	}
	for _, r := range formatTypes {
		if int(r[0]) == d.format {
			d.formatType = int(r[1])
		}
	}
	if d.formatType == 0 {
		return nil, fmt.Errorf("options: unknown table format %d", d.format)
	}
	all, err := d.enumAll()
	if err != nil {
		return nil, err
	}
	var extra14 []int16
	v3e := int16(-1)
	for _, tr := range all {
		d.entries = append(d.entries, entry{obj: tr.id, val: tr.val})
		switch tr.id {
		case 0x14:
			extra14 = append(extra14, tr.val)
		case 0x3e:
			v3e = tr.val
		}
	}
	for _, o := range dummyObjects {
		d.entries = append(d.entries, entry{obj: o})
	}
	if d.format > 8 {
		switch {
		case v3e == 0:
			d.entries = append(d.entries, entry{obj: 0x406, val: 10})
		case v3e > -1 && v3e < 3:
			for _, v := range extra14 {
				d.entries = append(d.entries, entry{obj: 0x406, val: v})
			}
		}
	}
	for i := range d.entries {
		e := &d.entries[i]
		co, cv := tableObjectToFront(e.obj), fromTableValue(e.obj, e.val)
		if co == int16(ObjDuplex) && d.format < 9 {
			e.obj, e.val = -1, -1
		} else {
			e.obj, e.val = co, cv
		}
	}
	return d, nil
}

// FormatType is the front end's format type of the database (11 for the MP280).
func (d *Database) FormatType() int { return d.formatType }

// Tables gives access to the underlying resource tables.
func (d *Database) Tables() *printdb.Table { return d.tables }

// --- value translation -------------------------------------------------------

func objectToTable(o Object) int16 {
	v, ok := objectMap.forward(int16(o))
	if !ok {
		return -1
	}
	return v
}

func tableObjectToFront(t int16) int16 {
	v, ok := objectMap.reverse(t)
	if !ok {
		return -1
	}
	return v
}

// toTableValue translates a front-end value of obj to the table's value.
func toTableValue(obj int16, v int16) int16 {
	m, pass := valueMap(Object(obj))
	if pass {
		return v
	}
	if m == nil {
		return -1
	}
	if v > 999 {
		return v - 1000
	}
	if r, ok := m.forward(v); ok {
		return r
	}
	return -1
}

// fromTableValue translates a table value of table object tobj to the
// front-end value.
func fromTableValue(tobj int16, v int16) int16 {
	if c := tableObjectToFront(tobj); c >= 0 {
		m, pass := valueMap(Object(c))
		if pass {
			return v
		}
		if m != nil {
			if r, ok := m.reverse(v); ok {
				return r
			}
		}
	}
	switch tobj {
	case 0x40, 1, 10, 0x43, 0x4b:
		return v + 1000
	}
	return -1
}

// --- table access ------------------------------------------------------------

func triples(data []byte, n int) []triple {
	w := printdb.Int16s(data)
	out := make([]triple, 0, n)
	for i := 0; i < n && 3*i+2 < len(w); i++ {
		out = append(out, triple{w[3*i], w[3*i+1], w[3*i+2]})
	}
	return out
}

// enumAll returns every selectable (option, value) of the model and stores the
// custom-size limits.
func (d *Database) enumAll() ([]triple, error) {
	blob, err := d.tables.Lookup(printdb.TableFormat, nil)
	if err != nil || len(blob.Data) < 32 {
		return nil, errors.New("options: format table missing")
	}
	if int32(binary.LittleEndian.Uint32(blob.Data[28:])) > 0 || (blob.Status != 0 && blob.Status != 2) {
		return nil, errors.New("options: format table not usable")
	}
	for i, v := range printdb.Int32s(blob.Data[8:24]) {
		d.limits[i] = int64(v)
	}
	r, err := d.tables.Lookup(printdb.TableAllOptions, nil)
	if err != nil || r.Count == 0 || r.Status != 0 {
		return nil, errors.New("options: option table missing")
	}
	return triples(r.Data, r.Count), nil
}

// --- entry helpers -----------------------------------------------------------

// endOfBlock reports whether entry i is the last of its option's block.
func (d *Database) endOfBlock(i int) bool {
	return i == len(d.entries)-1 || d.entries[i+1].obj != d.entries[i].obj
}

// current returns the selected value of a table object, in table form.
func (d *Database) current(tobj int16) int16 {
	c := tableObjectToFront(tobj)
	for _, e := range d.entries {
		if e.obj == c && e.isCur {
			return toTableValue(c, e.val)
		}
	}
	return -1
}

// pending returns the value the caller marked for a table object, or -1.
func (d *Database) pending(tobj int16) int16 {
	c := tableObjectToFront(tobj)
	for i, e := range d.entries {
		if e.obj == c {
			if e.isTmp {
				return toTableValue(c, e.val)
			}
			if i != len(d.entries)-1 && c != d.entries[i+1].obj {
				return -1
			}
		}
	}
	return -1
}

// setCurrent makes (tobj, tval) the selected value, storing it in the free
// slot of the block if no entry has that value.
func (d *Database) setCurrent(tobj, tval int16) {
	v := fromTableValue(tobj, tval)
	c := tableObjectToFront(tobj)
	for i := range d.entries {
		if d.entries[i].obj == c {
			d.entries[i].isCur = false
			if !d.endOfBlockStrict(i) {
				continue
			}
			break
		}
	}
	for i := range d.entries {
		e := &d.entries[i]
		if e.obj != c {
			continue
		}
		if e.val == v {
			e.isCur, e.disabled = true, false
			return
		}
		if d.endOfBlockStrict(i) {
			e.val, e.isCur, e.disabled = v, true, false
			return
		}
	}
}

// endOfBlockStrict is endOfBlock for the callers that guard with n-1 != i.
func (d *Database) endOfBlockStrict(i int) bool {
	return i != len(d.entries)-1 && d.entries[i+1].obj != d.entries[i].obj
}

// setDisabled sets or clears the disabled flag of (tobj, tval).
func (d *Database) setDisabled(tobj, tval int16, flag bool) {
	v := fromTableValue(tobj, tval)
	c := tableObjectToFront(tobj)
	for i := range d.entries {
		if d.entries[i].obj != c {
			continue
		}
		if d.entries[i].val == v {
			d.entries[i].disabled = flag
			return
		}
		if d.endOfBlockStrict(i) {
			return
		}
	}
}

// applyPending turns the caller's pending selections into current ones.
func (d *Database) applyPending() {
	for i := range d.entries {
		if !d.entries[i].isTmp {
			continue
		}
		e := d.entries[i]
		v := toTableValue(e.obj, e.val)
		d.setCurrent(objectToTable(Object(e.obj)), v)
		d.entries[i].isTmp = false
	}
}

// --- defaults ----------------------------------------------------------------

// defaults marks the default (and initially selected) value of each option
// from table 1002.
func (d *Database) defaults() error {
	blob, err := d.tables.Lookup(printdb.TableFormat, nil)
	if err != nil || (blob.Status != 0 && blob.Status != 2) {
		return errors.New("options: format table missing")
	}
	r, err := d.tables.Lookup(printdb.TableDefaults, nil)
	if err != nil || r.Count == 0 || r.Status != 0 {
		return errors.New("options: defaults table missing")
	}
	e := d.entries
	n := len(e)
	sel14 := int16(-1)
	for _, tr := range triples(r.Data, r.Count) {
		id, dv := tr.id, tr.val
		mark := false
		switch {
		case id == 5:
			if fromTableValue(5, dv) == -1 {
				dv = 7
			} else {
				sz := fromTableValue(5, 0x22)
				c5 := tableObjectToFront(5)
				has := false
				for k := 0; k < n; k++ {
					if e[k].obj == c5 && e[k].val == sz {
						has = true
						break
					} else if n-1 != k && c5 != e[k+1].obj && e[k].obj == c5 {
						break
					}
				}
				if has && dv == 0x1d {
					mark, dv = true, 7
				}
			}
		case id == 0x14:
			sel14 = dv
		}
		for k := 0; k < n; k++ {
			ed := objectToTable(Object(e[k].obj))
			cv := toTableValue(e[k].obj, e[k].val)
			if ed != id {
				continue
			}
			if mark && cv == 0x1d {
				e[k].disabled = true
			}
			if n-1 != k && e[k].obj != e[k+1].obj {
				e[k].isDef, e[k].val, e[k].isCur = true, fromTableValue(id, dv), true
				break
			}
			if dv == cv {
				e[k].isDef, e[k].isCur = true, true
				break
			}
		}
	}
	if d.format > 8 {
		c14 := fromTableValue(0x14, sel14)
		six := false
		for k := range e {
			if e[k].obj == int16(ObjInkSettings) {
				if e[k].val == 6 {
					six = true
					e[k].isDef, e[k].isCur = true, true
				} else if c14 == e[k].val {
					e[k].isDef, e[k].isCur = true, true
				}
			}
		}
		if !six {
			for k := range e {
				if e[k].obj == int16(ObjCartridge) && c14 != e[k].val {
					e[k].val, e[k].disabled = -1, true
				}
			}
		}
	}
	return nil
}

// --- dependency engine -------------------------------------------------------

// dependencyInput describes, for the dependency tables, the current value of
// the options that influence each other and which of them the caller changed.
// changedID/changedVal are the pending item hints of the callers
// (-1 for none); withInkSettings selects the cartridge-settings option for the
// cartridge slot.
func (d *Database) dependencyInput(hintID, hintVal int16, withInkSettings bool) (in []triple, changed bool) {
	std := func(id int16) {
		cur := d.current(id)
		v := d.pending(id)
		if v < 0 || v == cur {
			in = append(in, triple{id, cur, 1})
		} else {
			in = append(in, triple{id, v, 3})
			changed = true
		}
	}
	// media
	cur := d.current(1)
	v := d.pending(1)
	switch {
	case v < 0:
		if hintID != 1 {
			in = append(in, triple{1, cur, 1})
		} else {
			if hintVal != -1 {
				cur = hintVal
			}
			in = append(in, triple{1, cur, 3})
		}
	case v == cur:
		in = append(in, triple{1, cur, 1})
	default:
		in = append(in, triple{1, v, 3})
		changed = true
	}
	std(3)
	std(5)
	std(0x11)
	in = append(in, triple{0x13, d.current(0x13), 1})
	// cartridge
	u4 := int16(0x14)
	if withInkSettings {
		if sv := d.current(0x406); sv != -1 && sv != 10 {
			u4 = 0x406
		}
	}
	cart := d.current(u4)
	v = d.pending(u4)
	switch {
	case v < 0:
		k := int16(1)
		if hintID == 0x14 {
			k = 3
		}
		in = append(in, triple{0x14, cart, k})
	case v == cart:
		in = append(in, triple{0x14, cart, 1})
	default:
		in = append(in, triple{0x14, v, 3})
		changed = true
	}
	// grayscale
	g := d.current(0x15)
	v = d.pending(0x15)
	if v < 0 || v == g {
		in = append(in, triple{0x15, g, 1})
	} else {
		in = append(in, triple{0x15, v, 3})
		changed = true
	}
	// margin type
	mt := d.current(0x23)
	if mt == -1 {
		t := triple{0x23, 0, 1}
		v = d.pending(0x23)
		if v < 0 {
			if hintID == 0x23 {
				t.kind = 3
				if hintVal != -1 {
					t.val = hintVal
				}
			}
		} else if v != t.val {
			t.val, t.kind = v, 3
			changed = true
		}
		in = append(in, t)
	} else {
		t := triple{0x23, mt, 1}
		v = d.pending(0x23)
		switch {
		case v < 0:
			if hintID == 0x23 {
				t.kind = 3
				if hintVal != -1 {
					t.val = hintVal
				}
			}
		case v != mt:
			t.val, t.kind = v, 3
			changed = true
		}
		in = append(in, t)
	}
	std(0x27)
	in = append(in, triple{0x2e, 0, 1})
	std(10)
	// duplex
	dp := d.current(0x26)
	if dp == -1 {
		in = append(in, triple{0x26, 0, 1})
	} else {
		v = d.pending(0x26)
		if v >= 0 && v != dp {
			in = append(in, triple{0x26, v, 3})
			changed = true
		} else {
			in = append(in, triple{0x26, dp, 1})
		}
	}
	return in, changed
}

// dependencies runs the dependency tables for the given input. With quiet set
// the results of "select" kinds are not fed back into later lookups.
func (d *Database) dependencies(in []triple, quiet bool) ([]triple, error) {
	var q printdb.ResourceQuery
	found := false
	var cid, cval int16
	for _, t := range in {
		q[t.id] = t.val
		if t.kind == kindChanged {
			found, cid, cval = true, t.id, t.val
		}
	}
	if !found {
		return nil, nil
	}
	q[printdb.OptChangedID], q[printdb.OptChangedValue] = cid, cval
	seq, err := d.tables.Lookup(printdb.TableDependencies, &q)
	if err != nil {
		return nil, fmt.Errorf("options: dependency table: %w", err)
	}
	if uint16(seq.Status) > 1 {
		return nil, errors.New("options: dependency table status")
	}
	if seq.Count == 0 {
		return nil, nil
	}
	ids := printdb.Int16s(seq.Data)
	limit := len(ids)
	if seq.Status == 0 && limit > 8 {
		limit = 8
	}
	var out []triple
	for k := 0; k < limit && ids[k] != 0; k++ {
		r, err := d.tables.Lookup(int(ids[k]), &q)
		if err != nil || r.Status != 0 {
			return nil, fmt.Errorf("options: dependency sub-table %d", ids[k])
		}
		for _, t := range triples(r.Data, r.Count) {
			out = append(out, t)
			if !quiet && (t.kind == kindSelect || t.kind == kindSelectQuiet) {
				q[t.id] = t.val
			}
		}
	}
	return out, nil
}

// menulink applies the pending selections and the consequences the
// dependency tables prescribe (greying out values, changing others).
func (d *Database) menulink(initial bool) error {
	hint := int16(-1)
	if initial {
		hint = 0x14
	}
	in, changed := d.dependencyInput(hint, -1, false)
	var out []triple
	if initial || changed {
		var err error
		if out, err = d.dependencies(in, false); err != nil {
			return err
		}
	}
	d.applyPending()
	for _, t := range out {
		switch t.kind {
		case kindUnavailable:
			d.setDisabled(t.id, t.val, true)
		case kindSelect, kindSelectQuiet:
			d.setCurrent(t.id, t.val)
		case kindEnable, kindEnableQuiet:
			d.setDisabled(t.id, t.val, false)
		}
	}
	if d.format > 6 {
		d.fixSupply()
	}
	d.fixDither()
	return nil
}

// fixSupply keeps the paper source selection valid: if the selected source is
// missing or disabled it falls back to the default, else to the first
// enabled one.
func (d *Database) fixSupply() {
	e := d.entries
	n := len(e)
	supply := int16(ObjSupply)
	found := false
	for i := 0; i < n; i++ {
		if e[i].obj != supply {
			continue
		}
		if e[i].isCur && (e[i].val == -1 || e[i].disabled) {
			e[i].isCur = false
			found = true
			break
		}
		if d.endOfBlockStrict(i) {
			return
		}
	}
	if !found {
		return
	}
	for i := 0; i < n; i++ {
		if e[i].obj != supply {
			continue
		}
		if e[i].isDef && !e[i].disabled {
			e[i].isCur = true
			return
		}
		if d.endOfBlockStrict(i) {
			for j := 0; j < n; j++ {
				if e[j].obj != supply {
					continue
				}
				if !e[j].disabled && e[j].val != -1 {
					e[j].isCur = true
					return
				}
				if d.endOfBlockStrict(j) {
					return
				}
			}
			return
		}
	}
}

// fixDither disables the "pattern, high speed" halftoning (value 1), falls
// back to pattern if it was selected, and records the default if none was set.
func (d *Database) fixDither() {
	e := d.entries
	n := len(e)
	dither := int16(ObjDither)
	hadDefault, wasSelected := false, false
	for i := 0; i < n; i++ {
		if e[i].obj != dither {
			continue
		}
		if e[i].isDef {
			hadDefault = true
		}
		if e[i].val == 1 {
			if e[i].isCur {
				wasSelected = true
				e[i].isCur = false
			}
			e[i].disabled = true
		}
		if d.endOfBlockStrict(i) {
			break
		}
	}
	if wasSelected {
		for i := 0; i < n; i++ {
			if e[i].obj != dither {
				continue
			}
			if e[i].val == 0 {
				e[i].isCur = true
				break
			}
			if i != n-1 && e[i+1].obj != dither {
				break
			}
		}
	}
	if hadDefault {
		return
	}
	i := 0
	for i < n && e[i].obj != dither {
		i++
	}
	for ; i < n; i++ {
		if e[i].obj != dither {
			return
		}
		if e[i].isCur {
			e[i].isDef = true
			return
		}
	}
}

// --- queries used by the command-line front end -----------------------------

// Find returns the index of (obj, val), ignoring availability, or -1.
func (d *Database) Find(obj Object, val int) int {
	for i, e := range d.entries {
		if e.obj == int16(obj) {
			if int(e.val) == val {
				return i
			}
			if d.endOfBlock(i) {
				break
			}
		}
	}
	return -1
}

// Selectable returns the index of (obj, val) if it exists and is enabled, or -1.
func (d *Database) Selectable(obj Object, val int) int {
	for i, e := range d.entries {
		if e.obj == int16(obj) {
			if int(e.val) == val && !e.disabled {
				return i
			}
			if d.endOfBlock(i) {
				break
			}
		}
	}
	return -1
}

// Current returns the selected value of obj, or -1.
func (d *Database) Current(obj Object) int {
	for _, e := range d.entries {
		if e.obj == int16(obj) && e.isCur {
			return int(e.val)
		}
	}
	return -1
}

// Default returns the default value of obj, or -1.
func (d *Database) Default(obj Object) int {
	for _, e := range d.entries {
		if e.obj == int16(obj) && e.isDef {
			return int(e.val)
		}
	}
	return -1
}

// ErrNotSelectable is returned when a value is not available in the current
// combination of choices.
var ErrNotSelectable = errors.New("options: inappropriate selection")

// Select chooses val for obj and lets the dependency rules adjust the other
// options. It fails if the value does not exist or is greyed out.
func (d *Database) Select(obj Object, val int) error {
	if d.Selectable(obj, val) < 0 {
		return ErrNotSelectable
	}
	for i := range d.entries {
		e := &d.entries[i]
		if e.obj == int16(obj) {
			if int(e.val) == val {
				e.isTmp = true
				break
			}
			if d.endOfBlock(i) {
				break
			}
		}
	}
	return d.menulink(false)
}
