// SPDX-License-Identifier: GPL-2.0-only

package mockprinter

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var socketCounter atomic.Int64

// Handle is a running mock for tests.
type Handle struct {
	Transport string // value for MP280_STATUS_TRANSPORT
	Scenario  string // path of the scenario file
	LogPath   string // file the events are also appended to
	l         net.Listener
	mu        sync.Mutex
	log       []string
}

// Start runs a mock printer on a fresh abstract unix socket with the given
// scenario file content (directives and BSCC text) until the test ends.
func Start(t testing.TB, scenario string) *Handle {
	t.Helper()
	dir := t.TempDir()
	h := &Handle{Scenario: filepath.Join(dir, "scenario"), LogPath: filepath.Join(dir, "mock.log")}
	os.WriteFile(h.LogPath, nil, 0o644)
	h.Set(scenario)
	name := fmt.Sprintf("@mp280-mock-%d-%d", os.Getpid(), socketCounter.Add(1))
	l, err := net.Listen("unix", name)
	if err != nil {
		t.Fatal(err)
	}
	h.l = l
	h.Transport = "unix:" + name
	srv := &Server{ScenarioPath: h.Scenario, Interval: 5 * time.Millisecond, Log: func(s string) {
		h.mu.Lock()
		h.log = append(h.log, s)
		if f, err := os.OpenFile(h.LogPath, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(f, s)
			f.Close()
		}
		h.mu.Unlock()
	}}
	go srv.Serve(l)
	t.Cleanup(func() { l.Close() })
	return h
}

// Set replaces the scenario file content (the mock sees it at once).
func (h *Handle) Set(content string) {
	tmp := h.Scenario + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err == nil {
		os.Rename(tmp, h.Scenario)
	}
}

// Log returns the events so far.
func (h *Handle) Log() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.log...)
}

// Requests returns only the REQ events, as "StartJob ModeShift EndJob ...".
func (h *Handle) Requests() []string {
	var out []string
	for _, l := range h.Log() {
		if f, ok := strings.CutPrefix(l, "REQ "); ok {
			out = append(out, strings.Fields(f)[0])
		}
	}
	return out
}

// Close stops the mock; later connections are refused.
func (h *Handle) Close() { h.l.Close() }
