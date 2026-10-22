// SPDX-License-Identifier: GPL-2.0-only

package cupsbackend

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

// ListStock runs the stock backend without arguments (discovery) and writes
// its stdout, filtered, to w. A missing stock backend lists nothing.
func ListStock(real string, w io.Writer, models []string) error {
	cmd := exec.Command(real)
	cmd.Args = []string{"usb"}
	cmd.Stderr = nil
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return nil // no real backend: nothing to list
	}
	ferr := FilterDiscovery(out, w, models)
	_ = cmd.Wait()
	return ferr
}

// Outcome is how the stock backend ended.
type Outcome struct {
	ExitCode int
	Signal   syscall.Signal // non-zero if it was killed by a signal
	Got      os.Signal      // non-nil if the wrapper itself received SIGTERM/SIGINT/SIGHUP
}

// fdOpen reports whether a file descriptor is open in this process.
func fdOpen(fd uintptr) bool {
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFD, 0)
	return e == 0
}

// RunStock runs the stock backend as a child with the SAME argument vector
// (only argv[0] is replaced by realURI), the environment with DEVICE_URI set
// to realURI, and the same stdin/stdout/stderr and back/side-channel
// descriptors 3 and 4. SIGTERM, SIGINT and SIGHUP received meanwhile are
// forwarded to the child (job cancel); the child is killed with SIGTERM if
// the wrapper dies (Linux). It returns when the child ended.
func RunStock(real, realURI string, args []string, extra func(*exec.Cmd)) (Outcome, error) {
	cmd := exec.Command(real)
	cmd.Args = append([]string{realURI}, args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if len(e) < 11 || e[:11] != "DEVICE_URI=" {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "DEVICE_URI="+realURI)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	var keep []*os.File // fds 3 and 4 must not be closed by a finalizer while the child runs
	if fdOpen(3) {
		f3 := os.NewFile(3, "back-channel")
		keep = append(keep, f3)
		cmd.ExtraFiles = append(cmd.ExtraFiles, f3)
		if fdOpen(4) {
			f4 := os.NewFile(4, "side-channel")
			keep = append(keep, f4)
			cmd.ExtraFiles = append(cmd.ExtraFiles, f4)
		}
	}
	if extra != nil {
		extra(cmd)
	}
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)

	type result struct {
		err error
	}
	started := make(chan error, 1)
	done := make(chan result, 1)
	go func() {
		// PR_SET_PDEATHSIG is tied to the starting thread: keep it alive.
		runtime.LockOSThread()
		setPdeathsig(cmd)
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		done <- result{cmd.Wait()}
	}()
	if err := <-started; err != nil {
		return Outcome{ExitCode: ExitFailed}, err
	}
	var out Outcome
	for {
		select {
		case s := <-sigs:
			out.Got = s
			if ss, ok := s.(syscall.Signal); ok {
				_ = cmd.Process.Signal(ss)
			}
		case r := <-done:
			runtime.KeepAlive(keep)
			var ee *exec.ExitError
			if errors.As(r.err, &ee) {
				if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
					out.Signal = ws.Signal()
					out.ExitCode = 128 + int(ws.Signal())
				} else {
					out.ExitCode = ee.ExitCode()
				}
			} else if r.err != nil {
				return out, r.err
			}
			return out, nil
		}
	}
}

// Reraise makes the wrapper die from the same signal as its child did, so
// cupsd sees the true cause. If that does not terminate the process it
// returns and the caller exits with 128+signal.
func Reraise(sig syscall.Signal) {
	signal.Reset(sig)
	_ = syscall.Kill(os.Getpid(), sig)
}
