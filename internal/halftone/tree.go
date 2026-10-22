// SPDX-License-Identifier: GPL-2.0-only

package halftone

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// treeMagic starts every parameter tree ("block tree").
const treeMagic = 0x50178516

// Entry kinds, taken from the top byte of an entry tag.
const (
	tagString = 0x9a
	tagScalar = 0xf8
	tagTree   = 0xdc
)

var errTree = errors.New("halftone: malformed parameter tree")

// tree is a read-only view of a decoded ("plain") parameter tree: a
// 16-byte header {magic, size, count, flags} followed by count entries
// {tag, value}. Strings, arrays and nested trees live in the same buffer at
// the offset stored in the entry. Trees handed to this package must already
// be decrypted, decompressed and delta-decoded (the table container codec
// does that); this type only walks them.
type tree struct{ b []byte }

func openTree(b []byte) (tree, error) {
	if len(b) < 16 || binary.LittleEndian.Uint32(b) != treeMagic {
		return tree{}, errTree
	}
	size := binary.LittleEndian.Uint32(b[4:])
	count := binary.LittleEndian.Uint32(b[8:])
	if size < 16 || uint64(size) > uint64(len(b)) || uint64(16+8*uint64(count)) > uint64(size) {
		return tree{}, errTree
	}
	return tree{b: b[:size]}, nil
}

func (t tree) count() int { return int(binary.LittleEndian.Uint32(t.b[8:])) }

func (t tree) entry(k int) (tag, val uint32, err error) {
	if k < 0 || k >= t.count() {
		return 0, 0, fmt.Errorf("%w: no entry %d", errTree, k)
	}
	o := 16 + 8*k
	return binary.LittleEndian.Uint32(t.b[o:]), binary.LittleEndian.Uint32(t.b[o+4:]), nil
}

// name returns the string at entry k (entry 0 is the schema name).
func (t tree) name(k int) (string, error) {
	tag, off, err := t.entry(k)
	if err != nil {
		return "", err
	}
	n := tag & 0xffffff
	if tag>>24 != tagString || uint64(off)+uint64(n) > uint64(len(t.b)) {
		return "", fmt.Errorf("%w: entry %d is not a string", errTree, k)
	}
	return string(t.b[off : off+n]), nil
}

// scalar returns the signed 32-bit immediate at entry k.
func (t tree) scalar(k int) (int, error) {
	tag, v, err := t.entry(k)
	if err != nil {
		return 0, err
	}
	if tag>>24 != tagScalar {
		return 0, fmt.Errorf("%w: entry %d is not a scalar", errTree, k)
	}
	return int(int32(v)), nil
}

// sub returns the nested tree at entry k.
func (t tree) sub(k int) (tree, error) {
	tag, off, err := t.entry(k)
	if err != nil {
		return tree{}, err
	}
	if tag>>24 != tagTree || uint64(off) >= uint64(len(t.b)) {
		return tree{}, fmt.Errorf("%w: entry %d is not a nested tree", errTree, k)
	}
	return openTree(t.b[off:])
}

// array returns the raw bytes of the array at entry k together with its
// element size in bytes and element count.
func (t tree) array(k int) (data []byte, elemSize, n int, err error) {
	tag, off, err := t.entry(k)
	if err != nil {
		return nil, 0, 0, err
	}
	if top := tag >> 24; top == tagString || top == tagScalar || top == tagTree || tag&0x80000000 != 0 {
		return nil, 0, 0, fmt.Errorf("%w: entry %d is not an array", errTree, k)
	}
	elemSize = int((tag>>26)&15) + 1
	n = int(tag & 0x03ffffff)
	end := uint64(off) + uint64(elemSize)*uint64(n)
	if end > uint64(len(t.b)) {
		return nil, 0, 0, fmt.Errorf("%w: array %d runs past the tree", errTree, k)
	}
	return t.b[off:end], elemSize, n, nil
}

// bytesN returns the byte array (1-byte elements) at entry k, which must have
// exactly n elements.
func (t tree) bytesN(k, n int) ([]byte, error) {
	d, es, cnt, err := t.array(k)
	if err != nil {
		return nil, err
	}
	if es != 1 || cnt != n {
		return nil, fmt.Errorf("%w: entry %d: want %d bytes, have %d elements of %d bytes", errTree, k, n, cnt, es)
	}
	return d, nil
}

// int16s returns the 16-bit little-endian array at entry k as signed values.
func (t tree) int16s(k int) ([]int16, error) {
	d, es, n, err := t.array(k)
	if err != nil {
		return nil, err
	}
	if es != 2 {
		return nil, fmt.Errorf("%w: entry %d is not a 16-bit array", errTree, k)
	}
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(d[2*i:]))
	}
	return out, nil
}
