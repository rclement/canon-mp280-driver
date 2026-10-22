// SPDX-License-Identifier: GPL-2.0-only

// Package printdb reads Canon's printer database ("cnb_*.tbl"): the container
// format and the codecs that unpack its entries (self-keyed obfuscation, LZW,
// running-sum delta coding, integrity digest, block trees, LUT blobs).
//
// All multi-byte values are little endian.
package printdb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// Group identifies one of the four directories of a table file.
type Group int

// Directory groups present in the table header (file offsets 8, 12, 16, 20).
const (
	GroupOptions    Group = 8  // option database
	GroupModes      Group = 12 // print-mode database
	GroupLUT        Group = 16 // 3D colour look-up tables
	GroupBlockTrees Group = 20 // halftone / tone-curve block trees
)

// Table is an in-memory .tbl file.
type Table struct {
	data []byte
}

// ErrNotFound is returned when a requested entry does not exist.
var ErrNotFound = errors.New("printdb: entry not found")

// Load reads a whole table file.
func Load(path string) (*Table, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return FromBytes(b)
}

// FromBytes wraps table file contents (not copied).
func FromBytes(b []byte) (*Table, error) {
	if len(b) < 24 {
		return nil, errors.New("printdb: table too short")
	}
	return &Table{data: b}, nil
}

func (t *Table) u32(off uint64) (uint32, bool) {
	if off+4 > uint64(len(t.data)) {
		return 0, false
	}
	return binary.LittleEndian.Uint32(t.data[off:]), true
}

func (t *Table) dir(g Group) (offset uint32, count int, err error) {
	switch g {
	case GroupOptions, GroupModes, GroupLUT, GroupBlockTrees:
	default:
		return 0, 0, fmt.Errorf("printdb: unknown group %d", g)
	}
	off, ok := t.u32(uint64(g))
	if !ok {
		return 0, 0, errors.New("printdb: truncated header")
	}
	n, ok := t.u32(uint64(off))
	if !ok || uint64(off)+4+uint64(n)*12 > uint64(len(t.data)) {
		return 0, 0, errors.New("printdb: bad directory")
	}
	return off, int(n), nil
}

// Entry returns the raw payload (without the length prefix) of group/sub.
// The slice aliases the table data.
func (t *Table) Entry(g Group, sub int) ([]byte, error) {
	dir, n, err := t.dir(g)
	if err != nil {
		return nil, err
	}
	for i := 0; i < n; i++ {
		e := uint64(dir) + 4 + 12*uint64(i)
		s, _ := t.u32(e + 4)
		if int(s) != sub {
			continue
		}
		off, _ := t.u32(e + 8)
		l, ok := t.u32(uint64(off))
		if !ok || uint64(off)+4+uint64(l) > uint64(len(t.data)) {
			return nil, fmt.Errorf("printdb: entry %d/%d out of range", g, sub)
		}
		return t.data[uint64(off)+4 : uint64(off)+4+uint64(l)], nil
	}
	return nil, fmt.Errorf("%w: group %d sub %d", ErrNotFound, g, sub)
}
