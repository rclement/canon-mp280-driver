// SPDX-License-Identifier: GPL-2.0-only

package image

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// A tiny TIFF writer for tests: explicit control over which tags exist.

type tiffTag struct {
	tag  uint16
	typ  uint16 // 1 byte, 2 ascii, 3 short, 4 long, 5 rational
	vals []uint32
	den  []uint32 // rational denominators
}

type tiffFile struct {
	big            bool
	tags           []tiffTag
	strips         [][]byte // pixel data per strip; StripOffsets/Counts generated unless omitted
	omit           map[uint16]bool
	extraIFD       *tiffFile // second IFD
	rawTail        []byte
	countsOverride []uint32
}

func (f *tiffFile) set(tag uint16, typ uint16, vals ...uint32) {
	for i := range f.tags {
		if f.tags[i].tag == tag {
			f.tags[i] = tiffTag{tag: tag, typ: typ, vals: vals}
			return
		}
	}
	f.tags = append(f.tags, tiffTag{tag: tag, typ: typ, vals: vals})
}

func (f *tiffFile) setRational(tag uint16, num, den uint32) {
	for i := range f.tags {
		if f.tags[i].tag == tag {
			f.tags[i] = tiffTag{tag: tag, typ: 5, vals: []uint32{num}, den: []uint32{den}}
			return
		}
	}
	f.tags = append(f.tags, tiffTag{tag: tag, typ: 5, vals: []uint32{num}, den: []uint32{den}})
}

func (f *tiffFile) bytes() []byte {
	bo := binary.ByteOrder(binary.LittleEndian)
	hdr := []byte("II*\x00")
	if f.big {
		bo = binary.BigEndian
		hdr = []byte("MM\x00*")
	}
	var data bytes.Buffer // everything after the 8 byte header: pixel data first, then IFD
	var offs, cnts []uint32
	for _, s := range f.strips {
		offs = append(offs, uint32(8+data.Len()))
		cnts = append(cnts, uint32(len(s)))
		data.Write(s)
	}
	if f.countsOverride != nil {
		cnts = f.countsOverride
	}
	tags := append([]tiffTag(nil), f.tags...)
	if len(f.strips) > 0 {
		tags = append(tags, tiffTag{tag: 273, typ: 4, vals: offs}, tiffTag{tag: 279, typ: 4, vals: cnts})
	}
	var keep []tiffTag
	for _, t := range tags {
		if !f.omit[t.tag] {
			keep = append(keep, t)
		}
	}
	tags = keep
	sort.Slice(tags, func(i, j int) bool { return tags[i].tag < tags[j].tag })
	if data.Len()%2 == 1 {
		data.WriteByte(0)
	}
	ifdOff := 8 + data.Len()
	ifdSize := 2 + 12*len(tags) + 4
	var ifd, ext bytes.Buffer
	extBase := ifdOff + ifdSize
	put16 := func(b *bytes.Buffer, v uint16) { var t [2]byte; bo.PutUint16(t[:], v); b.Write(t[:]) }
	put32 := func(b *bytes.Buffer, v uint32) { var t [4]byte; bo.PutUint32(t[:], v); b.Write(t[:]) }
	put16(&ifd, uint16(len(tags)))
	for _, t := range tags {
		var vb bytes.Buffer
		for i, v := range t.vals {
			switch t.typ {
			case 1, 2:
				vb.WriteByte(byte(v))
			case 3:
				put16(&vb, uint16(v))
			case 4:
				put32(&vb, v)
			case 5:
				put32(&vb, v)
				put32(&vb, t.den[i])
			}
		}
		put16(&ifd, t.tag)
		put16(&ifd, t.typ)
		cnt := uint32(len(t.vals))
		put32(&ifd, cnt)
		if vb.Len() <= 4 {
			b := vb.Bytes()
			for len(b) < 4 {
				b = append(b, 0)
			}
			ifd.Write(b)
		} else {
			put32(&ifd, uint32(extBase+ext.Len()))
			ext.Write(vb.Bytes())
			if ext.Len()%2 == 1 {
				ext.WriteByte(0)
			}
		}
	}
	put32(&ifd, 0)
	out := append([]byte(nil), hdr...)
	var t4 [4]byte
	bo.PutUint32(t4[:], uint32(ifdOff))
	out = append(out, t4[:]...)
	out = append(out, data.Bytes()...)
	out = append(out, ifd.Bytes()...)
	out = append(out, ext.Bytes()...)
	return append(out, f.rawTail...)
}

// simpleTIFF returns an uncompressed RGB (or gray/other) image file with one strip per rowsPerStrip rows.
func simpleTIFF(w, h, spp, bps int, photometric uint32, rowsPerStrip int, pix func(i int) byte) *tiffFile {
	f := &tiffFile{omit: map[uint16]bool{}}
	f.set(256, 3, uint32(w))
	f.set(257, 3, uint32(h))
	bv := make([]uint32, spp)
	for i := range bv {
		bv[i] = uint32(bps)
	}
	f.set(258, 3, bv...)
	f.set(259, 3, 1)
	f.set(262, 3, photometric)
	f.set(277, 3, uint32(spp))
	f.set(278, 3, uint32(rowsPerStrip))
	f.set(284, 3, 1)
	f.setRational(282, 300, 1)
	f.setRational(283, 300, 1)
	f.set(296, 3, 2)
	row := (w*spp*bps + 7) / 8
	n := 0
	for y := 0; y < h; y += rowsPerStrip {
		rows := min(rowsPerStrip, h-y)
		s := make([]byte, rows*row)
		for i := range s {
			s[i] = pix(n)
			n++
		}
		f.strips = append(f.strips, s)
	}
	return f
}

func ramp2(i int) byte { return byte(i*13 + 1) }
