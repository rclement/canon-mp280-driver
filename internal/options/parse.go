// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"canon-mp280-driver/internal/jobstream"
)

// FitMode is how the image is scaled to the printable area.
type FitMode int

// Scaling modes.
const (
	FitNone  FitMode = iota
	FitShort         // --fit: whole image inside the printable area
	FitLong          // --full: printable area completely covered
)

// Location is where a smaller image sits on the page.
type Location int

// Image placements.
const (
	UpperLeft Location = iota
	Center
)

// Colour holds the colour-processing choices; none of them changes the
// page-setup commands.
type Colour struct {
	Gamma, Intent                          int
	BalanceC, BalanceM, BalanceY, BalanceK int
	Density, Contrast, Tone                int
}

// Settings is the outcome of command-line analysis: the device choices (in
// the vocabulary of the option database), colour choices, the paper
// geometry and the layout options.
type Settings struct {
	ModelID int // front-end model id (1370 for the MP280)
	Device  Device
	Colour  Colour

	Paper MarginInfo // selected paper size and its printable area

	Copies    int
	Fit       FitMode
	Location  Location
	Percent   int     // 0 = off, else 20..400
	ImageRes  int     // dpi forced by --imageres, 0 = use the image's own
	BBox      *[4]int // --bbox left,bottom,right,top in points
	Extension int     // borderless extension level 0..3
	RevPrint  bool
	Collate   bool

	// IVEC is true when the stream must include the IVEC XML documents (the
	// front end writes them unless --bidi was given).
	IVEC bool
	// File is the input file argument, "" for standard input.
	File string
}

// ErrNoJob is returned when the command line asks for information only
// (--help, --internalversion, --supportnetwork); nothing is printed.
var ErrNoJob = errors.New("options: no job requested")

// Rejection is returned for a command line the front end refuses.
type Rejection struct {
	Reason string
	Cause  error // underlying error, if any
}

func (r *Rejection) Error() string { return r.Reason }

// Unwrap returns the underlying error.
func (r *Rejection) Unwrap() error { return r.Cause }

func reject(format string, a ...any) error { return &Rejection{Reason: fmt.Sprintf(format, a...)} }

// Front-end constants used by the rules below.
const (
	cartridgeBlack   = 1 // cartridge-settings value "black only"
	cartridgeNA      = 6
	supplyASF        = 1
	supplyFront      = 10 // "front feeder", also reached through --paperload frontplain
	supplyFrontPlain = 11 // --paperload frontplain, an alias resolved below
	intentVivid      = 1003
	mediaPlain       = 0
	mediaPostcard    = 5
	mediaEnvelope    = 6
	mediaAddressCard = 1056
	mediaFineArt     = 1040
	mediaFineArtOth  = 1041
	sizeA4           = 2
	sizeB5           = 7
	sizeLetter       = 12
	sizeA4FineArt    = 1066
	sizeLetterFineAr = 1069
	marginBorderless = 1
	uibinPatternHS   = 1
	pgapNA           = 3
	defaultExtension = 3
)

// Configure opens the printer database in dir for the named model (e.g.
// "MP280") and analyses the command line the way the original front end does. It
// returns the database (with the selection applied) and the settings.
func Configure(dir, model string, args []string) (*Database, *Settings, error) {
	conf, err := LoadConf(filepath.Join(dir, "cif"+strings.ToLower(model)+".conf"))
	if err != nil {
		return nil, nil, err
	}
	raw, err := parseArgs(args)
	if err != nil {
		return nil, nil, err
	}
	if raw.set["internalversion"] || raw.set["supportnetwork"] {
		return nil, nil, ErrNoJob
	}
	id, ok := conf.ID("model", strings.ToUpper(model))
	if !ok {
		return nil, nil, reject("invalid printer model name")
	}
	s := &Settings{ModelID: id, Copies: 1, Extension: -1}
	if len(raw.extra) > 0 {
		s.File = raw.extra[0]
	}
	if raw.set["imageres"] {
		if v := raw.ints["imageres"]; v < 1 || v > 32767 {
			return nil, nil, reject("invalid image resolution")
		}
		s.ImageRes = raw.ints["imageres"]
	}
	if raw.set["bbox"] {
		b, ok := parseBBox(raw.strs["bbox"])
		if !ok {
			return nil, nil, reject("invalid bbox value")
		}
		s.BBox = &b
	}
	db, err := Open(dir, id)
	if err != nil {
		return nil, nil, err
	}
	if err := s.analyse(db, conf, raw, strings.ToUpper(model)); err != nil {
		return nil, nil, err
	}
	return db, s, nil
}

// parseBBox is the "--bbox a,b,c,d" parser: four comma separated
// non-negative integers, where an empty field reads as 0 and a field stops at
// its first non-digit.
func parseBBox(s string) ([4]int, bool) {
	var v [4]int
	i, pos := 0, 0
	for {
		j := pos
		for j < len(s) && (s[j] == '-' || (s[j] >= '0' && s[j] <= '9')) {
			if j-pos > 22 {
				return v, false
			}
			j++
		}
		if j < len(s) && s[j] != ',' {
			return v, false
		}
		if i > 3 {
			return v, false
		}
		n := atoi(s[pos:j])
		if n < 0 {
			return v, false
		}
		v[i] = n
		i++
		if j >= len(s) {
			break
		}
		pos = j + 1
	}
	return v, i == 4
}

// selectByName chooses the value a resource-file word stands for.
func selectByName(db *Database, conf *Conf, section, word string, obj Object, kindName string) error {
	id, ok := conf.ID(section, word)
	if !ok {
		return reject("invalid %s", kindName)
	}
	if err := db.Select(obj, id); err != nil {
		return reject("inappropriate %s selection", kindName)
	}
	return nil
}

func (s *Settings) analyse(db *Database, conf *Conf, raw *rawOptions, model string) error {
	ft := db.FormatType()
	var c Colour
	c.Intent = 0
	if ft >= formatType6 {
		if c.Intent = db.Current(ObjIntent); c.Intent < 0 {
			return reject("internal error")
		}
	}
	c.Gamma = 2 // 1.4
	if g := db.Current(ObjGamma); g > 0 {
		c.Gamma = g
	}
	borderlessOK := db.Find(ObjMarginType, 1) >= 0
	gapOK := db.Find(ObjPaperGap, 0) >= 0
	duplexOK := db.Find(ObjDuplex, 1) >= 0
	inkSettingsOK := db.Current(ObjInkSettings) >= 0 && db.Find(ObjInkSettings, cartridgeNA) < 0
	vividOK := db.Find(ObjVivid, 0) >= 0
	toneOK := db.Find(ObjBlackAdjust, 1001) >= 0

	set := raw.set
	if set["help"] {
		return ErrNoJob
	}
	if set["copies"] {
		if v := raw.ints["copies"]; v < 1 || v > 999 {
			return reject("invalid copies value")
		}
		s.Copies = raw.ints["copies"]
	}
	s.RevPrint, s.Collate = set["revprint"], set["collate"]
	count := 0
	if set["fit"] {
		s.Fit = FitShort
		count++
	}
	if set["full"] {
		s.Fit = FitLong
		count++
	}
	if set["location"] {
		switch raw.strs["location"] {
		case "center":
			s.Location = Center
		case "upperleft":
			s.Location = UpperLeft
		default:
			return reject("invalid location option")
		}
	}
	if set["percent"] {
		if v := raw.ints["percent"]; v < 20 || v > 400 {
			return reject("invalid scaling value")
		}
		s.Percent = raw.ints["percent"]
		count++
	}
	if count > 1 {
		return reject("only one of --fit / --full / --percent can be selected")
	}

	if set["cartridge"] {
		if err := selectByName(db, conf, "cartridge", raw.strs["cartridge"], ObjCartridge, "cartridge"); err != nil {
			return err
		}
	}
	if set["media"] {
		if err := selectByName(db, conf, "media", raw.strs["media"], ObjMedia, "mediatype"); err != nil {
			return err
		}
	}
	grayscale := false
	if set["grayscale"] {
		if err := db.Select(ObjGrayscale, 0); err != nil {
			return reject("inappropriate grayscale selection")
		}
		grayscale = true
	}
	if set["borderless"] {
		if !borderlessOK {
			return reject("--borderless is not supported by this printer")
		}
		if err := db.Select(ObjMarginType, 1); err != nil {
			return reject("inappropriate borderless selection")
		}
	}
	if set["extension"] {
		if !set["borderless"] {
			return reject("cannot select --extension without --borderless")
		}
		e := raw.ints["extension"]
		if ft == formatType4 {
			if e < 0 || e > 3 {
				return reject("invalid extension")
			}
			if db.Selectable(ObjOverMargin, e) < 0 {
				return reject("inappropriate extension selection")
			}
		} else if e < 0 || e > 3 {
			return reject("invalid extension")
		}
		s.Extension = e
	} else if d := db.Default(ObjOverMargin); d >= 0 {
		s.Extension = d
	} else if id, ok := conf.ID("extension_default", model); ok {
		s.Extension = id
	} else {
		s.Extension = defaultExtension
	}
	if set["quality"] {
		if err := selectByName(db, conf, "quality", raw.strs["quality"], ObjQuality, "print quality"); err != nil {
			return err
		}
	}
	frontPlain := false
	if set["paperload"] {
		id, ok := conf.ID("paperload", raw.strs["paperload"])
		if !ok {
			return reject("invalid paper load")
		}
		if id == supplyFrontPlain {
			if db.Find(ObjSupply, supplyFront) < 0 {
				return reject("inappropriate paper load selection")
			}
			id, frontPlain = supplyASF, true
		}
		if err := db.Select(ObjSupply, id); err != nil {
			return reject("inappropriate paper load selection")
		}
	}
	if set["papersize"] {
		if err := selectByName(db, conf, "papersize", raw.strs["papersize"], ObjPaperSize, "papersize"); err != nil {
			return err
		}
	}
	if set["duplex"] {
		if !duplexOK {
			return reject("--duplex is not supported by this printer")
		}
		if err := db.Select(ObjDuplex, 1); err != nil {
			return reject("inappropriate duplex selection")
		}
	}
	if set["stapleside"] {
		if !set["duplex"] {
			return reject("cannot select --stapleside without --duplex")
		}
		if v := raw.strs["stapleside"]; v != "longside" && v != "shortside" {
			return reject("invalid stapleside name")
		}
	}
	if set["halftoning"] {
		if err := selectByName(db, conf, "halftoning", raw.strs["halftoning"], ObjDither, "halftoning mode"); err != nil {
			return err
		}
	}
	if set["papergap"] {
		if !gapOK {
			return reject("--papergap is not supported by this printer")
		}
		if err := selectByName(db, conf, "papergap", raw.strs["papergap"], ObjPaperGap, "papergap"); err != nil {
			return err
		}
	}
	if set["renderintent"] {
		id, ok := conf.ID("renderintent", raw.strs["renderintent"])
		if !ok {
			return reject("invalid rendering intent")
		}
		if id == intentVivid {
			if !vividOK {
				return reject("inappropriate rendering intent")
			}
		} else if ft >= formatType6 && db.Selectable(ObjIntent, id) < 0 {
			return reject("inappropriate rendering intent")
		}
		c.Intent = id
	}
	if set["gamma"] {
		id, ok := conf.ID("gamma", raw.strs["gamma"])
		if !ok {
			return reject("invalid gamma value")
		}
		c.Gamma = id
	}
	balance := func(name string, dst *int) error {
		if set[name] {
			if v := raw.ints[name]; v < -50 || v > 50 {
				return reject("invalid %s value", name)
			}
			*dst = raw.ints[name]
		}
		return nil
	}
	for _, b := range []struct {
		n string
		d *int
	}{{"balance_c", &c.BalanceC}, {"balance_m", &c.BalanceM}, {"balance_y", &c.BalanceY}} {
		if err := balance(b.n, b.d); err != nil {
			return err
		}
	}
	if set["balance_k"] {
		if ft >= formatType6 {
			return reject("--balance_k is not supported by this printer")
		}
		if err := balance("balance_k", &c.BalanceK); err != nil {
			return err
		}
	}
	if set["tone"] {
		switch {
		case ft < formatType11 || !toneOK:
			return reject("--tone is not supported by this printer")
		case !db.ConfirmBlackAdjustMedia():
			return reject("inappropriate media selection")
		case !grayscale:
			return reject("cannot use --tone without --grayscale")
		}
		if err := balance("tone", &c.Tone); err != nil {
			return err
		}
	}
	if set["contrast"] {
		if ft < formatType6 {
			return reject("--contrast is not supported by this printer")
		}
		if err := balance("contrast", &c.Contrast); err != nil {
			return err
		}
	}
	if err := balance("density", &c.Density); err != nil {
		return err
	}
	s.Colour = c

	// Paper size.
	dev := &s.Device
	dev.PaperSize = db.Current(ObjPaperSize)
	dev.PaperWidth, dev.PaperHeight = -1, -1
	if dev.PaperSize == jobstream.SizeUser {
		for _, p := range []struct {
			name string
			dst  *int64
		}{{"paperwidth", &dev.PaperWidth}, {"paperheight", &dev.PaperHeight}} {
			if set[p.name] {
				v, ok := digitsOnly(raw.strs[p.name])
				if !ok {
					return reject("%s: invalid numeric value", raw.strs[p.name])
				}
				*p.dst = v
			}
		}
		if dev.PaperWidth == -1 || dev.PaperHeight == -1 {
			return reject("both --paperwidth and --paperheight must be set with --papersize user")
		}
	} else {
		if set["paperwidth"] || set["paperheight"] {
			return reject("cannot select --paperwidth and --paperheight without --papersize user")
		}
		if _, _, ok := PaperSizeOf(dev.PaperSize); !ok {
			return reject("unknown paper size")
		}
	}
	m, err := db.Margin(dev.PaperWidth, dev.PaperHeight)
	if err != nil {
		return &Rejection{Reason: err.Error(), Cause: err}
	}
	s.Paper = m
	dev.PaperWidth, dev.PaperHeight = m.Width, m.Height

	if set["inkcartridgesettings"] {
		if !inkSettingsOK {
			return reject("--inkcartridgesettings is not supported by this printer")
		}
		if err := selectByName(db, conf, "cartridge", raw.strs["inkcartridgesettings"], ObjInkSettings, "inkcartridgesettings"); err != nil {
			return err
		}
	}

	s.IVEC = false
	if ft >= formatType9 && db.Find(ObjCommandType, 1) >= 0 && !set["bidi"] {
		s.IVEC = true
	}
	if ft >= formatType8 {
		if db.ConfirmSupplySize() != 1 {
			return reject("inappropriate papersize selection")
		}
		if duplexOK {
			return reject("duplex size confirmation is not implemented")
		}
	}

	// Read the final selection back.
	dev.Model = s.ModelID
	dev.Media = db.Current(ObjMedia)
	dev.Quality = db.Current(ObjQuality)
	dev.Supply = db.Current(ObjSupply)
	if frontPlain && dev.Media == mediaPlain &&
		(dev.PaperSize == sizeA4 || dev.PaperSize == sizeLetter || dev.PaperSize == sizeB5) {
		dev.Supply = supplyFront
	}
	if dev.PaperGap = db.Current(ObjPaperGap); dev.PaperGap == -1 {
		dev.PaperGap = pgapNA
	}
	if dev.Dither = db.Current(ObjDither); dev.Dither == uibinPatternHS {
		dev.Dither = 0
	}
	dev.Banner = db.Current(ObjBanner)
	dev.Ink = db.Current(ObjCartridge)
	dev.Gray = db.Current(ObjGrayscale)
	if dev.MarginType = db.Current(ObjMarginType); dev.MarginType == -1 {
		dev.MarginType = 0
	}
	if dev.Duplex = db.Current(ObjDuplex); dev.Duplex == -1 {
		dev.Duplex = 0
	}
	if dev.InkSettings = db.Current(ObjInkSettings); dev.InkSettings == -1 {
		dev.InkSettings = cartridgeNA
	}
	return s.checkSettings(db, conf)
}

// digitsOnly parses decimal digits only.
func digitsOnly(s string) (int64, bool) {
	var v int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		v = v*10 + int64(r-'0')
	}
	return v, true
}

// checkSettings is the final consistency pass of the front end: the black
// cartridge setting is only kept where the media allows it, and fine-art
// media and sizes must go together.
func (s *Settings) checkSettings(db *Database, conf *Conf) error {
	dev := &s.Device
	ft := db.FormatType()
	if dev.InkSettings == cartridgeBlack {
		fresh, err := newBare(db.tables)
		if err != nil {
			return reject("internal error")
		}
		if err := fresh.defaults(); err != nil {
			return reject("internal error")
		}
		switch {
		case ft >= formatType10:
			if !fresh.forceCurrent(ObjInkSettings, dev.InkSettings) || !fresh.forceCurrent(ObjMedia, dev.Media) ||
				!fresh.forceCurrent(ObjMarginType, dev.MarginType) {
				return reject("internal error")
			}
			switch fresh.ConfirmCartridgeMedia() {
			case 0:
				dev.InkSettings = fresh.Default(ObjInkSettings)
			case 1:
			default:
				return reject("internal error")
			}
		case ft >= formatType9:
			if (dev.Media != mediaPlain && dev.Media != mediaPostcard && dev.Media != mediaAddressCard &&
				dev.Media != mediaEnvelope) || dev.MarginType == marginBorderless {
				dev.InkSettings = fresh.Default(ObjInkSettings)
			}
		case ft >= 7:
			if (dev.Media != mediaPlain && dev.Media != mediaPostcard && dev.Media != mediaEnvelope) ||
				dev.MarginType == marginBorderless {
				dev.InkSettings = fresh.Default(ObjInkSettings)
			}
		default:
			if dev.Media != mediaPlain || dev.MarginType == marginBorderless {
				dev.InkSettings = fresh.Default(ObjInkSettings)
			}
		}
	}
	fineMedia := dev.Media == mediaFineArt || dev.Media == mediaFineArtOth
	fineSize := dev.PaperSize == sizeA4FineArt || dev.PaperSize == sizeLetterFineAr
	if fineMedia != fineSize {
		return reject("you cannot print with this combination of media and paper size")
	}
	return nil
}

// forceCurrent is the front end's own "set current" on a database: it fails
// if the value is missing or disabled.
func (d *Database) forceCurrent(obj Object, val int) bool {
	o := int16(obj)
	for i := range d.entries {
		if d.entries[i].obj == o {
			d.entries[i].isCur = false
			if d.endOfBlockStrict(i) {
				break
			}
		}
	}
	for i := range d.entries {
		e := &d.entries[i]
		if e.obj != o {
			continue
		}
		if int(e.val) == val {
			if e.disabled {
				return false
			}
			e.isCur = true
			return true
		}
		if d.endOfBlockStrict(i) {
			e.val, e.isCur = int16(val), true
			return false
		}
	}
	return false
}

// binMethod converts the halftoning option value to the job interface's
// binarisation method (0 dither, 1 error diffusion).
func binMethod(dither int) int {
	switch dither {
	case 3:
		return 1
	case 4:
		return 2
	case 5:
		return 3
	}
	return 0
}

// JobSettings combines the command-line result with the printer-implied
// cartridge and resolution into the settings of a job.
func (s *Settings) JobSettings(com ComSystem) jobstream.Settings {
	d := s.Device
	model, _ := tableModel(s.ModelID)
	return jobstream.Settings{
		Model:       model,
		InkType:     com.InkType,
		InkSettings: com.InkSettings,
		Media:       d.Media, PaperSize: d.PaperSize, Supply: d.Supply, Dither: binMethod(d.Dither),
		Gray: d.Gray, Quality: d.Quality, Banner: d.Banner, MarginType: d.MarginType,
		PaperGap: d.PaperGap, Duplex: d.Duplex,
		PaperWidth: d.PaperWidth, PaperHeight: d.PaperHeight,
		Gamma: s.Colour.Gamma, Intent: s.Colour.Intent,
		BalanceC: s.Colour.BalanceC, BalanceM: s.Colour.BalanceM, BalanceY: s.Colour.BalanceY,
		BalanceK: s.Colour.BalanceK, Density: s.Colour.Density, Contrast: s.Colour.Contrast,
		MonochromeTone: s.Colour.Tone,
	}
}

// NewJob prepares the printer job these settings describe, started at the
// given time.
func (s *Settings) NewJob(db *Database, start time.Time) (*jobstream.Job, error) {
	dev := s.Device
	com, err := db.ComSystem(dev)
	if err != nil {
		return nil, err
	}
	j, err := jobstream.NewJob(db.Tables(), s.JobSettings(com), start)
	if err != nil {
		return nil, err
	}
	j.IVEC = s.IVEC
	return j, nil
}
