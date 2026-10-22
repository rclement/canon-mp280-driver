// SPDX-License-Identifier: GPL-2.0-only

// Package golden checks the printer streams the driver produces against
// recorded expectations: length and sha256 per case, plus a few complete
// streams used to locate the first differing byte. A change in output
// needs a stated reason. See docs/TESTING.md.
//
//	go test ./test/golden   (uses data/canon; MP280_TABLE_DIR overrides)
//	MP280_GOLDEN_BIG=1 ...         # also the full A4 300 dpi pages
package golden

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"canon-mp280-driver/internal/cif"
	"canon-mp280-driver/internal/filter"
)

var setTime = regexp.MustCompile(`SetTime=\d{14}`)

func mask(b []byte) []byte { return setTime.ReplaceAll(b, []byte("SetTime=00000000000000")) }

// Expect is the stored result of one case.
type Expect struct {
	Len  int
	Hash string // hex sha256 of the masked stream
}

func digest(b []byte) Expect {
	h := sha256.Sum256(b)
	return Expect{Len: len(b), Hash: hex.EncodeToString(h[:])}
}

const (
	expectedFile = "testdata/expected.tsv"
	fullDir      = "testdata/full"
	ppdFile      = "../../ppd/canonmp280-oss.ppd"
)

func readExpected() (map[string]Expect, error) {
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		return nil, err
	}
	m := map[string]Expect{}
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if ln == "" || ln[0] == '#' {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) != 3 {
			return nil, fmt.Errorf("bad line %q", ln)
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			return nil, err
		}
		m[f[0]] = Expect{Len: n, Hash: f[2]}
	}
	return m, nil
}

func readFull(c Case) ([]byte, bool) {
	f, err := os.Open(filepath.Join(fullDir, c.slug()+".bin.gz"))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, false
	}
	b, err := io.ReadAll(z)
	return b, err == nil
}

// tableDir makes cif.Run use the Canon tables fetched by make unless MP280_TABLE_DIR is set.
func tableDir(t *testing.T) string {
	d := os.Getenv("MP280_TABLE_DIR")
	if d == "" {
		abs, err := filepath.Abs("../../data/canon")
		if err != nil {
			t.Fatal(err)
		}
		d = abs
		t.Setenv("MP280_TABLE_DIR", d)
	}
	if _, err := os.Stat(filepath.Join(d, "cifmp280.conf")); err != nil {
		t.Fatalf("data tables missing: %v", err)
	}
	return d
}

func stdin(c Case) []byte {
	var b bytes.Buffer
	for _, im := range c.Images {
		b.Write(im.PPM())
	}
	return b.Bytes()
}

// runGo runs a case and returns the masked stream.
func runGo(c Case, ppdPath string) ([]byte, error) {
	var out bytes.Buffer
	if !c.isFilter() {
		// The MP280 cartridge is always given first (a later --cartridge wins).
		err := cif.Run(append([]string{"--cartridge", "bkcolor"}, c.Args...), bytes.NewReader(stdin(c)), &out)
		var setup *cif.SetupError
		if !errors.As(err, &setup) {
			err = nil // unusable options or images end the job with exit 0 and no output
		}
		return mask(out.Bytes()), err
	}
	copies := c.Copies
	if copies == 0 {
		copies = 1
	}
	dir, err := os.MkdirTemp("", "golden-filter")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.ps")
	if err := os.WriteFile(in, []byte(c.PS), 0o644); err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	env := filter.Env{PPD: ppdPath, DeviceURI: "file:/dev/null", Ghostscript: os.Getenv("MP280_GS")}
	filter.Run(context.Background(), []string{"1", "user", "title", strconv.Itoa(copies), c.Opts, in}, env, nil, &out, &stderr)
	return mask(out.Bytes()), nil
}

func haveGhostscript() bool {
	_, err := filter.FindGhostscript(os.Getenv("MP280_GS"))
	return err == nil
}

func selected(c Case) (skip string) {
	if c.Big && os.Getenv("MP280_GOLDEN_BIG") == "" {
		return "full A4 case: set MP280_GOLDEN_BIG=1"
	}
	if c.isFilter() && !haveGhostscript() {
		return "Ghostscript not found"
	}
	return ""
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// Limit on concurrently running cases; the A4 pages are memory hungry.
var sem = make(chan struct{}, 4)

func TestGolden(t *testing.T) {
	tableDir(t)
	cases := Cases()
	ppdPath, err := filepath.Abs(ppdFile)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := readExpected()
	if err != nil {
		t.Fatalf("cannot read %s: %v", expectedFile, err)
	}

	dump := os.Getenv("MP280_GOLDEN_DUMP")
	for _, c := range cases {
		t.Run(c.ID(), func(t *testing.T) {
			if why := selected(c); why != "" {
				t.Skip(why)
			}
			want, ok := expected[c.ID()]
			if !ok {
				t.Fatalf("no expectation stored")
			}
			t.Parallel()
			sem <- struct{}{}
			defer func() { <-sem }()
			got, err := runGo(c, ppdPath)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if dump != "" {
				_ = os.MkdirAll(dump, 0o755)
				_ = os.WriteFile(filepath.Join(dump, c.slug()+".bin"), got, 0o644)
			}
			d := digest(got)
			if d == want {
				return
			}
			msg := fmt.Sprintf("stream differs: got %d bytes sha256 %s, want %d bytes sha256 %s", d.Len, d.Hash[:16], want.Len, want.Hash[:16])
			if full, ok := readFull(c); ok {
				msg += fmt.Sprintf("; first difference at byte %d of the stored stream", firstDiff(got, full))
			}
			t.Error(msg)
		})
	}
}

// TestPPD checks that the shipped PPD parses and offers the options the cases use.
func TestPPD(t *testing.T) {
	f, err := filter.OpenPPD(ppdFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, kw := range []string{"PageSize", "MediaType", "CNQuality", "CNGrayscale", "CNCartridge"} {
		if f.Option(kw) == nil {
			t.Errorf("PPD has no option %s", kw)
		}
	}
	if len(f.KeyTable) == 0 {
		t.Error("PPD has no CNPpdToOptKey table")
	}
}

// TestCaseTable checks the case list itself.
func TestCaseTable(t *testing.T) {
	cs := Cases()
	groups := map[string]int{}
	for _, c := range cs {
		groups[c.Group]++
	}
	t.Logf("%d cases: %v", len(cs), groups)
	if len(cs) < 100 || len(cs) > 250 {
		t.Errorf("curated set should have 100-250 cases, has %d", len(cs))
	}
}
