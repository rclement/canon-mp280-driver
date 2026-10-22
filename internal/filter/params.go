// SPDX-License-Identifier: GPL-2.0-only

package filter

import (
	"fmt"
	"strconv"
	"strings"
)

// Job is everything the option mapping needs.
type Job struct {
	PPD       *PPDFile
	Options   []Setting // the CUPS option string (argv[5]), sorted like cupsParseOptions
	PSParams  []Setting // settings found in the PostScript header
	DeviceURI string    // $DEVICE_URI
}

// Plan is the result of the mapping: the cif command line (without the
// program name) and the Ghostscript raster geometry.
type Plan struct {
	CifArgs       []string
	Resolution    int // dpi
	Width, Height int // Ghostscript device size in pixels
}

// bidiSchemes are the device URI schemes of backends that can talk back to
// the printer. The original backends are cnijusb: and cnijnet:; with them cif is
// asked for a bidirectional job (--bidi). Other URIs get no --bidi. In particular
// mp280usb: must not be listed: that wrapper hands the job to CUPS' stock usb
// backend, which is uni-directional, and a stream that announces a reply channel
// nobody reads makes the printer drop the job.
var bidiSchemes = []string{"cnijusb:", "cnijnet:"}

// Build maps the PPD choices and job options to cif options and works out the
// Ghostscript geometry, exactly as the original filter does:
//
//  1. the PostScript header settings and then the job options are marked in
//     the PPD (cupsMarkOptions). Unknown choices are ignored: the PPD default
//     stays marked.
//  2. every PPD option listed in the *%CNPpdToOptKey table becomes a cif
//     option from its marked choice, lower-cased (4X6 style sizes excepted).
//     Entries are only added once: the first one wins.
//  3. then the job options themselves are converted the same way. For options
//     that exist in the PPD this is a no-op (step 2 already set them, so an
//     unknown choice never overrides the default); options the PPD does not
//     list (CNHalftoning, CNGamma, CNInkCartridgeSettings, CNBalance*,
//     CNCopies) only reach cif through this step.
//  4. without borderless a --bbox of the imageable area is added, always
//     --fit, --location for position=, and --bidi for the Canon backends.
func Build(j *Job) (*Plan, error) {
	f := j.PPD
	if len(f.KeyTable) == 0 {
		return nil, fmt.Errorf("the PPD has no *%%CNPpdToOptKey table")
	}
	opts := append([]Setting(nil), j.Options...)

	// Custom page size given as PageSize=Custom.WxHunit.
	var customW, customH, customUnit string
	customOK := false
	for _, o := range opts {
		if o.Name == "PageSize" && strings.HasPrefix(o.Value, "Custom") {
			rest := ""
			if len(o.Value) > 7 {
				rest = o.Value[7:]
			}
			customW, customH, customUnit, customOK = parseCustomSize(rest)
		}
	}

	f.MarkDefaults()
	defSize := f.PageSize("")

	// (1) settings from the PostScript header, one at a time.
	for _, p := range j.PSParams {
		f.MarkOptions([]Setting{p})
	}

	// A size taken from the header that is not the default, while the job
	// options carry "media" but no PageSize: keep the header's size.
	size := f.PageSize("")
	if size != nil && defSize != nil && !strings.EqualFold(size.Name, defSize.Name) {
		_, media := GetOption(opts, "media")
		_, pagesize := GetOption(opts, "PageSize")
		if media && !pagesize {
			opts = AddOption(opts, "PageSize", size.Name)
		}
	}

	// (2) job options.
	f.MarkOptions(opts)
	size = f.PageSize("")
	if size == nil {
		return nil, fmt.Errorf("the PPD has no marked page size")
	}

	reso := 0
	if c := f.FindMarkedChoice("Resolution"); c != nil {
		reso = atoi(c.Name)
	}
	if reso <= 0 {
		return nil, fmt.Errorf("the PPD has no usable Resolution")
	}
	minW, maxW, minL, maxL := int64(-1), int64(-1), int64(-1), int64(-1)
	for _, o := range opts {
		switch o.Name {
		case "CNMinWidth":
			minW = atol(o.Value)
		case "CNMaxWidth":
			maxW = atol(o.Value)
		case "CNMinLength":
			minL = atol(o.Value)
		case "CNMaxLength":
			maxL = atol(o.Value)
		}
	}

	var list paramList
	borderless := false

	// (3) options of the PPD.
	for _, k := range f.KeyTable {
		c := f.FindMarkedChoice(k.PPDKey)
		if c == nil {
			continue
		}
		choice := c.Name
		if k.PPDKey == "PageSize" && choice == "Custom" {
			// Custom sizes travel in hundredths of a millimetre.
			pw := int64(float64(size.Width) * 2540.0 / 72.0)
			pl := int64(float64(size.Length) * 2540.0 / 72.0)
			switch {
			case customUnit == "mm" && customOK:
				pw = scaleFraction(customW, pw, 2, 100)
				pl = scaleFraction(customH, pl, 2, 100)
			case customUnit == "cm" && customOK:
				pw = scaleFraction(customW, pw, 3, 1000)
				pl = scaleFraction(customH, pl, 3, 1000)
			}
			if minW != -1 && pw < minW {
				pw = minW
			}
			if maxW != -1 && pw > maxW {
				pw = maxW
			}
			if minL != -1 && pl < minL {
				pl = minL
			}
			if maxL != -1 && pl > maxL {
				pl = maxW // sic: the original clamps the length with the maximum width
			}
			list.add(k.OptKey, "user")
			list.add("--paperwidth", strconv.FormatInt(pw, 10))
			list.add("--paperheight", strconv.FormatInt(pl, 10))
		}
		if k.PPDKey == "Duplex" {
			switch choice {
			case "DuplexNoTumble":
				list.add(k.OptKey, "--stapleside longside")
			case "DuplexTumble":
				list.add(k.OptKey, "--stapleside shortside")
			}
			continue
		}
		choice = lowerExceptSize(choice)
		if k.PPDKey == "CNCartridge" && (choice == "bk" || choice == "color") {
			// The MP280 has both cartridges fitted, and cif refuses --cartridge bk|color
			// for it ("inappropriate cartridge selection"). "Black Only" / "Color Only"
			// mean printing with just that ink, which cif does through
			// --inkcartridgesettings.
			list.add(k.OptKey, "bkcolor")
			list.add("--inkcartridgesettings", choice)
			continue
		}
		if k.PPDKey == "PageSize" {
			var bl bool
			if choice, bl = stripBorderless(choice); bl {
				list.add("--borderless", "")
				borderless = true
			}
		}
		list.add(k.OptKey, choice)
	}

	// (4) job options, converted one by one.
	position := ""
	for _, o := range opts {
		if o.Name == "position" {
			switch o.Value {
			case "center":
				position = "center"
			case "top-left":
				position = "upperleft"
			}
		}
		optKey, ok := f.OptKey(o.Name)
		if !ok {
			continue
		}
		value := lowerExceptSize(o.Value)
		switch {
		case o.Name == "Duplex":
			if strings.EqualFold(value, "True") {
				list.add(optKey, "")
			}
		case o.Name == "CNGrayscale":
			// A bare flag for cif: only emit it when the boolean is True
			// (boolean grayscale fix of the original filter).
			if strings.EqualFold(value, "True") {
				list.add(optKey, "")
			}
		default:
			if o.Name == "PageSize" {
				if v, bl := stripBorderless(value); bl {
					value = v
					list.add("--borderless", "")
					borderless = true
				}
			}
			list.add(optKey, value)
		}
	}

	if !borderless {
		bbox := fmt.Sprintf("%d,%d,%d,%d", int(size.Left), int(size.Bottom),
			int(float64(size.Right)+0.99), int(float64(size.Top)+0.99))
		list.add("--bbox", bbox)
	}
	list.add("--fit", "")
	if position != "" {
		list.add("--location", position)
	}
	bidi := false
	for _, s := range bidiSchemes {
		if strings.HasPrefix(j.DeviceURI, s) {
			bidi = true
		}
	}
	if bidi && f.ModelNumber >= 356 {
		list.add("--bidi", "")
	}

	args := cifArgs(list.items, reso)

	// Ghostscript page size: points to pixels in single precision, like
	// "(int)(width * (float)reso / 72.0)".
	gw := int(float64(size.Width*float32(reso)) / 72.0)
	gh := int(float64(size.Length*float32(reso)) / 72.0)
	return &Plan{CifArgs: args, Resolution: reso, Width: gw, Height: gh}, nil
}

// cifArgs builds the final argument vector from the collected pairs, in the
// order the original filter produces: --imageres first, --extension next
// (only for borderless jobs), then the rest in the order it was collected.
// "false" values drop the option, "true" and empty values make it a flag.
func cifArgs(items []Setting, reso int) []string {
	var rest []Setting
	ext, extOn, borderless := "", false, false
	for _, it := range items {
		if it.Name == "--borderless" {
			borderless = true
		}
		if it.Name == "--extension" {
			extOn, ext = true, it.Value
			continue
		}
		if it.Name == "--percent" {
			continue
		}
		if len(it.Name)+1 > 255 || len(it.Value)+1 > 255 {
			continue
		}
		rest = append(rest, it)
	}
	var all []Setting
	all = append(all, Setting{Name: "--imageres", Value: strconv.Itoa(reso)})
	if extOn && borderless {
		all = append(all, Setting{Name: "--extension", Value: ext})
	}
	all = append(all, rest...)

	var args []string
	for _, it := range all {
		if it.Value == "false" {
			continue
		}
		args = append(args, it.Name)
		if it.Value != "true" && it.Value != "" {
			args = append(args, it.Value)
		}
	}
	return args
}

// paramList is an ordered parameter list: a key that is
// already present is not replaced, so the first value wins.
type paramList struct{ items []Setting }

func (l *paramList) add(key, value string) {
	for _, it := range l.items {
		if it.Name == key {
			return
		}
	}
	l.items = append(l.items, Setting{Name: key, Value: value})
}

// lowerExceptSize lower-cases a choice unless it is an inch size such as
// "4X6" or "8X10" (digits, X, digits; a ".suffix" is allowed).
func lowerExceptSize(s string) string {
	if isSizeX(s) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func isSizeX(s string) bool {
	state := 1
	for i := 0; i < len(s) && state != 0; i++ {
		c := s[i]
		if c == '.' {
			break
		}
		switch state {
		case 1:
			if c >= '0' && c <= '9' {
				state = 2
			} else {
				state = 0
			}
		case 2:
			if c == 'X' {
				state = 3
			} else if !(c >= '0' && c <= '9') {
				state = 0
			}
		case 3, 4:
			if c >= '0' && c <= '9' {
				state = 4
			} else {
				state = 0
			}
		}
	}
	return state == 4
}

// stripBorderless removes a ".bl" suffix (is_borderless).
func stripBorderless(s string) (string, bool) {
	if strings.HasSuffix(s, ".bl") && len(s) >= 3 {
		return s[:len(s)-3], true
	}
	return s, false
}

// parseCustomSize splits "WIDTHxHEIGHTunit" (the part after "Custom.").
func parseCustomSize(s string) (w, h, unit string, ok bool) {
	i := 0
	for i < len(s) && s[i] != 'x' {
		i++
	}
	w = s[:i]
	if w == "" {
		return
	}
	j := i + 1
	k := j
	for k < len(s) && (s[k] >= '0' && s[k] <= '9' || s[k] == '.') {
		k++
	}
	if j > len(s) {
		return
	}
	h = s[j:k]
	if h == "" {
		return "", "", "", false
	}
	return w, h, s[k:], true
}

// scaleFraction emulates the original parsing that reads "%ld.%<n>s" from a decimal
// size and scales it to hundredths (mm) or thousandths (cm) of the unit:
// whole*factor + fraction digits scaled. If the whole part cannot be read
// the previous value (derived from points) is kept and only scaled.
func scaleFraction(text string, prev int64, fracDigits int, factor int64) int64 {
	whole, frac := prev, ""
	s := strings.TrimLeft(text, " \t\n\r\v\f")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	d := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > d {
		whole, _ = strconv.ParseInt(s[:i], 10, 64)
		s = s[i:]
		if strings.HasPrefix(s, ".") {
			s = strings.TrimLeft(s[1:], " \t\n\r\v\f")
			n := 0
			for n < len(s) && n < fracDigits && !strings.ContainsRune(" \t\n\r\v\f", rune(s[n])) {
				n++
			}
			frac = s[:n]
		}
	}
	a := atol(frac)
	// factor is 100 for mm (2 digits) and 1000 for cm (3 digits).
	switch len(frac) {
	case 0:
		return whole * factor
	default:
		unit := factor
		for i := 0; i < len(frac); i++ {
			unit /= 10
		}
		return whole*factor + a*unit
	}
}

func atoi(s string) int { return int(atol(s)) }

// atol is C's atol: optional sign and leading digits, anything else ends it.
func atol(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	v, _ := strconv.ParseInt(s[:i], 10, 64)
	return v
}
