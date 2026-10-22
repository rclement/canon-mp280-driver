// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "fmt"

// Kind selects the halftoning algorithm of one ink.
type Kind int

const (
	// SingleInkDiffusion is SingleInkDiffuser (yellow, black).
	SingleInkDiffusion Kind = iota + 1
	// JointPairDiffusion is PairDiffuser. Exactly two inks of a mode use it
	// (cyan then magenta); the first carries the primary tree.
	JointPairDiffusion
	// OrderedDithering is OrderedDither.
	OrderedDithering
	// FixedWeightDiffusion is FixedWeightDiffuser (needs no tree).
	FixedWeightDiffusion
)

// InkConfig describes how one ink plane is produced.
type InkConfig struct {
	ID           int    // 0 C, 1 M, 2 Y, 3 K
	Kind         Kind   // algorithm
	BitsPerPixel int    // 1 or 2 bits per pixel in the output plane
	Levels       int    // number of dot levels (2..4)
	Tree         []byte // decoded parameter tree; nil for FixedWeightDiffusion
}

// Config describes a print mode's halftoning: the row width in pixels
// (4800, 2400 or 9600 for the MP280) and its inks in plane order.
type Config struct {
	Width int
	Inks  []InkConfig
}

// Range is the part of an ink row that holds dots: both ends are multiples
// of 8 pixels. End <= Start means the ink has no ink on that row.
type Range struct{ Start, End int }

// Empty reports whether the range holds no pixels.
func (r Range) Empty() bool { return r.End <= r.Start }

// Engine runs the halftoning entities of one print mode over single rows. It
// owns the error diffusion state but not the input row buffers; Page adds the
// buffer maintenance of the real pipeline on top.
type Engine struct {
	width   int
	inks    []InkConfig
	single  []func(in []uint16, out []byte, start, end, line int) error
	resets  []func()
	pair    *PairDiffuser
	pairA   int
	pairB   int
	scratch [2][]byte
}

// NewEngine builds the entities for cfg.
func NewEngine(cfg Config) (*Engine, error) {
	e := &Engine{width: cfg.Width, inks: cfg.Inks, pairA: -1, pairB: -1}
	e.single = make([]func([]uint16, []byte, int, int, int) error, len(cfg.Inks))
	for i, ink := range cfg.Inks {
		switch ink.Kind {
		case SingleInkDiffusion:
			d, err := NewSingleInkDiffuser(ink.Tree, ink.BitsPerPixel, ink.Levels, cfg.Width)
			if err != nil {
				return nil, fmt.Errorf("ink %d: %w", i, err)
			}
			e.single[i] = d.Row
			e.resets = append(e.resets, d.Reset)
		case OrderedDithering:
			d, err := NewOrderedDither(ink.Tree, ink.BitsPerPixel, ink.Levels)
			if err != nil {
				return nil, fmt.Errorf("ink %d: %w", i, err)
			}
			e.single[i] = d.Row
		case FixedWeightDiffusion:
			d, err := NewFixedWeightDiffuser(ink.ID, ink.BitsPerPixel, ink.Levels, cfg.Width)
			if err != nil {
				return nil, fmt.Errorf("ink %d: %w", i, err)
			}
			e.single[i] = d.Row
			e.resets = append(e.resets, d.Reset)
		case JointPairDiffusion:
			switch {
			case e.pairA < 0:
				e.pairA = i
			case e.pairB < 0:
				e.pairB = i
			default:
				return nil, fmt.Errorf("halftone: more than two inks use the joint pair entity")
			}
		default:
			return nil, fmt.Errorf("halftone: ink %d has unknown kind %d", i, ink.Kind)
		}
	}
	if e.pairA >= 0 {
		if e.pairB < 0 {
			return nil, fmt.Errorf("halftone: the joint pair entity needs two inks")
		}
		a, b := cfg.Inks[e.pairA], cfg.Inks[e.pairB]
		if a.BitsPerPixel != b.BitsPerPixel || a.Levels != b.Levels {
			return nil, fmt.Errorf("halftone: pair inks differ in packing")
		}
		p, err := NewPairDiffuser(a.Tree, b.Tree, a.BitsPerPixel, a.Levels, cfg.Width)
		if err != nil {
			return nil, err
		}
		e.pair = p
		e.resets = append(e.resets, p.Reset)
		for k := range e.scratch {
			e.scratch[k] = make([]byte, (paddedWidth(cfg.Width)*a.BitsPerPixel+7)/8+8)
		}
	}
	return e, nil
}

// Reset clears all error diffusion state.
func (e *Engine) Reset() {
	for _, r := range e.resets {
		r()
	}
}

// Row halftones one output row. in[k] is the input buffer of ink k indexed by
// absolute pixel, at least padded-width entries long; ranges[k] is the ink's
// run. planes[k] must be zero on entry and at least ceil(paddedWidth*bpp/8)
// bytes; the dots of ink k's run are written into it, other bytes are left
// alone. line is the absolute output row from the top of the page.
//
// The joint pair runs over the union of its two runs whenever either is
// non-empty and reads both buffers there; its output for an ink is kept only
// inside that ink's own run.
func (e *Engine) Row(line int, in [][]uint16, ranges []Range, planes [][]byte) error {
	if len(in) != len(e.inks) || len(ranges) != len(e.inks) || len(planes) != len(e.inks) {
		return fmt.Errorf("halftone: row has %d/%d/%d ink slices, mode has %d inks", len(in), len(ranges), len(planes), len(e.inks))
	}
	for i, f := range e.single {
		if f == nil || ranges[i].Empty() {
			continue
		}
		if err := f(in[i], planes[i], ranges[i].Start, ranges[i].End, line); err != nil {
			return err
		}
	}
	if e.pair == nil {
		return nil
	}
	ra, rb := ranges[e.pairA], ranges[e.pairB]
	if ra.Empty() && rb.Empty() {
		return nil
	}
	start, end := union(ra, rb)
	scratch := [2][]byte{e.scratch[0], e.scratch[1]}
	if err := e.pair.Row([2][]uint16{in[e.pairA], in[e.pairB]}, scratch, start, end, line); err != nil {
		return err
	}
	bpp := e.inks[e.pairA].BitsPerPixel
	for k, ink := range [2]int{e.pairA, e.pairB} {
		r := ranges[ink]
		if r.Empty() {
			continue
		}
		copy(planes[ink][r.Start*bpp/8:r.End*bpp/8], scratch[k][r.Start*bpp/8:r.End*bpp/8])
	}
	return nil
}

func union(a, b Range) (start, end int) {
	switch {
	case a.Empty():
		return b.Start, b.End
	case b.Empty():
		return a.Start, a.End
	}
	return min(a.Start, b.Start), max(a.End, b.End)
}
