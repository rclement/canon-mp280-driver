// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"math/rand"
	"testing"
)

type pngSpec struct {
	w, h        int
	depth, col  int
	interlace   byte
	palette     int  // entries in PLTE (palette images), 0 = none
	trns        bool // add a tRNS chunk
	phys, gama  bool
	idatSplit   int // split compressed data into chunks of this size (0 = one chunk)
	filters     bool
	badCRC      string // chunk name whose CRC is damaged ("" none); "IDAT" damages the last IDAT
	dropIEND    bool
	trailing    int
	extra       string // extra chunk placed before IDAT
	firstIDATAt int
}

func pngChunk(name string, data []byte, bad bool) []byte {
	b := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], name)
	b = append(b, data...)
	c := crc32.ChecksumIEEE(b[4:])
	if bad {
		c ^= 0x55
	}
	return binary.BigEndian.AppendUint32(b, c)
}

func channels(col int) int {
	switch col {
	case 2:
		return 3
	case 4:
		return 2
	case 6:
		return 4
	}
	return 1
}

func makePNG(s pngSpec, rnd *rand.Rand) []byte {
	out := []byte("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, uint32(s.w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(s.h))
	ihdr[8], ihdr[9], ihdr[12] = byte(s.depth), byte(s.col), s.interlace
	out = append(out, pngChunk("IHDR", ihdr, s.badCRC == "IHDR")...)
	if s.col == 3 {
		pl := make([]byte, 3*s.palette)
		rnd.Read(pl)
		if s.palette > 0 {
			out = append(out, pngChunk("PLTE", pl, s.badCRC == "PLTE")...)
		}
	}
	if s.trns {
		out = append(out, pngChunk("tRNS", []byte{0, 128, 255}, false)...)
	}
	if s.phys {
		out = append(out, pngChunk("pHYs", []byte{0, 0, 0x2e, 0x23, 0, 0, 0x2e, 0x23, 1}, s.badCRC == "pHYs")...)
	}
	if s.gama {
		out = append(out, pngChunk("gAMA", []byte{0, 1, 0x86, 0xa0}, false)...)
	}
	if s.extra != "" {
		out = append(out, pngChunk(s.extra, []byte("xx"), false)...)
	}
	rowBytes := (s.w*s.depth*channels(s.col) + 7) / 8
	var raw bytes.Buffer
	for y := 0; y < s.h; y++ {
		ft := byte(0)
		if s.filters {
			ft = byte(rnd.Intn(5))
		}
		raw.WriteByte(ft)
		row := make([]byte, rowBytes)
		rnd.Read(row)
		if s.col == 3 && s.depth == 8 && s.palette > 0 {
			for i := range row {
				row[i] = byte(rnd.Intn(s.palette))
			}
		}
		raw.Write(row)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()
	zd := z.Bytes()
	if s.idatSplit <= 0 {
		out = append(out, pngChunk("IDAT", zd, s.badCRC == "IDAT")...)
	} else {
		for i := 0; i < len(zd); i += s.idatSplit {
			e := min(len(zd), i+s.idatSplit)
			out = append(out, pngChunk("IDAT", zd[i:e], s.badCRC == "IDAT" && e == len(zd))...)
		}
	}
	if !s.dropIEND {
		out = append(out, pngChunk("IEND", nil, s.badCRC == "IEND")...)
	}
	for i := 0; i < s.trailing; i++ {
		out = append(out, byte(rnd.Intn(256)))
	}
	return out
}

func TestPNGUnit(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	d, err := Open(bytes.NewReader(makePNG(pngSpec{w: 5, h: 4, depth: 8, col: 2, filters: true}, rnd)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := d.NextPage()
	if err != nil || p == nil || p.Width != 5 || p.Height != 4 {
		t.Fatalf("page %v %v", p, err)
	}
	row := make([]byte, 15)
	if err := p.ReadRow(row, 0, 3, 5); err != nil {
		t.Fatal(err)
	}
	// interlaced is refused
	d, err = Open(bytes.NewReader(makePNG(pngSpec{w: 5, h: 4, depth: 8, col: 2, interlace: 1}, rnd)))
	if err == nil {
		if p, err := d.NextPage(); err == nil && p != nil {
			t.Error("interlaced accepted")
		}
	}
	// PNG is not accepted on stdin
	if _, err := OpenStdin(bytes.NewReader(makePNG(pngSpec{w: 2, h: 2, depth: 8, col: 2}, rnd))); err == nil {
		t.Error("PNG on stdin accepted")
	}
}
