// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bytes"
	"fmt"
	"testing"
)

func ppmBytes(header string, w, h int, fill func(i int) byte) []byte {
	b := []byte(header)
	for i := 0; i < w*h*3; i++ {
		b = append(b, fill(i))
	}
	return b
}

func ramp(i int) byte { return byte(i*7 + 3) }

func TestPPMUnit(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		ok    bool
		w, h  int64
		bytes int // pixel bytes available
	}{
		{"plain", "P6\n3 2\n255\n", true, 3, 2, 18},
		{"comment", "P6\n# hi\n3 2\n255\n", true, 3, 2, 18},
		{"double space", "P6\n3  2\n255\n", true, 3, 2, 18},
		{"trailing space", "P6\n3 2 \n255\n", false, 0, 0, 18},
		{"maxval 65535", "P6\n3 2\n65535\n", false, 0, 0, 18},
		{"zero width", "P6\n0 2\n255\n", false, 0, 0, 18},
		{"no complete row", "P6\n3 2\n255\n", false, 0, 0, 8},
		{"one row of two", "P6\n3 2\n255\n", true, 3, 2, 9},
		{"one line header", "P6 3 2 255\n", false, 0, 0, 18},
		{"maxval trailing text", "P6\n3 2\n255abc\n", true, 3, 2, 18},
	}
	for _, c := range cases {
		data := append([]byte(c.in), bytes.Repeat([]byte{9}, c.bytes)...)
		d, err := OpenStdin(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: open: %v", c.name, err)
		}
		p, err := d.NextPage()
		if c.ok != (err == nil && p != nil) {
			t.Errorf("%s: ok=%v err=%v", c.name, c.ok, err)
			continue
		}
		if c.ok && (p.Width != c.w || p.Height != c.h) {
			t.Errorf("%s: got %dx%d", c.name, p.Width, p.Height)
		}
	}
}

func TestPPMMissingBytesAreZero(t *testing.T) {
	data := append([]byte("P6\n2 2\n255\n"), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10) // 10 of 12 bytes
	d, err := OpenStdin(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NextPage()
	if err != nil || p == nil {
		t.Fatal(err)
	}
	row := make([]byte, 6)
	if err := p.ReadRow(row, 0, 1, 2); err != nil {
		t.Fatal(err)
	}
	if want := []byte{7, 8, 9, 10, 0, 0}; !bytes.Equal(row, want) {
		t.Errorf("row 1 = %v, want %v", row, want)
	}
}

func TestPPMMultiPage(t *testing.T) {
	data := append(ppmBytes("P6\n2 1\n255\n", 2, 1, ramp), ppmBytes("P6\n1 3\n255\n", 1, 3, ramp)...)
	d, _ := OpenStdin(bytes.NewReader(data))
	var dims []string
	for {
		p, err := d.NextPage()
		if err != nil {
			t.Fatal(err)
		}
		if p == nil {
			break
		}
		dims = append(dims, fmt.Sprintf("%dx%d", p.Width, p.Height))
	}
	if fmt.Sprint(dims) != "[2x1 1x3]" {
		t.Errorf("pages %v", dims)
	}
	// A file delivers only its first page.
	f, _ := Open(bytes.NewReader(data))
	n := 0
	for {
		p, _ := f.NextPage()
		if p == nil {
			break
		}
		n++
	}
	if n != 1 {
		t.Errorf("file pages = %d, want 1", n)
	}
}

func TestPPMSizeLimitsLikeCShort(t *testing.T) {
	// Wider than 32767: header accepted, every row request fails (the original driver keeps the size in a short).
	data := ppmBytes("P6\n40000 1\n255\n", 40000, 1, ramp)
	d, _ := OpenStdin(bytes.NewReader(data))
	p, err := d.NextPage()
	if err != nil || p == nil {
		t.Fatalf("page: %v", err)
	}
	if err := p.ReadRow(make([]byte, 3*100), 0, 0, 100); err == nil {
		t.Error("row request on 40000 pixel wide image should fail")
	}
}
