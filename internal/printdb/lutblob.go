// SPDX-License-Identifier: GPL-2.0-only

package printdb

import (
	"bytes"
	"errors"
	"fmt"
)

var lutMagic = []byte{0xff, 0x00, 0xff, 0x00}

// LUTBlobType is the first byte of a decoded group-16 blob.
type LUTBlobType int

// Decoded blob layouts. N is the grid size; colour planes are N^3 bytes each,
// stored planar, with the first input channel (red) varying slowest.
const (
	LUTBuiltinNodes LUTBlobType = 0 // 3 planes, N=17, grid nodes come from a built-in set
	LUTOwnNodes     LUTBlobType = 1 // N, nodes[N], 3 planes
	LUTFront        LUTBlobType = 2 // N, 3, nsets, nodes[nsets][N], 3 planes (RGB to RGB)
	LUTBack         LUTBlobType = 3 // K, ids[K], N, nodes[N], K planes (RGB to ink channels)
)

// LUTBlob is a parsed decoded LUT; slices alias the blob.
type LUTBlob struct {
	Type     LUTBlobType
	N        int    // grid size
	Planes   int    // number of stored planes (output channels)
	NodeSets int    // node sets stored (front: several; others 0 or 1)
	Nodes    []byte // NodeSets*N grid positions; nil for LUTBuiltinNodes
	IDs      []byte // ink channel id per plane (LUTBack only)
	Data     []byte // Planes planes of N^3 bytes
}

// ParseLUTBlob splits a decoded blob into header fields and plane data.
func ParseLUTBlob(b []byte) (*LUTBlob, error) {
	if len(b) < 2 {
		return nil, errors.New("printdb: LUT blob too short")
	}
	l := &LUTBlob{Type: LUTBlobType(b[0])}
	var hdr int
	switch l.Type {
	case LUTBuiltinNodes:
		l.N, l.Planes, hdr = 17, 3, 1
	case LUTOwnNodes:
		l.N, l.Planes, l.NodeSets = int(b[1]), 3, 1
		hdr = 2 + l.N
		if len(b) < hdr {
			return nil, errors.New("printdb: LUT blob truncated")
		}
		l.Nodes = b[2:hdr]
	case LUTFront:
		if len(b) < 4 {
			return nil, errors.New("printdb: LUT blob truncated")
		}
		l.N, l.Planes, l.NodeSets = int(b[1]), 3, int(b[3])
		hdr = 4 + l.NodeSets*l.N
		if len(b) < hdr {
			return nil, errors.New("printdb: LUT blob truncated")
		}
		l.Nodes = b[4:hdr]
	case LUTBack:
		l.Planes = int(b[1])
		if len(b) < l.Planes+3 {
			return nil, errors.New("printdb: LUT blob truncated")
		}
		l.IDs = b[2 : 2+l.Planes]
		l.N, l.NodeSets = int(b[l.Planes+2]), 1
		hdr = l.Planes + 3 + l.N
		if len(b) < hdr {
			return nil, errors.New("printdb: LUT blob truncated")
		}
		l.Nodes = b[l.Planes+3 : hdr]
	default:
		return nil, fmt.Errorf("printdb: unknown LUT blob type %d", b[0])
	}
	n3 := l.N * l.N * l.N
	if hdr+n3*l.Planes > len(b) {
		return nil, errors.New("printdb: LUT blob data truncated")
	}
	l.Data = b[hdr:]
	return l, nil
}

// blobDims returns grid size and plane count from a decoded blob header.
func blobDims(b []byte) (n, planes int, err error) {
	if len(b) < 2 {
		return 0, 0, errors.New("short blob")
	}
	switch b[0] {
	case 0:
		return 17, 3, nil
	case 1, 2:
		return int(b[1]), 3, nil
	case 3:
		k := int(b[1])
		if len(b) < k+3 {
			return 0, 0, errors.New("short blob")
		}
		return int(b[k+2]), k, nil
	}
	return 0, 0, errors.New("unknown blob type")
}

// headerUndelta applies two passes of a running byte sum over bytes 1..n-1.
func headerUndelta(p []byte) {
	for pass := 0; pass < 2; pass++ {
		for i := 1; i < len(p); i++ {
			p[i] += p[i-1]
		}
	}
}

// volumeUndelta integrates `planes` planes of N^3 bytes along the fastest
// axis, then the N stride, then the N^2 stride.
func volumeUndelta(p []byte, n, planes int) error {
	if n < 2 || n > 255 {
		return errors.New("bad grid size")
	}
	n2, n3 := n*n, n*n*n
	if len(p) < n3*planes {
		return errors.New("short volume")
	}
	for c := 0; c < planes; c++ {
		v := p[c*n3 : (c+1)*n3]
		for r := 0; r < n2; r++ {
			for i := 1; i < n; i++ {
				v[r*n+i] += v[r*n+i-1]
			}
		}
		for k := 0; k < n; k++ {
			for j := n; j < n2; j++ {
				v[k*n2+j] += v[k*n2+j-n]
			}
		}
		for j := n2; j < n3; j++ {
			v[j] += v[j-n2]
		}
	}
	return nil
}

// decodePlainLUT decodes the "plain" form starting at the flags byte:
// flags2 (bit0 volume delta, bit1 header_len present), u16 header_len,
// compression byte (bit0 LZW), body.
func decodePlainLUT(p []byte) ([]byte, error) {
	if len(p) <= 8 || p[0]&1 != 0 {
		return nil, errors.New("printdb: bad plain LUT entry")
	}
	f2 := p[5]
	hdr := 0
	if f2&2 != 0 {
		hdr = int(le.Uint16(p[6:]))
	}
	body := p[9:]
	var buf []byte
	if p[8]&1 != 0 {
		var err error
		if buf, err = Decompress(body); err != nil {
			return nil, err
		}
	} else {
		buf = append([]byte(nil), body...)
	}
	if len(buf) < hdr {
		return nil, errors.New("printdb: LUT header longer than body")
	}
	headerUndelta(buf[:hdr])
	n, planes, err := blobDims(buf)
	if err != nil || len(buf)-hdr < n*n*n*planes {
		return nil, errors.New("printdb: LUT body too short")
	}
	if f2&1 != 0 {
		if err := volumeUndelta(buf[hdr:], n, planes); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

// DecodeLUT returns the decoded blob of a group-16 entry (sub >= 2). Delta
// entries are resolved against their base through the directory in sub 1 and
// verified with the base digest.
func (t *Table) DecodeLUT(sub int) ([]byte, error) {
	if sub < 2 {
		return nil, fmt.Errorf("printdb: sub %d is not a LUT", sub)
	}
	e, err := t.Entry(GroupLUT, sub)
	if err != nil {
		return nil, err
	}
	if len(e) < 4 || !bytes.Equal(e[:4], lutMagic) {
		return append([]byte{0}, e...), nil // legacy: type 0 + raw payload
	}
	if len(e) < 5 || e[4] != 4 {
		return append([]byte(nil), e[min(4, len(e)):]...), nil // other version: raw tail
	}
	if len(e) < 6 {
		return nil, errors.New("printdb: LUT entry truncated")
	}
	if e[5]&1 == 0 {
		return decodePlainLUT(e[5:])
	}
	return t.decodeDeltaLUT(sub, e)
}

func (t *Table) decodeDeltaLUT(sub int, e []byte) ([]byte, error) {
	d, err := t.Entry(GroupLUT, 1)
	if err != nil {
		return nil, err
	}
	if len(d) < 4 || le.Uint16(d) != 0 {
		return nil, errors.New("printdb: bad base directory")
	}
	cnt := int(int16(le.Uint16(d[2:])))
	base := -1
	for i := 0; i < cnt && 4+4*i+4 <= len(d); i++ {
		q := d[4+4*i:]
		if int(int16(le.Uint16(q))) == sub {
			base = int(int16(le.Uint16(q[2:])))
			break
		}
	}
	if base < 2 {
		return nil, fmt.Errorf("printdb: no base for LUT %d", sub)
	}
	be, err := t.Entry(GroupLUT, base)
	if err != nil {
		return nil, err
	}
	if len(be) < 6 || !bytes.Equal(be[:4], lutMagic) || be[4] != 4 || be[5]&1 != 0 {
		return nil, errors.New("printdb: base LUT is not a plain entry")
	}
	bdec, err := decodePlainLUT(be[5:])
	if err != nil {
		return nil, err
	}
	if len(e) < 0xe {
		return nil, errors.New("printdb: delta LUT truncated")
	}
	if le.Uint32(e[6:]) != Digest(bdec, DigestSeed) {
		return nil, errors.New("printdb: base LUT digest mismatch")
	}
	f2 := e[10]
	hdr := 0
	if f2&2 != 0 {
		hdr = int(le.Uint16(e[11:]))
	}
	var out []byte
	if e[13]&1 != 0 {
		if out, err = Decompress(e[0xe:]); err != nil {
			return nil, err
		}
	} else {
		out = append([]byte(nil), e[0xe:]...)
	}
	n, planes, err := blobDims(bdec)
	if err != nil || len(out) != len(bdec) || len(out) < hdr {
		return nil, errors.New("printdb: delta LUT does not match its base")
	}
	headerUndelta(out[:hdr])
	if f2&1 != 0 {
		if err := volumeUndelta(out[hdr:], n, planes); err != nil {
			return nil, err
		}
	}
	for i := range out {
		out[i] += bdec[i]
	}
	n2, planes2, err := blobDims(out)
	if err != nil || out[0] == 0 || out[0] > 3 || n2 != n || (out[0] == 3 && planes2 != planes) {
		return nil, errors.New("printdb: decoded delta LUT inconsistent with base")
	}
	return out, nil
}
