// SPDX-License-Identifier: GPL-2.0-only

// Package cupsbackend implements the parts of the CUPS backend protocol a
// wrapper backend needs: argument and environment conventions, the ATTR:,
// STATE:, DEBUG: and ERROR: lines on stderr, device URIs, locating and
// running CUPS' stock `usb` backend as a child with signal forwarding, and
// filtering its discovery output.
//
// See backend(7): a backend is run with no arguments for discovery (one
// "direct URI "make-model" "info" "device-id"" line per device on stdout) and
// with job-id user title copies options [file] for a job.
package cupsbackend

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Exit codes of a backend (cups/backend.h).
const (
	ExitOK           = 0
	ExitFailed       = 1
	ExitAuthRequired = 2
	ExitHold         = 3
	ExitStop         = 4
	ExitCancel       = 5
	ExitRetry        = 6
	ExitRetryCurrent = 7
)

// JobArgs are the arguments cupsd passes to a backend for a job.
type JobArgs struct {
	JobID, User, Title, Copies, Options string
	File                                string // "" when the data comes on stdin
}

// ErrUsage is returned by ParseArgs for a wrong argument count.
var ErrUsage = errors.New("usage: backend job-id user title copies options [file]")

// ParseArgs parses os.Args[1:] of a job invocation (6 or 7 arguments in
// argv, i.e. 5 or 6 after the program name).
func ParseArgs(args []string) (JobArgs, error) {
	if len(args) != 5 && len(args) != 6 {
		return JobArgs{}, ErrUsage
	}
	j := JobArgs{JobID: args[0], User: args[1], Title: args[2], Copies: args[3], Options: args[4]}
	if len(args) == 6 {
		j.File = args[5]
	}
	return j, nil
}

// Log writes backend protocol lines to stderr. Every line is a single Write
// call, so lines of a child sharing stderr are never interleaved mid-line.
type Log struct {
	mu sync.Mutex
	W  io.Writer
}

// Stderr is the log on the process' standard error.
func Stderr() *Log { return &Log{W: os.Stderr} }

// Line writes one raw protocol line (a newline is appended).
func (l *Log) Line(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = io.WriteString(l.W, strings.TrimRight(s, "\n")+"\n")
}

// Debug writes "DEBUG: ..." (visible in cupsd's error_log at LogLevel debug).
func (l *Log) Debug(format string, a ...any) { l.Line("DEBUG: " + fmt.Sprintf(format, a...)) }

// Error writes "ERROR: ...".
func (l *Log) Error(format string, a ...any) { l.Line("ERROR: " + fmt.Sprintf(format, a...)) }
