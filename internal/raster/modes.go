// SPDX-License-Identifier: GPL-2.0-only

package raster

// Mode selection: which ink planes a page has and how many bits per pixel each
// plane carries, derived from media, quality, grayscale, borderless and head.

// qualityIndex maps the quality code to the row of the mode tables.
func qualityIndex(q int16) int {
	switch {
	case q == 5:
		return 3
	case q < 6 && q == 0:
		return 4
	case q < 6:
		return 0
	case q == 10:
		return 2
	case q == 15:
		return 1
	}
	return 0
}

// mediaIndex maps the media code to the column of the mode tables.
func mediaIndex(m int16) int {
	switch m {
	case 1:
		return 2
	case 7, 8:
		return 1
	case 0x0f, 0x16, 0x18:
		return 4
	case 0x10:
		return 5
	case 0x12:
		return 3
	case 0x1b:
		return 6
	case 0x1c:
		return 7
	case 0x24:
		return 9
	case 0x2a:
		return 8
	case 0x32:
		return 11
	case 0x33:
		return 10
	case 0x36:
		return 13
	case 0x38:
		return 12
	}
	return 0
}

// printHead maps the head-type code to the head index of the tables.
func printHead(headType int16) int {
	switch headType {
	case 1:
		return 1
	case 2:
		return 2
	}
	return 0
}

// isGrayscale reports whether the grayscale flag selects black-only printing.
func isGrayscale(flag int16) bool { return flag == 1 || flag == 2 }

// isPlainOrPostcardMedia reports the media indices 0, 1 and 12, for which
// grayscale and borderless printing change the plane set.
func isPlainOrPostcardMedia(mi int) bool { return mi == 0 || mi == 1 || mi == 12 }

// planeMode returns the plane mode: 1 = K, 2/3/4 = CMY, 5 = CMYK, 0 = none.
func planeMode(p Params, head int) int {
	qi, mi := qualityIndex(p.Quality), mediaIndex(p.Media)
	mode := int(printModeTable[mi+qi*14+head*70])
	special := isPlainOrPostcardMedia(mi)
	if p.Borderless != 1 && isGrayscale(p.Grayscale) && head != 1 && special {
		mode = 1
	}
	if p.Borderless == 1 && special {
		mode = 2
	}
	return mode & 0xf
}

// bitDepth returns the bits per pixel of a plane.
func bitDepth(p Params, plane, head int) int {
	qi, mi := qualityIndex(p.Quality), mediaIndex(p.Media)
	b := int(colourBitTable[plane+qi*98+mi*7+head*490]) & 0xf
	borderless := p.Borderless == 1
	gray := isGrayscale(p.Grayscale)
	if gray && !borderless && head != 1 && p.Flag74 == 0 {
		if mi != 1 && mi != 12 {
			if mi != 0 {
				return b
			}
			if qi == 3 {
				return 2
			}
		}
		return 1
	}
	if p.Flag74 != 1 || mi != 0 {
		return b
	}
	if gray && !borderless && head != 1 {
		return 1
	}
	if p.Grayscale != 0 || borderless || qi != 3 || head != 0 {
		return b
	}
	switch {
	case plane < 2:
		return 2
	case plane > 3:
		return b
	}
	return 1
}

// interleaveType returns the high nibble of the colour-bit table entry: non-zero
// for planes the printer wants interleaved ("Tgl" planes of other models).
func interleaveType(p Params, plane, head int) int {
	qi, mi := qualityIndex(p.Quality), mediaIndex(p.Media)
	v := int(colourBitTable[plane+qi*98+mi*7+head*490])
	if p.Borderless != 1 && isGrayscale(p.Grayscale) && head != 1 && isPlainOrPostcardMedia(mi) {
		v = 0
	}
	return v >> 4
}
