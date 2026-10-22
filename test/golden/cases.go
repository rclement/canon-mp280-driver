// SPDX-License-Identifier: GPL-2.0-only

package golden

import (
	"fmt"
	"strconv"
	"strings"
)

// Case is one golden case. Cif cases run internal/cif.Run on PPM images with
// cif style options. Filter cases run internal/filter.Run on a PostScript
// document with CUPS style job options. Expected output is the printer stream
// with the SetTime digits masked; an empty stream means "refused".
type Case struct {
	Name   string
	Group  string
	Args   []string    // cif options (cif cases)
	Images []ImageSpec // PPM images on stdin (cif cases)
	PS     string      // PostScript document (filter cases)
	Opts   string      // CUPS option string (filter cases)
	Copies int         // CUPS copies argument (filter cases, default 1)
	Full   bool        // also store the whole masked stream (for debugging)
	Big    bool        // only with MP280_GOLDEN_BIG=1 (full A4 pages)
}

func (c Case) isFilter() bool { return c.PS != "" }

// ID is the unique key in the expected file: group and name.
func (c Case) ID() string { return c.Group + "/" + c.Name }

func (c Case) slug() string {
	var b strings.Builder
	for _, r := range c.ID() {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '=', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

var photo = ImageSpec{"photo", 200, 150, 1}

// base returns the usual cif options: media, paper size, quality, 300 dpi.
func base(media, size string, quality int, extra ...string) []string {
	return append([]string{"--media", media, "--papersize", size, "--quality", strconv.Itoa(quality), "--imageres", "300"}, extra...)
}

func describe(args []string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.Join(args, " "), "--", ""))
}

// Cases returns the curated list.
func Cases() []Case {
	var out []Case
	add := func(group string, args []string, imgs ...ImageSpec) *Case {
		if len(imgs) == 0 {
			imgs = []ImageSpec{photo}
		}
		names := make([]string, len(imgs))
		for i, im := range imgs {
			names[i] = im.String()
		}
		out = append(out, Case{Name: describe(args) + " | " + strings.Join(names, "+"), Group: group, Args: args, Images: imgs})
		return &out[len(out)-1]
	}

	// Media x quality x paper size, colour photo, no layout options.
	for _, q := range []int{1, 2, 3, 4} {
		for _, s := range []string{"a4", "letter"} {
			add("media", base("plain", s, q))
		}
	}
	for _, q := range []int{1, 2, 3, 4} {
		add("media", base("glossypaper", "4X6", q))
	}
	add("media", base("glossypaper", "5X7", 2))
	add("media", base("glossypaper", "l", 2))
	add("media", base("glossypaper", "a4", 2))
	for _, m := range []string{"matte", "semigloss", "glossygold", "otherphoto"} {
		add("media", base(m, "4X6", 2))
	}
	for _, s := range []string{"4X6", "5X7", "a4"} {
		for _, q := range []int{1, 2} {
			add("media", base("proplatinum", s, q))
		}
	}
	for _, q := range []int{2, 3, 4} {
		add("media", base("postcard", "postcard", q))
	}
	for _, q := range []int{2} {
		add("media", base("ijpostcard", "postcard", q))
	}
	add("media", base("postcardaddress", "postcard", 3))
	add("media", base("glossypost", "postcard", 2))
	for _, s := range []string{"envelop10p", "envelopdlp", "envj4p"} {
		add("media", base("envelope", s, 3))
	}
	add("media", base("envelope", "envelopdlp", 2))
	for _, q := range []int{2, 3, 4} {
		add("media", base("highres", "a4", q))
	}
	add("media", base("tshirt", "a4", 3))

	// Colour versus grayscale.
	gray := func(media, size string, q int, im ...ImageSpec) {
		add("gray", base(media, size, q, "--grayscale"), im...)
	}
	gray("plain", "a4", 3)
	gray("plain", "a4", 2)
	gray("plain", "a4", 4)
	gray("plain", "letter", 3)
	gray("glossypaper", "4X6", 2)
	gray("proplatinum", "4X6", 1)
	gray("postcard", "postcard", 3)
	gray("envelope", "envelopdlp", 3)
	gray("plain", "a4", 3, ImageSpec{"gray", 200, 150, 1})
	gray("plain", "a4", 3, ImageSpec{"text", 240, 160, 2})

	// Render intent.
	for _, m := range []struct {
		media, size string
		q           int
	}{{"plain", "a4", 3}, {"plain", "a4", 2}, {"glossypaper", "4X6", 2}, {"proplatinum", "4X6", 2}, {"matte", "a4", 2}} {
		for _, ri := range []string{"photo", "vivid"} {
			add("intent", base(m.media, m.size, m.q, "--renderintent", ri))
		}
	}
	add("intent", base("plain", "a4", 3, "--renderintent", "graphics"), ImageSpec{"text", 240, 160, 3})
	add("intent", base("plain", "a4", 3, "--renderintent", "vivid"), ImageSpec{"bars", 200, 100, 1})

	// Borderless.
	for _, b := range []struct {
		media, size string
		q           int
	}{{"glossypaper", "4X6", 2}, {"glossypaper", "4X6", 3}, {"glossypaper", "4X6", 4},
		{"glossypaper", "a4", 2}, {"plain", "a4", 3}, {"plain", "a4", 2},
		{"postcard", "postcard", 3}, {"proplatinum", "4X6", 1},
		{"envelope", "envelopdlp", 3}} {
		add("borderless", base(b.media, b.size, b.q, "--borderless"))
	}
	for e := 0; e <= 3; e++ {
		add("borderless", base("glossypaper", "4X6", 2, "--borderless", "--extension", strconv.Itoa(e)))
	}
	add("borderless", base("glossypaper", "4X6", 2, "--borderless", "--fit"))
	add("borderless", base("postcard", "postcard", 3, "--borderless", "--fit"))
	add("borderless", base("plain", "a4", 3, "--borderless", "--fit"))
	add("borderless", base("glossypaper", "4X6", 2, "--borderless", "--grayscale"))

	// Sliders and gamma.
	for _, s := range []struct{ k, v string }{{"density", "-20"}, {"density", "20"}, {"contrast", "-20"}, {"contrast", "20"},
		{"balance_c", "15"}, {"balance_m", "-15"}, {"gamma", "1.8"}, {"gamma", "2.2"}} {
		add("sliders", base("plain", "a4", 3, "--"+s.k, s.v))
	}
	for _, s := range []struct{ k, v string }{{"density", "20"}, {"contrast", "-20"}, {"balance_m", "20"}, {"gamma", "1.8"}} {
		add("sliders", base("glossypaper", "4X6", 2, "--"+s.k, s.v))
	}
	add("sliders", base("plain", "a4", 3, "--balance_c", "10", "--balance_m", "-20", "--balance_y", "33", "--density", "-15", "--contrast", "22"))
	add("sliders", base("glossypaper", "4X6", 2, "--balance_c", "10", "--balance_m", "-20", "--balance_y", "33", "--density", "-15", "--contrast", "22"))
	add("sliders", base("plain", "a4", 3, "--grayscale", "--density", "20"))
	add("sliders", base("plain", "a4", 3, "--grayscale", "--gamma", "1.8"))
	add("sliders", base("plain", "a4", 3, "--density", "50", "--contrast", "-50"))
	add("sliders", base("plain", "a4", 3, "--halftoning", "ed"))

	// Ink cartridge settings.
	for _, ics := range []string{"bk", "color"} {
		add("cartridge", base("plain", "a4", 3, "--inkcartridgesettings", ics))
		add("cartridge", base("plain", "a4", 2, "--inkcartridgesettings", ics, "--grayscale"))
		add("cartridge", base("glossypaper", "4X6", 2, "--inkcartridgesettings", ics))
		add("cartridge", base("envelope", "envelopdlp", 3, "--inkcartridgesettings", ics))
		add("cartridge", base("plain", "a4", 3, "--inkcartridgesettings", ics, "--borderless"))
	}

	// Copies, collate, reverse order, multi page PPM streams.
	p := func(seed uint64) ImageSpec { return ImageSpec{"photo", 120, 90, seed} }
	three := []ImageSpec{p(1), {"bars", 120, 90, 1}, {"text", 120, 120, 2}}
	add("pages", base("plain", "a4", 3), three...)
	add("pages", base("plain", "a4", 3, "--copies", "2"), p(1))
	add("pages", base("plain", "a4", 3, "--copies", "3"), three...)
	add("pages", base("plain", "a4", 3, "--copies", "2", "--collate"), three...)
	add("pages", base("plain", "a4", 3, "--copies", "2", "--revprint"), three...)
	add("pages", base("plain", "a4", 3, "--copies", "2", "--collate", "--revprint"), three...)
	add("pages", base("plain", "a4", 2, "--grayscale"), three...)
	add("pages", base("glossypaper", "4X6", 2), three...)
	add("pages", base("glossypaper", "4X6", 2, "--borderless", "--copies", "2"), p(1), p(2))
	add("pages", base("postcard", "postcard", 3, "--location", "center"), three...)

	// Image content.
	for _, im := range []ImageSpec{{"text", 300, 200, 1}, {"bars", 240, 120, 1}, {"gray", 256, 64, 1}, {"margins", 240, 180, 1}} {
		add("content", base("plain", "a4", 3), im)
		add("content", base("plain", "a4", 2), im)
		add("content", base("glossypaper", "4X6", 2), im)
	}
	add("content", base("plain", "a4", 4), ImageSpec{"text", 300, 200, 1})
	add("content", base("plain", "a4", 1), ImageSpec{"margins", 240, 180, 1})

	// Paper sizes and custom sizes (paper width/height in mm).
	for _, s := range []string{"a5", "legal", "5X7", "l", "8X10"} {
		media := "plain"
		if s == "5X7" || s == "l" || s == "8X10" {
			media = "glossypaper"
		}
		add("size", base(media, s, 3))
	}
	for _, w := range [][2]string{{"14800", "21000"}, {"10000", "10000"}, {"8900", "12700"}, {"20000", "30000"}, {"9000", "25000"}} {
		add("size", base("plain", "user", 3, "--paperwidth", w[0], "--paperheight", w[1]))
	}
	add("size", base("glossypaper", "user", 2, "--paperwidth", "10200", "--paperheight", "15200"))

	// Layout: --fit, --full, --percent, --location, resolution.
	add("layout", base("plain", "a4", 3, "--fit"))
	add("layout", base("plain", "a4", 2, "--fit"))
	add("layout", base("plain", "letter", 3, "--fit"), ImageSpec{"margins", 160, 200, 3})
	add("layout", base("plain", "a4", 3, "--fit", "--grayscale"))
	add("layout", base("glossypaper", "4X6", 2, "--fit"))
	add("layout", base("glossypaper", "4X6", 3, "--fit", "--location", "center"))
	add("layout", base("postcard", "postcard", 3, "--fit"))
	add("layout", base("proplatinum", "4X6", 1, "--fit"))
	add("layout", base("plain", "a4", 3, "--full"))
	add("layout", base("plain", "a4", 3, "--location", "center"))
	add("layout", base("plain", "a4", 3, "--location", "upperleft"))
	add("layout", base("plain", "a4", 3, "--bbox", "20,20,500,700", "--fit"))
	for _, r := range []string{"600"} {
		add("layout", []string{"--media", "plain", "--papersize", "a4", "--quality", "3", "--imageres", r})
		add("layout", []string{"--media", "glossypaper", "--papersize", "4X6", "--quality", "2", "--imageres", r})
	}

	// Full A4 pages at 300 dpi (hash only, MP280_GOLDEN_BIG=1).
	big := func(args []string, im ImageSpec) {
		c := add("big", args, im)
		c.Big = true
	}
	big(base("plain", "a4", 3), ImageSpec{"photo", 2480, 3508, 7})
	big(base("plain", "a4", 2, "--grayscale"), ImageSpec{"margins", 2480, 3508, 3})
	big(base("glossypaper", "a4", 2, "--renderintent", "vivid"), ImageSpec{"photo", 2480, 3508, 9})

	// Option acceptance and rejection (a rejected job has an empty stream).
	tiny := ImageSpec{"photo", 16, 16, 1}
	for _, o := range [][]string{
		// valid
		base("plain", "a4", 3, "--density", "50"),
		base("plain", "a4", 3, "--balance_c", "50", "--balance_m", "-50"),
		base("plain", "a4", 3, "--gamma", "1.8", "--renderintent", "vivid"),
		base("plain", "a4", 3, "--copies", "5"),
		base("plain", "a4", 3, "--paperload", "cassette"),
		base("glossypaper", "4X6", 2, "--paperload", "asf", "--borderless"),
		// invalid values
		base("plain", "a4", 0),
		base("plain", "a4", 5),
		base("plain", "a4", 3, "--density", "51"),
		base("plain", "a4", 3, "--density", "x"),
		base("plain", "a4", 3, "--renderintent", "bogus"),
		base("plain", "a4", 3, "--cartridge", "bogus"),
		base("plain", "a4", 3, "--location", "right"),
		base("plain", "a4", 3, "--fit", "--full"),
		base("plain", "a4", 3, "--fit", "--percent", "50"),
		base("bogus", "a4", 3),
		base("plain", "bogus", 3),
		base("plain", "user", 3),
		base("plain", "user", 3, "--paperwidth", "200000", "--paperheight", "300000"),
		// combinations the printer cannot do
		base("glossypaper", "4X6", 4),
		base("glossypaper", "4X6", 5),
		base("proplatinum", "4X6", 4),
		base("envelope", "envelopdlp", 3, "--borderless"),
		base("plain", "a4", 3, "--duplex"),
		base("plain", "a4", 3, "--tone", "1"),
		base("plain", "user", 3, "--paperwidth", "10000", "--paperheight", "15000", "--borderless"),
		base("glossypaper", "4X6", 2, "--borderless", "--paperload", "cassette"),
		base("highres", "a4", 3, "--borderless"),
	} {
		add("options", o, tiny)
	}
	out = append(out, filterCases()...)

	// Whole streams for a handful of small, representative cases.
	for _, name := range fullNames {
		found := false
		for i := range out {
			if out[i].ID() == name {
				out[i].Full = true
				found = true
			}
		}
		if !found {
			panic("fullNames: unknown case " + name)
		}
	}
	seen := map[string]bool{}
	for _, c := range out {
		if seen[c.ID()] {
			panic("duplicate case " + c.ID())
		}
		seen[c.ID()] = true
	}
	return out
}

// fullNames lists the cases whose complete stream is stored for debugging.
var fullNames = []string{
	"media/media plain papersize a4 quality 3 imageres 300 | photo-200x150-s1",
	"media/media plain papersize letter quality 1 imageres 300 | photo-200x150-s1",
	"media/media glossypaper papersize 4X6 quality 2 imageres 300 | photo-200x150-s1",
	"media/media proplatinum papersize 4X6 quality 1 imageres 300 | photo-200x150-s1",
	"media/media postcard papersize postcard quality 3 imageres 300 | photo-200x150-s1",
	"media/media envelope papersize envelopdlp quality 3 imageres 300 | photo-200x150-s1",
	"gray/media plain papersize a4 quality 3 imageres 300 grayscale | photo-200x150-s1",
	"borderless/media glossypaper papersize 4X6 quality 2 imageres 300 borderless | photo-200x150-s1",
	"cartridge/media plain papersize a4 quality 3 imageres 300 inkcartridgesettings bk | photo-200x150-s1",
	"pages/media plain papersize a4 quality 3 imageres 300 copies 2 collate | photo-120x90-s1+bars-120x90-s1+text-120x120-s2",
	"options/media plain papersize a4 quality 3 imageres 300 density 50 | photo-16x16-s1",
}

func psHeader(w, h, pages int) string {
	return fmt.Sprintf("%%!PS-Adobe-3.0\n%%%%BoundingBox: 0 0 %d %d\n%%%%Pages: %d\n%%%%EndComments\n", w, h, pages)
}

func psPage(w, h, n int, body string) string {
	return fmt.Sprintf("%%%%Page: %d %d\n<< /PageSize [%d %d] >> setpagedevice\ngsave\n%s\ngrestore\nshowpage\n", n, n, w, h, body)
}

// psBars: eight colour bars (integer coordinates only).
func psBars(w, h int) string {
	cols := [][3]int{{1, 1, 1}, {1, 1, 0}, {0, 1, 1}, {0, 1, 0}, {1, 0, 1}, {1, 0, 0}, {0, 0, 1}, {0, 0, 0}}
	var b strings.Builder
	bw := (w - 24) / 8
	for i, c := range cols {
		fmt.Fprintf(&b, "%d %d %d setrgbcolor %d %d %d %d rectfill\n", c[0], c[1], c[2], 12+i*bw, h*55/100, bw, h*35/100)
	}
	return b.String()
}

// psGradient: 64 colour steps and a 64 step gray ramp.
func psGradient(w, h int) string {
	var b strings.Builder
	sw := (w - 24) / 64
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&b, "%d 64 div %d 64 div 0.5 setrgbcolor %d %d %d %d rectfill\n", i, 63-i, 12+i*sw, h/10, sw+1, h*4/10)
		fmt.Fprintf(&b, "%d 63 div setgray %d %d %d %d rectfill\n", i, 12+i*sw, h/20, sw+1, h/25)
	}
	return b.String()
}

// psImage: a colorimage of the synthetic photo scaled over the page area.
func psImage(w, h, x, y, sx, sy int, spec ImageSpec) string {
	ppm := spec.PPM()
	data := ppm[len(ppm)-spec.W*spec.H*3:]
	return fmt.Sprintf("gsave %d %d translate %d %d scale %d %d 8 [%d 0 0 -%d 0 %d] <%x> false 3 colorimage grestore\n",
		x, y, sx, sy, spec.W, spec.H, spec.W, spec.H, spec.H, data)
}

func psDoc(w, h int, contents ...string) string {
	var b strings.Builder
	b.WriteString(psHeader(w, h, len(contents)))
	for i, c := range contents {
		b.WriteString(psPage(w, h, i+1, c))
	}
	b.WriteString("%%EOF\n")
	return b.String()
}

func filterCases() []Case {
	var out []Case
	mixed := func(w, h int) string {
		return psBars(w, h) + psGradient(w, h) + psImage(w, h, w/10, h*3/5, w*8/10, h/5, ImageSpec{"photo", 37, 23, 2})
	}
	full := func(w, h int) string {
		return psImage(w, h, -3, -3, w+6, h+6, ImageSpec{"photo", 24, 16, 3})
	}
	// Page sizes in points: l 252x360, 4X6 288x432, postcard 283x420.
	type sz struct {
		name string
		w, h int
	}
	l, x46, pc := sz{"l", 252, 360}, sz{"4X6", 288, 432}, sz{"postcard", 283, 420}
	add := func(name string, s sz, bl bool, opts string, copies int, contents ...func(w, h int) string) {
		w, h := s.w, s.h
		size := s.name
		if bl {
			w, h = w+1, h+1
			size += ".bl"
		}
		var cs []string
		for _, f := range contents {
			cs = append(cs, f(w, h))
		}
		out = append(out, Case{Name: name, Group: "filter", PS: psDoc(w, h, cs...), Opts: "PageSize=" + size + " " + opts, Copies: copies})
	}
	gl := "MediaType=glossypaper"
	add("l glossy q2 mixed", l, false, gl+" CNQuality=2", 1, mixed)
	add("4X6 glossy q2 mixed", x46, false, gl+" CNQuality=2", 1, mixed)
	add("4X6 glossy q2 gray", x46, false, gl+" CNQuality=2 CNGrayscale=True", 1, mixed)
	add("4X6 glossy q2 vivid", x46, false, gl+" CNQuality=2 CNRenderIntent=Vivid", 1, mixed)
	add("4X6.bl glossy q2 fullbleed", x46, true, gl+" CNQuality=2", 1, full)
	add("4X6 proplatinum q1", x46, false, "MediaType=proplatinum CNQuality=1", 1, mixed)
	add("postcard plain q3", pc, false, "MediaType=postcard CNQuality=3", 1, mixed)
	add("l sliders", l, false, gl+" CNQuality=2 CNDensity=20 CNContrast=-10 CNBalanceC=10", 1, mixed)
	add("postcard ink bk", pc, false, "MediaType=postcard CNQuality=3 CNInkCartridgeSettings=bk", 1, mixed)
	add("postcard ink color", pc, false, "MediaType=postcard CNQuality=3 CNInkCartridgeSettings=color", 1, mixed)
	add("postcard cartridge Bk", pc, false, "MediaType=postcard CNQuality=3 CNCartridge=Bk", 1, mixed)
	add("postcard cartridge Color", pc, false, "MediaType=postcard CNQuality=3 CNCartridge=Color", 1, mixed)
	add("l two pages", l, false, gl+" CNQuality=2", 1, mixed, bars)
	add("l copies 2", l, false, gl+" CNQuality=2 Collate=True", 2, mixed)
	add("l glossy q4", l, false, gl+" CNQuality=4", 1, bars)
	add("4X6 unknown option ignored", x46, false, gl+" CNQuality=2 Bogus=1 CNQuality=9", 1, bars)
	return out
}

func gradientOnly(w, h int) string { return psGradient(w, h) }
func bars(w, h int) string         { return psBars(w, h) }
