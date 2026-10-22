// SPDX-License-Identifier: GPL-2.0-only

package pipeline

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"canon-mp280-driver/internal/jobstream"
	"canon-mp280-driver/internal/options"
)

func TestFrontEndValue(t *testing.T) {
	tests := []struct {
		v     int
		pairs [][2]int
		want  int
	}{
		{3, gammaPairs, 18},
		{0, gammaPairs, 0},
		{5, gammaPairs, -1},
		{9, gammaPairs, -1},
		{1018, gammaPairs, 18},
		{2, intentPairs, 3},
		{1003, intentPairs, 3},
	}
	for _, tt := range tests {
		if got := cnclValue(tt.v, tt.pairs); got != tt.want {
			t.Errorf("cnclValue(%d) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func TestReplayOrder(t *testing.T) {
	pages := []storedPage{[]byte("A"), []byte("B"), []byte("C")}
	tests := []struct {
		name         string
		collate, rev bool
		copies       int
		want         string // what replay adds after the first copy of every page went out
	}{
		{"plain copies are written inline", false, false, 3, ""},
		{"collated copies", true, false, 3, "ABCABC"},
		{"reverse", false, true, 2, "CCBBAA"},
		{"reverse and collated", true, true, 2, "CBACBA"},
	}
	for _, tt := range tests {
		p := &Printer{cfg: Config{Collate: tt.collate, ReversePrint: tt.rev}}
		var out bytes.Buffer
		if err := p.replay(&out, pages, tt.copies); err != nil {
			t.Fatal(err)
		}
		if out.String() != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, out.String(), tt.want)
		}
	}
}

// gradientPage is a synthetic page source: rows of a horizontal ramp.
type gradientPage struct {
	geo     Geometry
	failAt  int // row whose Row call fails (-1: none)
	pixels  []byte
	rowsOut int
}

func (g *gradientPage) Geometry() Geometry { return g.geo }

func (g *gradientPage) Row(y int) (Row, error) {
	if y == g.failAt {
		return Row{}, errors.New("read error")
	}
	g.rowsOut++
	w := g.geo.Width
	if g.pixels == nil {
		g.pixels = make([]byte, 3*w)
	}
	for x := 0; x < w; x++ {
		v := byte(255 * x / max(w-1, 1))
		g.pixels[3*x], g.pixels[3*x+1], g.pixels[3*x+2] = v, v, v
	}
	return Row{Pixels: g.pixels}, nil
}

func tableDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("MP280_TABLE_DIR")
	if dir == "" {
		dir = "../../data/canon" // the Canon tables fetched by make
	}
	return dir
}

func newTestPrinter(t *testing.T, copies int, args ...string) *Printer {
	t.Helper()
	db, s, err := options.Configure(tableDir(t), "MP280", append([]string{"--cartridge", "bkcolor"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.NewJob(db, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	com, err := db.ComSystem(s.Device)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{Database: db.Tables(), Job: job, Settings: s.JobSettings(com), Copies: copies})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func pages(srcs ...PageSource) func() (PageSource, error) {
	i := 0
	return func() (PageSource, error) {
		if i == len(srcs) {
			return nil, io.EOF
		}
		i++
		return srcs[i-1], nil
	}
}

func TestPrintCopiesRepeatPage(t *testing.T) {
	geo := Geometry{Width: 4800, Height: 20, TopSkip: 5}
	var one, three bytes.Buffer
	if err := newTestPrinter(t, 1).Print(&one, pages(&gradientPage{geo: geo, failAt: -1})); err != nil {
		t.Fatal(err)
	}
	if err := newTestPrinter(t, 3).Print(&three, pages(&gradientPage{geo: geo, failAt: -1})); err != nil {
		t.Fatal(err)
	}
	head, _ := newTestPrinter(t, 1).cfg.Job.Begin()
	foot := jobstream.JobFooter()
	body := one.Bytes()[len(head) : one.Len()-len(foot)-endJobLen(t)]
	want := append(append(append([]byte{}, one.Bytes()[:len(head)+len(body)]...), body...), body...)
	want = append(want, one.Bytes()[len(head)+len(body):]...)
	if !bytes.Equal(maskTime(three.Bytes()), maskTime(want)) {
		t.Errorf("3 copies are not 3 times the page (%d vs %d bytes)", three.Len(), len(want))
	}
}

func endJobLen(t *testing.T) int {
	t.Helper()
	x, err := jobstream.EndJobXML(jobstream.DefaultJobID)
	if err != nil {
		t.Fatal(err)
	}
	return len(x)
}

func maskTime(b []byte) []byte {
	i := bytes.Index(b, []byte("SetTime="))
	if i < 0 {
		return b
	}
	out := append([]byte{}, b...)
	for k := i + 8; k < i+22 && k < len(out); k++ {
		out[k] = '0'
	}
	return out
}

// A page that fails half way leaves what was produced in the stream and the
// job is still closed properly, like the original front end does.
func TestPrintSourceErrorClosesJob(t *testing.T) {
	geo := Geometry{Width: 4800, Height: 40, TopSkip: 0}
	var out bytes.Buffer
	err := newTestPrinter(t, 1).Print(&out, pages(&gradientPage{geo: geo, failAt: 30}))
	if err == nil || !strings.Contains(err.Error(), "read error") {
		t.Fatalf("error = %v, want the read error", err)
	}
	tail := append(jobstream.JobFooter(), mustEndJob(t)...)
	if !bytes.HasSuffix(out.Bytes(), tail) {
		t.Errorf("stream does not end with the job footer")
	}
	if !bytes.Contains(out.Bytes(), []byte{0x1b, '(', 'F'}) {
		t.Errorf("no raster of the first rows in the stream")
	}
}

func mustEndJob(t *testing.T) []byte {
	t.Helper()
	x, err := jobstream.EndJobXML(jobstream.DefaultJobID)
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func TestPrintRejectsBadGeometry(t *testing.T) {
	var out bytes.Buffer
	err := newTestPrinter(t, 1).Print(&out, pages(&gradientPage{geo: Geometry{Width: 0, Height: 5}, failAt: -1}))
	if err == nil {
		t.Fatal("zero width accepted")
	}
}
