// SPDX-License-Identifier: GPL-2.0-only

// Command mp280-status queries printer state and ink levels of a Canon PIXMA
// MP280 over USB (IVEC status session) and prints them as JSON, as raw BSCC
// text, or as the CUPS backend stderr lines (ATTR: / STATE:).
//
//	mp280-status [-uri usb://Canon/MP280%20series?serial=X] [-timeout S] [-raw | -cups | -report] [-v]
//	mp280-status -parse FILE|-         decode a captured GetStatusResponse XML document (no device access)
//	mp280-status -parse-bscc TEXT      decode a bare BSCC text (no device access)
//
// Exit status: 0 ok, 1 query failed, 2 usage, 3 device busy (a print job
// holds the interface), 4 device not found. The USB session needs the printer
// interface to be free: do not run it while the queue is printing.
//
// MP280_STATUS_TRANSPORT=unix:/path talks to the software mock instead of USB.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"canon-mp280-driver/internal/status"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mp280-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	uri := fs.String("uri", "", "device URI (usb:// or mp280usb://; serial= selects the printer)")
	timeout := fs.Float64("timeout", 20, "session deadline in seconds")
	raw := fs.Bool("raw", false, "print the raw BSCC text")
	cups := fs.Bool("cups", false, "print the ATTR:/STATE: lines of the CUPS backend")
	report := fs.Bool("report", false, "print the supply report as JSON")
	verbose := fs.Bool("v", false, "trace the bytes exchanged on stderr")
	parseFile := fs.String("parse", "", "decode a GetStatusResponse XML file (- for stdin), no device access")
	parseBSCC := fs.String("parse-bscc", "", "decode a bare BSCC text, no device access")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (*parseFile != "" && *parseBSCC != "") {
		fs.Usage()
		return 2
	}

	var text string
	switch {
	case *parseBSCC != "":
		text = *parseBSCC
	case *parseFile != "":
		var r io.Reader = stdin
		if *parseFile != "-" {
			f, err := os.Open(*parseFile)
			if err != nil {
				fmt.Fprintf(stderr, "mp280-status: %v\n", err)
				return 1
			}
			defer f.Close()
			r = f
		}
		b, err := io.ReadAll(io.LimitReader(r, 1<<20))
		if err != nil {
			fmt.Fprintf(stderr, "mp280-status: %v\n", err)
			return 1
		}
		t, ok := status.ExtractStatus(b)
		if !ok {
			fmt.Fprintln(stderr, "mp280-status: no status in input")
			return 1
		}
		text = t
	default:
		opt := status.Options{Deadline: time.Duration(*timeout * float64(time.Second))}
		if *verbose {
			opt.Verbose = stderr
		}
		res, err := status.Run(*uri, opt)
		if err != nil {
			fmt.Fprintf(stderr, "mp280-status: %v\n", err)
			switch {
			case errors.Is(err, status.ErrBusy):
				return 3
			case errors.Is(err, status.ErrNoDevice):
				return 4
			}
			return 1
		}
		text = res.BSCC
	}

	if *raw {
		fmt.Fprintln(stdout, text)
		return 0
	}
	b, err := status.ParseBSCC(text)
	if err != nil {
		fmt.Fprintln(stderr, "mp280-status: status text has no BST field")
		return 1
	}
	switch {
	case *report:
		j, _ := status.Map(b).JSON()
		fmt.Fprintf(stdout, "%s\n", j)
	case *cups:
		for _, l := range status.Map(b).CupsLines() {
			fmt.Fprintln(stdout, l)
		}
	default:
		j, _ := json.MarshalIndent(b.View(), "", "  ")
		fmt.Fprintf(stdout, "%s\n", j)
	}
	return 0
}
