// SPDX-License-Identifier: GPL-2.0-only

package printdb

// 3D colour look-up table engine used by the printer colour pipeline: an N^3
// grid with possibly non-uniform node positions, several output channels, and
// tetrahedral interpolation with fixed-point weights that sum to 0x10000.

import (
	"errors"
	"fmt"
)

// MaxLUTChannels is the largest number of output channels an engine supports.
const MaxLUTChannels = 24

// LUTAxisOrder says which input channel varies fastest in the stored grid.
type LUTAxisOrder int

const (
	// LUTFirstChannelFastest: input channel 0 has the smallest stride.
	LUTFirstChannelFastest LUTAxisOrder = 0
	// LUTFirstChannelSlowest: input channel 0 has the largest stride. This is
	// the layout of the LUTs in the printer database (red is the slowest axis).
	LUTFirstChannelSlowest LUTAxisOrder = 1
)

// LUTEngine is a LUT ready for conversion. After Configure it is immutable and
// safe for concurrent Apply calls.
type LUTEngine struct {
	n, nch int
	nodes  []byte
	data   []byte // node-interleaved: node i at i*nch, zero padded

	configured bool
	shift      uint // output shift: 24 - bitsOut
	round      int
	inShift    uint // bitsIn - 8
	maxIn      int
	cell       [256]uint8
	frac       []uint16    // fractional position (16 bit) per input value
	axisOff    [256][3]int // byte offset of grid index along each input channel
	tet        [6][3]int   // offsets to vertices A, B and the far corner
}

// Middle vertices of the six tetrahedra: [tetrahedron][vertex][input channel].
var tetVertices = [6][2][3]int{
	{{0, 0, 1}, {0, 1, 1}}, {{0, 0, 1}, {1, 0, 1}}, {{1, 0, 0}, {1, 0, 1}},
	{{0, 1, 0}, {0, 1, 1}}, {{0, 1, 0}, {1, 1, 0}}, {{1, 0, 0}, {1, 1, 0}},
}

// Which stride class (0 = nch*N^2, 1 = nch*N, 2 = nch) each input channel uses.
var strideClass = [2][3]int{{2, 1, 0}, {0, 1, 2}}

// NewLUTEngine builds an engine from an N^3 grid. nodes holds N strictly increasing
// positions with nodes[0]==0 and nodes[N-1]==255; planar holds nch planes of
// N^3 bytes (plane c at c*N^3).
func NewLUTEngine(n int, nodes []byte, nch int, planar []byte) (*LUTEngine, error) {
	if n < 2 || n > 255 || nch < 1 || nch > MaxLUTChannels {
		return nil, errors.New("printdb: unsupported grid size or channel count")
	}
	if len(nodes) < n || nodes[0] != 0 || nodes[n-1] != 255 {
		return nil, errors.New("printdb: nodes must start at 0 and end at 255")
	}
	for i := 1; i < n; i++ {
		if nodes[i-1] >= nodes[i] {
			return nil, errors.New("printdb: nodes must be strictly increasing")
		}
	}
	n3 := n * n * n
	if len(planar) < n3*nch {
		return nil, errors.New("printdb: grid data too short")
	}
	e := &LUTEngine{n: n, nch: nch, nodes: append([]byte(nil), nodes[:n]...)}
	e.data = make([]byte, (n3+n*n+n+1)*nch)
	for i := 0; i < n3; i++ {
		for c := 0; c < nch; c++ {
			e.data[i*nch+c] = planar[c*n3+i]
		}
	}
	return e, nil
}

// Configure selects the axis order and the input/output bit depths (8..16).
// Inputs range over 0..0xff<<(bitsIn-8); outputs are scaled to bitsOut bits.
func (e *LUTEngine) Configure(order LUTAxisOrder, bitsIn, bitsOut int) error {
	if bitsIn < 8 || bitsIn > 16 || bitsOut < 8 || bitsOut > 16 {
		return errors.New("printdb: bit depth must be 8..16")
	}
	if order != LUTFirstChannelFastest && order != LUTFirstChannelSlowest {
		return errors.New("printdb: bad axis order")
	}
	sh := bitsIn - 8
	scale := 1 << sh
	n, nch := e.n, e.nch
	e.inShift = uint(sh)
	e.maxIn = 0xff << sh
	e.frac = make([]uint16, e.maxIn+1)
	for u := 0; u < 256; u++ {
		k := n - 1
		for u < int(e.nodes[k]) {
			k--
		}
		e.cell[u] = uint8(k)
		if k == n-1 {
			e.frac[scale*u] = 0
			continue
		}
		den := uint32(scale) * uint32(e.nodes[k+1]-e.nodes[k])
		for t := 0; t < scale; t++ {
			pos := uint32(t + (u-int(e.nodes[k]))*scale)
			e.frac[t+scale*u] = uint16((pos*0x10000 + den>>1) / den)
		}
	}
	stride := [3]int{nch * n * n, nch * n, nch}
	cls := strideClass[order]
	for i := 0; i < n; i++ {
		for ch := 0; ch < 3; ch++ {
			e.axisOff[i][cls[ch]] = i * stride[ch]
		}
	}
	for k := 0; k < 6; k++ {
		for v := 0; v < 2; v++ {
			off := 0
			for ch := 0; ch < 3; ch++ {
				off += tetVertices[k][v][cls[ch]] * stride[ch]
			}
			e.tet[k][v] = off
		}
		e.tet[k][2] = nch * (n*n + n + 1)
	}
	e.shift = uint(24 - bitsOut)
	e.round = 0
	if e.shift > 0 {
		e.round = 1 << (e.shift - 1)
	}
	e.configured = true
	return nil
}

// Apply converts one pixel with tetrahedral interpolation. in must not exceed
// the configured maximum; out must hold Channels() values.
func (e *LUTEngine) Apply(in [3]uint16, out []uint16) error {
	if !e.configured {
		return errors.New("printdb: not configured")
	}
	if len(out) < e.nch {
		return errors.New("printdb: output too short")
	}
	if int(in[0]) > e.maxIn || int(in[1]) > e.maxIn || int(in[2]) > e.maxIn {
		return fmt.Errorf("printdb: input exceeds maximum %d", e.maxIn)
	}
	base := e.axisOff[e.cell[in[0]>>e.inShift]][0] +
		e.axisOff[e.cell[in[1]>>e.inShift]][1] +
		e.axisOff[e.cell[in[2]>>e.inShift]][2]
	a, b, c := int(e.frac[in[0]]), int(e.frac[in[1]]), int(e.frac[in[2]])
	// Pick the tetrahedron by ordering the fractions; weights for the base
	// vertex, A, B and the far corner (1,1,1) sum to 0x10000.
	var k, wBase, wA, wB, wFar int
	bc := b - c
	if bc <= 0 {
		ac := a - c
		if ac <= 0 {
			ab := a - b
			if ab <= 0 {
				k, wBase, wA, wB, wFar = 0, 0x10000-c, -bc, -ab, a
			} else {
				k, wBase, wA, wB, wFar = 1, 0x10000-c, -ac, ab, b
			}
		} else {
			k, wBase, wA, wB, wFar = 2, 0x10000-a, ac, -bc, b
		}
	} else {
		ac := a - c
		if ac <= 0 {
			k, wBase, wA, wB, wFar = 3, 0x10000-b, bc, -ac, a
		} else {
			ab := a - b
			if ab <= 0 {
				k, wBase, wA, wB, wFar = 4, 0x10000-b, -ab, ac, c
			} else {
				k, wBase, wA, wB, wFar = 5, 0x10000-a, ab, bc, c
			}
		}
	}
	p := e.data[base:]
	pa, pb, pf := e.data[base+e.tet[k][0]:], e.data[base+e.tet[k][1]:], e.data[base+e.tet[k][2]:]
	for ch := 0; ch < e.nch; ch++ {
		sum := int(p[ch])*wBase + int(pa[ch])*wA + int(pf[ch])*wFar + int(pb[ch])*wB + e.round
		out[ch] = uint16(sum >> e.shift)
	}
	return nil
}

// builtinNodes are the grid positions used by type-0 front LUTs (17 nodes).
var builtinNodes = [5][17]byte{
	{0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0xff},
	{0x00, 0x05, 0x0e, 0x18, 0x25, 0x32, 0x41, 0x51, 0x61, 0x73, 0x85, 0x98, 0xab, 0xc0, 0xd5, 0xea, 0xff},
	{0x00, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xa0, 0xb0, 0xc0, 0xd0, 0xe0, 0xf0, 0xff},
	{0x00, 0x1d, 0x33, 0x46, 0x57, 0x68, 0x77, 0x86, 0x95, 0xa3, 0xb1, 0xbf, 0xcc, 0xda, 0xe6, 0xf3, 0xff},
	{0x00, 0x2b, 0x44, 0x58, 0x6a, 0x7a, 0x89, 0x97, 0xa4, 0xb1, 0xbd, 0xc9, 0xd5, 0xe0, 0xeb, 0xf5, 0xff},
}

// NewFrontLUT builds the RGB-to-RGB engine from a decoded type 0/1/2 blob using
// node set `nodeSet` (0..4).
func NewFrontLUT(blob []byte, nodeSet int) (*LUTEngine, error) {
	b, err := ParseLUTBlob(blob)
	if err != nil {
		return nil, err
	}
	if b.Type == LUTBack || nodeSet < 0 || nodeSet > 4 {
		return nil, errors.New("printdb: not a front LUT or bad node set")
	}
	var nodes []byte
	switch b.Type {
	case LUTBuiltinNodes:
		nodes = builtinNodes[nodeSet][:]
	case LUTOwnNodes:
		nodes = b.Nodes
	default:
		if nodeSet >= b.NodeSets {
			return nil, errors.New("printdb: node set not present in blob")
		}
		nodes = b.Nodes[nodeSet*b.N : (nodeSet+1)*b.N]
	}
	return NewLUTEngine(b.N, nodes, 3, b.Data)
}

// NewBackLUT builds an RGB-to-ink engine from a decoded type-3 blob, producing
// the ink channels listed in want (ids as stored in the blob), in that order.
func NewBackLUT(blob []byte, want []uint16) (*LUTEngine, error) {
	b, err := ParseLUTBlob(blob)
	if err != nil {
		return nil, err
	}
	if b.Type != LUTBack || len(want) < 1 || len(want) > b.Planes {
		return nil, errors.New("printdb: not a back LUT or bad channel list")
	}
	n3 := b.N * b.N * b.N
	planar := make([]byte, 0, n3*len(want))
	for _, w := range want {
		i := 0
		for i < b.Planes && uint16(b.IDs[i]) != w {
			i++
		}
		if i == b.Planes {
			return nil, fmt.Errorf("printdb: ink channel %d not in LUT", w)
		}
		planar = append(planar, b.Data[i*n3:(i+1)*n3]...)
	}
	return NewLUTEngine(b.N, b.Nodes, len(want), planar)
}
