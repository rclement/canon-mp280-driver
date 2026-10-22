// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bytes"
	"testing"
)

func TestTIFFUnit(t *testing.T) {
	f := simpleTIFF(5, 4, 3, 8, 2, 2, ramp2)
	d, err := Open(bytes.NewReader(f.bytes()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NextPage()
	if err != nil || p == nil {
		t.Fatalf("page: %v", err)
	}
	if p.Width != 5 || p.Height != 4 || p.BytesPerPixel != 3 || p.XRes != 300 || p.YRes != 300 || p.Gray {
		t.Errorf("info %+v", p.Info)
	}
	row := make([]byte, 15)
	if err := p.ReadRow(row, 0, 2, 5); err != nil {
		t.Fatal(err)
	}
	if row[0] != ramp2(2*15) {
		t.Errorf("row 2 starts with %d", row[0])
	}
	// centimetres
	f.set(296, 3, 3)
	d, _ = Open(bytes.NewReader(f.bytes()))
	p, _ = d.NextPage()
	if p == nil || p.XRes < 761.9 || p.XRes > 762.1 {
		t.Errorf("cm resolution %v", p)
	}
	// LZW is refused
	f.set(259, 3, 5)
	d, _ = Open(bytes.NewReader(f.bytes()))
	if p, err := d.NextPage(); p != nil || err == nil {
		t.Error("compressed TIFF accepted")
	}
}
