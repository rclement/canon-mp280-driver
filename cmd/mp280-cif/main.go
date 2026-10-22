// SPDX-License-Identifier: GPL-2.0-only

// Command mp280-cif converts an image (PPM, BMP, PNG or TIFF) on standard
// input into a Canon PIXMA MP280 printer stream on standard output. It takes
// the same options as the original front end and is mostly used for tests and
// debugging; the CUPS filter calls the same code in-process.
//
// Needs Canon's data tables (see MP280_TABLE_DIR and internal/cif).
// MP280_CPUPROFILE=<file> writes a CPU profile.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/pprof"

	"canon-mp280-driver/internal/cif"
)

func main() { os.Exit(realMain()) }

func realMain() (code int) {
	// A bug must never kill the spooler with a stack trace.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "mp280-cif: internal error:", r)
			code = 1
		}
	}()
	if f := os.Getenv("MP280_CPUPROFILE"); f != "" {
		if out, err := os.Create(f); err == nil {
			_ = pprof.StartCPUProfile(out)
			defer pprof.StopCPUProfile()
		}
	}
	if err := cif.Run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		// Like the original front end, a bad option or unreadable image is not a failure of the
		// tool itself: exit 0 (the stream, if any, is already complete).
		var setup *cif.SetupError
		if errors.As(err, &setup) {
			return 1
		}
	}
	return 0
}
