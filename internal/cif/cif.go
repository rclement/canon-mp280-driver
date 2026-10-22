// SPDX-License-Identifier: GPL-2.0-only

// Package cif is the image-to-printer-stream front end: the Go equivalent of
// the original front end. It is shared by cmd/mp280-cif and the filter.
package cif

import (
	"errors"
	"io"
	"os"
	"time"

	"canon-mp280-driver/internal/image"
	"canon-mp280-driver/internal/layout"
	"canon-mp280-driver/internal/options"
	"canon-mp280-driver/internal/pipeline"
)

// Run is the cif-equivalent: cif-style options on args, a PPM/BMP/PNG/TIFF image on
// stdin (or the file named by the options), the printer stream on stdout.
//
// Like the original front end it returns nil and writes nothing when the options are
// refused, and still closes the job when the image cannot be read.
func Run(args []string, stdin io.Reader, stdout io.Writer) error {
	err := RunChecked(args, stdin, stdout)
	var refused *Refused
	if errors.As(err, &refused) {
		return nil
	}
	return err
}

// Refused reports that the front end refused the job (bad option list, or an
// option combination the printer cannot print). the original front end exits 0 with no
// output then; Run keeps that, RunChecked lets the caller see it.
type Refused struct{ Reason string }

func (r *Refused) Error() string { return r.Reason }

// RunChecked is Run, except that a refused job is returned as *Refused.
func RunChecked(args []string, stdin io.Reader, stdout io.Writer) error {
	dir, err := findTableDir()
	if err != nil {
		return &SetupError{err}
	}
	db, s, err := options.Configure(dir, "MP280", args)
	if err != nil {
		var rej *options.Rejection
		if errors.Is(err, options.ErrNoJob) {
			return &Refused{Reason: "no job requested"}
		}
		if errors.As(err, &rej) {
			return &Refused{Reason: rej.Reason}
		}
		return err
	}
	job, err := s.NewJob(db, time.Now())
	if err != nil {
		return &Refused{Reason: err.Error()} // refused by the job interface: no usable stream is written
	}
	com, err := db.ComSystem(s.Device)
	if err != nil {
		return err
	}
	printer, err := pipeline.New(pipeline.Config{
		Database: db.Tables(), Job: job, Settings: s.JobSettings(com),
		Copies: s.Copies, Collate: s.Collate, ReversePrint: s.RevPrint,
	})
	if err != nil {
		return err
	}

	in := stdin
	if s.File != "" {
		f, err := os.Open(s.File)
		if err != nil {
			in = nil
		} else {
			defer f.Close()
			in = f
		}
	}
	var doc *image.Document
	opened := false
	next := func() (pipeline.PageSource, error) {
		if !opened {
			opened = true
			if in == nil {
				return nil, errors.New("cannot open input")
			}
			var err error
			if s.File == "" {
				doc, err = image.OpenStdin(in)
			} else {
				doc, err = image.Open(in)
			}
			if err != nil {
				return nil, err
			}
		}
		img, err := doc.NextPage()
		if err != nil {
			return nil, err
		}
		if img == nil {
			return nil, io.EOF
		}
		plan, err := layout.Compute(layout.InputFor(s, db, com, img.Info))
		if err != nil {
			return nil, err
		}
		return layout.NewPage(img, plan, layout.MirrorsMedia(s.Device.Media))
	}
	return printer.Print(stdout, next)
}

// SetupError reports an installation problem (for example missing data
// tables) as opposed to a problem with the job's options or image. the original
// front end exits 0 for the latter, so callers that mimic it exit non-zero
// only for a SetupError.
type SetupError struct{ Err error }

func (e *SetupError) Error() string { return e.Err.Error() }
func (e *SetupError) Unwrap() error { return e.Err }
