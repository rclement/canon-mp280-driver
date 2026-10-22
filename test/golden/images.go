// SPDX-License-Identifier: GPL-2.0-only

package golden

import (
	"bytes"
	"fmt"
)

// Synthetic test images. Everything is integer arithmetic on a fixed LCG so
// the pixels are identical on every platform and Go version (no floating
// point, no math package).

type lcg uint64

func (l *lcg) next() uint32 {
	*l = *l*6364136223846793005 + 1442695040888963407
	return uint32(*l >> 33)
}

// tri is a triangle wave of period 2*p scaled to 0..255.
func tri(t, p int) int {
	t %= 2 * p
	if t < 0 {
		t += 2 * p
	}
	if t > p {
		t = 2*p - t
	}
	return t * 255 / p
}

func clamp(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// ImageSpec names a generated image: kind, size in pixels and a seed.
type ImageSpec struct {
	Kind string
	W, H int
	Seed uint64
}

func (s ImageSpec) String() string { return fmt.Sprintf("%s-%dx%d-s%d", s.Kind, s.W, s.H, s.Seed) }

// Kinds: photo (smooth colour gradients plus faint noise), text (black glyph
// like blocks on white), bars (eight colour bars), gray (horizontal gray
// ramp), margins (photo inside a white frame with white rows).
func (s ImageSpec) PPM() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "P6\n%d %d\n255\n", s.W, s.H)
	rs := lcg(s.Seed*7919 + uint64(s.W)*31 + uint64(s.H))
	sd := int(s.Seed)
	bars := [8][3]byte{{255, 255, 255}, {255, 255, 0}, {0, 255, 255}, {0, 255, 0}, {255, 0, 255}, {255, 0, 0}, {0, 0, 255}, {0, 0, 0}}
	// text: a grid of cells, each either filled black or empty, rows spaced.
	cell := 6
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			var r, g, bl byte
			switch s.Kind {
			case "photo":
				n := int(rs.next()%7) - 3
				r = clamp(tri(x*3+y+sd*17, 90) + n)
				g = clamp(tri(y*2-x+sd*29, 70) + n)
				bl = clamp(tri(x+y*2+sd*41, 120) + n)
			case "text":
				r, g, bl = 255, 255, 255
				line, cy := y/(2*cell), y%(2*cell)
				cx := x / cell
				if cy < cell+2 && cy >= 2 && x%cell != cell-1 {
					h := uint32(line*7919+cx*104729+sd*31) * 2654435761
					if (h>>16)%5 < 3 && (line*cell*2+10) < s.H-4 {
						r, g, bl = 0, 0, 0
					}
				}
			case "bars":
				c := bars[x*8/s.W]
				r, g, bl = c[0], c[1], c[2]
			case "gray":
				r = clamp(x * 255 / max(s.W-1, 1))
				g, bl = r, r
			case "margins":
				r, g, bl = 255, 255, 255
				if x >= s.W/5 && x < s.W-s.W/5 && y >= s.H/5 && y < s.H-s.H/5 && y%9 != 4 {
					r = clamp(tri(x*3+y+sd*17, 90))
					g = clamp(tri(y*2-x+sd*29, 70))
					bl = clamp(tri(x+y*2+sd*41, 120))
				}
			default:
				panic("unknown image kind " + s.Kind)
			}
			b.Write([]byte{r, g, bl})
		}
	}
	return b.Bytes()
}
