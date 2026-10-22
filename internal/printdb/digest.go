// SPDX-License-Identifier: GPL-2.0-only

package printdb

// DigestSeed is the seed the original driver uses when hashing a decoded base LUT blob.
const DigestSeed uint32 = 0xfeedbac9

// Digest is the integrity hash over a decoded LUT blob; a delta-coded LUT
// stores the digest of its base blob so that a wrong base is detected. All
// arithmetic is 32-bit unsigned.
func Digest(p []byte, seed uint32) uint32 {
	n := len(p)
	s := seed
	if n == 0 {
		return s
	}
	cnt := uint32(n / 4)
	k := cnt
	for i := 0; i < int(cnt); i++ {
		q := p[4*i:]
		v := (s*2 - (s/0xb)*0xb) + (s-(s/0x11)*0x11)*uint32(q[0])
		v = v + ^(v & k) + ((v>>(v&3))%5)*uint32(q[1])
		v = v + (v%(k%0x17+0x1f))*(uint32(q[2])%0xd)
		t := k % (v%0x29 + 5)
		x := v ^ k
		k--
		s = v + ^t + x*uint32(q[3])
	}
	q := p[4*cnt:]
	r := n % 4
	if r > 0 {
		s = s*2 - (s/0xb)*0xb + (s-(s/0x11)*0x11)*uint32(q[0])
		q = q[1:]
	}
	if r-1 > 0 {
		s = s + ^(s & k) + ((s>>(s&3))%5)*uint32(q[0])
		q = q[1:]
	}
	if r-2 > 0 {
		s = s + (s%(k%0x17+0x1f))*(uint32(q[0])%0xd)
	}
	return s
}
