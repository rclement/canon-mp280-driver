// SPDX-License-Identifier: GPL-2.0-only

package filter

import (
	"bytes"
	"math"
	"os"
	"strconv"
	"strings"
)

// UIType is the kind of control an option has (*OpenUI ... : type).
type UIType int

// Option control types.
const (
	PickOne UIType = iota
	PickMany
	Boolean
)

// Choice is one selectable value of an option.
type Choice struct {
	Name   string // the choice keyword, e.g. "A4.bl"
	Text   string // the human readable text after the slash
	Marked bool
}

// Option is one *OpenUI ... *CloseUI block.
type Option struct {
	Keyword string
	Text    string
	UI      UIType
	Default string // the *Default<Keyword> value
	Choices []*Choice
}

// Size is one entry of the page size table, in PostScript points.
type Size struct {
	Name          string
	Width, Length float32
	Left, Bottom  float32
	Right, Top    float32
	Marked        bool
}

// KeyMapping is one "*%CNPpdToOptKey <ppd option> <cif option>" comment line:
// a table that says which PPD options are handed to the cif front end
// and under which command line switch.
type KeyMapping struct {
	PPDKey string
	OptKey string
}

// PPDFile is a parsed PPD.
type PPDFile struct {
	Product       string // *Product value as written, e.g. "(mp280)"
	ModelNumber   int    // *cupsModelNumber
	Options       []*Option
	Sizes         []*Size
	VariableSizes bool       // *CustomPageSize True was present
	CustomMin     [2]float32 // from *ParamCustomPageSize Width/Height
	CustomMax     [2]float32
	CustomMargins [4]float32 // *HWMargins left bottom right top
	KeyTable      []KeyMapping
}

// OpenPPD reads and parses the PPD file at path.
func OpenPPD(path string) (*PPDFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePPD(data), nil
}

// ParsePPD parses PPD text. It never fails: unknown or malformed lines are
// skipped like libcups does, so that a damaged file only loses information.
func ParsePPD(data []byte) *PPDFile {
	f := &PPDFile{}
	f.KeyTable = readKeyTable(data)
	defaults := map[string]string{}
	var open *Option
	sizes := map[string]*Size{}
	size := func(name string) *Size {
		s := sizes[name]
		if s == nil {
			s = &Size{Name: name}
			sizes[name] = s
			f.Sizes = append(f.Sizes, s)
		}
		return s
	}

	for _, ln := range logicalLines(data) {
		if !strings.HasPrefix(ln, "*") || strings.HasPrefix(ln, "*%") {
			continue
		}
		keyword, name, text, value := splitLine(ln[1:])
		switch keyword {
		case "OpenUI":
			kw := strings.TrimPrefix(name, "*")
			o := f.option(kw)
			if o == nil {
				o = &Option{Keyword: kw}
				f.Options = append(f.Options, o)
			}
			o.Text = text
			switch strings.TrimSpace(value) {
			case "Boolean":
				o.UI = Boolean
			case "PickMany":
				o.UI = PickMany
			default:
				o.UI = PickOne
			}
			open = o
			continue
		case "CloseUI":
			open = nil
			continue
		case "Product":
			f.Product = unquote(value)
		case "cupsModelNumber":
			f.ModelNumber, _ = strconv.Atoi(strings.TrimSpace(value))
		case "HWMargins":
			copy(f.CustomMargins[:], floats(value, 4))
		case "ParamCustomPageSize":
			// "Width: 1 points min max" (name = Width|Height)
			fl := floats(afterFirstFields(unquote(value), 2), 2)
			switch name {
			case "Width":
				f.CustomMin[0], f.CustomMax[0] = fl[0], fl[1]
			case "Height":
				f.CustomMin[1], f.CustomMax[1] = fl[0], fl[1]
			}
		case "CustomPageSize":
			if name == "True" {
				f.VariableSizes = true
			}
		case "PaperDimension":
			fl := floats(unquote(value), 2)
			s := size(name)
			s.Width, s.Length = fl[0], fl[1]
		case "ImageableArea":
			fl := floats(unquote(value), 4)
			s := size(name)
			s.Left, s.Bottom, s.Right, s.Top = fl[0], fl[1], fl[2], fl[3]
		}
		if strings.HasPrefix(keyword, "Default") && name == "" {
			defaults[keyword[len("Default"):]] = strings.TrimSpace(value)
			continue
		}
		if name != "" {
			o := open
			if o == nil || o.Keyword != keyword {
				o = f.option(keyword)
			}
			if o != nil && !strings.Contains(keyword, "UIConstraints") {
				o.Choices = append(o.Choices, &Choice{Name: name, Text: textOr(text, name)})
			}
		}
	}

	for kw, d := range defaults {
		if o := f.option(kw); o != nil {
			o.Default = d
		}
	}
	if f.VariableSizes {
		for _, kw := range []string{"PageSize", "PageRegion"} {
			if o := f.option(kw); o != nil && o.choice("Custom") == nil {
				o.Choices = append(o.Choices, &Choice{Name: "Custom", Text: "Custom"})
			}
		}
		c := size("Custom")
		c.Width, c.Length = f.CustomMax[0], f.CustomMax[1]
		f.setCustomMargins(c)
	}
	return f
}

func (f *PPDFile) setCustomMargins(s *Size) {
	m := f.CustomMargins
	s.Left, s.Bottom = m[0], m[1]
	s.Right, s.Top = s.Width-m[2], s.Length-m[3]
}

func textOr(text, name string) string {
	if text == "" {
		return name
	}
	return text
}

// option finds an option by keyword, case-insensitively (like ppdFindOption).
func (f *PPDFile) option(keyword string) *Option {
	for _, o := range f.Options {
		if strings.EqualFold(o.Keyword, keyword) {
			return o
		}
	}
	return nil
}

// Option returns the option with the given keyword, or nil.
func (f *PPDFile) Option(keyword string) *Option { return f.option(keyword) }

// choice finds a choice like ppdFindChoice: "Custom.xxx" and "{...}" look up
// the "Custom" choice; everything else compares case-insensitively.
func (o *Option) choice(name string) *Choice {
	if strings.HasPrefix(name, "{") || hasPrefixFold(name, "Custom.") {
		name = "Custom"
	}
	for _, c := range o.Choices {
		if strings.EqualFold(c.Name, name) {
			return c
		}
	}
	return nil
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// logicalLines splits the file on CR, LF or CRLF and joins the continuation
// lines of quoted values that span several lines.
func logicalLines(data []byte) []string {
	raw := strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' || r == '\r' })
	var out []string
	for i := 0; i < len(raw); i++ {
		ln := raw[i]
		if strings.HasPrefix(ln, "*") && !strings.HasPrefix(ln, "*%") {
			if p := strings.Index(ln, ":"); p >= 0 {
				v := strings.TrimLeft(ln[p+1:], " \t")
				if strings.HasPrefix(v, "\"") && strings.Count(v, "\"")%2 == 1 {
					for i+1 < len(raw) {
						i++
						ln += "\n" + raw[i]
						if strings.Contains(raw[i], "\"") {
							break
						}
					}
				}
			}
		}
		out = append(out, ln)
	}
	return out
}

// splitLine splits "Keyword Name/Text: value" (the leading '*' removed).
func splitLine(ln string) (keyword, name, text, value string) {
	head := ln
	if p := strings.Index(ln, ":"); p >= 0 {
		head, value = ln[:p], strings.TrimLeft(ln[p+1:], " \t")
	}
	if p := strings.Index(head, "/"); p >= 0 {
		head, text = head[:p], head[p+1:]
	}
	fields := strings.SplitN(strings.TrimSpace(head), " ", 2)
	keyword = strings.TrimSpace(fields[0])
	if p := strings.IndexAny(keyword, "\t"); p >= 0 {
		keyword = keyword[:p]
	}
	if len(fields) == 2 {
		name = strings.TrimSpace(fields[1])
	}
	return
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' {
		if e := strings.LastIndex(v, "\""); e > 0 {
			return v[1:e]
		}
	}
	return v
}

func afterFirstFields(s string, n int) string {
	fs := strings.Fields(s)
	if len(fs) <= n {
		return ""
	}
	return strings.Join(fs[n:], " ")
}

func floats(s string, n int) []float32 {
	out := make([]float32, n)
	for i, w := range strings.Fields(s) {
		if i >= n {
			break
		}
		if v, err := strconv.ParseFloat(w, 32); err == nil {
			out[i] = float32(v)
		}
	}
	return out
}

// readKeyTable extracts the "*%CNPpdToOptKey" comment lines the way
// alloc_opt_key_table does: lines end at CR or LF, and a last line without a
// terminator is not seen.
func readKeyTable(data []byte) []KeyMapping {
	var out []KeyMapping
	for len(data) > 0 {
		end := bytes.IndexAny(data, "\r\n")
		if end < 0 {
			break
		}
		ln := string(data[:end])
		data = data[end+1:]
		if !strings.HasPrefix(ln, "*%") {
			continue
		}
		// strtok(line, " "): tokens are separated by runs of spaces.
		tok := strings.FieldsFunc(ln, func(r rune) bool { return r == ' ' })
		if len(tok) >= 3 && tok[0] == "*%CNPpdToOptKey" {
			out = append(out, KeyMapping{PPDKey: tok[1], OptKey: tok[2]})
		}
	}
	return out
}

// OptKey returns the cif switch mapped to a PPD option name ("" if none).
func (f *PPDFile) OptKey(ppdKey string) (string, bool) {
	for _, k := range f.KeyTable {
		if k.PPDKey == ppdKey {
			return k.OptKey, true
		}
	}
	return "", false
}

// ---- marking (libcups semantics) -------------------------------------------

// MarkDefaults marks every option's default choice except PageRegion, like
// ppdMarkDefaults, and the matching page size.
func (f *PPDFile) MarkDefaults() {
	for _, s := range f.Sizes {
		s.Marked = false
	}
	for _, o := range f.Options {
		for _, c := range o.Choices {
			c.Marked = false
		}
	}
	for _, o := range f.Options {
		if o.Keyword != "PageRegion" {
			f.MarkOption(o.Keyword, o.Default)
		}
	}
}

// MarkOption marks one choice of one option (ppdMarkOption). Unknown options
// and choices are ignored without any change.
func (f *PPDFile) MarkOption(keyword, choice string) {
	if strings.EqualFold(keyword, "PageSize") && hasPrefixFold(choice, "Custom.") {
		f.PageSize(choice) // records the custom dimensions as a side effect
		choice = "Custom"
	}
	o := f.option(keyword)
	if o == nil {
		return
	}
	c := o.choice(choice)
	if c == nil {
		return
	}
	if o.UI != PickMany {
		for _, other := range o.Choices {
			other.Marked = false
		}
	}
	c.Marked = true
	switch {
	case strings.EqualFold(keyword, "PageSize"):
		f.clearOption("PageRegion")
		f.markSize(c.Name)
	case strings.EqualFold(keyword, "PageRegion"):
		f.clearOption("PageSize")
		f.markSize(c.Name)
	}
}

func (f *PPDFile) clearOption(keyword string) {
	if o := f.option(keyword); o != nil {
		for _, c := range o.Choices {
			c.Marked = false
		}
	}
}

func (f *PPDFile) markSize(name string) {
	for _, s := range f.Sizes {
		s.Marked = strings.EqualFold(s.Name, name)
	}
}

// FindMarkedChoice returns the marked choice of an option, else its default
// choice (ppdFindMarkedChoice). Nil if the PPD has no such option.
func (f *PPDFile) FindMarkedChoice(keyword string) *Choice {
	o := f.option(keyword)
	if o == nil {
		return nil
	}
	for _, c := range o.Choices {
		if c.Marked {
			return c
		}
	}
	return o.choice(o.Default)
}

// PageSize looks a page size up (ppdPageSize). An empty name returns the
// currently marked size. "Custom.WxH[unit]" updates and returns the Custom
// entry (units: in, ft, mm, cm, m, anything else means points).
func (f *PPDFile) PageSize(name string) *Size {
	if name == "" {
		for _, s := range f.Sizes {
			if s.Marked {
				return s
			}
		}
		return nil
	}
	if strings.HasPrefix(name, "Custom.") && f.VariableSizes {
		var custom *Size
		for _, s := range f.Sizes {
			if s.Name == "Custom" {
				custom = s
			}
		}
		if custom == nil {
			return nil
		}
		w, rest, ok := scanFloat(name[len("Custom."):])
		if !ok || !strings.HasPrefix(rest, "x") {
			return nil
		}
		l, units, ok := scanFloat(rest[1:])
		if !ok {
			return nil
		}
		scale := float64(1)
		switch strings.ToLower(units) {
		case "in":
			scale = 72
		case "ft":
			scale = 12 * 72
		case "mm":
			scale = 72 / 25.4
		case "cm":
			scale = 72 / 2.54
		case "m":
			scale = 72 / 0.0254
		}
		custom.Width, custom.Length = float32(w*scale), float32(l*scale)
		f.setCustomMargins(custom)
		return custom
	}
	for _, s := range f.Sizes {
		if strings.EqualFold(s.Name, name) {
			return s
		}
	}
	return nil
}

// scanFloat parses a leading decimal number like _cupsStrScand.
func scanFloat(s string) (v float64, rest string, ok bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return 0, s, false
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, s, false
	}
	return v, s[i:], true
}

// Setting is one name=value job option.
type Setting struct{ Name, Value string }

// MarkOptions applies job options to the PPD like cupsMarkOptions: the CUPS
// "media" option is split on commas and offered to PageSize, InputSlot and
// MediaType (each only when that option is not also given explicitly),
// "resolution"/"printer-resolution" set Resolution, and everything else is
// handed to MarkOption under its own name.
func (f *PPDFile) MarkOptions(opts []Setting) {
	has := func(name string) bool {
		for _, o := range opts {
			if strings.EqualFold(o.Name, name) {
				return true
			}
		}
		return false
	}
	for _, o := range opts {
		switch {
		case strings.EqualFold(o.Name, "media"):
			for _, part := range strings.Split(o.Value, ",") {
				if len(part) > 254 {
					part = part[:254]
				}
				if !has("PageSize") {
					f.MarkOption("PageSize", part)
				}
				if !has("InputSlot") {
					f.MarkOption("InputSlot", part)
				}
				if !has("MediaType") {
					f.MarkOption("MediaType", part)
				}
			}
		case strings.EqualFold(o.Name, "resolution"), strings.EqualFold(o.Name, "printer-resolution"):
			f.MarkOption("Resolution", o.Value)
		default:
			f.MarkOption(o.Name, o.Value)
		}
	}
}
