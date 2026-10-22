// SPDX-License-Identifier: GPL-2.0-only

package usbfs

// Linux _IOC request encoding of the generic ABI shared by amd64, arm64 and
// riscv64: 2 direction bits above 14 size bits, 8 type bits and 8 number bits.
// Architectures with another layout (mips, powerpc, sparc) are deliberately
// not supported.
const (
	iocNRBits   = 8
	iocTypeBits = 8
	iocSizeBits = 14

	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits

	iocWrite = 1
	iocRead  = 2
)

// ioc builds an ioctl request number.
func ioc(dir, typ, nr, size uintptr) uintptr {
	return dir<<iocDirShift | typ<<iocTypeShift | nr<<iocNRShift | size<<iocSizeShift
}

// Sizes of the kernel structures on a 64-bit ABI (checked against
// unsafe.Sizeof of the Go mirrors in the tests).
const (
	sizeBulkTransfer    = 24
	sizeSetInterface    = 8
	sizeDisconnectClaim = 264
	sizeUint            = 4
)

const usbfsType = 'U'

var (
	ioctlBulk            = ioc(iocRead|iocWrite, usbfsType, 2, sizeBulkTransfer)
	ioctlSetInterface    = ioc(iocRead, usbfsType, 4, sizeSetInterface)
	ioctlClaimInterface  = ioc(iocRead, usbfsType, 15, sizeUint)
	ioctlReleaseIface    = ioc(iocRead, usbfsType, 16, sizeUint)
	ioctlConnect         = ioc(0, usbfsType, 23, 0)
	ioctlDisconnectClaim = ioc(iocRead, usbfsType, 27, sizeDisconnectClaim)
)
