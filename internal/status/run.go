// SPDX-License-Identifier: GPL-2.0-only

package status

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"canon-mp280-driver/internal/usbfs"
)

// CanonVendorID is the USB vendor id of Canon.
const CanonVendorID = 0x04a9

// EnvTransport selects the software mock printer instead of USB: the value
// "unix:/path" or "unix:@abstract-name". Only tests and the e2e suite set it.
const EnvTransport = "MP280_STATUS_TRANSPORT"

// Errors of Run that callers react to.
var (
	// ErrNoDevice: no matching printer (or no permission to open it).
	ErrNoDevice = errors.New("no matching printer")
	// ErrBusy: the printer interface is held by someone else (a print job).
	ErrBusy = errors.New("printer interface is busy")
)

// unixTransport adapts a socket to Transport (the mock printer).
type unixTransport struct{ c net.Conn }

func (u unixTransport) Write(p []byte, timeout time.Duration) error {
	_ = u.c.SetWriteDeadline(time.Now().Add(timeout))
	_, err := u.c.Write(p)
	return err
}

func (u unixTransport) Read(p []byte, timeout time.Duration) (int, error) {
	_ = u.c.SetReadDeadline(time.Now().Add(timeout))
	n, err := u.c.Read(p)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return n, nil
	}
	return n, err
}

// usbTransport adapts a claimed usbfs printer.
type usbTransport struct{ p *usbfs.Printer }

func (u usbTransport) Write(b []byte, timeout time.Duration) error { return u.p.Write(b, timeout) }
func (u usbTransport) Read(b []byte, timeout time.Duration) (int, error) {
	return u.p.Read(b, timeout)
}

// SerialFromURI extracts the serial= query value of a usb:// or mp280usb://
// device URI (percent-decoded), or "".
func SerialFromURI(uri string) string {
	q := strings.IndexByte(uri, '?')
	if q < 0 {
		return ""
	}
	for _, kv := range strings.Split(uri[q+1:], "&") {
		if v, ok := strings.CutPrefix(kv, "serial="); ok {
			return percentDecode(v)
		}
	}
	return ""
}

func percentDecode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c >= 'a':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// Run opens the transport (USB, or the mock named by MP280_STATUS_TRANSPORT),
// runs one status session and closes it again. uri may carry serial=; with
// "" the first Canon printer-class device is used.
func Run(uri string, opt Options) (*Result, error) {
	if tr := os.Getenv(EnvTransport); tr != "" {
		path, ok := strings.CutPrefix(tr, "unix:")
		if !ok {
			return nil, fmt.Errorf("%w: unsupported %s %q", ErrNoDevice, EnvTransport, tr)
		}
		c, err := net.DialTimeout("unix", path, 2*time.Second)
		if err != nil {
			return nil, fmt.Errorf("%w: cannot connect to mock device %s", ErrNoDevice, path)
		}
		defer c.Close()
		return Query(unixTransport{c}, opt)
	}
	p, err := usbfs.OpenFirst(CanonVendorID, SerialFromURI(uri))
	switch {
	case errors.Is(err, usbfs.ErrBusy):
		return nil, fmt.Errorf("%w: %v", ErrBusy, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrNoDevice, err)
	}
	defer p.Close()
	return Query(usbTransport{p}, opt)
}
