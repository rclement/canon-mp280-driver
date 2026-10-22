// SPDX-License-Identifier: GPL-2.0-only

package printdb

import (
	"bytes"
	"math/rand"
	"testing"
)

// lzwEncode is a reference encoder for the TIFF-style stream Decompress reads.
func lzwEncode(src []byte) []byte {
	var out []byte
	var acc uint64
	nbits := 0
	emit := func(code, width int) {
		acc = acc<<uint(width) | uint64(code)
		nbits += width
		for nbits >= 8 {
			nbits -= 8
			out = append(out, byte(acc>>uint(nbits)))
		}
		acc &= 1<<uint(nbits) - 1
	}
	dict := map[string]int{}
	next, width := 0x102, 9
	emit(0x100, width)
	var w []byte
	for _, c := range src {
		wc := append(append([]byte(nil), w...), c)
		if len(wc) == 1 {
			w = wc
			continue
		}
		if _, ok := dict[string(wc)]; ok {
			w = wc
			continue
		}
		code := int(w[0])
		if len(w) > 1 {
			code = dict[string(w)]
		}
		emit(code, width)
		dict[string(wc)] = next
		next++
		if next >= 1<<uint(width) && width < 12 {
			width++
		}
		if next >= 4094 {
			emit(0x100, width)
			dict = map[string]int{}
			next, width = 0x102, 9
		}
		w = []byte{c}
	}
	if len(w) > 0 {
		code := int(w[0])
		if len(w) > 1 {
			code = dict[string(w)]
		}
		emit(code, width)
	}
	emit(0x101, width)
	if nbits > 0 {
		emit(0, 8-nbits)
	}
	return out
}

func TestDecompressRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	runs := make([]byte, 50000)
	for i := range runs {
		runs[i] = byte(i / 37)
	}
	noise := make([]byte, 30000)
	r.Read(noise)
	cases := map[string][]byte{
		"single": {42},
		"repeat": bytes.Repeat([]byte{1}, 5000),
		"text":   bytes.Repeat([]byte("abracadabra "), 900),
		"runs":   runs,
		"noise":  noise,
	}
	for name, src := range cases {
		got, err := Decompress(lzwEncode(src))
		if err != nil || !bytes.Equal(got, src) {
			t.Errorf("%s: round trip failed (err %v, %d vs %d bytes)", name, err, len(got), len(src))
		}
	}
}

func TestDecompressErrors(t *testing.T) {
	// clear, literal 'a', then code 0x150 (beyond dictionary)
	bad := []byte{0x80, 0x18, 0x6a, 0x00}
	if _, err := Decompress(bad); err == nil {
		t.Error("expected error for code beyond dictionary")
	}
}

func TestDigestShortInputs(t *testing.T) {
	if Digest(nil, DigestSeed) != DigestSeed {
		t.Error("empty input must return the seed")
	}
	// Tail handling must differ per length and be deterministic.
	seen := map[uint32]int{}
	for n := 1; n <= 9; n++ {
		d := Digest(bytes.Repeat([]byte{0x5a}, n), DigestSeed)
		if prev, ok := seen[d]; ok {
			t.Errorf("lengths %d and %d collide", prev, n)
		}
		seen[d] = n
	}
}

func TestVolumeUndelta(t *testing.T) {
	// Forward-difference a known volume along the three axes, then integrate.
	const n = 4
	vol := make([]byte, n*n*n)
	for i := range vol {
		vol[i] = byte(i*i + 3)
	}
	enc := append([]byte(nil), vol...)
	for j := n*n*n - 1; j >= n*n; j-- {
		enc[j] -= enc[j-n*n]
	}
	for k := 0; k < n; k++ {
		for j := n*n - 1; j >= n; j-- {
			enc[k*n*n+j] -= enc[k*n*n+j-n]
		}
	}
	for r := 0; r < n*n; r++ {
		for i := n - 1; i >= 1; i-- {
			enc[r*n+i] -= enc[r*n+i-1]
		}
	}
	if err := volumeUndelta(enc, n, 1); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc, vol) {
		t.Error("volumeUndelta did not invert the difference coding")
	}
}

func TestTableErrors(t *testing.T) {
	if _, err := FromBytes(make([]byte, 10)); err == nil {
		t.Error("short table accepted")
	}
	tb, _ := FromBytes(make([]byte, 64))
	if _, err := tb.Entry(GroupLUT, 5); err == nil {
		t.Error("entry found in garbage table")
	}
	if _, err := tb.Entry(Group(99), 1); err == nil {
		t.Error("bad group accepted")
	}
}
