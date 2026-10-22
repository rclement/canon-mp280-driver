// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

type bmpSpec struct {
	w, h      int32
	bits      uint16
	off       uint32
	sizeField int32 // 0: derive from the data
	xppm      int32
	yppm      int32
	cut       int // bytes removed from the end
	rowPad    int // extra bytes per row beyond the 4-byte alignment (0 = standard)
}

func makeBMP(s bmpSpec, rnd *rand.Rand) []byte {
	w := int(s.w)
	if w < 0 {
		w = 0
	}
	rowBytes := (w*3 + 3) &^ 3
	rows := int(s.h)
	if rows < 0 {
		rows = -rows
	}
	data := make([]byte, rowBytes*rows)
	for i := range data {
		data[i] = byte(rnd.Intn(256))
	}
	size := s.sizeField
	if size == 0 {
		size = int32(54 + len(data))
	}
	b := make([]byte, 54)
	b[0], b[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(b[2:], uint32(size))
	binary.LittleEndian.PutUint32(b[10:], s.off)
	binary.LittleEndian.PutUint32(b[14:], 40)
	binary.LittleEndian.PutUint32(b[18:], uint32(s.w))
	binary.LittleEndian.PutUint32(b[22:], uint32(s.h))
	binary.LittleEndian.PutUint16(b[26:], 1)
	binary.LittleEndian.PutUint16(b[28:], s.bits)
	binary.LittleEndian.PutUint32(b[38:], uint32(s.xppm))
	binary.LittleEndian.PutUint32(b[42:], uint32(s.yppm))
	b = append(b, data...)
	return b[:max(0, len(b)-s.cut)]
}

func std(w, h int32) bmpSpec { return bmpSpec{w: w, h: h, bits: 24, off: 54} }

func TestBMPUnit(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	s := std(3, 2)
	s.xppm, s.yppm = 11811, 5906
	d, err := Open(bytes.NewReader(makeBMP(s, rnd)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NextPage()
	if err != nil || p == nil {
		t.Fatalf("page: %v", err)
	}
	if p.Width != 3 || p.Height != 2 || p.BytesPerPixel != 3 {
		t.Errorf("info %+v", p.Info)
	}
	if d := p.XRes - 11811*0.0254; d > 1e-9 || d < -1e-9 {
		t.Errorf("xres %v", p.XRes)
	}
	// 8 bit and top-down files are refused
	for _, s := range []bmpSpec{{w: 3, h: 2, bits: 8, off: 54}, {w: 3, h: -2, bits: 24, off: 54}, {w: 3, h: 2, bits: 24, off: 138}} {
		d, err := Open(bytes.NewReader(makeBMP(s, rnd)))
		if err != nil {
			continue
		}
		if p, err := d.NextPage(); err == nil && p != nil {
			t.Errorf("%+v accepted", s)
		}
	}
}
