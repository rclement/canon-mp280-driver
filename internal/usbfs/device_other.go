// SPDX-License-Identifier: GPL-2.0-only

//go:build !(linux && (amd64 || arm64 || riscv64))

package usbfs

import (
	"errors"
	"time"
)

// Errors, see device_linux.go.
var (
	ErrBusy       = errors.New("usbfs: interface is claimed by another process")
	ErrNotFound   = errors.New("usbfs: no matching USB printer found")
	ErrPermission = errors.New("usbfs: permission denied")
)

// Candidate is a USB device found in sysfs.
type Candidate struct {
	Bus, Device int
	Serial      string
	VendorID    uint16
	ProductID   uint16
}

// Path is the usbfs node of the device.
func (c Candidate) Path() string { return "" }

// Printer is an opened device; unsupported on this platform.
type Printer struct{ Info DeviceInfo }

// List always fails on unsupported platforms.
func List(vendor uint16) ([]Candidate, error) { return nil, ErrUnsupported }

// Open always fails on unsupported platforms.
func Open(c Candidate) (*Printer, error) { return nil, ErrUnsupported }

// OpenFirst always fails on unsupported platforms.
func OpenFirst(vendor uint16, serial string) (*Printer, error) { return nil, ErrUnsupported }

// Interface is unavailable.
func (p *Printer) Interface() Interface { return Interface{} }

// Close is a no-op.
func (p *Printer) Close() error { return nil }

// Write always fails.
func (p *Printer) Write(b []byte, timeout time.Duration) error { return ErrUnsupported }

// Read always fails.
func (p *Printer) Read(b []byte, timeout time.Duration) (int, error) { return 0, ErrUnsupported }
