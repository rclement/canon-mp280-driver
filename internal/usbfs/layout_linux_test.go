// SPDX-License-Identifier: GPL-2.0-only

//go:build linux && (amd64 || arm64 || riscv64)

package usbfs

import (
	"testing"
	"unsafe"
)

// The ioctl numbers embed the kernel structure sizes; make sure the Go
// mirrors have exactly those sizes on this architecture.
func TestStructSizes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"usbdevfs_bulktransfer", unsafe.Sizeof(bulkTransfer{}), sizeBulkTransfer},
		{"usbdevfs_disconnect_claim", unsafe.Sizeof(disconnectClaim{}), sizeDisconnectClaim},
		{"usbdevfs_ioctl", unsafe.Sizeof(ioctlRequest{}), 16},
		{"usbdevfs_setinterface", 2 * unsafe.Sizeof(uint32(0)), sizeSetInterface},
		{"unsigned int", unsafe.Sizeof(uint32(0)), sizeUint},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: Sizeof = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Fatal("layout constants assume a 64-bit ABI")
	}
}
