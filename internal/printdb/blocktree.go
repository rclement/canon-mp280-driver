// SPDX-License-Identifier: GPL-2.0-only

package printdb

import (
	"encoding/binary"
	"errors"
)

// Block trees (group 20) hold halftone and tone-curve data. Header, four
// u32: magic, total size (multiple of 8, includes header), entry count, flags.
//
// Body: count x {tag, off} at +16, followed by the data the entries point at
// (offsets from the start of the tree, 8-aligned):
//
//	tag > 0 (bit 31 clear)  array; element size ((tag>>26)&0xf)+1 bytes,
//	                        element count tag&0x83ffffff, data at off
//	0xf8xxxxxx              scalar; off is the value itself
//	0x9axxxxxx              byte array of tag&0xffffff bytes at off
//	0xdcxxxxxx              nested block tree at off (own header)
const (
	treeMagic = 0x50178516

	TreeObfuscated uint32 = 0x80000000 // payload XOR-obfuscated, self keyed
	TreeCompressed uint32 = 0x40000000 // payload is LZW compressed
	TreeDeltaCoded uint32 = 0x20000000 // arrays are stored as running differences

	tagScalar = 0xf8000000
	tagBytes  = 0x9a000000
	tagNested = 0xdc000000
)

var le = binary.LittleEndian

// PlainBlockTree fully decodes a tree: deobfuscate, expand, undo delta
// coding, then recursively do the same for nested trees (re-laying out the
// parent when a nested tree changes size). The result has flags 0.
func PlainBlockTree(entry []byte) ([]byte, error) {
	b := append([]byte(nil), entry...)
	return plainTree(b)
}

// deobfuscate removes the self-keyed XOR in place and clears the flag. The
// key is derived from the data itself (a byte-lane checksum of the payload
// combined with the trailing word), so no secret is involved.
func deobfuscate(b []byte) error {
	if len(b) < 16 || le.Uint32(b) != treeMagic {
		return errors.New("printdb: bad block tree magic")
	}
	size := le.Uint32(b[4:])
	if size == 0xffffffff || size&7 != 0 || int32(size) < 0x10 || uint64(size) > uint64(len(b)) {
		return errors.New("printdb: bad block tree size")
	}
	flags := le.Uint32(b[12:])
	if flags&TreeObfuscated == 0 {
		return nil
	}
	d := b[16:size]
	n := len(d)
	if n&3 != 0 || n < 4 {
		return errors.New("printdb: bad block tree payload length")
	}
	var s [4]byte
	words := n >> 2
	for i := 0; i+1 < words; i++ {
		s[2] += d[4*i]
		s[3] += d[4*i+1]
		s[0] += d[4*i+2]
		s[1] += d[4*i+3]
	}
	w := d[n-4:]
	w0, w1, w2, w3 := w[0], w[1], w[2], w[3]
	key := uint32(w0^s[2])<<16 | uint32(s[1]^w1)<<8 | uint32(w3^s[0]) | uint32(w2^s[3])<<24
	m := n - 4 // bytes before the trailing word
	cs := uint32(0x8e03cab2)
	for k := 0; k < m; k++ {
		d[k] ^= byte(key)
		key <<= 1
		if t := key & 0xc0000000; t != 0xc0000000 && t != 0 {
			key |= 1
		}
	}
	p := d[:m]
	for ; len(p) >= 4; p = p[4:] {
		cs += uint32(p[0])*0x17 + uint32(p[1])*0xf9 + uint32(p[2]%0xd)*0x29 + 9 + uint32(p[3])*0x47
	}
	// m is a multiple of 4 here, so no tail bytes remain.
	w[0] = (w0 ^ s[2]) - byte(cs>>16)
	w[1] = (s[1] ^ w1) - byte(cs)
	w[2] = (w3 ^ s[0]) - byte(cs>>24)
	w[3] = (w2 ^ s[3]) - byte(cs>>8)
	le.PutUint32(b[12:], flags&^TreeObfuscated)
	return nil
}

// expand LZW-decompresses the payload if flagged; reports whether it did.
func expand(b []byte) ([]byte, bool, error) {
	flags := le.Uint32(b[12:])
	if flags&TreeCompressed == 0 {
		return b[:le.Uint32(b[4:])], false, nil
	}
	size := le.Uint32(b[4:])
	x, err := Decompress(b[16:size])
	if err != nil {
		return nil, false, err
	}
	if len(x)&7 != 0 {
		return nil, false, errors.New("printdb: expanded block tree not 8-aligned")
	}
	o := make([]byte, 16+len(x))
	copy(o, b[:16])
	copy(o[16:], x)
	le.PutUint32(o[4:], uint32(len(o)))
	le.PutUint32(o[12:], flags&^TreeCompressed)
	return o, true, nil
}

// undelta turns the running-difference arrays back into values; flags must be
// exactly TreeDeltaCoded.
func undelta(b []byte) error {
	if le.Uint32(b[12:]) != TreeDeltaCoded {
		return errors.New("printdb: unexpected block tree flags")
	}
	cnt := le.Uint32(b[8:])
	for i := uint32(0); i < cnt; i++ {
		tag := le.Uint32(b[16+8*i:])
		off := le.Uint32(b[20+8*i:])
		if int32(tag) <= 0 {
			switch tag & 0xff000000 {
			case tagBytes:
				n := tag & 0xffffff
				if uint64(off)+uint64(n) > uint64(len(b)) {
					return errors.New("printdb: block tree array out of range")
				}
				for k := uint32(1); k < n; k++ {
					b[off+k] += b[off+k-1]
				}
			case tagNested, tagScalar:
			default:
				return errors.New("printdb: unknown block tree tag")
			}
			continue
		}
		n := tag & 0x83ffffff
		width := (tag&0x3c000000)>>26 + 1
		if width != 1 && width != 2 && width != 4 {
			return errors.New("printdb: unsupported block tree element size")
		}
		if uint64(off)+uint64(n)*uint64(width) > uint64(len(b)) {
			return errors.New("printdb: block tree array out of range")
		}
		for k := uint32(1); k < n; k++ {
			p := off + k*width
			switch width {
			case 1:
				b[p] += b[p-1]
			case 2:
				le.PutUint16(b[p:], le.Uint16(b[p:])+le.Uint16(b[p-2:]))
			case 4:
				le.PutUint32(b[p:], le.Uint32(b[p:])+le.Uint32(b[p-4:]))
			}
		}
	}
	le.PutUint32(b[12:], 0)
	return nil
}

// replaceNested rebuilds tree b with entry idx replaced by nb; data is laid
// out sequentially in entry order.
func replaceNested(b []byte, idx int, nb []byte) ([]byte, error) {
	size, cnt := le.Uint32(b[4:]), le.Uint32(b[8:])
	off0 := le.Uint32(b[20+8*idx:])
	oldN, newN := le.Uint32(b[off0+4:]), le.Uint32(nb[4:])
	total := size - oldN + newN
	o := make([]byte, total+64)
	copy(o, b[:16])
	le.PutUint32(o[4:], total)
	cur := 16 + 8*cnt
	for i := uint32(0); i < cnt; i++ {
		tag := le.Uint32(b[16+8*i:])
		off := le.Uint32(b[20+8*i:])
		var src []byte
		var adv uint32
		switch {
		case int(i) == idx:
			tag = tagNested
			src, adv = nb, newN
		case int32(tag) <= 0:
			switch tag & 0xff000000 {
			case tagNested:
				l := le.Uint32(b[off+4:])
				src, adv = b[off:off+l], l
			case tagScalar:
				le.PutUint32(o[16+8*i:], tag)
				le.PutUint32(o[20+8*i:], off)
				continue
			case tagBytes:
				l := tag & 0xffffff
				src, adv = b[off:off+l], (l+8)&0x1fffff8
			default:
				return nil, errors.New("printdb: unknown block tree tag")
			}
		default:
			l := ((tag&0x3c000000)>>26 + 1) * (tag & 0x83ffffff)
			src, adv = b[off:off+l], (l+7)&^7
		}
		if adv&7 != 0 || uint64(cur)+uint64(adv) > uint64(len(o)) {
			return nil, errors.New("printdb: block tree re-layout overflow")
		}
		le.PutUint32(o[16+8*i:], tag)
		le.PutUint32(o[20+8*i:], cur)
		copy(o[cur:], src)
		cur += adv
	}
	return o[:total], nil
}

func plainTree(b []byte) ([]byte, error) {
	if len(b) < 16 || le.Uint32(b) != treeMagic {
		return nil, errors.New("printdb: bad block tree magic")
	}
	if err := deobfuscate(b); err != nil {
		return nil, err
	}
	b, _, err := expand(b)
	if err != nil {
		return nil, err
	}
	if le.Uint32(b[12:])&TreeDeltaCoded != 0 {
		if err := undelta(b); err != nil {
			return nil, err
		}
	}
	cnt := le.Uint32(b[8:])
	if 16+8*uint64(cnt) > uint64(len(b)) {
		return nil, errors.New("printdb: block tree directory out of range")
	}
	for i := uint32(0); i < cnt; i++ {
		tag := le.Uint32(b[16+8*i:])
		off := le.Uint32(b[20+8*i:])
		if int32(tag) > 0 {
			continue
		}
		switch tag & 0xff000000 {
		case tagNested:
			if uint64(off)+16 > uint64(len(b)) {
				return nil, errors.New("printdb: nested tree out of range")
			}
			n := le.Uint32(b[off+4:])
			if uint64(off)+uint64(n) > uint64(len(b)) {
				return nil, errors.New("printdb: nested tree out of range")
			}
			sub := append([]byte(nil), b[off:off+n]...)
			ns, err := plainTree(sub)
			if err != nil {
				return nil, err
			}
			if len(ns) != int(n) {
				if b, err = replaceNested(b, int(i), ns); err != nil {
					return nil, err
				}
			} else {
				copy(b[off:], ns)
			}
		case tagScalar, tagBytes:
		default:
			return nil, errors.New("printdb: unknown block tree tag")
		}
	}
	return b[:le.Uint32(b[4:])], nil
}
