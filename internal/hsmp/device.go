// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package hsmp

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Conn is the transport used to exchange one HSMP mailbox message.
//
// It exists so that everything above this package (most importantly the
// Prometheus collector) can be tested against a fake implementation without
// access to /dev/hsmp.
type Conn interface {
	// Send submits m to the HSMP mailbox and, on success, leaves the
	// response words in m.Args.
	Send(m *Message) error
	Close() error
}

// Device is the real Conn backed by the amd_hsmp character device.
//
// A mutex serializes Send calls: HSMP is a single serialized mailbox per
// socket and the driver itself queues concurrent callers, so there is
// nothing to gain from issuing ioctls concurrently and a wedged mailbox
// must not accumulate goroutines.
type Device struct {
	mu sync.Mutex
	f  *os.File
}

var _ Conn = (*Device)(nil)

// Open opens the HSMP character device.
//
// The device is opened read-only: every message this exporter sends is
// HSMP_GET-class, and the driver permits GET messages on a read-only file
// descriptor while reserving write access (SET messages) to root. On
// mainline kernels /dev/hsmp is created with mode 0644, so read-only access
// also minimizes the privilege the packaged udev rule has to grant.
func Open(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		// The *os.PathError already names the path and operation.
		return nil, fmt.Errorf("hsmp: %w", err)
	}
	return &Device{f: f}, nil
}

// Send implements Conn.
func (d *Device) Send(m *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	rc, err := d.f.SyscallConn()
	if err != nil {
		return fmt.Errorf("hsmp: raw conn: %w", err)
	}
	var errno unix.Errno
	cerr := rc.Control(func(fd uintptr) {
		for {
			_, _, e := unix.Syscall(unix.SYS_IOCTL, fd, IoctlCmd, uintptr(unsafe.Pointer(m)))
			if e != unix.EINTR {
				errno = e
				return
			}
		}
	})
	if cerr != nil {
		return fmt.Errorf("hsmp: ioctl control: %w", cerr)
	}
	if errno != 0 {
		return fmt.Errorf("hsmp: message 0x%02X sock %d: %w", m.MsgID, m.SockInd, errno)
	}
	return nil
}

// Close implements Conn.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.f.Close()
}

// IsUnsupported reports whether err means the HSMP message is not
// implemented on this platform, as opposed to a transient failure.
//
// The driver returns ENOMSG both for message IDs it does not know
// (validate_message) and for messages the SMU firmware rejects with
// HSMP_ERR_INVALID_MSG — i.e. "this platform/protocol version does not
// implement this command". EINVAL is included because pre-6.x driver
// revisions and HSMP_ERR_INVALID_INPUT surface unsupported argument shapes
// that way, and ENOTSUP/EOPNOTSUPP (one value on Linux) defensively.
func IsUnsupported(err error) bool {
	return errors.Is(err, unix.ENOMSG) ||
		errors.Is(err, unix.EOPNOTSUPP) ||
		errors.Is(err, unix.EINVAL)
}
