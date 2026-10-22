// SPDX-License-Identifier: GPL-2.0-only

package cupsbackend

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	j, err := ParseArgs([]string{"7", "alice", "doc", "2", "media=a4", "/tmp/f"})
	if err != nil || j.JobID != "7" || j.Copies != "2" || j.Options != "media=a4" || j.File != "/tmp/f" {
		t.Fatalf("got %+v, %v", j, err)
	}
	j, err = ParseArgs([]string{"7", "alice", "doc", "2", "media=a4"})
	if err != nil || j.File != "" {
		t.Fatalf("stdin job: %+v, %v", j, err)
	}
	if _, err := ParseArgs([]string{"1", "2"}); !errors.Is(err, ErrUsage) {
		t.Errorf("short argv: %v", err)
	}
}

func TestSwapScheme(t *testing.T) {
	got, err := SwapScheme("mp280usb://Canon/MP280%20series?serial=X", Scheme, StockScheme)
	if err != nil || got != "usb://Canon/MP280%20series?serial=X" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := SwapScheme("ipp://x", Scheme, StockScheme); err == nil {
		t.Error("foreign scheme accepted")
	}
}

func TestLogLines(t *testing.T) {
	var b bytes.Buffer
	l := &Log{W: &b}
	l.Debug("x=%d", 3)
	l.Error("bad")
	l.Line("ATTR: marker-levels=50,80\n")
	if got, want := b.String(), "DEBUG: x=3\nERROR: bad\nATTR: marker-levels=50,80\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFilterDiscovery(t *testing.T) {
	in := strings.Join([]string{
		`network socket "Unknown" "AppSocket/HP JetDirect"`,
		`direct usb://Canon/MP280%20series?serial=X "Canon MP280 series" "Canon MP280 series USB #1" "MFG:Canon;MDL:MP280 series;"`,
		`direct usb://Canon/iP100?serial=Y "Canon iP100" "Canon iP100 USB #1" ""`,
		`direct usb://HP/LaserJet "HP LaserJet" "HP USB" ""`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := FilterDiscovery(strings.NewReader(in), &out, []string{"MP280"}); err != nil {
		t.Fatal(err)
	}
	want := `direct mp280usb://Canon/MP280%20series?serial=X "Canon MP280 series" "Canon MP280 series USB #1 (with ink status)" "MFG:Canon;MDL:MP280 series;"` + "\n"
	if out.String() != want {
		t.Errorf("got %q\nwant %q", out.String(), want)
	}
}
