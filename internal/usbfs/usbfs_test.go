// SPDX-License-Identifier: GPL-2.0-only

package usbfs

import "testing"

func TestIOCFormula(t *testing.T) {
	// Values from <linux/usbdevice_fs.h>: the BULK number embeds the 24-byte
	// (64-bit) or 16-byte (32-bit ARM) usbdevfs_bulktransfer.
	bulk := uintptr(0xC0185502)
	if ptrSize == 4 {
		bulk = 0xC0105502
	}
	for _, tc := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"BULK", ioctlBulk, bulk},
		{"SETINTERFACE", ioctlSetInterface, 0x80085504},
		{"CLAIMINTERFACE", ioctlClaimInterface, 0x8004550F},
		{"RELEASEINTERFACE", ioctlReleaseIface, 0x80045510},
		{"CONNECT", ioctlConnect, 0x5517},
		{"DISCONNECT_CLAIM", ioctlDisconnectClaim, 0x8108551B},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#x, want %#x", tc.name, tc.got, tc.want)
		}
	}
}

func TestParseDescriptors(t *testing.T) {
	dev := []byte{18, 1, 0, 2, 0, 0, 0, 64, 0xa9, 0x04, 0x46, 0x17, 0, 1, 1, 2, 3, 1}
	cfg := []byte{9, 2, 0, 0, 1, 1, 0, 0x80, 50}
	other := []byte{9, 4, 0, 0, 2, 8, 6, 80, 0, 7, 5, 0x81, 2, 0, 2, 0, 7, 5, 0x02, 2, 0, 2, 0}
	prn := []byte{9, 4, 1, 0, 2, 7, 1, 2, 0, 7, 5, 0x01, 2, 0, 2, 0, 7, 5, 0x82, 2, 0, 2, 0}
	b := append(append(append(dev, cfg...), other...), prn...)
	info, err := ParseDescriptors(b)
	if err != nil {
		t.Fatal(err)
	}
	if info.VendorID != 0x04a9 || info.ProductID != 0x1746 {
		t.Fatalf("ids %04x:%04x", info.VendorID, info.ProductID)
	}
	pi, err := info.PrinterInterface()
	if err != nil {
		t.Fatal(err)
	}
	if pi.Number != 1 || pi.EPOut != 0x01 || pi.EPIn != 0x82 {
		t.Fatalf("interface %+v", pi)
	}
	if _, err := ParseDescriptors(dev[:10]); err == nil {
		t.Fatal("short descriptor accepted")
	}
	if _, err := ParseDescriptors(append(dev, cfg...)); err != nil {
		t.Fatal(err)
	}
	info, _ = ParseDescriptors(append(dev, other...))
	if _, err := info.PrinterInterface(); err == nil {
		t.Fatal("non-printer device accepted")
	}
}
