// SPDX-License-Identifier: GPL-2.0-only

// Package statuse2e tests the built binaries mp280-backend and mp280-status
// against the software mock printer, a fake stock usb backend, and (e2e) a
// private cupsd. Nothing here touches the USB bus or a real printer.
package statuse2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mp280bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binDir = dir
	for name, pkg := range map[string]string{
		"mp280-backend": "canon-mp280-driver/cmd/mp280-backend",
		"mp280-status":  "canon-mp280-driver/cmd/mp280-status",
		"mock-printer":  "canon-mp280-driver/test/mockprinter/cmd/mock-printer",
	} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), pkg)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", pkg, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
