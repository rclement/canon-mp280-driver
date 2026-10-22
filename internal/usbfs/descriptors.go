// SPDX-License-Identifier: GPL-2.0-only

// Package usbfs is a minimal pure-Go client of the Linux usbfs interface
// (/dev/bus/usb/BBB/DDD), enough to talk to a USB printer-class interface:
// find the device, claim the interface, bulk transfers and the IEEE-1284
// device id request. It needs no cgo and no libusb.
//
// The ioctl request numbers and structure layouts are only defined here for
// amd64, arm64 and riscv64 (64-bit little-endian, generic _IOC encoding);
// on every other platform Open fails with ErrUnsupported.
package usbfs

import (
	"errors"
	"fmt"
)

// ErrUnsupported is returned on platforms the package cannot drive.
var ErrUnsupported = errors.New("usbfs: unsupported platform (need linux on amd64, arm64 or riscv64)")

// ClassPrinter is the USB interface class of printers.
const ClassPrinter = 7

// Interface is one alternate setting of a USB interface that has a bulk IN
// and a bulk OUT endpoint, the shape of a printer-class data interface.
type Interface struct {
	Number    int
	Alternate int
	Class     int
	EPIn      byte // bulk IN endpoint address (bit 7 set)
	EPOut     byte // bulk OUT endpoint address
}

// DeviceInfo is the identity of a device read from its descriptors.
type DeviceInfo struct {
	VendorID, ProductID uint16
	Interfaces          []Interface
}

const (
	descTypeDevice    = 1
	descTypeConfig    = 2
	descTypeInterface = 4
	descTypeEndpoint  = 5
	endpointBulk      = 2
)

// ParseDescriptors decodes the byte string a usbfs device file returns on
// read: the 18-byte device descriptor followed by the configuration
// descriptors. Only interfaces with one bulk IN and one bulk OUT endpoint are
// reported.
func ParseDescriptors(b []byte) (DeviceInfo, error) {
	var info DeviceInfo
	if len(b) < 18 || b[0] != 18 || b[1] != descTypeDevice {
		return info, errors.New("usbfs: bad device descriptor")
	}
	info.VendorID = uint16(b[8]) | uint16(b[9])<<8
	info.ProductID = uint16(b[10]) | uint16(b[11])<<8
	rest := b[18:]
	var cur *Interface
	flush := func() {
		if cur != nil && cur.EPIn != 0 && cur.EPOut != 0 {
			info.Interfaces = append(info.Interfaces, *cur)
		}
		cur = nil
	}
	for len(rest) >= 2 {
		n := int(rest[0])
		if n < 2 || n > len(rest) {
			break
		}
		d := rest[:n]
		switch d[1] {
		case descTypeInterface:
			flush()
			if n >= 9 {
				cur = &Interface{Number: int(d[2]), Alternate: int(d[3]), Class: int(d[5])}
			}
		case descTypeEndpoint:
			if cur != nil && n >= 7 && d[3]&3 == endpointBulk {
				if d[2]&0x80 != 0 {
					if cur.EPIn == 0 {
						cur.EPIn = d[2]
					}
				} else if cur.EPOut == 0 {
					cur.EPOut = d[2]
				}
			}
		}
		rest = rest[n:]
	}
	flush()
	return info, nil
}

// PrinterInterface returns the first printer-class interface with bulk
// endpoints.
func (d DeviceInfo) PrinterInterface() (Interface, error) {
	for _, i := range d.Interfaces {
		if i.Class == ClassPrinter {
			return i, nil
		}
	}
	return Interface{}, fmt.Errorf("usbfs: device %04x:%04x has no printer-class interface with bulk endpoints", d.VendorID, d.ProductID)
}
