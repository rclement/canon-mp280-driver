// SPDX-License-Identifier: GPL-2.0-only

package cif

import (
	"errors"
	"os"
	"path/filepath"
)

// systemDir is where distro packages and `make install-data` put the tables.
// It is a variable so packagers can set it at link time:
//
//	go build -ldflags "-X canon-mp280-driver/internal/cif.systemDir=/opt/x/tables"
var systemDir = "/usr/share/canon-mp280/tables"

const probeFile = "cnb_3700.tbl"

// findTableDir returns the tables directory: $MP280_TABLE_DIR if set, else systemDir,
// else a per-user fallback ($XDG_DATA_HOME or ~/.local/share)/canon-mp280/tables.
func findTableDir() (string, error) {
	if d := os.Getenv("MP280_TABLE_DIR"); d != "" {
		return d, nil
	}
	cands := []string{systemDir}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		cands = append(cands, filepath.Join(x, "canon-mp280", "tables"))
	} else if h, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(h, ".local", "share", "canon-mp280", "tables"))
	}
	for _, d := range cands {
		if _, err := os.Stat(filepath.Join(d, probeFile)); err == nil {
			return d, nil
		}
	}
	return "", errors.New("Canon data tables not found: set MP280_TABLE_DIR or install them to " + systemDir)
}
