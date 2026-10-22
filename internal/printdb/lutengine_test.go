// SPDX-License-Identifier: GPL-2.0-only

package printdb

import (
	"testing"
)

func uniformNodes(n int) []byte {
	nodes := make([]byte, n)
	for i := range nodes {
		nodes[i] = byte(i * 255 / (n - 1))
	}
	return nodes
}

// identityLUT maps (r,g,b) to itself on a uniform grid (red slowest).
func identityLUT(n int) (nodes, planar []byte) {
	nodes = uniformNodes(n)
	n3 := n * n * n
	planar = make([]byte, 3*n3)
	for r := 0; r < n; r++ {
		for g := 0; g < n; g++ {
			for b := 0; b < n; b++ {
				i := r*n*n + g*n + b
				planar[i], planar[n3+i], planar[2*n3+i] = nodes[r], nodes[g], nodes[b]
			}
		}
	}
	return
}

func TestIdentityLUT(t *testing.T) {
	nodes, planar := identityLUT(17)
	e, err := NewLUTEngine(17, nodes, 3, planar)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Configure(LUTFirstChannelSlowest, 8, 8); err != nil {
		t.Fatal(err)
	}
	// Node positions are exact; interior points are within rounding of identity.
	for _, in := range [][3]uint16{{0, 0, 0}, {255, 255, 255}, {16, 32, 240}, {255, 0, 128}, {7, 99, 201}} {
		var out [3]uint16
		if err := e.Apply(in, out[:]); err != nil {
			t.Fatal(err)
		}
		for c := 0; c < 3; c++ {
			d := int(out[c]) - int(in[c])
			if d < -1 || d > 1 {
				t.Errorf("in %v out %v: channel %d off by %d", in, out, c, d)
			}
		}
	}
}

func TestValidationAndRange(t *testing.T) {
	nodes, planar := identityLUT(5)
	cases := []struct {
		name  string
		nodes []byte
		n, ch int
	}{
		{"bad first node", []byte{1, 64, 128, 192, 255}, 5, 3},
		{"bad last node", []byte{0, 64, 128, 192, 254}, 5, 3},
		{"not increasing", []byte{0, 128, 128, 192, 255}, 5, 3},
		{"too many channels", nodes, 5, 25},
		{"no channels", nodes, 5, 0},
	}
	for _, c := range cases {
		if _, err := NewLUTEngine(c.n, c.nodes, c.ch, planar); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
	e, _ := NewLUTEngine(5, nodes, 3, planar)
	var out [3]uint16
	if err := e.Apply([3]uint16{}, out[:]); err == nil {
		t.Error("apply before configure should fail")
	}
	if err := e.Configure(LUTFirstChannelSlowest, 12, 16); err != nil {
		t.Fatal(err)
	}
	if err := e.Apply([3]uint16{0xff0 + 1, 0, 0}, out[:]); err == nil {
		t.Error("out-of-range input should fail")
	}
	if err := e.Apply([3]uint16{0xff0, 0, 0}, out[:]); err != nil {
		t.Error(err)
	}
	if e.Configure(LUTFirstChannelSlowest, 7, 8) == nil || e.Configure(2, 8, 8) == nil {
		t.Error("bad configuration accepted")
	}
}

// A tetrahedral interpolation reproduces a linear function exactly (up to
// rounding), also on a grid with non-uniform node positions.
func TestNonUniformGridLinear(t *testing.T) {
	nodes := []byte{0, 16, 64, 160, 255}
	n := len(nodes)
	n3 := n * n * n
	planar := make([]byte, 3*n3)
	for r := 0; r < n; r++ {
		for g := 0; g < n; g++ {
			for b := 0; b < n; b++ {
				i := r*n*n + g*n + b
				planar[i], planar[n3+i], planar[2*n3+i] = nodes[r], nodes[g], 255-nodes[b]
			}
		}
	}
	e, err := NewLUTEngine(n, nodes, 3, planar)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Configure(LUTFirstChannelSlowest, 8, 8); err != nil {
		t.Fatal(err)
	}
	for _, in := range [][3]uint16{{3, 50, 100}, {200, 17, 230}, {90, 90, 90}, {255, 128, 1}} {
		var out [3]uint16
		if err := e.Apply(in, out[:]); err != nil {
			t.Fatal(err)
		}
		want := [3]int{int(in[0]), int(in[1]), 255 - int(in[2])}
		for c := range want {
			if d := int(out[c]) - want[c]; d < -1 || d > 1 {
				t.Errorf("in %v out %v want %v", in, out, want)
				break
			}
		}
	}
}
