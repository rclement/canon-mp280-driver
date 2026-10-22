// SPDX-License-Identifier: GPL-2.0-only

package colour

import "testing"

// Known values of the fixed-point routines (inputs and results recorded once
// and kept as fixtures; the slider tables are built from these routines).
func TestFixedPointKnownValues(t *testing.T) {
	ratio := []struct{ a, b, want uint32 }{
		{87892090, 2343488274, 161081946},
		{4146959328, 3010242006, 1621850905},
		{1827760816, 668418708, 3154459805},
	}
	for _, c := range ratio {
		if got := fixRatio(c.a, c.b); got != c.want {
			t.Errorf("fixRatio(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	sqrt := []struct{ p, want uint32 }{
		{87892090, 614405120}, {4146959328, 4220314525}, {1827760816, 2801816005},
	}
	for _, c := range sqrt {
		if got := fixSqrt16(c.p); got != c.want {
			t.Errorf("fixSqrt16(%d) = %d, want %d", c.p, got, c.want)
		}
	}
	mul := []struct {
		p1, p2, p3, p4 uint32
		want           int32
	}{
		{87892090, 13, 2343488274, 13, 670631572},
		{4146959328, 17, 3010242006, 4, 48823616},
		{1827760816, 33, 668418708, 32, -1}, // bit count above 32 is refused
	}
	for _, c := range mul {
		if got := fixMul(c.p1, c.p2, c.p3, c.p4); got != c.want {
			t.Errorf("fixMul(%d,%d,%d,%d) = %d, want %d", c.p1, c.p2, c.p3, c.p4, got, c.want)
		}
	}
	pow := []struct {
		xm           uint32
		xf           int32
		ym, yf, want uint32
	}{
		{408065076, 30, 3907746633, 30, 31749145},
		{2127060039, 30, 1703563823, 3, 0},
		{1245300505, 30, 3657851537, 30, 1779046076},
	}
	for _, c := range pow {
		if got := fixPow(c.xm, c.xf, c.ym, c.yf); got != c.want {
			t.Errorf("fixPow(%d,%d,%d,%d) = %d, want %d", c.xm, c.xf, c.ym, c.yf, got, c.want)
		}
	}
}

func fnv(t []uint16) uint64 {
	h := uint64(1469598103934665603)
	for _, v := range t {
		h ^= uint64(v & 0xff)
		h *= 1099511628211
		h ^= uint64(v >> 8)
		h *= 1099511628211
	}
	return h
}

func TestSliderTablesKnownHashes(t *testing.T) {
	cases := []struct {
		a      Adjust
		hashes [3]uint64 // colour tables R, G, B
	}{
		{Adjust{0, 0, -4, -14, 26}, [3]uint64{0xb588a17b26eda43b, 0xb588a17b26eda43b, 0x0d0525cfa4664f66}},
		{Adjust{0, 0, 0, -16, 0}, [3]uint64{0xb596f7ecf9b79d27, 0xb596f7ecf9b79d27, 0xb596f7ecf9b79d27}},
	}
	for _, c := range cases {
		tab, err := NewSliderTables(c.a, 8)
		if err != nil {
			t.Fatal(err)
		}
		for k := range tab {
			if h := fnv(tab[k]); h != c.hashes[k] {
				t.Errorf("%+v table %d: hash %016x, want %016x", c.a, k, h, c.hashes[k])
			}
		}
	}
}

func TestSliderValidation(t *testing.T) {
	if _, err := NewSliderTables(Adjust{Density: 51}, 8); err == nil {
		t.Error("density 51 accepted")
	}
	if !(Adjust{}).IsZero() || (Adjust{Contrast: 1}).IsZero() {
		t.Error("IsZero wrong")
	}
	tab, _ := NewSliderTables(Adjust{Contrast: 20}, 8)
	if tab[0][0] != 0 || tab[0][255] != 255 {
		t.Errorf("endpoints not fixed: %d %d", tab[0][0], tab[0][255])
	}
}

func TestSelectModeRules(t *testing.T) {
	cases := []struct {
		req            ModeRequest
		front, back, n int
		ok             bool
	}{
		{ModeRequest{Media: MediaPlain, Quality: 3}, 1092, 2492, 4, true},
		{ModeRequest{Media: MediaPlain, Quality: 3, Vivid: true}, 1592, 2492, 4, true},
		{ModeRequest{Media: MediaPlain, Quality: 3, Borderless: true}, 1099, 2399, 3, true},
		{ModeRequest{Media: MediaPostcard, Quality: 2, Borderless: true}, 1081, 2381, 3, true},
		{ModeRequest{Media: MediaPostcard, Quality: 2, Gray: true}, 1092, 0, 1, true},
		{ModeRequest{Media: MediaPlain, Quality: 5}, 0, 0, 0, false},
		{ModeRequest{Media: MediaMatte, Quality: 3, Pattern: true}, 0, 0, 0, false},
	}
	for _, c := range cases {
		m, err := SelectMode(c.req)
		if (err == nil) != c.ok {
			t.Fatalf("%+v: err=%v", c.req, err)
		}
		if !c.ok {
			continue
		}
		if m.FrontLUT != c.front || m.BackLUT != c.back && c.back != 0 || len(m.Inks) != c.n {
			t.Errorf("%+v: front %d back %d inks %d", c.req, m.FrontLUT, m.BackLUT, len(m.Inks))
		}
	}
	if m, _ := SelectMode(ModeRequest{Media: MediaPostcard, Quality: 2, Gray: true}); m.ToneSub != 2483 {
		t.Errorf("postcard gray tone curve %d", m.ToneSub)
	}
}

func TestToneCurve(t *testing.T) {
	var s [256]uint16
	for i := range s {
		s[i] = uint16(i * 16) // identity curve: 255*16 = 4080
	}
	c, err := NewToneCurve(s, 4080)
	if err != nil {
		t.Fatal(err)
	}
	for v := 0; v <= 16320; v += 37 {
		if got := c.Apply(uint16(v), 14); int(got) != v*4 {
			t.Fatalf("Apply(%d)=%d want %d", v, got, v*4)
		}
	}
	if _, err := NewToneCurve(s, 1000); err == nil {
		t.Error("bad maximum accepted")
	}
}

func TestLuma(t *testing.T) {
	if got := Luma(255, 255, 255, 8, 12); got != 4080 {
		t.Errorf("white luma %d", got)
	}
	if got := Luma(0, 0, 0, 8, 12); got != 0 {
		t.Errorf("black luma %d", got)
	}
}
