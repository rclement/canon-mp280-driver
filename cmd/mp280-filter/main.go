// SPDX-License-Identifier: GPL-2.0-only

// Command mp280-filter is the CUPS filter for the Canon PIXMA MP280: it takes
// PostScript (stdin or file) and writes the printer stream. Install it in
// CUPS's serverbin; the PPD names it as *cupsFilter.
//
//	mp280-filter job user title copies options [file]     env: PPD, DEVICE_URI
//
// Needs Ghostscript ($MP280_GS, PATH or a common location) and Canon's data
// tables (MP280_TABLE_DIR, see internal/cif).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"canon-mp280-driver/internal/filter"
)

func main() { os.Exit(realMain()) }

func realMain() (code int) {
	// A bug must never kill the spooler with a stack trace.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "ERROR: mp280-filter: internal error:", r)
			code = 1
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	return filter.Run(ctx, os.Args[1:], filter.EnvFromOS(), os.Stdin, os.Stdout, os.Stderr)
}
