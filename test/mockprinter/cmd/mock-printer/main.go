// SPDX-License-Identifier: GPL-2.0-only

// Command mock-printer runs the software MP280 simulator on a unix socket.
//
//	mock-printer SOCKET SCENARIO_FILE [-interval-ms N] [-log FILE]
//
// SOCKET starting with '@' is a Linux abstract socket.
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"canon-mp280-driver/test/mockprinter"
)

func main() {
	interval := flag.Int("interval-ms", 20, "milliseconds between status documents")
	logPath := flag.String("log", "", "append event log here")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: mock-printer [-interval-ms N] [-log FILE] SOCKET SCENARIO")
		os.Exit(2)
	}
	sock, scen := flag.Arg(0), flag.Arg(1)
	if sock[0] != '@' {
		os.Remove(sock)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mock-printer:", err)
		os.Exit(1)
	}
	srv := &mockprinter.Server{ScenarioPath: scen, Interval: time.Duration(*interval) * time.Millisecond}
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			srv.Log = func(s string) { fmt.Fprintln(f, s) }
		}
	}
	fmt.Fprintf(os.Stderr, "mock-printer: listening on %s\n", sock)
	srv.Serve(l)
}
