// SPDX-License-Identifier: GPL-2.0-only

//go:build linux

package statuse2e

import (
	"bytes"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"canon-mp280-driver/test/mockprinter"
)

const scenDir = "testdata/scenarios"

func scen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(scenDir, name+".bscc"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const fakeUSB = `#!/bin/sh
# fake stock "usb" backend: records arguments/environment, copies the data, exit code from FAKE_RC
if [ $# = 0 ]; then
  echo 'direct usb://Canon/MP280%20series?serial=AAA111 "Canon MP280 series" "Canon MP280 series USB #1" "MFG:Canon;MDL:MP280 series;"'
  echo 'direct usb://Canon/MG3600%20series?serial=BBB222 "Canon MG3600 series" "Canon MG3600 series USB #1" "MFG:Canon;MDL:MG3600 series;"'
  echo 'direct usb://HP/LaserJet?serial=CCC333 "HP LaserJet" "HP LaserJet USB #1" "MFG:HP;"'
  echo 'direct usb "Unknown" "Universal Printer Class"'
  exit 0
fi
{ echo "argc=$#"; for a in "$@"; do echo "arg=$a"; done; echo "DEVICE_URI=$DEVICE_URI"; echo "argv0=$0"; } > "$FAKE_OUT.args"
cp "$FAKE_MOCKLOG" "$FAKE_OUT.log_at_start" 2>/dev/null
echo "INFO: fake backend running" >&2
if [ -n "${FAKE_SLEEP:-}" ]; then
  trap 'echo got-term > "$FAKE_OUT.term"; kill $SP 2>/dev/null; exit 3' TERM
  sleep "$FAKE_SLEEP" & SP=$!; wait $SP
fi
if [ $# -ge 6 ]; then cat "$6" > "$FAKE_OUT.data"; else cat > "$FAKE_OUT.data"; fi
cp "$FAKE_MOCKLOG" "$FAKE_OUT.log_at_end" 2>/dev/null
echo "INFO: fake backend done" >&2
exit "${FAKE_RC:-0}"
`

const uri = "mp280usb://Canon/MP280%20series?serial=AAA111"

type env struct {
	dir  string
	fake string
	mock *mockprinter.Handle
}

func newEnv(t *testing.T, scenario string) *env {
	t.Helper()
	e := &env{dir: t.TempDir()}
	e.fake = filepath.Join(e.dir, "fakeusb")
	if err := os.WriteFile(e.fake, []byte(fakeUSB), 0o755); err != nil {
		t.Fatal(err)
	}
	e.mock = mockprinter.Start(t, scenario)
	return e
}

func (e *env) out(name string) string { return filepath.Join(e.dir, "o."+name) }

func (e *env) read(t *testing.T, name string) string {
	b, _ := os.ReadFile(e.out(name))
	return string(b)
}

// cmd builds a wrapper command (argv[0] = the device URI, like cupsd does).
func (e *env) cmd(extraEnv []string, args ...string) *exec.Cmd {
	c := exec.Command(filepath.Join(binDir, "mp280-backend"), args...)
	c.Args[0] = uri
	c.Env = append(os.Environ(), "MP280_REAL_BACKEND="+e.fake, "FAKE_OUT="+filepath.Join(e.dir, "o"),
		"FAKE_MOCKLOG="+e.mock.LogPath, "MP280_STATUS_TRANSPORT="+e.mock.Transport, "DEVICE_URI="+uri)
	c.Env = append(c.Env, extraEnv...)
	return c
}

func (e *env) run(t *testing.T, extraEnv []string, stdin []byte, args ...string) (rc int, stdout, stderr string, took time.Duration) {
	t.Helper()
	c := e.cmd(extraEnv, args...)
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	if stdin != nil {
		c.Stdin = bytes.NewReader(stdin)
	}
	start := time.Now()
	err := c.Run()
	took = time.Since(start)
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return rc, so.String(), se.String(), took
}

func TestWrapperDiscovery(t *testing.T) {
	e := newEnv(t, scen(t, "idle"))
	rc, out, _, _ := e.run(t, nil, nil)
	check := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Errorf("%s (stdout %q)", name, out)
		}
	}
	check("discovery exit 0", rc == 0)
	check("lists the MP280 under the new scheme", strings.TrimSpace(out) ==
		`direct mp280usb://Canon/MP280%20series?serial=AAA111 "Canon MP280 series" "Canon MP280 series USB #1 (with ink status)" "MFG:Canon;MDL:MP280 series;"`)
	check("drops other models, vendors and generic lines", strings.Count(out, "\n") == 1)
	_, out, _, _ = e.run(t, []string{"MP280_MODELS=MP280,MG3600"}, nil)
	check("MP280_MODELS widens the list", strings.Count(out, "\n") == 2)
	rc, out, _, _ = e.run(t, []string{"MP280_REAL_BACKEND=/nonexistent"}, nil)
	check("no stock backend: empty list, exit 0", rc == 0 && out == "")
}

func TestWrapperJob(t *testing.T) {
	e := newEnv(t, scen(t, "low_ink_colour"))
	data := make([]byte, 1<<20)
	rand.Read(data)
	rc, _, se, _ := e.run(t, []string{"FAKE_RC=0"}, data, "7", "alice", "my doc", "1", "copies=1")
	if rc != 0 {
		t.Errorf("exit status %d", rc)
	}
	if got, _ := os.ReadFile(e.out("data")); !bytes.Equal(got, data) {
		t.Error("stdin data (1 MiB) not passed unchanged")
	}
	args := e.read(t, "args")
	if got := strings.Join(grep(args, "arg="), "|"); got != "arg=7|arg=alice|arg=my doc|arg=1|arg=copies=1" {
		t.Errorf("stock backend arguments: %q", got)
	}
	if !strings.Contains(args, "DEVICE_URI=usb://Canon/MP280%20series?serial=AAA111\n") {
		t.Errorf("DEVICE_URI not rewritten: %q", args)
	}
	if !strings.Contains(args, "argv0=usb://Canon") && !strings.Contains(args, "argv0=") {
		t.Error("argv0 missing")
	}
	start := strings.Join(reqs(e.read(t, "log_at_start")), " ")
	if start != "StartJob ModeShift EndJob" {
		t.Errorf("pre-job session not complete before the stock backend started: %q", start)
	}
	if n := strings.Count(e.read(t, "log_at_end"), "REQ StartJob"); n != 1 {
		t.Errorf("a session ran while the stock backend was running (%d StartJob at its end)", n)
	}
	if n := strings.Count(strings.Join(e.mock.Log(), "\n"), "REQ StartJob"); n != 2 {
		t.Errorf("post-job session did not run after the backend: %d StartJob", n)
	}
	if n := len(grep(se, "ATTR: marker-colors=")); n != 2 {
		t.Errorf("ATTR lines: %d, want 2 (pre and post)", n)
	}
	if !strings.Contains(se, "STATE: +marker-supply-low-warning") {
		t.Error("no low warning on stderr")
	}
	var order []string
	for _, l := range strings.Split(se, "\n") {
		if strings.HasPrefix(l, "ATTR:") || strings.HasPrefix(l, "INFO: fake backend") {
			order = append(order, l[:min(len(l), 20)])
		}
	}
	if got := strings.Join(order, "|"); got != "ATTR: marker-colors=|INFO: fake backend r|INFO: fake backend d|ATTR: marker-colors=" {
		t.Errorf("stderr order: %q", got)
	}
}

func grep(s, prefix string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func reqs(log string) []string {
	var out []string
	for _, l := range strings.Split(log, "\n") {
		if r, ok := strings.CutPrefix(l, "REQ "); ok {
			out = append(out, strings.Fields(r)[0])
		}
	}
	return out
}

func TestWrapperExitCodes(t *testing.T) {
	e := newEnv(t, scen(t, "idle"))
	data := []byte("some data")
	file := filepath.Join(e.dir, "in")
	os.WriteFile(file, data, 0o644)
	for _, rc := range []string{"1", "4", "5", "6", "7"} {
		got, _, _, _ := e.run(t, []string{"FAKE_RC=" + rc}, nil, "8", "bob", "t", "1", "", file)
		want := map[string]int{"1": 1, "4": 4, "5": 5, "6": 6, "7": 7}[rc]
		if got != want {
			t.Errorf("exit code %s passed through as %d", rc, got)
		}
		if b, _ := os.ReadFile(e.out("data")); !bytes.Equal(b, data) {
			t.Errorf("file argument not read (rc %s)", rc)
		}
	}
}

func TestWrapperDeviceUnreachable(t *testing.T) {
	e := newEnv(t, scen(t, "idle"))
	e.mock.Close()
	data := make([]byte, 4096)
	rand.Read(data)
	rc, _, se, took := e.run(t, []string{"FAKE_RC=0"}, data, "9", "u", "t", "1", "")
	if rc != 0 {
		t.Errorf("exit %d", rc)
	}
	if got, _ := os.ReadFile(e.out("data")); !bytes.Equal(got, data) {
		t.Error("data not intact")
	}
	if strings.Contains(se, "ATTR") || strings.Contains(se, "STATE") || !strings.Contains(se, "DEBUG: mp280usb") {
		t.Errorf("stderr should have DEBUG only: %q", se)
	}
	if took > 2*time.Second {
		t.Errorf("delay %v", took)
	}
}

func TestWrapperSilentPrinterBounded(t *testing.T) {
	e := newEnv(t, "#silent\n"+scen(t, "idle"))
	data := []byte("payload")
	rc, _, _, took := e.run(t, []string{"FAKE_RC=0", "MP280_PRE_TIMEOUT=1", "MP280_POST_TIMEOUT=1"}, data, "9", "u", "t", "1", "")
	if rc != 0 {
		t.Errorf("exit %d", rc)
	}
	if got, _ := os.ReadFile(e.out("data")); !bytes.Equal(got, data) {
		t.Error("data not intact")
	}
	if took > 8*time.Second {
		t.Errorf("total delay not bounded: %v", took)
	}
}

func TestWrapperKillSwitch(t *testing.T) {
	e := newEnv(t, scen(t, "idle"))
	data := []byte("payload")
	_, _, se, _ := e.run(t, []string{"MP280_STATUS_DISABLE=1"}, data, "9", "u", "t", "1", "")
	if len(e.mock.Requests()) != 0 {
		t.Error("session ran although disabled")
	}
	if got, _ := os.ReadFile(e.out("data")); !bytes.Equal(got, data) || strings.Contains(se, "ATTR") {
		t.Error("not a pure pass-through")
	}
}

func TestWrapperProbe(t *testing.T) {
	e := newEnv(t, scen(t, "empty_black"))
	rc, _, se, _ := e.run(t, []string{"FAKE_RC=3"}, nil, "10", "u", "canon-status-probe", "1", "", "/dev/null")
	if rc != 0 {
		t.Errorf("probe exit %d", rc)
	}
	if _, err := os.Stat(e.out("args")); err == nil {
		t.Error("stock backend was started by a probe")
	}
	if n := len(e.mock.Requests()); n != 3 {
		t.Errorf("probe made %d requests, want one session of 3", n)
	}
	if !strings.Contains(se, "STATE: +marker-supply-empty-error") {
		t.Errorf("no empty error: %q", se)
	}
	e.mock.Close()
	if rc, _, _, _ := e.run(t, nil, nil, "11", "u", "canon-status-probe", "1", "", "/dev/null"); rc != 0 {
		t.Errorf("probe with unreachable printer exit %d", rc)
	}
}

func TestWrapperSigtermForwarded(t *testing.T) {
	e := newEnv(t, scen(t, "idle"))
	c := e.cmd([]string{"FAKE_SLEEP=30", "FAKE_RC=0"}, "12", "u", "t", "1", "")
	c.Stdin = bytes.NewReader(nil)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(e.out("args")); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	c.Process.Signal(syscall.SIGTERM)
	err := c.Wait()
	took := time.Since(start)
	rc := -1
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err == nil {
		rc = 0
	}
	if _, serr := os.Stat(e.out("term")); serr != nil || rc != 3 {
		t.Errorf("SIGTERM not forwarded (term file err %v, exit %d)", serr, rc)
	}
	if n := strings.Count(strings.Join(e.mock.Log(), "\n"), "REQ StartJob"); n != 1 {
		t.Errorf("post-job query after a cancel: %d StartJob", n)
	}
	if took > 3*time.Second {
		t.Errorf("cancel not prompt: %v", took)
	}
}

var _ = mockprinter.Start
