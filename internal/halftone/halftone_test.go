// SPDX-License-Identifier: GPL-2.0-only

package halftone

import "testing"

func TestSingleQuantiserTables(t *testing.T) {
	cases := []struct {
		levels int
		// {first index, level, recon} breakpoints
		want [][3]int
	}{
		{2, [][3]int{{0, 0, 0}, {128, 1, 255}, {640, 0, 0}, {1023, 0, 0}}},
		{3, [][3]int{{0, 0, 0}, {63, 0, 0}, {64, 1, 128}, {190, 1, 128}, {191, 2, 255}, {640, 0, 0}}},
		{4, [][3]int{{42, 0, 0}, {43, 1, 85}, {127, 1, 85}, {128, 2, 170}, {213, 3, 255}, {639, 3, 255}, {640, 0, 0}}},
	}
	for _, c := range cases {
		q := newSingleQuantiser(c.levels)
		for _, w := range c.want {
			if int(q.level[w[0]]) != w[1] || int(q.recon[w[0]]) != w[2] {
				t.Errorf("L=%d idx %d: level %d recon %d, want %d %d", c.levels, w[0], q.level[w[0]], q.recon[w[0]], w[1], w[2])
			}
		}
	}
}

func TestPairCells(t *testing.T) {
	var c pairChannel
	// Boundaries 0, 10, 30 over u = value+128: value 5 is nearer 0 than 10 -> cell 0, ties go up.
	if err := c.buildCells([]int16{0, 10, 30}); err != nil {
		t.Fatal(err)
	}
	for _, w := range [][2]int{{-128, 0}, {-1, 0}, {0, 0}, {4, 0}, {5, 1}, {9, 1}, {10, 1}, {19, 1}, {20, 2}, {29, 2}, {30, 2}, {383, 2}} {
		if got := c.cell[w[0]+128]; int(got) != w[1] {
			t.Errorf("value %d: cell %d, want %d", w[0], got, w[1])
		}
	}
	if err := c.buildCells([]int16{0, 10, 500}); err == nil {
		t.Error("boundaries past 383 must be rejected")
	}
}

func TestBadTreesAreErrors(t *testing.T) {
	for _, b := range [][]byte{nil, make([]byte, 8), make([]byte, 64), {0x16, 0x85, 0x17, 0x50, 0xff, 0xff, 0xff, 0x7f, 1, 0, 0, 0, 0, 0, 0, 0}} {
		if _, err := NewSingleInkDiffuser(b, 1, 2, 64); err == nil {
			t.Errorf("tree %v accepted", b)
		}
		if _, err := NewPairDiffuser(b, b, 1, 2, 64); err == nil {
			t.Errorf("pair tree %v accepted", b)
		}
		if _, err := NewOrderedDither(b, 1, 2); err == nil {
			t.Errorf("dither tree %v accepted", b)
		}
	}
}

func TestFixedWeightProPlatinumTable(t *testing.T) {
	// Y of Photo Paper Pro Platinum q1: 2 bpp, 3 levels. A flat 50% field must
	// give mostly level 1 with some level 2 and 0 dots on average ~ 50%.
	f, err := NewFixedWeightDiffuser(2, 2, 3, 256)
	if err != nil {
		t.Fatal(err)
	}
	in := make([]uint16, 264)
	for i := range in {
		in[i] = 0x8000
	}
	sum := 0
	for line := 0; line < 8; line++ {
		out := make([]byte, 80)
		if err := f.Row(in, out, 0, 256, line); err != nil {
			t.Fatal(err)
		}
		for x := 0; x < 256; x++ {
			sum += int(out[x/4] >> ((3 - x%4) * 2) & 3)
		}
	}
	if avg := float64(sum) / (8 * 256); avg < 0.9 || avg > 1.1 {
		t.Fatalf("mean level %.2f, want about 1.0", avg)
	}
}

func TestMisalignedRunIsError(t *testing.T) {
	f, _ := NewFixedWeightDiffuser(2, 2, 3, 64)
	if err := f.Row(make([]uint16, 72), make([]byte, 24), 1, 8, 0); err == nil {
		t.Fatal("misaligned start accepted")
	}
	if err := f.Row(make([]uint16, 8), make([]byte, 24), 0, 16, 0); err == nil {
		t.Fatal("short input accepted")
	}
}
