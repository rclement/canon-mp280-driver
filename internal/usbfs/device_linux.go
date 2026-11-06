// SPDX-License-Identifier: GPL-2.0-only

//go:build linux && (amd64 || arm || arm64 || riscv64)

package usbfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// Errors a caller distinguishes: a busy interface means another process (a
// running print job) owns it and the right reaction is to try again later.
var (
	ErrBusy       = errors.New("usbfs: interface is claimed by another process")
	ErrNotFound   = errors.New("usbfs: no matching USB printer found")
	ErrPermission = errors.New("usbfs: permission denied opening the USB device (udev rule / group lp?)")
)

// Candidate is a USB device found in sysfs. Nothing is sent to it.
type Candidate struct {
	Bus, Device int
	Serial      string
	VendorID    uint16
	ProductID   uint16
}

// Path is the usbfs node of the device.
func (c Candidate) Path() string {
	return fmt.Sprintf("/dev/bus/usb/%03d/%03d", c.Bus, c.Device)
}

const sysfsDevices = "/sys/bus/usb/devices"

func readSysfs(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// List returns the devices of one vendor, read from sysfs only (this never
// opens or talks to a device). Interface entries such as "1-2:1.0" have no
// idVendor file and are skipped.
func List(vendor uint16) ([]Candidate, error) {
	dirs, err := filepath.Glob(filepath.Join(sysfsDevices, "*"))
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, d := range dirs {
		v, err := strconv.ParseUint(readSysfs(d, "idVendor"), 16, 16)
		if err != nil || uint16(v) != vendor {
			continue
		}
		p, _ := strconv.ParseUint(readSysfs(d, "idProduct"), 16, 16)
		bus, _ := strconv.Atoi(readSysfs(d, "busnum"))
		dev, _ := strconv.Atoi(readSysfs(d, "devnum"))
		if bus == 0 || dev == 0 {
			continue
		}
		out = append(out, Candidate{Bus: bus, Device: dev, Serial: readSysfs(d, "serial"),
			VendorID: uint16(v), ProductID: uint16(p)})
	}
	return out, nil
}

// Printer is an opened device with a claimed printer-class interface.
type Printer struct {
	fd       int
	iface    Interface
	claimed  bool
	detached bool // the kernel's usblp driver was unbound and must be restored
	Info     DeviceInfo
}

// Open opens the device node, reads its descriptors and claims the
// printer-class interface. A bound usblp kernel driver is detached (and
// re-attached by Close); an interface held by any other driver or process
// yields ErrBusy.
func Open(c Candidate) (*Printer, error) {
	fd, err := syscall.Open(c.Path(), syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
			return nil, fmt.Errorf("%w: %s", ErrPermission, c.Path())
		}
		return nil, fmt.Errorf("usbfs: open %s: %w", c.Path(), err)
	}
	p := &Printer{fd: fd}
	buf := make([]byte, 4096)
	n, err := syscall.Read(fd, buf)
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("usbfs: read descriptors: %w", err)
	}
	if p.Info, err = ParseDescriptors(buf[:n]); err != nil {
		p.Close()
		return nil, err
	}
	if p.iface, err = p.Info.PrinterInterface(); err != nil {
		p.Close()
		return nil, err
	}
	if err := p.claim(); err != nil {
		p.Close()
		return nil, err
	}
	if p.iface.Alternate != 0 {
		sel := struct{ iface, alt uint32 }{uint32(p.iface.Number), uint32(p.iface.Alternate)}
		_ = p.ioctl(ioctlSetInterface, unsafe.Pointer(&sel))
	}
	return p, nil
}

// OpenFirst opens the first Canon-style printer of the vendor whose serial
// number matches (any device if serial is empty). Devices that cannot be
// opened are skipped; the most informative error is returned if none works.
func OpenFirst(vendor uint16, serial string) (*Printer, error) {
	cands, err := List(vendor)
	if err != nil {
		return nil, err
	}
	var first error
	for _, c := range cands {
		if serial != "" && c.Serial != serial {
			continue
		}
		p, err := Open(c)
		if err == nil {
			return p, nil
		}
		if first == nil || errors.Is(err, ErrBusy) {
			first = err
		}
	}
	if first != nil {
		return nil, first
	}
	return nil, ErrNotFound
}

func (p *Printer) ioctl(req uintptr, arg unsafe.Pointer) error {
	for {
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(p.fd), req, uintptr(arg))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// ioctlN is ioctl for requests whose return value is a byte count.
func (p *Printer) ioctlN(req uintptr, arg unsafe.Pointer) (int, error) {
	for {
		r, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(p.fd), req, uintptr(arg))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return 0, e
		}
		return int(r), nil
	}
}

// disconnectClaim mirrors struct usbdevfs_disconnect_claim.
type disconnectClaim struct {
	iface  uint32
	flags  uint32
	driver [256]byte
}

const disconnectClaimIfDriver = 1

// ioctlRequest mirrors struct usbdevfs_ioctl (used to re-connect a driver).
type ioctlRequest struct {
	iface int32
	code  int32
	data  unsafe.Pointer
}

var ioctlViaInterface = ioc(iocRead|iocWrite, usbfsType, 18, sizeIoctl)

func (p *Printer) claim() error {
	n := uint32(p.iface.Number)
	err := p.ioctl(ioctlClaimInterface, unsafe.Pointer(&n))
	if err == nil {
		p.claimed = true
		return nil
	}
	if err != syscall.EBUSY {
		return fmt.Errorf("usbfs: claim interface %d: %w", p.iface.Number, err)
	}
	// Bound to a driver. Take it over only if it is the kernel's usblp:
	// never kick another usbfs user (a running job) off the interface.
	dc := disconnectClaim{iface: n, flags: disconnectClaimIfDriver}
	copy(dc.driver[:], "usblp")
	if err := p.ioctl(ioctlDisconnectClaim, unsafe.Pointer(&dc)); err != nil {
		return fmt.Errorf("%w (interface %d: %v)", ErrBusy, p.iface.Number, err)
	}
	p.claimed, p.detached = true, true
	return nil
}

// Interface reports the claimed interface.
func (p *Printer) Interface() Interface { return p.iface }

// Close releases the interface, restores usblp if it was detached and closes
// the device.
func (p *Printer) Close() error {
	if p.fd < 0 {
		return nil
	}
	if p.claimed {
		n := uint32(p.iface.Number)
		_ = p.ioctl(ioctlReleaseIface, unsafe.Pointer(&n))
		if p.detached {
			req := ioctlRequest{iface: int32(p.iface.Number), code: int32(ioctlConnect)}
			_ = p.ioctl(ioctlViaInterface, unsafe.Pointer(&req))
		}
	}
	err := syscall.Close(p.fd)
	p.fd = -1
	return err
}

type bulkTransfer struct {
	ep, length, timeout uint32
	data                unsafe.Pointer
}

func ms(d time.Duration) uint32 {
	m := d.Milliseconds()
	if m < 1 {
		m = 1
	}
	if m > 1<<30 {
		m = 1 << 30
	}
	return uint32(m)
}

// Write sends all of b to the bulk OUT endpoint, each transfer bounded by
// timeout.
func (p *Printer) Write(b []byte, timeout time.Duration) error {
	for len(b) > 0 {
		t := bulkTransfer{ep: uint32(p.iface.EPOut), length: uint32(len(b)), timeout: ms(timeout), data: unsafe.Pointer(&b[0])}
		n, err := p.ioctlN(ioctlBulk, unsafe.Pointer(&t))
		runtime.KeepAlive(b)
		if err != nil {
			return fmt.Errorf("usbfs: bulk write: %w", err)
		}
		if n == 0 {
			return errors.New("usbfs: bulk write made no progress")
		}
		b = b[n:]
	}
	return nil
}

// Read reads one transfer from the bulk IN endpoint. It returns (0, nil) when
// nothing arrives within timeout. The buffer is used in whole 512-byte
// packets when it is large enough, so a long reply is never a babble error.
func (p *Printer) Read(b []byte, timeout time.Duration) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(b) >= 512 {
		b = b[:len(b)-len(b)%512]
	}
	t := bulkTransfer{ep: uint32(p.iface.EPIn), length: uint32(len(b)), timeout: ms(timeout), data: unsafe.Pointer(&b[0])}
	n, err := p.ioctlN(ioctlBulk, unsafe.Pointer(&t))
	runtime.KeepAlive(b)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, syscall.ETIMEDOUT):
		return 0, nil
	case errors.Is(err, syscall.EPIPE):
		ep := uint32(p.iface.EPIn)
		_ = p.ioctl(ioctlClearHalt, unsafe.Pointer(&ep))
	}
	return 0, fmt.Errorf("usbfs: bulk read: %w", err)
}

var ioctlClearHalt = ioc(iocRead, usbfsType, 21, sizeUint)
