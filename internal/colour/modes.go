// SPDX-License-Identifier: GPL-2.0-only

package colour

import (
	"errors"
	"fmt"
)

// Media identifiers as numbered in the original front end.conf [media] section.
const (
	MediaPlain        = 0
	MediaHighRes      = 8
	MediaTShirt       = 9
	MediaPostcard     = 5
	MediaEnvelope     = 6
	MediaGlossyPaper  = 12
	MediaIJPostcard   = 15
	MediaMatte        = 1028
	MediaOtherPhoto   = 1036
	MediaSemiGloss    = 1042
	MediaGlossyGold   = 1050
	MediaProPlatinum  = 1051
	MediaGlossyPost   = 1054
	MediaPostcardAddr = 1056
)

// Algorithm identifies the halftoning algorithm ("entity") assigned to an ink.
type Algorithm int

// Halftone algorithms used by the MP280. The numbers are the printer database's entity
// numbers and are kept because the printer database refers to them.
const (
	// AlgorithmOrderedDither is a 104x104 threshold-matrix dither (the "dither
	// pattern" halftoning option).
	AlgorithmOrderedDither Algorithm = 1
	// AlgorithmFixedDiffusion is error diffusion with fixed (7,5,3,1)/16
	// weights and no table; only the yellow of Pro Platinum at 1200 dpi.
	AlgorithmFixedDiffusion Algorithm = 2
	// AlgorithmDiffusion is serpentine error diffusion with intensity
	// dependent coefficients; used for yellow and black.
	AlgorithmDiffusion Algorithm = 5
	// AlgorithmJointDiffusion diffuses cyan and magenta together with a joint
	// two-dimensional quantiser.
	AlgorithmJointDiffusion Algorithm = 6
)

// Ink identifiers used by the back LUT, tone curves and the halftoner.
const (
	InkCyan    = 0
	InkMagenta = 1
	InkYellow  = 2
	InkBlack   = 3
)

// InkConfig says how one ink channel is halftoned.
type InkConfig struct {
	Ink       int       // InkCyan..InkBlack
	Algorithm Algorithm // halftone algorithm
	BitsPerPx int       // bits per pixel of the dot plane (1 or 2)
	Levels    int       // number of dot sizes/levels (2..4)
	TreeSub   int       // group-20 table id of the halftone parameters (0 = none)
}

// Mode is the colour/halftone configuration of a print mode: which look-up
// tables, tone curves, inks and halftone algorithms are used.
type Mode struct {
	Width    int         // output pixels per row: 4800, 2400 or 9600
	Inks     []InkConfig // in ascending ink id; 1 (gray K), 3 (CMY) or 4 (CMYK) entries
	FrontLUT int         // group-16 id of the front RGB->RGB table
	BackLUT  int         // group-16 id of the back RGB->ink table (0: none, K-only gray)
	ToneSub  int         // group-20 id of the tone-curve tree
	// BackBits is the precision of the back LUT output fed to the tone
	// curve (14).
	BackBits int
}

// ModeRequest is the job-level selection of a mode.
type ModeRequest struct {
	Media      int  // MediaPlain etc.
	Quality    int  // --quality 1..5
	Gray       bool // --grayscale
	Vivid      bool // --renderintent vivid
	Pattern    bool // --halftoning pattern (plain paper only)
	Borderless bool
	// ColourOnly is set when the job asks for the colour cartridge alone
	// (--inkcartridgesettings color): no black ink, so the page is printed
	// with CMY and the gray option is ignored.
	ColourOnly bool
}

// ErrUnsupportedMode is returned for combinations the original front end rejects.
var ErrUnsupportedMode = errors.New("colour: unsupported media/quality combination")

func ink(id int, alg Algorithm, bpp, levels, sub int) InkConfig {
	return InkConfig{Ink: id, Algorithm: alg, BitsPerPx: bpp, Levels: levels, TreeSub: sub}
}

// SelectMode returns the mode for a request. The tables were measured from
// the original driver over every accepted combination (they are data in the
// printer database that has not been decoded), so they are an explicit
// switch here rather than derived values.
func SelectMode(r ModeRequest) (Mode, error) {
	if r.ColourOnly && !r.Borderless {
		return selectColourOnly(r)
	}
	m, err := baseMode(r.Media, r.Quality, r.Gray, r.Vivid, r.Pattern)
	if err != nil {
		return Mode{}, err
	}
	isPostcardClass := r.Media == MediaPostcard || r.Media == MediaEnvelope || r.Media == MediaPostcardAddr
	if !r.Borderless && len(m.Inks) == 1 && isPostcardClass {
		// Gray on postcard-class media reuses the plain q3 gray front LUT and
		// the "2483" tone curve.
		m.FrontLUT = 1092 + vividOffset(r.Vivid)
		m.ToneSub = 2483
	}
	if r.Borderless && len(m.Inks) != 3 {
		// Borderless printing on plain-class media uses CMY only, with its own
		// LUTs; the gray option is ignored.
		m, err = baseMode(r.Media, r.Quality, false, r.Vivid, r.Pattern)
		if err != nil {
			return Mode{}, err
		}
		if len(m.Inks) == 4 {
			front, back := 1099, 2399
			if isPostcardClass {
				front, back = 1081, 2381
			}
			m.Inks = m.Inks[:3]
			m.FrontLUT = front + vividOffset(r.Vivid)
			m.BackLUT, m.ToneSub = back, back
		}
	}
	return m, nil
}

func vividOffset(vivid bool) int {
	if vivid {
		return 500
	}
	return 0
}

func baseMode(media, q int, gray, vivid, pattern bool) (Mode, error) {
	var front, back int
	photo, valid := false, false
	switch media {
	case MediaPlain:
		valid = q == 2 || q == 3 || q == 4
		front = map[int]int{2: 1091, 3: 1092, 4: 1093}[q]
		back = 2492
		if q == 2 {
			back = 2491
		}
	case MediaEnvelope, MediaPostcard, MediaPostcardAddr:
		valid, front, back = q == 2 || q == 3, 1082, 2482
	case MediaGlossyGold, MediaSemiGloss, MediaGlossyPaper, MediaGlossyPost:
		photo, valid, front, back = true, q == 2 || q == 3, 1021, 2321
	case MediaIJPostcard:
		photo, valid, front, back = true, q == 2 || q == 3, 1061, 2321
	case MediaMatte, MediaHighRes:
		photo, valid, front, back = true, q == 2 || q == 3, 1031, 2331
	case MediaOtherPhoto:
		photo, valid, front, back = true, q == 2, 1041, 2341
	case MediaTShirt:
		photo, valid, front, back = true, q == 2, 1051, 2321
	case MediaProPlatinum:
		photo, valid = true, q >= 1 && q <= 3
		front, back = 1001, 2301
		if q == 1 {
			front, back = 1010, 2310
		}
	default:
		return Mode{}, fmt.Errorf("%w: media %d", ErrUnsupportedMode, media)
	}
	if !valid || (pattern && !(media == MediaPlain && (q == 3 || q == 4))) {
		return Mode{}, fmt.Errorf("%w: media %d quality %d", ErrUnsupportedMode, media, q)
	}
	m := Mode{Width: 4800, BackBits: 14, FrontLUT: front + vividOffset(vivid), BackLUT: back, ToneSub: back}
	if q == 4 {
		m.Width = 2400
	}
	if photo && q == 1 {
		m.Width = 9600
	}
	switch {
	case gray && !photo:
		// K only; gray ramp goes through the front LUT, no back LUT.
		m.BackLUT = 0
		switch {
		case pattern:
			m.Inks = []InkConfig{ink(InkBlack, AlgorithmOrderedDither, 1, 2, 2098)}
		case media == MediaPlain && q == 2:
			m.Inks = []InkConfig{ink(InkBlack, AlgorithmDiffusion, 2, 3, 2018)}
		case media == MediaPlain && q == 4:
			m.Inks = []InkConfig{ink(InkBlack, AlgorithmDiffusion, 1, 2, 2082)}
		default:
			m.Inks = []InkConfig{ink(InkBlack, AlgorithmDiffusion, 1, 2, 2035)}
		}
	case photo:
		if media == MediaProPlatinum && q == 1 {
			m.Inks = []InkConfig{
				ink(InkCyan, AlgorithmJointDiffusion, 2, 3, 2023),
				ink(InkMagenta, AlgorithmJointDiffusion, 2, 3, 2033),
				ink(InkYellow, AlgorithmFixedDiffusion, 2, 3, 0)}
		} else {
			m.Inks = []InkConfig{
				ink(InkCyan, AlgorithmJointDiffusion, 2, 4, 2041),
				ink(InkMagenta, AlgorithmJointDiffusion, 2, 4, 2042),
				ink(InkYellow, AlgorithmDiffusion, 2, 4, 2043)}
		}
	case pattern && q == 3:
		m.Inks = []InkConfig{
			ink(InkCyan, AlgorithmOrderedDither, 2, 3, 2094), ink(InkMagenta, AlgorithmOrderedDither, 2, 3, 2095),
			ink(InkYellow, AlgorithmOrderedDither, 1, 2, 2096), ink(InkBlack, AlgorithmOrderedDither, 1, 2, 2097)}
	case pattern:
		m.Inks = []InkConfig{
			ink(InkCyan, AlgorithmOrderedDither, 1, 2, 2094), ink(InkMagenta, AlgorithmOrderedDither, 1, 2, 2095),
			ink(InkYellow, AlgorithmOrderedDither, 1, 2, 2096), ink(InkBlack, AlgorithmOrderedDither, 1, 2, 2097)}
	case media == MediaPlain && q == 2:
		m.Inks = []InkConfig{
			ink(InkCyan, AlgorithmJointDiffusion, 2, 4, 2041), ink(InkMagenta, AlgorithmJointDiffusion, 2, 4, 2042),
			ink(InkYellow, AlgorithmDiffusion, 2, 3, 2018), ink(InkBlack, AlgorithmDiffusion, 2, 3, 2018)}
	case media == MediaPlain && q == 4:
		m.Inks = []InkConfig{
			ink(InkCyan, AlgorithmJointDiffusion, 1, 2, 2080), ink(InkMagenta, AlgorithmJointDiffusion, 1, 2, 2081),
			ink(InkYellow, AlgorithmDiffusion, 1, 2, 2082), ink(InkBlack, AlgorithmDiffusion, 1, 2, 2082)}
	default: // plain q3, envelope and postcards q2/q3
		m.Inks = []InkConfig{
			ink(InkCyan, AlgorithmJointDiffusion, 2, 3, 2003), ink(InkMagenta, AlgorithmJointDiffusion, 2, 3, 2013),
			ink(InkYellow, AlgorithmDiffusion, 1, 2, 2035), ink(InkBlack, AlgorithmDiffusion, 1, 2, 2035)}
	}
	return m, nil
}

// selectColourOnly is the mode of a job printed with the colour cartridge
// alone. It is the colour mode without the black ink: gray is ignored, the
// CMY back LUT and tone curve of the borderless post-card class media (2381)
// replace the CMYK ones, and the front LUT is 1081 (1095 at quality 4, which
// only plain paper offers). Plain paper quality 2 uses the quality 3 mode. Media that already use three inks are unchanged.
func selectColourOnly(r ModeRequest) (Mode, error) {
	q := r.Quality
	if r.Media == MediaPlain && q == 2 {
		q = 3 // measured: the colour cartridge has no draft CMY mode on plain paper
	}
	m, err := baseMode(r.Media, q, false, r.Vivid, r.Pattern)
	if err != nil {
		return Mode{}, err
	}
	if len(m.Inks) == 4 {
		front := 1081
		if r.Quality == 4 {
			front = 1095
		}
		m.Inks = m.Inks[:3]
		m.FrontLUT = front + vividOffset(r.Vivid)
		m.BackLUT, m.ToneSub = 2381, 2381
	}
	return m, nil
}
