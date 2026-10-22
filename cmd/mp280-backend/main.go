// SPDX-License-Identifier: GPL-2.0-only

// Command mp280-backend is a CUPS backend that wraps CUPS' own `usb` backend
// and adds ink-level and printer-state reporting for the Canon PIXMA MP280.
// It is installed as `mp280usb`.
//
//	discovery (no arguments)  runs `usb`, keeps the Canon MP280 devices and
//	                          lists them again with the scheme mp280usb:
//	job (5 or 6 arguments)    1. status query -> stderr "ATTR: marker-..." and
//	                             "STATE: +reason" lines (bounded, never blocks
//	                             the job)
//	                          2. runs the stock `usb` backend as a child with
//	                             the same arguments, environment (DEVICE_URI
//	                             rewritten to usb:...), stdin, back/side
//	                             channel descriptors; forwards SIGTERM/SIGINT/
//	                             SIGHUP
//	                          3. status query again after the child exited
//	                          4. exits with the child's exit status (death by
//	                             signal is re-raised)
//	probe job                 title "canon-status-probe": only the status
//	                          query; the stock backend is not started; exit 0
//	                          whatever happens (a probe never stops the queue)
//
// The status query and the stock backend never run at the same time: the USB
// interface is claimed exclusively. A failing query (printer off, busy, no
// permission, protocol) is only logged at DEBUG level and never changes the
// print job.
//
// Environment (all optional; cupsd passes only what is set with SetEnv):
//
//	MP280_REAL_BACKEND         path of the stock usb backend
//	MP280_STATUS_TRANSPORT     unix:/socket = software mock instead of USB (tests)
//	MP280_PRE_TIMEOUT, MP280_POST_TIMEOUT, MP280_PROBE_TIMEOUT   seconds (5 / 12 / 20)
//	MP280_STATUS_DISABLE=1     behave exactly like the stock backend
//	MP280_MODELS               comma list of model names to list at discovery (default MP280)
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"canon-mp280-driver/internal/cupsbackend"
	"canon-mp280-driver/internal/status"
)

// ProbeTitle is the job title of a status-only job.
const ProbeTitle = "canon-status-probe"

func envSeconds(name string, def time.Duration) time.Duration {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v > 0 {
		return time.Duration(v * float64(time.Second))
	}
	return def
}

type wrapper struct {
	log *cupsbackend.Log
	uri string // usb:... URI the stock backend and the status code use
	tmo struct{ pre, post, probe time.Duration }
}

// query runs one status session and maps it. err is non-nil on any failure
// (already logged at DEBUG level).
func (w *wrapper) query(timeout time.Duration) (status.Report, *status.Result, error) {
	res, err := status.Run(w.uri, status.Options{Deadline: timeout, EndGrace: 3 * time.Second})
	if err != nil {
		w.log.Debug("mp280usb: %v", err)
		return status.Report{}, res, err
	}
	b, err := status.ParseBSCC(res.BSCC)
	if err != nil {
		w.log.Debug("mp280usb: status text without BST field")
		return status.Report{}, res, err
	}
	return status.Map(b), res, nil
}

// emit prints the ATTR line, then one STATE line per managed keyword (one
// write each) and a DEBUG summary.
func (w *wrapper) emit(r status.Report, when string, took time.Duration) {
	for _, l := range r.CupsLines() {
		w.log.Line(l)
	}
	w.log.Debug("mp280usb: %s status in %.2fs: %s, black %d%%, colour %d%%, reasons %v",
		when, took.Seconds(), r.Activity, r.Markers[0].Level, r.Markers[1].Level, r.Reasons.Names())
}

func main() { os.Exit(run(os.Args)) }

func run(argv []string) int {
	log := cupsbackend.Stderr()
	real, realErr := cupsbackend.FindStockUSB()
	if len(argv) == 1 {
		return discovery(real, realErr)
	}
	job, err := cupsbackend.ParseArgs(argv[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Usage: %s job-id user title copies options [file]\n", argv[0])
		return cupsbackend.ExitFailed
	}
	uri := os.Getenv("DEVICE_URI")
	if uri == "" {
		uri = argv[0]
	}
	realURI, err := cupsbackend.SwapScheme(uri, cupsbackend.Scheme, cupsbackend.StockScheme)
	if err != nil {
		log.Error("mp280usb: %v", err)
		return cupsbackend.ExitFailed
	}
	w := &wrapper{log: log, uri: realURI}
	w.tmo.pre = envSeconds("MP280_PRE_TIMEOUT", 5*time.Second)
	w.tmo.post = envSeconds("MP280_POST_TIMEOUT", 12*time.Second)
	w.tmo.probe = envSeconds("MP280_PROBE_TIMEOUT", 20*time.Second)
	disabled := os.Getenv("MP280_STATUS_DISABLE") == "1"

	if !disabled && job.Title == ProbeTitle {
		t0 := time.Now()
		if r, _, err := w.query(w.tmo.probe); err == nil {
			w.emit(r, "probe", time.Since(t0))
		}
		return cupsbackend.ExitOK // a probe never fails the queue
	}

	if !disabled {
		t0 := time.Now()
		if r, _, err := w.query(w.tmo.pre); err == nil {
			w.emit(r, "pre-job", time.Since(t0))
		}
	}

	if realErr != nil {
		log.Error("mp280usb: %v", realErr)
		return cupsbackend.ExitFailed
	}
	out, err := cupsbackend.RunStock(real, realURI, argv[1:], nil)
	if err != nil {
		log.Error("mp280usb: cannot execute %s: %v", real, err)
		return cupsbackend.ExitFailed
	}

	if !disabled && out.Got == nil {
		w.postJob(realURI)
	}
	if out.Signal != 0 {
		cupsbackend.Reraise(out.Signal)
	}
	return out.ExitCode
}

// postJob asks again after the job: the printer may still be finishing the
// last page (status "printing"/"busy"), so it retries once a second until
// idle, within the budget.
func (w *wrapper) postJob(realURI string) {
	t0 := time.Now()
	budget := w.tmo.post
	var last status.Report
	have := false
	for time.Since(t0) < budget {
		left := budget - time.Since(t0)
		if left > 6*time.Second {
			left = 6 * time.Second
		}
		r, _, err := w.query(left)
		if err == nil {
			last, have = r, true
			if r.Activity == "idle" {
				break
			}
		} else if errors.Is(err, status.ErrNoDevice) {
			break // printer gone / mock gone: retrying will not help
		}
		time.Sleep(time.Second)
	}
	if have {
		w.emit(last, "post-job", time.Since(t0))
	}
}

func discovery(real string, realErr error) int {
	if realErr != nil {
		return cupsbackend.ExitOK
	}
	models := []string{"MP280"}
	if v := os.Getenv("MP280_MODELS"); v != "" {
		models = strings.Split(v, ",")
	}
	_ = cupsbackend.ListStock(real, os.Stdout, models)
	return cupsbackend.ExitOK
}
