// SPDX-License-Identifier: GPL-2.0-only

package usbfs

// Linux _IOC request encoding of the generic ABI shared by amd64, arm, arm64 and
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

// ptrSize is the size of a user pointer: 4 on 32-bit ARM, 8 elsewhere.
const ptrSize = 4 << (^uintptr(0) >> 63)

// Sizes of the kernel structures (checked against unsafe.Sizeof of the Go
// mirrors in the tests). Those holding a user pointer depend on the word size:
// bulk transfer is 3 x u32 + pointer, usbdevfs_ioctl is 2 x i32 + pointer.
const (
	sizeBulkTransfer    = 8 + 2*ptrSize
	sizeIoctl           = 8 + ptrSize
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
