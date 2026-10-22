// SPDX-License-Identifier: GPL-2.0-only

package status

import (
	"errors"
	"fmt"
	"io"
	"time"

	"canon-mp280-driver/internal/jobstream"
)

// Transport is the byte channel to the printer: the bulk endpoints of the USB
// printer-class interface, or a unix socket to the software mock.
type Transport interface {
	// Write sends all of p within timeout.
	Write(p []byte, timeout time.Duration) error
	// Read waits at most timeout for data. It returns (0, nil) on timeout and
	// an error when the channel is gone.
	Read(p []byte, timeout time.Duration) (int, error)
}

// Errors of a status session. All are wrapped in *SessionError.
var (
	ErrTimeout = errors.New("no (valid) answer before the deadline")
	ErrIO      = errors.New("transport error")
	ErrProto   = errors.New("protocol error")
)

// SessionError says at which stage the session failed.
type SessionError struct {
	Stage string // "startjob", "modeshift", "status" or "endjob"
	Err   error
}

func (e *SessionError) Error() string {
	return fmt.Sprintf("status query failed at stage %s: %v", e.Stage, e.Err)
}
func (e *SessionError) Unwrap() error { return e.Err }

// Options tune one session.
type Options struct {
	// Deadline bounds the whole session apart from the final EndJob wait.
	Deadline time.Duration
	// EndGrace is how long to wait for EndJobResponse after sending EndJob
	// (default 5 s). The real printer interleaves ~150 status documents
	// before the answer.
	EndGrace time.Duration
	// Verbose, if set, receives a trace of the bytes exchanged.
	Verbose io.Writer
	// Now is the clock (tests); time.Now by default.
	Now func() time.Time
}

// Result is the outcome of a session.
type Result struct {
	BSCC        string        // status text of the first GetStatusResponse
	JobID       string        // job id of the session (echoed by the printer)
	Started     bool          // StartJob was accepted
	Ended       bool          // EndJobResponse was seen
	StatusReads int           // number of reads that produced status
	Elapsed     time.Duration //
	Stage       string        // last stage reached, for diagnostics
}

const (
	defaultJobID = jobstream.DefaultJobID
	readChunk    = 4096
	writeTimeout = 3 * time.Second
	pollSlice    = 300 * time.Millisecond
	staleDrain   = 50 * time.Millisecond
)

type session struct {
	tr   Transport
	opt  Options
	acc  []byte
	read []byte
}

func (s *session) now() time.Time {
	if s.opt.Now != nil {
		return s.opt.Now()
	}
	return time.Now()
}

func (s *session) trace(dir string, p []byte) {
	if s.opt.Verbose != nil {
		fmt.Fprintf(s.opt.Verbose, "%s %s\n", dir, p)
	}
}

func (s *session) send(doc []byte) error {
	s.trace(">>", doc)
	return s.tr.Write(doc, writeTimeout)
}

// fill reads once, waiting at most wait, and appends to the buffer.
func (s *session) fill(wait time.Duration) (int, error) {
	n, err := s.tr.Read(s.read, wait)
	if err != nil || n <= 0 {
		return n, err
	}
	s.trace("<<", s.read[:n])
	if len(s.acc)+n > maxBufferedBytes { // resync: drop old junk
		s.acc = s.acc[:0]
	}
	s.acc = append(s.acc, s.read[:n]...)
	return n, nil
}

// await keeps examining and reading until check succeeds or the deadline
// passes.
func (s *session) await(until time.Time, check func() bool) error {
	for {
		if len(s.acc) > 0 && check() {
			return nil
		}
		s.acc = dropSeen(s.acc)
		left := until.Sub(s.now())
		if left <= 0 {
			return ErrTimeout
		}
		if left > pollSlice {
			left = pollSlice
		}
		if _, err := s.fill(left); err != nil {
			return fmt.Errorf("%w: %v", ErrIO, err)
		}
	}
}

// Query runs the IVEC status session of the printer:
//
//	StartJob (bidi=1) -> ModeShift -> GetStatusResponse stream -> EndJob
//
// VERIFIED on a real MP280: the printer echoes the host's job id, streams one
// GetStatusResponse per read (hundreds per session) and needs no wake-up
// bytes. It always sends EndJob when the printer accepted StartJob, so no job
// stays open. A session that fails returns a *SessionError and a Result that
// still tells how far it got.
func Query(tr Transport, opt Options) (*Result, error) {
	if opt.EndGrace <= 0 {
		opt.EndGrace = 5 * time.Second
	}
	s := &session{tr: tr, opt: opt, read: make([]byte, readChunk)}
	res := &Result{JobID: defaultJobID, Stage: "startjob"}
	t0 := s.now()
	until := t0.Add(opt.Deadline)
	fail := func(err error) (*Result, error) {
		res.Elapsed = s.now().Sub(t0)
		return res, &SessionError{Stage: res.Stage, Err: err}
	}

	// Drop stale data left in the printer's output buffer by an earlier session.
	for i := 0; i < 3; i++ {
		if n, _ := s.fill(staleDrain); n <= 0 {
			break
		}
	}
	s.acc = s.acc[:0]

	start, err := jobstream.StartJobXML(defaultJobID, true)
	if err != nil {
		return fail(fmt.Errorf("%w: %v", ErrProto, err))
	}
	if err := s.send(start); err != nil {
		return fail(fmt.Errorf("%w: %v", ErrIO, err))
	}
	if err := s.await(until, func() bool {
		id, ok := matchReply(s.acc, replyStartJob, defaultJobID)
		if ok {
			res.JobID = id
			s.acc = s.acc[:0]
		}
		return ok
	}); err != nil {
		return fail(err)
	}
	res.Started = true

	// From here on EndJob is sent whatever happens.
	var qerr error
	func() {
		res.Stage = "modeshift"
		doc, err := jobstream.ModeShiftXML(res.JobID)
		if err != nil {
			qerr = fmt.Errorf("%w: %v", ErrProto, err)
			return
		}
		if err := s.send(doc); err != nil {
			qerr = fmt.Errorf("%w: %v", ErrIO, err)
			return
		}
		// The status XML can already follow the answer in the same read, so
		// the buffer is not discarded here.
		if qerr = s.await(until, func() bool {
			_, ok := matchReply(s.acc, replyModeShift, res.JobID)
			return ok
		}); qerr != nil {
			return
		}
		res.Stage = "status"
		qerr = s.await(until, func() bool {
			text, ok := ExtractStatus(s.acc)
			if ok {
				res.BSCC, res.StatusReads = text, res.StatusReads+1
			}
			return ok
		})
	}()

	failedStage := res.Stage
	res.Stage = "endjob"
	if doc, err := jobstream.EndJobXML(res.JobID); err == nil && s.send(doc) == nil {
		// Status documents queued before the answer are skipped.
		s.acc = s.acc[:0]
		if s.await(s.now().Add(opt.EndGrace), func() bool {
			_, ok := matchReply(s.acc, replyEndJob, res.JobID)
			return ok
		}) == nil {
			res.Ended = true
		}
	}
	res.Elapsed = s.now().Sub(t0)
	if qerr != nil {
		res.Stage = failedStage
		return res, &SessionError{Stage: failedStage, Err: qerr}
	}
	return res, nil
}
