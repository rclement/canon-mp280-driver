// SPDX-License-Identifier: GPL-2.0-only

//go:build !race

// Package perf holds the timing check of the full pipeline. The race detector
// slows it about tenfold, so it only runs in normal builds.
package perf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"canon-mp280-driver/internal/cif"
	"canon-mp280-driver/test/golden"
)

// noisePPM returns a w x h PPM of pseudo-random pixels, the worst case for
// the halftoner.
func noisePPM(w, h int) []byte {
	b := make([]byte, 0, w*h*3+32)
	b = fmt.Appendf(b, "P6\n%d %d\n255\n", w, h)
	x := uint64(7)
	for i := 0; i < w*h*3; i++ {
		x = x*6364136223846793005 + 1442695040888963407
		b = append(b, byte(x>>56))
	}
	return b
}

// TestPerformanceA4 times a full A4 colour page at 600 dpi (about 4960 x 7016
// pixels, 14 MB of stream). The target is 15 seconds. It uses the fetched Canon tables unless MP280_TABLE_DIR is set. It is opt-in
// (MP280_PERF=1) because it measures wall time and must not share the machine
// with other test packages.
func TestPerformanceA4(t *testing.T) {
	if os.Getenv("MP280_TABLE_DIR") == "" {
		abs, err := filepath.Abs("../../data/canon")
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("MP280_TABLE_DIR", abs) // the Canon tables fetched by make
	}
	if os.Getenv("MP280_PERF") == "" {
		t.Skip("timing check: run it alone with `make perf` (MP280_PERF=1)")
	}
	images := map[string][]byte{
		"photo": golden.ImageSpec{Kind: "photo", W: 4960, H: 7016, Seed: 7}.PPM(),
		"noise": noisePPM(4960, 7016),
	}
	for name, img := range images {
		var out bytes.Buffer
		start := time.Now()
		args := []string{"--cartridge", "bkcolor", "--media", "plain", "--papersize", "a4", "--quality", "3", "--imageres", "600"}
		if err := cif.Run(args, bytes.NewReader(img), &out); err != nil {
			t.Fatal(err)
		}
		d := time.Since(start)
		t.Logf("A4 600 dpi %s: %v, %d bytes of stream", name, d, out.Len())
		if out.Len() < 1<<20 {
			t.Errorf("%s: stream is only %d bytes", name, out.Len())
		}
		if d > 15*time.Second {
			t.Errorf("%s: %v exceeds the 15 s target", name, d)
		}
	}
}
