// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "testing"

// noTreeConfig is a mode whose entities need no table, for testing buffer
// maintenance alone.
func noTreeConfig(width, nink int) Config {
	cfg := Config{Width: width}
	for i := 0; i < nink; i++ {
		cfg.Inks = append(cfg.Inks, InkConfig{ID: i, Kind: FixedWeightDiffusion, BitsPerPixel: 2, Levels: 3})
	}
	return cfg
}

// TestInkRowBuffers checks the persistent buffer rules (ranges and the whole
// buffer after every row) against recorded buffers from randomised
func TestPageRulesSmall(t *testing.T) {
	page, err := NewPage(noTreeConfig(64, 2))
	if err != nil {
		t.Fatal(err)
	}
	ones := func(n int, v uint16) []uint16 {
		s := make([]uint16, n)
		for i := range s {
			s[i] = v
		}
		return s
	}
	// Row 0: ink 0 fills [10,30), ink 1 nothing.
	res, _ := page.Row(0, 10, 30, [][]uint16{ones(20, 500), nil})
	if res.Ranges[0] != (Range{8, 32}) || !res.Ranges[1].Empty() {
		t.Fatalf("row 0 ranges %v", res.Ranges)
	}
	if b := page.Buffer(0); b[9] != 0 || b[10] != 500 || b[29] != 500 || b[30] != 0 {
		t.Fatal("row 0 buffer wrong")
	}
	// Row 1: narrower span to the right; ink 0 cells left of the span are
	// cleared by the empty-ink rule only for the ink without dots (ink 0 here
	// has dots at 40, so its stale cells at 10..29 stay).
	res, _ = page.Row(1, 40, 50, [][]uint16{ones(10, 7), nil})
	if res.Ranges[0] != (Range{40, 56}) {
		t.Fatalf("row 1 ranges %v", res.Ranges)
	}
	if page.Buffer(0)[20] != 500 {
		t.Fatal("stale cell outside the span must survive when the ink has dots")
	}
	// Row 2: ink 0 has no dots in its span: everything left of the span is wiped.
	res, _ = page.Row(2, 44, 48, [][]uint16{nil, ones(4, 9)})
	if !res.Ranges[0].Empty() || res.Ranges[1] != (Range{40, 48}) {
		t.Fatalf("row 2 ranges %v", res.Ranges)
	}
	if b := page.Buffer(0); b[20] != 0 || b[40] != 0 {
		t.Fatal("cells left of the span must be cleared for an ink without dots")
	}
	// White row: nothing changes.
	res, _ = page.Row(3, 5, 5, [][]uint16{nil, nil})
	if !res.Ranges[0].Empty() || page.Buffer(1)[44] != 9 {
		t.Fatal("white row must not touch the buffers")
	}
	page.Start()
	if page.Buffer(1)[44] != 0 {
		t.Fatal("Start must clear the buffers")
	}
	if _, err := page.Row(0, 0, 65, [][]uint16{nil, nil}); err == nil {
		t.Fatal("span beyond the width must be an error")
	}
}

func TestPageGapRule(t *testing.T) {
	// A fixed-weight diffuser leaves a visible error trail; a skipped input row
	// must clear it, a delivered white row must not.
	cfg := noTreeConfig(32, 1)
	run := func(skip bool) []byte {
		p, _ := NewPage(cfg)
		v := make([]uint16, 32)
		for i := range v {
			v[i] = 20000
		}
		p.BeginInputRow(0)
		p.Row(0, 0, 32, [][]uint16{v})
		if skip {
			p.BeginInputRow(5)
		} else {
			p.BeginInputRow(1)
		}
		res, _ := p.Row(5, 0, 32, [][]uint16{v})
		return append([]byte(nil), res.Planes[0][:8]...)
	}
	fresh, _ := NewPage(cfg)
	v := make([]uint16, 32)
	for i := range v {
		v[i] = 20000
	}
	fresh.BeginInputRow(5)
	ref, _ := fresh.Row(5, 0, 32, [][]uint16{v})
	if string(run(true)) != string(ref.Planes[0][:8]) {
		t.Fatal("skipped input rows must reset the error state")
	}
	if string(run(false)) == string(ref.Planes[0][:8]) {
		t.Fatal("consecutive rows must carry the error state")
	}
}
