// SPDX-License-Identifier: GPL-2.0-only

package pipeline

import (
	"errors"
	"fmt"

	"canon-mp280-driver/internal/colour"
	"canon-mp280-driver/internal/halftone"
	"canon-mp280-driver/internal/jobstream"
	"canon-mp280-driver/internal/printdb"
	"canon-mp280-driver/internal/raster"
)

// Config is everything the pipeline needs to print a job.
type Config struct {
	// Database is the printer database (cnb_3700.tbl) with the LUTs, tone
	// curves and halftone parameter trees.
	Database *printdb.Table
	// Job writes the non-raster parts of the stream. The caller chooses its
	// options (IVEC) before the call to Print.
	Job *jobstream.Job
	// Settings are the choices the job was created from; the colour and
	// halftone stages read media, quality, grayscale, halftoning, borderless
	// and the colour sliders from them.
	Settings jobstream.Settings

	// Copies is the number of copies of every page (0 or 1: one).
	Copies int
	// Collate prints whole copies of the document one after the other instead
	// of all copies of page 1, then all copies of page 2, ...
	Collate bool
	// ReversePrint prints the pages last to first.
	ReversePrint bool
}

// pageMode is the print mode derived from the settings: which colour tables,
// halftone entities and raster planes a page uses.
type pageMode struct {
	mode       colour.Mode
	gray       bool
	gamma      int
	adjust     colour.Adjust
	rasterArgs raster.Params
}

// cnclValue is the translation the original job interface applies to values of its
// own vocabulary: 1000 and more pass through as v-1000, the others go through
// a list of (from, to) pairs. -1 means "no translation".
func cnclValue(v int, pairs [][2]int) int {
	if v > 999 {
		return v - 1000
	}
	for _, p := range pairs {
		if p[0] == v {
			return p[1]
		}
	}
	return -1
}

var (
	// gammaPairs: gamma setting to gamma code (10 = 1.0, 14, 18, 22).
	gammaPairs = [][2]int{{0, 0}, {1, 10}, {2, 14}, {3, 18}, {4, 22}, {5, -1}}
	// intentPairs: rendering intent to intent code (3 = vivid).
	intentPairs = [][2]int{{0, 0}, {1, 2}, {2, 3}, {3, 4}, {4, -1}}
)

const (
	bjIntentVivid       = 3
	headTypeMP280       = 4  // print-head code of the MP280 in the raster parameter block
	qualityCodeDraft    = 5  // raster quality code of --quality 2
	qualityCodeStandard = 10 // raster quality code of --quality 3
	grayDBOn            = 1  // database value of "grayscale on"
	grayDBForced        = 2  // grayscale forced by the black-only cartridge setting

	// inkSettingColourOnly is the job interface value of --inkcartridgesettings
	// color (the black-only value is 1002).
	inkSettingColourOnly = 1001
	marginDBBorder       = 1 // database value of "borderless"
)

// modeFor derives the page mode from the settings and the job's resolved
// page setup (database codes of media, quality, gray, banner, margin).
func modeFor(s jobstream.Settings, setup *jobstream.PageSetup) (pageMode, error) {
	gray := setup.Gray == grayDBOn || setup.Gray == grayDBForced
	borderless := setup.MarginType == marginDBBorder
	colourOnly := s.InkSettings == inkSettingColourOnly
	req := colour.ModeRequest{
		Media:      s.Media,
		Quality:    setup.Quality/5 + 1,
		Gray:       gray,
		Vivid:      cnclValue(s.Intent, intentPairs) == bjIntentVivid,
		Pattern:    s.Dither == 0,
		Borderless: borderless,
		ColourOnly: colourOnly,
	}
	mode, err := colour.SelectMode(req)
	if err != nil {
		return pageMode{}, err
	}
	rasterQuality := setup.Quality
	if colourOnly && !borderless && s.Media == colour.MediaPlain && rasterQuality == qualityCodeDraft {
		// Colour cartridge on plain paper: the draft quality prints as quality 3
		// (see colour.SelectMode), plane depths included.
		rasterQuality = qualityCodeStandard
	}
	b2i := func(b bool) int16 {
		if b {
			return 1
		}
		return 0
	}
	return pageMode{
		mode:  mode,
		gray:  gray,
		gamma: cnclValue(s.Gamma, gammaPairs),
		adjust: colour.Adjust{
			BalanceCyan: s.BalanceC, BalanceMagenta: s.BalanceM, BalanceYellow: s.BalanceY,
			Density: s.Density, Contrast: s.Contrast,
		},
		// The parameter block the original output module reads: only these fields
		// matter, the rest is zero.
		rasterArgs: raster.Params{
			Media: int16(setup.Media), Quality: int16(rasterQuality), Grayscale: b2i(gray),
			Banner: int16(setup.Banner), HeadType: headTypeMP280, Borderless: b2i(borderless || colourOnly),
		},
	}, nil
}

// halftoneKinds maps the algorithm numbers of the mode table to the halftone
// package's entities.
var halftoneKinds = map[colour.Algorithm]halftone.Kind{
	colour.AlgorithmOrderedDither:  halftone.OrderedDithering,
	colour.AlgorithmFixedDiffusion: halftone.FixedWeightDiffusion,
	colour.AlgorithmDiffusion:      halftone.SingleInkDiffusion,
	colour.AlgorithmJointDiffusion: halftone.JointPairDiffusion,
}

// halftoneConfig builds the halftone configuration of a mode for rows of
// width output pixels, decoding the parameter trees from the database.
func halftoneConfig(db *printdb.Table, mode colour.Mode, width int) (halftone.Config, error) {
	cfg := halftone.Config{Width: width}
	trees := map[int][]byte{}
	for _, ink := range mode.Inks {
		kind, ok := halftoneKinds[ink.Algorithm]
		if !ok {
			return cfg, fmt.Errorf("pipeline: unknown halftone algorithm %d", ink.Algorithm)
		}
		var tree []byte
		if ink.TreeSub != 0 {
			if tree, ok = trees[ink.TreeSub]; !ok {
				entry, err := db.Entry(printdb.GroupBlockTrees, ink.TreeSub)
				if err != nil {
					return cfg, fmt.Errorf("pipeline: halftone tree %d: %w", ink.TreeSub, err)
				}
				if tree, err = printdb.PlainBlockTree(entry); err != nil {
					return cfg, fmt.Errorf("pipeline: halftone tree %d: %w", ink.TreeSub, err)
				}
				trees[ink.TreeSub] = tree
			}
		}
		cfg.Inks = append(cfg.Inks, halftone.InkConfig{
			ID: ink.Ink, Kind: kind, BitsPerPixel: ink.BitsPerPx, Levels: ink.Levels, Tree: tree,
		})
	}
	return cfg, nil
}

// newConverter builds the colour converter of a mode.
func newConverter(db *printdb.Table, m pageMode) (*colour.Converter, error) {
	return colour.NewConverter(db, colour.Config{
		Mode: m.mode, Gray: m.gray, Gamma: m.gamma, Adjust: m.adjust,
	})
}

var errNoJob = errors.New("pipeline: Config needs Database and Job")
