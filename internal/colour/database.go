// SPDX-License-Identifier: GPL-2.0-only

package colour

import (
	"encoding/binary"
	"errors"
	"fmt"

	"canon-mp280-driver/internal/printdb"
)

// lutEngine adapts printdb.LUTEngine, whose Apply reports errors. The colour
// stage only feeds inputs inside the configured range, so an error here means
// a programming mistake and the output is left zeroed.
type lutEngine struct{ e *printdb.LUTEngine }

func (a *lutEngine) Apply(in [3]uint16, out []uint16) {
	if err := a.e.Apply(in, out); err != nil {
		clear(out)
	}
}

// frontLUT returns the group-16 front table sub (RGB to RGB) using node set
// nodeSet, configured for bitsIn to bitsOut precision.
func frontLUT(db *printdb.Table, sub, nodeSet, bitsIn, bitsOut int) (*lutEngine, error) {
	blob, err := db.DecodeLUT(sub)
	if err != nil {
		return nil, fmt.Errorf("colour: front LUT %d: %w", sub, err)
	}
	e, err := printdb.NewFrontLUT(blob, nodeSet)
	if err != nil {
		return nil, fmt.Errorf("colour: front LUT %d: %w", sub, err)
	}
	return configured(e, bitsIn, bitsOut)
}

// backLUT returns the group-16 back table sub (RGB to inks) restricted to the
// ink ids in that order, configured for bitsIn to bitsOut precision.
func backLUT(db *printdb.Table, sub int, inks []int, bitsIn, bitsOut int) (*lutEngine, error) {
	blob, err := db.DecodeLUT(sub)
	if err != nil {
		return nil, fmt.Errorf("colour: back LUT %d: %w", sub, err)
	}
	want := make([]uint16, len(inks))
	for i, ink := range inks {
		if ink < 0 || ink > 0xffff {
			return nil, fmt.Errorf("colour: bad ink id %d", ink)
		}
		want[i] = uint16(ink)
	}
	e, err := printdb.NewBackLUT(blob, want)
	if err != nil {
		return nil, fmt.Errorf("colour: back LUT %d: %w", sub, err)
	}
	return configured(e, bitsIn, bitsOut)
}

func configured(e *printdb.LUTEngine, bitsIn, bitsOut int) (*lutEngine, error) {
	// The printer database stores red as the slowest-varying axis.
	if err := e.Configure(printdb.LUTFirstChannelSlowest, bitsIn, bitsOut); err != nil {
		return nil, err
	}
	return &lutEngine{e}, nil
}

// toneSamples returns the 256 samples and the maximum value of the tone curve
// of ink in the group-20 tree sub.
//
// The tree (HTTCS000) holds, as entry 1, the number of curves and from entry 2
// on one nested tree per curve: {name, ink id, 0, 0, max, samples}.
func toneSamples(db *printdb.Table, sub, ink int) (samples [256]uint16, max int, err error) {
	entry, err := db.Entry(printdb.GroupBlockTrees, sub)
	if err != nil {
		return samples, 0, fmt.Errorf("colour: tone tree %d: %w", sub, err)
	}
	tree, err := printdb.PlainBlockTree(entry)
	if err != nil {
		return samples, 0, fmt.Errorf("colour: tone tree %d: %w", sub, err)
	}
	root := treeView{tree}
	_, count, err := root.entry(1)
	if err != nil {
		return samples, 0, err
	}
	for k := 0; k < int(count); k++ {
		_, off, err := root.entry(2 + k)
		if err != nil {
			return samples, 0, err
		}
		if uint64(off) >= uint64(len(tree)) {
			return samples, 0, errors.New("colour: curve offset outside tree")
		}
		curve := treeView{tree[off:]}
		_, id, err := curve.entry(1)
		if err != nil {
			return samples, 0, err
		}
		if int(id) != ink {
			continue
		}
		_, maxValue, err := curve.entry(4)
		if err != nil {
			return samples, 0, err
		}
		tag, dataOff, err := curve.entry(5)
		if err != nil {
			return samples, 0, err
		}
		if n := tag & 0x03ffffff; n != 256 {
			return samples, 0, fmt.Errorf("colour: tone curve has %d samples, want 256", n)
		}
		if uint64(dataOff)+512 > uint64(len(curve.b)) {
			return samples, 0, errors.New("colour: tone curve data outside tree")
		}
		for i := range samples {
			samples[i] = binary.LittleEndian.Uint16(curve.b[int(dataOff)+2*i:])
		}
		return samples, int(maxValue), nil
	}
	return samples, 0, fmt.Errorf("colour: no tone curve for ink %d in tree %d", ink, sub)
}

// treeView reads the {tag, value} entries of a decoded block tree (see
// printdb): a 16-byte header followed by 8-byte entries.
type treeView struct{ b []byte }

func (t treeView) entry(k int) (tag, val uint32, err error) {
	off := 16 + 8*k
	if k < 0 || off+8 > len(t.b) {
		return 0, 0, fmt.Errorf("colour: tree entry %d outside tree", k)
	}
	return binary.LittleEndian.Uint32(t.b[off:]), binary.LittleEndian.Uint32(t.b[off+4:]), nil
}
