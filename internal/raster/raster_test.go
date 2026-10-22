// SPDX-License-Identifier: GPL-2.0-only

package raster

import (
	"bytes"
	"testing"
)

func TestPackBits(t *testing.T) {
	rep := func(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"empty", nil, nil},
		{"single", []byte{7}, []byte{0, 7}},
		{"literal", []byte{1, 2, 3}, []byte{2, 1, 2, 3}},
		{"run of two alone is a repeat", []byte{5, 5}, []byte{0xff, 5}},
		{"run of three", []byte{9, 9, 9}, []byte{0xfe, 9}},
		{"run of two folded into literal", []byte{1, 2, 2, 3}, []byte{3, 1, 2, 2, 3}},
		{"run of three closes literal", []byte{1, 2, 2, 2, 3}, []byte{0, 1, 0xfe, 2, 0, 3}},
		{"long run split", rep(4, 130), []byte{0x81, 4, 0xff, 4}},
		{"run of 128", rep(4, 128), []byte{0x81, 4}},
		{"literal of 128 splits", cat(rep(0, 0), seq(130)), cat([]byte{0x7f}, seq(128), []byte{1}, seq(130)[128:])},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appendPackBits(nil, tt.in); !bytes.Equal(got, tt.want) {
				t.Errorf("got % x, want % x", got, tt.want)
			}
		})
	}
}

// seq returns n bytes with no two equal neighbours.
func seq(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}

func TestZeroRun(t *testing.T) {
	tests := []struct {
		n    int
		want []byte
	}{
		{1, []byte{0, 0}},
		{2, []byte{0xff, 0}},
		{128, []byte{0x81, 0}},
		{129, []byte{0x81, 0, 0, 0}},
		{300, []byte{0x81, 0, 0x81, 0, 0xd5, 0}},
	}
	for _, tt := range tests {
		if got := appendZeroRun(nil, tt.n); !bytes.Equal(got, tt.want) {
			t.Errorf("n=%d: got % x, want % x", tt.n, got, tt.want)
		}
	}
}

func TestBannerRejected(t *testing.T) {
	if _, err := New(Params{Banner: 1}); err == nil {
		t.Error("banner mode accepted")
	}
}

func TestBlankPage(t *testing.T) {
	e, err := New(Params{Quality: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := e.AppendFlush(nil)
	want := []byte{0x1b, 0x28, 0x49, 1, 0, 1, 0x1b, 0x28, 0x4a, 1, 0, 8}
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestSingleBlackRow(t *testing.T) {
	// Mode 1 (grayscale quality 3: 1 bit per pixel), one row with the first pixel set.
	e, err := New(Params{Quality: 10, Grayscale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if e.Mode() != 1 || e.BitsPerPixel(0) != 1 {
		t.Fatalf("mode %d bpp %d", e.Mode(), e.BitsPerPixel(0))
	}
	out, err := e.AppendRow(nil, 8, []Plane{{Data: []byte{0x80}, End: 8}})
	if err != nil {
		t.Fatal(err)
	}
	// Setup, then nothing: the band is emitted at flush, preceded by a skip of one band.
	want := []byte{0x1b, 0x28, 0x62, 1, 0, 1, 0x1b, 0x28, 0x49, 1, 0, 1, 0x1b, 0x28, 0x4a, 1, 0, 8}
	if !bytes.Equal(out, want) {
		t.Fatalf("row: got % x, want % x", out, want)
	}
	out = e.AppendFlush(nil)
	want = []byte{
		0x1b, 0x28, 0x65, 2, 0, 0, 1, // skip one band
		0x1b, 0x28, 0x4c, 1, 0, 'K', // black only
		0x1b, 0x28, 0x46, 10, 0, // ESC ( F, 10 payload bytes
	}
	// Row slot 0 is packbits {0x80} = {0, 0x80} plus the 0x80 terminator; seven blank lines follow.
	want = append(want, 0, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80)
	if !bytes.Equal(out, want) {
		t.Fatalf("flush: got % x, want % x", out, want)
	}
}

func TestShortPlaneData(t *testing.T) {
	e, err := New(Params{Quality: 10, Grayscale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.AppendRow(nil, 0, []Plane{{Data: []byte{1}, End: 64}}); err == nil {
		t.Error("short plane data accepted")
	}
}

func TestTallBand(t *testing.T) {
	tests := []struct {
		name     string
		p        Params
		wantRows byte
	}{
		{"proplatinum q1", Params{Media: 0x33, Quality: 0, HeadType: 4}, 16},
		{"proplatinum q2", Params{Media: 0x33, Quality: 5, HeadType: 4}, 8},
		{"plain q3", Params{Media: 0, Quality: 10, HeadType: 4}, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := New(tt.p)
			if err != nil {
				t.Fatal(err)
			}
			planes := make([]Plane, e.NumPlanes())
			for i := range planes {
				planes[i] = Plane{Data: []byte{0xff, 0xff, 0xff, 0xff}, End: 8}
			}
			out, err := e.AppendRow(nil, 0, planes)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out, []byte{0x1b, 0x28, 0x4a, 0x01, 0x00, tt.wantRows}) {
				t.Errorf("no ESC ( J %d in % x", tt.wantRows, out)
			}
			// A row in the next 8-row slot must stay in the same band for the tall case.
			out, err = e.AppendRow(nil, 8, planes)
			if err != nil {
				t.Fatal(err)
			}
			if tall := tt.wantRows == 16; tall == (len(out) != 0) {
				t.Errorf("row 8 produced %d bytes, tall=%v", len(out), tall)
			}
		})
	}
}
