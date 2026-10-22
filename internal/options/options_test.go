// SPDX-License-Identifier: GPL-2.0-only

package options

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"canon-mp280-driver/internal/jobstream"
)

func TestParseConf(t *testing.T) {
	c := ParseConf([]byte("# x\r\n[model]\r\nMP280\t\t1370\r\nMP250 1356\n\n[quality]\n1\t\t-6\n[ bad name ]\nk 3 4\nz 9\n"))
	for _, tc := range []struct {
		sec, key string
		want     int
		ok       bool
	}{{"model", "MP280", 1370, true}, {"model", "MP250", 1356, true}, {"model", "nope", 0, false},
		{"quality", "1", -6, true}, {"badname", "k", 0, false}, {"badname", "z", 0, false}, {"none", "x", 0, false}} {
		got, ok := c.ID(tc.sec, tc.key)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ID(%q,%q) = %d,%v want %d,%v", tc.sec, tc.key, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseInt(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
		ok   bool
	}{{"0", 0, true}, {"42", 42, true}, {"-50", -50, true}, {"+7", 7, true}, {"0x10", 16, true}, {"010", 8, true},
		{"", 0, true}, {" 5", 5, true}, {"5 ", 0, false}, {"abc", 0, false}, {"08", 0, false}, {"0x", 0, false},
		{"-", 0, false}, {"2147483648", 0, false}, {"-2147483648", -2147483648, true}} {
		got, err := parseInt(tc.in)
		if (err == nil) != tc.ok || (tc.ok && got != tc.want) {
			t.Errorf("parseInt(%q) = %d, %v", tc.in, got, err)
		}
	}
}

func TestParseArgs(t *testing.T) {
	r, err := parseArgs([]string{"--media", "plain", "--quality=3", "-g", "-pa4", "-c", "bk", "--copies", "2", "file.ppm", "--", "-x"})
	if err != nil {
		t.Fatal(err)
	}
	if r.strs["media"] != "plain" || r.strs["quality"] != "3" || !r.set["grayscale"] || r.strs["papersize"] != "a4" ||
		r.strs["cartridge"] != "bk" || r.ints["copies"] != 2 || !reflect.DeepEqual(r.extra, []string{"file.ppm", "-x"}) {
		t.Errorf("unexpected parse: %+v", r)
	}
	for _, bad := range [][]string{{"--bogus"}, {"--media"}, {"--fit=1"}, {"--copies", "x"}, {"-z"}, {"-m"}} {
		if _, err := parseArgs(bad); !errors.Is(err, ErrUsage) {
			t.Errorf("parseArgs(%v) = %v, want usage error", bad, err)
		}
	}
}

func TestParseBBox(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want [4]int
		ok   bool
	}{{"1,2,3,4", [4]int{1, 2, 3, 4}, true}, {",,,", [4]int{}, true}, {"1,2,3", [4]int{}, false},
		{"1,2,3,4,5", [4]int{}, false}, {"-1,2,3,4", [4]int{}, false}, {"1,a,3,4", [4]int{}, false}, {"1,2,3,4,", [4]int{}, false}} {
		got, ok := parseBBox(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseBBox(%q) = %v,%v", tc.in, got, ok)
		}
	}
}

func TestValueMaps(t *testing.T) {
	// A4 round trip: front-end 2 <-> table 3.
	if v := toTableValue(int16(ObjPaperSize), 2); v != 3 {
		t.Errorf("A4 to table = %d", v)
	}
	if v := fromTableValue(10, 3); v != 2 {
		t.Errorf("A4 from table = %d", v)
	}
	// Values of 1000+ pass through, table-only values get +1000.
	if v := toTableValue(int16(ObjPaperSize), 1050); v != 50 {
		t.Errorf("L to table = %d", v)
	}
	if v := fromTableValue(10, 50); v != 50 && v != 1050 {
		t.Errorf("L from table = %d", v)
	}
	if v := fromTableValue(1, 55); v != 1055 {
		t.Errorf("media passthrough = %d", v)
	}
	if v := fromTableValue(7, 5); v != 5 { // resolution objects pass through
		t.Errorf("pass-through object = %d", v)
	}
	if v := toTableValue(int16(ObjQuality), -4); v != 10 {
		t.Errorf("quality 3 = %d", v)
	}
}

func TestPaperSizeOf(t *testing.T) {
	w, h, ok := PaperSizeOf(2)
	if !ok || w != 21000 || h != 29700 {
		t.Errorf("A4 = %d x %d", w, h)
	}
	if _, _, ok := PaperSizeOf(9999); ok {
		t.Error("unknown size found")
	}
}

func tableDir(t *testing.T) string {
	d := os.Getenv("MP280_TABLE_DIR")
	if d == "" {
		d = "../../data/canon" // the Canon tables fetched by make
	}
	if _, err := os.Stat(filepath.Join(d, "cnb_3700.tbl")); err != nil {
		t.Fatalf("data tables missing: %v", err)
	}
	return d
}

func TestDefaultSelection(t *testing.T) {
	db, s, err := Configure(tableDir(t), "MP280", []string{"--cartridge", "bkcolor"})
	if err != nil {
		t.Fatal(err)
	}
	if db.FormatType() != 11 || s.ModelID != 1370 {
		t.Errorf("format type %d, model %d", db.FormatType(), s.ModelID)
	}
	d := s.Device
	// Defaults: plain paper, A4, quality 3 (-4), automatic sheet feeder, error diffusion.
	if d.Media != 0 || d.PaperSize != 2 || d.Quality != jobstream.QualityMedium || d.Supply != 1 || d.Dither != 3 {
		t.Errorf("unexpected defaults: %+v", d)
	}
	if !s.IVEC || s.Paper.Width != 21000 || s.Paper.Height != 29700 {
		t.Errorf("ivec %v paper %dx%d", s.IVEC, s.Paper.Width, s.Paper.Height)
	}
	j, err := s.NewJob(db, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if j.Setup().XDPI != 600 {
		t.Errorf("resolution %d", j.Setup().XDPI)
	}
}

func TestRejections(t *testing.T) {
	dir := tableDir(t)
	for _, tc := range []struct {
		args []string
		want error
	}{
		{[]string{"--help"}, ErrNoJob},
		{[]string{"--internalversion"}, ErrNoJob},
		{[]string{"--media", "bogus"}, nil},
		{[]string{"--quality", "4", "--media", "glossypaper"}, nil},
		{[]string{"--fit", "--full"}, nil},
		{[]string{"--extension", "1"}, nil},
		{[]string{"--duplex"}, nil},
		{[]string{"--papersize", "user", "--paperwidth", "10", "--paperheight", "10"}, nil},
		{[]string{"--renderintent", "graphics"}, nil},
	} {
		_, _, err := Configure(dir, "MP280", tc.args)
		if err == nil {
			t.Errorf("%v accepted", tc.args)
		} else if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%v: %v, want %v", tc.args, err, tc.want)
		}
	}
	// A custom size inside the limits is accepted and reported back.
	_, s, err := Configure(dir, "MP280", []string{"--papersize", "user", "--paperwidth", "10000", "--paperheight", "20000"})
	if err != nil || s.Paper.Width != 10000 || s.Paper.Height != 20000 {
		t.Errorf("user size: %v %+v", err, s)
	}
	var sr *SizeRangeError
	if _, _, err = Configure(dir, "MP280", []string{"--papersize", "user", "--paperwidth", "10", "--paperheight", "10"}); !errors.As(err, &sr) {
		t.Errorf("range error expected, got %v", err)
	}
}
