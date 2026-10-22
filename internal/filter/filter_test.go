// SPDX-License-Identifier: GPL-2.0-only

package filter

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func genPPD(t *testing.T) *PPDFile {
	t.Helper()
	f, err := OpenPPD("../../ppd/canonmp280-oss.ppd")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestParseOptions(t *testing.T) {
	got := ParseOptions(`PageSize=A4 landscape nocollate MediaType='glossy paper' x=a\ b Zed=1 zed=2`)
	// sorted case-insensitively: collate, landscape, MediaType, PageSize, x, Zed
	want := []Setting{{Name: "collate", Value: "false"}, {Name: "landscape", Value: "true"}, {Name: "MediaType", Value: "glossy paper"},
		{Name: "PageSize", Value: "A4"}, {Name: "x", Value: "a b"}, {Name: "Zed", Value: "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestScanHeader(t *testing.T) {
	ps := "%!PS\n%%BeginFeature: *PageSize 4X6\n<</CNPageSizeName(4X6.bl)/PageSize[288 432]>>setpagedevice\n%%EndFeature\n%%Page: 1 1\n%%BeginFeature: *MediaType glossypaper\n%%EndPageSetup\nrest\n"
	h, err := ScanHeader(strings.NewReader(ps))
	if err != nil {
		t.Fatal(err)
	}
	want := []Setting{{Name: "PageSize", Value: "4X6.bl"}, {Name: "MediaType", Value: "glossypaper"}}
	if !reflect.DeepEqual(h.Params, want) {
		t.Errorf("params %v", h.Params)
	}
	rest, _ := io.ReadAll(h.Rest)
	if string(h.Head)+string(rest) != ps || string(rest) != "rest\n" {
		t.Errorf("head/rest split wrong: %q | %q", h.Head, rest)
	}
}

func TestBuild(t *testing.T) {
	cases := []struct {
		opts, uri string
		want      string
	}{
		{"", "", "--imageres 600 --papersize a4 --media plain --paperload asf --cartridge bkcolor --quality 3 --renderintent vivid --density 0 --contrast 0 --bbox 9,14,586,834 --fit"},
		{"PageSize=4X6.bl MediaType=glossypaper CNQuality=2", "",
			"--imageres 600 --extension 2 --borderless --papersize 4X6 --media glossypaper --paperload asf --cartridge bkcolor --quality 2 --renderintent vivid --density 0 --contrast 0 --fit"},
		// "Black Only" / "Color Only" print with one ink; cif rejects --cartridge bk|color
		{"CNCartridge=Bk", "", "--imageres 600 --papersize a4 --media plain --paperload asf --cartridge bkcolor --inkcartridgesettings bk --quality 3 --renderintent vivid --density 0 --contrast 0 --bbox 9,14,586,834 --fit"},
		// the status wrapper and the stock usb backend are uni-directional: no --bidi
		{"", "mp280usb://Canon/MP280%20series?serial=1", "--imageres 600 --papersize a4 --media plain --paperload asf --cartridge bkcolor --quality 3 --renderintent vivid --density 0 --contrast 0 --bbox 9,14,586,834 --fit"},
		{"", "usb://Canon/MP280%20series?serial=1", "--imageres 600 --papersize a4 --media plain --paperload asf --cartridge bkcolor --quality 3 --renderintent vivid --density 0 --contrast 0 --bbox 9,14,586,834 --fit"},
		{"CNQuality=9 CNGrayscale=True CNGamma=1.8 position=center", "cnijusb:/dev/usb/lp0",
			"--imageres 600 --papersize a4 --media plain --paperload asf --cartridge bkcolor --quality 3 --renderintent vivid --density 0 --grayscale --contrast 0 --gamma 1.8 --bbox 9,14,586,834 --fit --location center --bidi"},
	}
	for _, c := range cases {
		f := genPPD(t)
		p, err := Build(&Job{PPD: f, Options: ParseOptions(c.opts), DeviceURI: c.uri})
		if err != nil {
			t.Fatal(err)
		}
		got := strings.Join(p.CifArgs, " ")
		if got != c.want {
			t.Errorf("%q:\n got  %s\n want %s", c.opts, got, c.want)
		}
		if p.Resolution != 600 {
			t.Errorf("resolution %d", p.Resolution)
		}
	}
	p, _ := Build(&Job{PPD: genPPD(t)})
	if p.Width != 4958 || p.Height != 7016 {
		t.Errorf("A4 gs size %dx%d", p.Width, p.Height)
	}
}

func TestLowerExceptSize(t *testing.T) {
	for in, want := range map[string]string{"4X6": "4X6", "4X6.bl": "4X6.bl", "A4": "a4", "BkColor": "bkcolor", "8X10": "8X10", "4X": "4x"} {
		if got := lowerExceptSize(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}
