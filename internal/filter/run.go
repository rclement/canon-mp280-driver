// SPDX-License-Identifier: GPL-2.0-only

package filter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"canon-mp280-driver/internal/cif"
)

// Env is the process environment the filter needs.
type Env struct {
	PPD         string // $PPD
	DeviceURI   string // $DEVICE_URI
	Ghostscript string // $MP280_GS (optional)
}

// EnvFromOS reads the environment.
func EnvFromOS() Env {
	return Env{PPD: os.Getenv("PPD"), DeviceURI: os.Getenv("DEVICE_URI"), Ghostscript: os.Getenv("MP280_GS")}
}

// FindGhostscript resolves the gs binary: $MP280_GS, then PATH, then common
// install locations.
func FindGhostscript(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if p, err := exec.LookPath("gs"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/usr/bin/gs", "/usr/local/bin/gs", "/opt/homebrew/bin/gs",
		"/opt/local/bin/gs", "/run/current-system/sw/bin/gs"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("Ghostscript (gs) not found; set MP280_GS")
}

// Run is the CUPS filter. args is argv[1:] (job user title copies options
// [file]); it returns the exit status. Messages go to stderr with CUPS
// prefixes. Deliberate deviation from the original driver: a job the front end refuses
// ends with an ERROR line and status 1 instead of a silent status 0.
func Run(ctx context.Context, args []string, env Env, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 5 || len(args) > 6 {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: illegal parameter number (job user title copies options [file])")
		return 1
	}
	opts := ParseOptions(args[4])
	in := stdin
	if len(args) == 6 {
		f, err := os.Open(args[5])
		if err != nil {
			fmt.Fprintln(stderr, "ERROR: mp280-filter: can't open file", args[5])
			return 1
		}
		defer f.Close()
		in = f
	}
	if env.PPD == "" {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: $PPD is not set")
		return 1
	}
	pf, err := OpenPPD(env.PPD)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: can't read PPD:", err)
		return 1
	}
	hdr, err := ScanHeader(in)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: read error:", err)
		return 1
	}
	plan, err := Build(&Job{PPD: pf, Options: opts, PSParams: hdr.Params, DeviceURI: env.DeviceURI})
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: can't make parameter:", err)
		return 1
	}
	fmt.Fprintf(stderr, "DEBUG: mp280-filter: cif %s (gs %dx%d at %d dpi)\n",
		strings.Join(plan.CifArgs, " "), plan.Width, plan.Height, plan.Resolution)

	gs, err := FindGhostscript(env.Ghostscript)
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter:", err)
		return 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, gs,
		fmt.Sprintf("-r%d", plan.Resolution), fmt.Sprintf("-g%dx%d", plan.Width, plan.Height),
		"-q", "-dNOPROMPT", "-dSAFER", "-sDEVICE=ppmraw", "-sOutputFile=-", "-")
	cmd.Stdin = io.MultiReader(strings.NewReader(string(hdr.Head)), hdr.Rest)
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter:", err)
		return 1
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, "ERROR: mp280-filter: cannot start", filepath.Base(gs)+":", err)
		return 1
	}

	cerr := cif.RunChecked(plan.CifArgs, out, stdout)
	if cerr != nil {
		cancel() // stop Ghostscript; nothing more to render
	} else {
		_, _ = io.Copy(io.Discard, out)
	}
	werr := cmd.Wait()

	var refused *cif.Refused
	switch {
	case errors.As(cerr, &refused):
		fmt.Fprintln(stderr, "ERROR: mp280-filter: the printer driver refused this job:", refused.Reason)
		return 1
	case cerr != nil:
		fmt.Fprintln(stderr, "ERROR: mp280-filter:", cerr)
		return 1
	case ctx.Err() != nil:
		fmt.Fprintln(stderr, "ERROR: mp280-filter: interrupted")
		return 1
	case werr != nil:
		fmt.Fprintln(stderr, "ERROR: mp280-filter: Ghostscript failed:", werr)
		return 1
	}
	return 0
}
