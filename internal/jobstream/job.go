// SPDX-License-Identifier: GPL-2.0-only

package jobstream

import (
	"canon-mp280-driver/internal/printdb"
	"errors"
	"time"
)

// Job writes the non-raster parts of a print job. Typical use:
//
//	j, err := jobstream.NewJob(tables, settings, time.Now())
//	out := j.Begin()                   // XML, SSR, BJL
//	out = append(out, j.StartPage()...) // page header
//	... raster layer bytes ...
//	out = append(out, j.EndPage()...)
//	out = append(out, j.End()...)
type Job struct {
	// IVEC makes Begin and End include the IVEC XML documents. the original front
	// end writes them unless it was started with --bidi (the USB language
	// monitor then adds them itself).
	IVEC bool
	// JobID is the id inside the IVEC documents (DefaultJobID when empty).
	JobID string

	time  time.Time
	setup *PageSetup
	page  int
	state jobState
}

type jobState int

const (
	stateNew jobState = iota
	stateBegun
	stateInPage
	stateEnded
)

// ErrSequence is returned when the calls are made out of order.
var ErrSequence = errors.New("jobstream: call out of sequence")

// NewJob validates the settings against the printer database and prepares a
// job started at the given time. The settings are validated before any output
// exists, so a refused job leaves the stream untouched.
func NewJob(t *printdb.Table, s Settings, start time.Time) (*Job, error) {
	if _, err := SetTimeValue(start); err != nil {
		return nil, err
	}
	ps, err := Resolve(t, s)
	if err != nil {
		return nil, err
	}
	return &Job{IVEC: true, JobID: DefaultJobID, time: start, setup: ps}, nil
}

// Setup returns the resolved page setup (resolution, geometry, raster codes).
func (j *Job) Setup() *PageSetup { return j.setup }

func (j *Job) id() string {
	if j.JobID == "" {
		return DefaultJobID
	}
	return j.JobID
}

// Begin returns the bytes that open the job: the IVEC StartJob and ModeShift
// documents (if IVEC), the SSR definition and the BJL block with the time.
func (j *Job) Begin() ([]byte, error) {
	if j.state != stateNew {
		return nil, ErrSequence
	}
	var out []byte
	if j.IVEC {
		d1, err := StartJobXML(j.id(), false)
		if err != nil {
			return nil, err
		}
		d2, err := ModeShiftXML(j.id())
		if err != nil {
			return nil, err
		}
		out = append(append(out, d1...), d2...)
	}
	out = append(out, SSRDefinition()...)
	blk, err := StartJobBlock(j.time)
	if err != nil {
		return nil, err
	}
	j.state = stateBegun
	return append(out, blk...), nil
}

// StartPage returns the page header. The first page of a job starts with
// ESC ( a, b, q; later pages only repeat the setup commands.
func (j *Job) StartPage() ([]byte, error) {
	if j.state != stateBegun {
		return nil, ErrSequence
	}
	j.page++
	var out []byte
	if j.page == 1 {
		out = pageHeaderPrefix()
	}
	j.state = stateInPage
	return append(out, j.setup.Commands...), nil
}

// EndPage returns the page footer.
func (j *Job) EndPage() ([]byte, error) {
	if j.state != stateInPage {
		return nil, ErrSequence
	}
	j.state = stateBegun
	return PageFooter(), nil
}

// End returns the job footer and, if IVEC, the EndJob document.
func (j *Job) End() ([]byte, error) {
	if j.state != stateBegun {
		return nil, ErrSequence
	}
	out := JobFooter()
	if j.IVEC {
		d, err := EndJobXML(j.id())
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	j.state = stateEnded
	return out, nil
}
