// SPDX-License-Identifier: GPL-2.0-only

package layout

import (
	"bytes"
	"testing"

	"canon-mp280-driver/internal/image"
	"canon-mp280-driver/internal/options"
)

// a4 at 600 dpi, normal margins (values of the MP280 table, 1/100 mm).
func a4() Input {
	return Input{
		Width: 100, Height: 50, PrinterXRes: 600, PrinterYRes: 600,
		PaperWidth: 21000, PaperLength: 29700, TopMargin: 340, LeftMargin: 340,
		AreaWidth: 20320, AreaLength: 28370,
	}
}

func TestComputeDefaultResolution(t *testing.T) {
	p, err := Compute(a4())
	if err != nil {
		t.Fatal(err)
	}
	// 100 px at 120 dpi on a 600 dpi grid: 500 px wide, 250 high.
	if p.OutWidth != 500 || p.OutHeight != 250 || p.MaskWidth != 100 || p.MaskHeight != 50 {
		t.Errorf("plan %+v", p)
	}
	if want := int64(4800); p.PageWidth != want-1 && p.PageWidth != want { // 20320/2540*600 = 4800
		t.Errorf("page width %d", p.PageWidth)
	}
}

func TestComputeYResolutionFollowsX(t *testing.T) {
	in := a4()
	in.XRes, in.YRes = 300, 75 // the original driver uses 300 for y as well
	p, _ := Compute(in)
	if p.OutWidth != 200 || p.OutHeight != 100 {
		t.Errorf("plan %+v", p)
	}
	in.XRes, in.YRes = 300, 0 // no y resolution: 120 dpi
	p, _ = Compute(in)
	if p.OutWidth != 200 || p.OutHeight != 250 {
		t.Errorf("plan %+v", p)
	}
}

func TestComputeFitCenterPercent(t *testing.T) {
	in := a4()
	in.Fit = options.FitShort
	p, _ := Compute(in)
	if p.OutWidth != p.PageWidth || p.OutHeight != 2400 {
		t.Errorf("fit: %+v", p)
	}
	in.Location = options.Center
	in.Percent = 50
	p, _ = Compute(in)
	if p.LeftSkip != p.PageWidth/4 {
		t.Errorf("center/percent: %+v", p)
	}
}

func TestComputeBBox(t *testing.T) {
	in := a4()
	in.BBox = &[4]int{10, 0, 50, 20} // points at 120 dpi: x 16.67..83.33 px
	p, _ := Compute(in)
	if p.WidthOffset != 17 || p.MaskWidth != 66 || p.HeightOffset != 50-33 {
		t.Errorf("bbox: %+v", p)
	}
	in.BBox = &[4]int{10, 0, 5000, 20} // outside the image: ignored
	p, _ = Compute(in)
	if p.WidthOffset != 0 || p.MaskWidth != 100 {
		t.Errorf("bbox outside: %+v", p)
	}
}

func TestHExtend(t *testing.T) {
	in := []byte{1, 2, 3}
	out := make([]byte, 6)
	hExtend(in, out, 3, 6, 1)
	if !bytes.Equal(out, []byte{1, 1, 2, 2, 3, 3}) {
		t.Errorf("up: %v", out)
	}
	out = make([]byte, 2)
	hExtend([]byte{1, 2, 3, 4}, out, 4, 2, 1)
	if !bytes.Equal(out, []byte{1, 3}) {
		t.Errorf("down: %v", out)
	}
}

func page(t *testing.T, ppm string, plan Plan, mirror bool) *Page {
	t.Helper()
	d, err := image.OpenStdin(bytes.NewReader([]byte(ppm)))
	if err != nil {
		t.Fatal(err)
	}
	img, err := d.NextPage()
	if err != nil || img == nil {
		t.Fatal(err)
	}
	p, err := NewPage(img, plan, mirror)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRows(t *testing.T) {
	ppm := "P6\n2 2\n255\n" + "\x01\x01\x01\x02\x02\x02" + "\x03\x03\x03\x04\x04\x04"
	plan := Plan{MaskWidth: 2, MaskHeight: 2, OutWidth: 4, OutHeight: 4, LeftSkip: 1, PageWidth: 8}
	p := page(t, ppm, plan, false)
	want := [][]byte{{1, 1, 2, 2}, {1, 1, 2, 2}, {3, 3, 4, 4}, {3, 3, 4, 4}}
	for y, w := range want {
		r, err := p.Row(y)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		for x := 1; x < 5; x++ {
			got = append(got, r.Pixels[3*x])
		}
		if !bytes.Equal(got, w) || r.White || r.LeftWhite != 1 || r.RightWhite != 3 {
			t.Errorf("row %d: %v left %d right %d white %v", y, got, r.LeftWhite, r.RightWhite, r.White)
		}
	}
	if _, err := p.Row(9); err == nil {
		t.Error("out of order row accepted")
	}
}

func TestWhiteAndMirror(t *testing.T) {
	ppm := "P6\n1 1\n255\n\xff\xff\xff"
	plan := Plan{MaskWidth: 1, MaskHeight: 1, OutWidth: 1, OutHeight: 1, PageWidth: 3}
	r, _ := page(t, ppm, plan, false).Row(0)
	if !r.White {
		t.Error("white row not detected")
	}
	ppm = "P6\n1 1\n255\n\x10\x20\x30"
	plan.LeftSkip = 0
	r, _ = page(t, ppm, plan, true).Row(0)
	if !bytes.Equal(r.Pixels[6:9], []byte{0x10, 0x20, 0x30}) || r.LeftWhite != 2 || r.RightWhite != 0 {
		t.Errorf("mirrored row %v left %d right %d", r.Pixels, r.LeftWhite, r.RightWhite)
	}
}

func TestDegeneratePlan(t *testing.T) {
	d, _ := image.OpenStdin(bytes.NewReader([]byte("P6\n1 1\n255\nabc")))
	img, _ := d.NextPage()
	if _, err := NewPage(img, Plan{PageWidth: 4}, false); err == nil {
		t.Error("zero-size plan accepted")
	}
	if !MirrorsMedia(2) || !MirrorsMedia(9) || MirrorsMedia(0) {
		t.Error("MirrorsMedia")
	}
}
