// SPDX-License-Identifier: GPL-2.0-only

//go:build linux

package statuse2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"canon-mp280-driver/test/mockprinter"
)

func cli(t *testing.T, env []string, args ...string) (int, string, string) {
	t.Helper()
	c := exec.Command(filepath.Join(binDir, "mp280-status"), args...)
	c.Env = append(os.Environ(), env...)
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	rc := 0
	if err := c.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		rc = ee.ExitCode()
	}
	return rc, so.String(), se.String()
}

func TestCLIParseOffline(t *testing.T) {
	rc, out, _ := cli(t, nil, "-parse", "testdata/mp280_idle_getstatusresponse.xml")
	if rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	var got, want any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile("testdata/mp280_idle_expected.json")
	json.Unmarshal(b, &want)
	gb, _ := json.Marshal(got)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Errorf("JSON differs from the conformance expectation:\n%s\n%s", gb, wb)
	}
	rc, out, _ = cli(t, nil, "-parse", "testdata/mp280_idle_getstatusresponse.xml", "-raw")
	if !strings.HasPrefix(out, "BST:00;PID:") || rc != 0 {
		t.Errorf("raw: %q", out)
	}
	_, out, _ = cli(t, nil, "-parse", "testdata/cases/low_ink_colour.xml", "-cups")
	if !strings.Contains(out, "marker-levels=70,10") || !strings.Contains(out, "STATE: +marker-supply-low-warning") {
		t.Errorf("cups: %q", out)
	}
	_, out, _ = cli(t, nil, "-parse-bscc", "BST:08;CHD:CLM,BKM;HRI:EU;CIR:CL=100,BK=100;DOC:4,01,1000;", "-report")
	if !strings.Contains(out, `"media-empty-error"`) {
		t.Errorf("report: %q", out)
	}
	if rc, _, se := cli(t, nil, "-parse", "testdata/scenarios/idle.bscc"); rc != 1 || !strings.Contains(se, "no status") {
		t.Errorf("garbage input: rc %d %q", rc, se)
	}
	if rc, _, _ := cli(t, nil, "-bogus"); rc != 2 {
		t.Errorf("usage rc %d", rc)
	}
}

func TestCLILive(t *testing.T) {
	h := mockprinter.Start(t, scen(t, "low_ink_colour"))
	rc, out, _ := cli(t, []string{"MP280_STATUS_TRANSPORT=" + h.Transport}, "-cups")
	if rc != 0 || !strings.Contains(out, "marker-levels=70,10") {
		t.Errorf("rc %d out %q", rc, out)
	}
	h.Close()
	if rc, _, _ := cli(t, []string{"MP280_STATUS_TRANSPORT=" + h.Transport}, "-cups"); rc != 4 {
		t.Errorf("no device: rc %d, want 4", rc)
	}
	h2 := mockprinter.Start(t, "#silent\n"+scen(t, "idle"))
	if rc, _, _ := cli(t, []string{"MP280_STATUS_TRANSPORT=" + h2.Transport}, "-timeout", "1"); rc != 1 {
		t.Errorf("silent: rc %d, want 1", rc)
	}
}
