// SPDX-License-Identifier: GPL-2.0-only

package jobstream

import (
	"errors"
	"fmt"
)

// Settings are the print choices of a job in the vocabulary of the front end
// (the values the front end produces). A value of
// 1000 or more is passed through to the printer database as value-1000; the
// other values are translated with the tables below.
type Settings struct {
	// Model is the database model number (370 for the MP280).
	Model int

	// InkType is the cartridge combination (1001 colour .. 1006), used when
	// InkSettings is InkSettingsFromType.
	InkType int
	// InkSettings is the user's cartridge choice (0..5 cartridge ids,
	// 6 = not applicable) or InkSettingsFromType.
	InkSettings int

	Media      int // media type, see MediaType values in package options
	PaperSize  int // front end paper size id, SizeUser for a custom size
	Supply     int // paper source (1 = automatic sheet feeder)
	Dither     int // halftoning: 0 pattern, 1 error diffusion
	Gray       int // GrayOn, GrayOff, GrayForced
	Quality    int // QualityHighest .. QualityHighSpeed
	Banner     int // 2 = not applicable
	MarginType int // MarginNormal or MarginBorderless
	PaperGap   int // 3 = not applicable
	Duplex     int // 0 = off (the MP280 has no duplexer)

	// PaperWidth and PaperHeight are the custom size in 1/100 mm; only used
	// when PaperSize is SizeUser.
	PaperWidth, PaperHeight int64

	// Colour-processing choices. They do not change the setup commands but
	// the library validates them when the job is configured.
	Gamma          int // 0..4 (default, 1.0, 1.4, 1.8, 2.2)
	Intent         int // 0 photo, 3 vivid are the only ones the engine accepts
	BalanceC       int // -50..50
	BalanceM       int
	BalanceY       int
	BalanceK       int
	Density        int // -50..50
	Contrast       int // -50..50
	MonochromeTone int // -50..50
}

// Values of Settings fields that the code needs by name.
const (
	InkSettingsFromType = 0x14 // use InkType instead of InkSettings
	inkBlackOnly        = 1002 // black cartridge only

	SizeUser = 39 // custom paper size

	GrayOn     = 0 // --grayscale
	GrayOff    = 1
	GrayForced = 2

	MarginNormal     = 0
	MarginBorderless = 1

	// Absolute quality positions as the front end numbers them.
	QualitySpecial   = -6 // --quality 1
	QualityHigh      = -5 // 2
	QualityMedium    = -4 // 3
	QualityNormal    = -3 // 4
	QualityHighSpeed = -2 // 5

	IntentPhoto = 0    // rendering intent "photo"
	IntentVivid = 1003 // "vivid" (database value 3)

	intentPhotoDB = 0 // database values the raster engine accepts
	intentVividDB = 3
)

// ErrInvalidSetting is returned (wrapped) when the library would refuse a
// value (the original driver error -5).
var ErrInvalidSetting = errors.New("jobstream: invalid setting")

// pairMap is a list of (from, to) pairs, used to translate value
// domains.
type pairMap [][2]int

func (m pairMap) lookup(v int) (int, bool) {
	for _, p := range m {
		if p[0] == v {
			return p[1], true
		}
	}
	return 0, false
}

// toDB translates a Settings value to the database value: values of 1000 and
// more pass through minus 1000, others go through the map.
func (m pairMap) toDB(v int) (int, bool) {
	if v > 999 {
		return v - 1000, true
	}
	return m.lookup(v)
}

// Translation tables from Settings values to database values.
var (
	cartridgeMap = pairMap{{4, 2}, {5, 1}, {6, 3}, {7, 1}, {8, 3}, {9, 2}, {10, 1}, {11, 3}, {0, 2}, {1, 1}, {2, 1}, {3, 3},
		{12, 2}, {13, 4}, {14, 6}, {17, 3}, {15, 1}, {16, 3}, {18, 1}, {19, 10}, {20, 10}}
	mediaMap = pairMap{{0, 0}, {1, 1}, {2, 3}, {3, 4}, {4, 6}, {5, 7}, {6, 8}, {7, 15}, {8, 16}, {9, 18}, {10, 20},
		{11, 21}, {12, 22}, {13, 24}, {14, 26}, {15, 27}, {16, 128}, {17, 30}}
	paperSizeMap = pairMap{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {5, 6}, {6, 7}, {7, 8}, {8, 9}, {9, 10}, {10, 11},
		{11, 12}, {12, 13}, {13, 14}, {14, 15}, {15, 16}, {16, 17}, {17, 18}, {18, 19}, {19, 20}, {20, 21}, {21, 22},
		{22, 23}, {23, 24}, {24, 26}, {25, 28}, {26, 29}, {27, 30}, {28, 31}, {29, 32}, {30, 33}, {31, 34}, {32, 35},
		{33, 40}, {34, 41}, {35, 42}, {36, 43}, {37, 44}, {38, 45}, {39, 256}, {40, 260}}
	supplyMap = pairMap{{0, 4}, {1, 7}, {2, 20}, {3, 10}, {4, 11}, {5, 15}, {6, 16}, {7, 8}, {8, 28}, {12, 31},
		{9, 29}, {10, 34}, {13, 36}, {14, 30}}
	ditherMap  = pairMap{{1, 2595}, {0, 2560}}
	grayMap    = pairMap{{1, 0}, {0, 1}, {2, 2}, {3, 2}}
	qualityMap = pairMap{{-6, 0}, {-5, 5}, {-4, 10}, {-3, 15}, {-2, 20}, {3, 30}}
	bannerMap  = pairMap{{0, 1}, {1, 2}, {2, 5}}
	marginMap  = pairMap{{0, 0}, {1, 1}, {2, 10}}
	gapMap     = pairMap{{0, 0}, {1, 1}, {2, 2}, {3, 10}}
	duplexMap  = pairMap{{0, 0}, {1, 2}}
	gammaMap   = pairMap{{0, 0}, {1, 10}, {2, 14}, {3, 18}, {4, 22}, {5, -1}}
	intentMap  = pairMap{{0, 0}, {1, 2}, {2, 3}, {3, 4}, {4, -1}}
)

// DBChoices are the choices of a job as database values: the numbers the
// printer tables are keyed by.
type DBChoices struct {
	Model                                                                           int
	Media, Cartridge, PaperSize, Supply, Gray, Quality, Banner, Margin, Gap, Duplex int
	Dither                                                                          int   // halftoning code (2560 pattern, 2595 error diffusion), -1 unknown
	Width, Height                                                                   int64 // custom paper size, 1/100 mm

	// Slot43 overrides the value of table slot 43 (default 99); 0 keeps the default.
	Slot43 int
}

func lookupOrErr(m pairMap, v int, what string) (int, error) {
	r, ok := m.toDB(v)
	if !ok || r < 0 {
		return 0, fmt.Errorf("%w: %s %d", ErrInvalidSetting, what, v)
	}
	return r, nil
}

// translate maps Settings to database values, failing like the original driver's
// the original driver for a value without a translation.
func (s Settings) translate() (DBChoices, error) {
	var u DBChoices
	var err error
	ink, gray := s.InkSettings, s.Gray
	if ink == InkSettingsFromType {
		ink = s.InkType
	}
	if ink == inkBlackOnly {
		gray = GrayForced
	}
	if u.Cartridge, err = lookupOrErr(cartridgeMap, ink, "cartridge"); err != nil {
		return u, err
	}
	if u.Media, err = lookupOrErr(mediaMap, s.Media, "media"); err != nil {
		return u, err
	}
	if u.PaperSize, err = lookupOrErr(paperSizeMap, s.PaperSize, "paper size"); err != nil {
		return u, err
	}
	if u.Supply, err = lookupOrErr(supplyMap, s.Supply, "paper source"); err != nil {
		return u, err
	}
	// An unknown halftoning is not refused here; the dither-class lookup
	// fails later.
	u.Dither = -1
	if d, ok := ditherMap.toDB(s.Dither); ok {
		u.Dither = d
	}
	if u.Gray, err = lookupOrErr(grayMap, gray, "grayscale"); err != nil {
		return u, err
	}
	if s.Quality >= 0 {
		return u, fmt.Errorf("%w: quality %d", ErrInvalidSetting, s.Quality)
	}
	if u.Quality, err = lookupOrErr(qualityMap, s.Quality, "quality"); err != nil {
		return u, err
	}
	if u.Banner, err = lookupOrErr(bannerMap, s.Banner, "banner"); err != nil {
		return u, err
	}
	if u.Margin, err = lookupOrErr(marginMap, s.MarginType, "margin type"); err != nil {
		return u, err
	}
	if u.Gap, err = lookupOrErr(gapMap, s.PaperGap, "paper gap"); err != nil {
		return u, err
	}
	if u.Duplex, err = lookupOrErr(duplexMap, s.Duplex, "duplex"); err != nil {
		return u, err
	}
	u.Model = s.Model
	if u.PaperSize == userPaperSizeCode {
		u.Width, u.Height = s.PaperWidth, s.PaperHeight
	}
	return u, nil
}

// validateColour applies the library's checks of the colour-processing values
// and of the intent the raster engine accepts.
func (s Settings) validateColour() error {
	if v, ok := gammaMap.toDB(s.Gamma); !ok || v < 0 {
		return fmt.Errorf("%w: gamma %d", ErrInvalidSetting, s.Gamma)
	}
	intent, ok := intentMap.toDB(s.Intent)
	if !ok || intent < 0 {
		return fmt.Errorf("%w: intent %d", ErrInvalidSetting, s.Intent)
	}
	for _, c := range []struct {
		name string
		v    int
	}{{"cyan balance", s.BalanceC}, {"magenta balance", s.BalanceM}, {"yellow balance", s.BalanceY},
		{"black balance", s.BalanceK}, {"density", s.Density}, {"contrast", s.Contrast},
		{"monochrome tone", s.MonochromeTone}} {
		if c.v < -50 || c.v > 50 {
			return fmt.Errorf("%w: %s %d", ErrInvalidSetting, c.name, c.v)
		}
	}
	if intent != intentPhotoDB && intent != intentVividDB {
		return fmt.Errorf("%w: intent %d is not supported by the raster engine", ErrInvalidSetting, s.Intent)
	}
	return nil
}
