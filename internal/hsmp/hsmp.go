// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

// Package hsmp is a minimal, cgo-free wrapper around the Linux amd_hsmp
// driver's character-device interface (/dev/hsmp).
//
// The ABI mirrored here is defined by the kernel's uapi header
// arch/x86/include/uapi/asm/amd_hsmp.h. Every constant in this file is
// locked down by assertions in hsmp_test.go; re-verify against the target
// kernel's headers before changing any of them.
package hsmp

// MaxMsgLen is HSMP_MAX_MSG_LEN: the number of 32-bit argument/response
// words in an HSMP mailbox message.
const MaxMsgLen = 8

// MessageSize is the size in bytes of struct hsmp_message as laid out by a
// C compiler on x86_64: 4 + 2 + 2 + 4*8 + 2 = 42 bytes of fields, rounded
// up to 44 by the struct's 4-byte alignment.
const MessageSize = 44

// Message is the Go equivalent of struct hsmp_message.
//
// The trailing padding is written out explicitly instead of relying on the
// Go compiler's natural padding happening to match the C layout;
// TestMessageABI asserts that unsafe.Sizeof(Message{}) == MessageSize and
// that every field sits at its C offset.
type Message struct {
	MsgID      uint32            // message ID
	NumArgs    uint16            // number of input argument words
	ResponseSz uint16            // number of expected output words
	Args       [MaxMsgLen]uint32 // argument/response buffer
	SockInd    uint16            // socket number
	_          uint16            // explicit tail padding up to MessageSize
}

// _IOWR pieces for asm-generic ioctl encoding on x86_64.
const (
	iocNRShift   = 0
	iocTypeShift = 8
	iocSizeShift = 16
	iocDirShift  = 30

	iocWrite = 1
	iocRead  = 2
)

// BaseIoctlNR is HSMP_BASE_IOCTL_NR.
const BaseIoctlNR = 0xF8

// IoctlCmd is HSMP_IOCTL_CMD, i.e.
// _IOWR(HSMP_BASE_IOCTL_NR, 0, struct hsmp_message).
//
// It is _IOWR: the kernel writes the response back into the same struct's
// Args, so callers pass a pointer to a single mutable Message per call.
// The expected value 0xC02CF800 is asserted in hsmp_test.go.
const IoctlCmd = uintptr((iocRead|iocWrite)<<iocDirShift |
	MessageSize<<iocSizeShift |
	BaseIoctlNR<<iocTypeShift |
	0<<iocNRShift)

// Message IDs used by this exporter, from enum hsmp_message_ids.
// All of these are HSMP_GET-class messages: they read telemetry and are
// permitted by the driver on a read-only file descriptor.
const (
	// MsgGetProtoVer (0x03): num_args=0, response_sz=1,
	// out: Args[0] = HSMP interface (protocol) version.
	MsgGetProtoVer uint32 = 0x03

	// MsgGetBoostLimit (0x0A): num_args=1, response_sz=1,
	// in: Args[0] = APIC id, out: Args[0] = boost (max frequency) limit in MHz.
	MsgGetBoostLimit uint32 = 0x0A

	// MsgGetProcHot (0x0B): num_args=0, response_sz=1,
	// out: Args[0] = PROCHOT status (0 or 1) for the socket.
	MsgGetProcHot uint32 = 0x0B

	// MsgGetRaplUnits (0x30): num_args=0, response_sz=1,
	// out: Args[0] = RAPL units word; time unit in bits [19:16], energy
	// status unit (ESU) in bits [12:8], mirroring MSR_AMD_RAPL_POWER_UNIT.
	MsgGetRaplUnits uint32 = 0x30

	// MsgGetRaplCoreCounter (0x31): num_args=1, response_sz=2,
	// in: Args[0] = APIC id[15:0],
	// out: Args[0] = energy counter bits [31:0], Args[1] = bits [63:32].
	//
	// Known upstream doc inconsistency: the comment block above this entry
	// in arch/x86/include/uapi/asm/amd_hsmp.h says response_sz = 1, but the
	// authoritative message descriptor tuple for 0x31 is {1, 2, HSMP_GET} —
	// response_sz = 2, matching the documented 64-bit split output. 2 is
	// correct; do not "fix" this back to 1.
	MsgGetRaplCoreCounter uint32 = 0x31

	// MsgGetRaplPackageCounter (0x32): num_args=0, response_sz=2. Unused by
	// this exporter (node_exporter's rapl collector covers package energy);
	// listed for completeness only.
	MsgGetRaplPackageCounter uint32 = 0x32
)

// Note: HSMP_GET_ENABLED_HSMP_CMDS (0x37) is referenced in some AMD
// documentation but does not exist in the mainline uapi header (the message
// enum ends at HSMP_GET_RAPL_PACKAGE_COUNTER = 0x32 as of Linux 6.x, 2026).
// Feature detection therefore probes messages directly and treats ENOMSG as
// "not implemented" — see IsUnsupported.
