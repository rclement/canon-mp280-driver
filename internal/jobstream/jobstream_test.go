// SPDX-License-Identifier: GPL-2.0-only

package jobstream

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestXMLDocuments(t *testing.T) {
	s1, _ := StartJobXML(DefaultJobID, false)
	s2, _ := ModeShiftXML(DefaultJobID)
	e, _ := EndJobXML(DefaultJobID)
	for _, c := range []struct {
		name string
		doc  []byte
		size int
		part string
	}{
		{"StartJob", s1, 289, "<ivec:operation>StartJob</ivec:operation>"},
		{"ModeShift", s2, 391, "<vcn:ijmode>1</vcn:ijmode>"},
		{"EndJob", e, 263, "<ivec:operation>EndJob</ivec:operation>"},
	} {
		if len(c.doc) != c.size {
			t.Errorf("%s: %d bytes, want %d", c.name, len(c.doc), c.size)
		}
		if !strings.Contains(string(c.doc), c.part) || !strings.HasSuffix(string(c.doc), "</cmd>") {
			t.Errorf("%s: unexpected text %q", c.name, c.doc)
		}
	}
	b, _ := StartJobXML(DefaultJobID, true)
	if !strings.Contains(string(b), "<ivec:bidi>1</ivec:bidi>") {
		t.Error("bidi flag not written")
	}
	if _, err := StartJobXML("short", false); err == nil {
		t.Error("short job id accepted")
	}
}

func TestBJLBlocks(t *testing.T) {
	if got := hex.EncodeToString(SSRDefinition()); got != "1b5b4b0b00001e0009"+hex.EncodeToString([]byte("SSR=DF;")) {
		t.Errorf("SSR definition %s", got)
	}
	ts := time.Date(2026, 10, 18, 9, 5, 7, 0, time.Local)
	blk, err := StartJobBlock(ts)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0x1b, '[', 'K', 2, 0, 0, 0x1f}, "BJLSTART\nControlMode=Common\nSetTime=20261018090507\nBJLEND\n"...)
	want = append(want, 0x1b, '[', 'K', 2, 0, 0, 0x0f)
	if !bytes.Equal(blk, want) || len(blk) != 72 {
		t.Errorf("StartJobBlock = %q (%d bytes)", blk, len(blk))
	}
	if v, _ := SetTimeValue(time.Date(12345, 1, 2, 3, 4, 5, 0, time.UTC)); v != "23450102030405" {
		t.Errorf("year is printed modulo 10000, got %s", v)
	}
	if _, err := SetTimeValue(time.Date(-5, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("negative year accepted")
	}
	if got := hex.EncodeToString(PageFooter()); got != "1b2865020000010c" {
		t.Errorf("page footer %s", got)
	}
	if got := hex.EncodeToString(JobFooter()); got != "1b2862010000"+"1b2861010000"+"1b40" {
		t.Errorf("job footer %s", got)
	}
}

func TestCeilDiv(t *testing.T) {
	for _, c := range []struct{ x, d, want int64 }{{0, 254, 0}, {1, 254, 1}, {254, 254, 1}, {255, 254, 2}, {508, 254, 2}, {509, 254, 3}} {
		if got := ceilDiv(c.x, c.d); got != c.want {
			t.Errorf("ceilDiv(%d,%d)=%d want %d", c.x, c.d, got, c.want)
		}
	}
}

func TestSettingsTranslate(t *testing.T) {
	s := Settings{Model: 370, InkSettings: InkSettingsFromType, InkType: 1004, Media: 0, PaperSize: 2, Supply: 1,
		Dither: 1, Gray: GrayOff, Quality: QualityMedium, Banner: 2, MarginType: MarginNormal, PaperGap: 3}
	u, err := s.translate()
	if err != nil {
		t.Fatal(err)
	}
	want := DBChoices{Model: 370, Media: 0, Cartridge: 4, PaperSize: 3, Supply: 7, Gray: 0, Quality: 10, Banner: 5, Margin: 0, Gap: 10, Duplex: 0, Dither: 2595}
	if u != want {
		t.Errorf("translate = %+v, want %+v", u, want)
	}
	// Black cartridge forces the gray input value 2.
	s.InkSettings = 1002
	if u, _ = s.translate(); u.Gray != 2 || u.Cartridge != 2 {
		t.Errorf("black cartridge: %+v", u)
	}
	bad := s
	bad.Quality = 3
	if _, err := bad.translate(); err == nil {
		t.Error("non-negative quality accepted")
	}
	s.Intent = IntentVivid
	s.Gamma = 2
	if err := s.validateColour(); err != nil {
		t.Errorf("vivid refused: %v", err)
	}
	s.Intent = 1
	if err := s.validateColour(); err == nil {
		t.Error("graphics intent accepted")
	}
	s.Intent, s.Density = 0, 51
	if err := s.validateColour(); err == nil {
		t.Error("density 51 accepted")
	}
}
