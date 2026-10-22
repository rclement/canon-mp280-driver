// SPDX-License-Identifier: GPL-2.0-only

//go:build linux

package statuse2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestE2EPrivateCupsd runs e2e/e2e.sh: a private cupsd in a user+mount
// namespace (nothing outside is touched), a queue whose backend is the
// wrapper, and the mock printer. It is skipped where cupsd/ipptool/unshare
// are missing or namespaces are not permitted.
func TestE2EPrivateCupsd(t *testing.T) {
	for _, tool := range []string{"cupsd", "ipptool", "lpadmin", "lp", "lpstat", "unshare", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not in PATH", tool)
		}
	}
	if err := exec.Command("unshare", "-rm", "true").Run(); err != nil {
		t.Skipf("unshare -rm not permitted: %v", err)
	}
	scenarios, _ := filepath.Abs(scenDir)
	script, _ := filepath.Abs("e2e/e2e.sh")
	c := exec.Command("bash", script)
	c.Env = append(os.Environ(), "BUILD="+binDir, "SCENARIOS="+scenarios)
	out, err := c.CombinedOutput()
	t.Logf("%s", out)
	m := regexp.MustCompile(`-- (\d+) checks, (\d+) failures`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("e2e did not finish: %v", err)
	}
	n, _ := strconv.Atoi(string(m[1]))
	if string(m[2]) != "0" || err != nil {
		t.Fatalf("e2e failed: %s failures (%v)", m[2], err)
	}
	if n < 27 {
		t.Errorf("only %d e2e checks ran, expected 27", n)
	}
}
