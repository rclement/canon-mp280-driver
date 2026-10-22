// SPDX-License-Identifier: GPL-2.0-only

package cupsbackend

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnvRealBackend overrides the location of the stock usb backend.
const EnvRealBackend = "MP280_REAL_BACKEND"

// Well-known CUPS server-bin directories searched last.
var wellKnownServerbins = []string{"/usr/lib/cups", "/usr/libexec/cups", "/usr/local/lib/cups"}

// FindStockUSB locates CUPS' stock `usb` backend. Order: $MP280_REAL_BACKEND;
// `usb` next to the running executable (both live in the CUPS serverbin's
// backend directory) unless that is the executable itself; `cups-config
// --serverbin`; well-known directories.
func FindStockUSB() (string, error) {
	if p := os.Getenv(EnvRealBackend); p != "" {
		return p, nil
	}
	self, _ := os.Executable()
	if self != "" {
		self, _ = filepath.EvalSymlinks(self)
	}
	var cands []string
	if self != "" {
		cands = append(cands, filepath.Join(filepath.Dir(self), "usb"))
	}
	if out, err := exec.Command("cups-config", "--serverbin").Output(); err == nil {
		if d := strings.TrimSpace(string(bytes.TrimSpace(out))); d != "" {
			cands = append(cands, filepath.Join(d, "backend", "usb"))
		}
	}
	for _, d := range wellKnownServerbins {
		cands = append(cands, filepath.Join(d, "backend", "usb"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			if self != "" {
				if real, err := filepath.EvalSymlinks(c); err == nil && real == self {
					continue
				}
			}
			return c, nil
		}
	}
	return "", errors.New("cannot find CUPS' stock usb backend (set " + EnvRealBackend + ")")
}
