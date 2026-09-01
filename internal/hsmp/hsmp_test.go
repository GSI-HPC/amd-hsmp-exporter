// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package hsmp

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestMessageABI locks the kernel ABI: struct size, field offsets and the
// ioctl request number. If any of these assertions fails, the Go struct no
// longer matches struct hsmp_message in
// arch/x86/include/uapi/asm/amd_hsmp.h and every ioctl would corrupt memory
// or be rejected — fix the struct, never the test, unless the kernel ABI
// itself has verifiably changed.
func TestMessageABI(t *testing.T) {
	var m Message

	if got := unsafe.Sizeof(m); got != MessageSize {
		t.Errorf("unsafe.Sizeof(Message{}) = %d, want %d", got, MessageSize)
	}
	if got := unsafe.Offsetof(m.MsgID); got != 0 {
		t.Errorf("offsetof(MsgID) = %d, want 0", got)
	}
	if got := unsafe.Offsetof(m.NumArgs); got != 4 {
		t.Errorf("offsetof(NumArgs) = %d, want 4", got)
	}
	if got := unsafe.Offsetof(m.ResponseSz); got != 6 {
		t.Errorf("offsetof(ResponseSz) = %d, want 6", got)
	}
	if got := unsafe.Offsetof(m.Args); got != 8 {
		t.Errorf("offsetof(Args) = %d, want 8", got)
	}
	if got := unsafe.Offsetof(m.SockInd); got != 40 {
		t.Errorf("offsetof(SockInd) = %d, want 40", got)
	}
}

func TestIoctlCmd(t *testing.T) {
	// _IOWR(0xF8, 0, struct hsmp_message[44]):
	// dir 3<<30 | size 44<<16 | type 0xF8<<8 | nr 0.
	if IoctlCmd != 0xC02CF800 {
		t.Errorf("IoctlCmd = %#X, want 0xC02CF800", IoctlCmd)
	}
}

func TestMessageIDs(t *testing.T) {
	ids := map[string]struct{ got, want uint32 }{
		"HSMP_GET_PROTO_VER":            {MsgGetProtoVer, 0x03},
		"HSMP_GET_BOOST_LIMIT":          {MsgGetBoostLimit, 0x0A},
		"HSMP_GET_PROC_HOT":             {MsgGetProcHot, 0x0B},
		"HSMP_GET_RAPL_UNITS":           {MsgGetRaplUnits, 0x30},
		"HSMP_GET_RAPL_CORE_COUNTER":    {MsgGetRaplCoreCounter, 0x31},
		"HSMP_GET_RAPL_PACKAGE_COUNTER": {MsgGetRaplPackageCounter, 0x32},
	}
	for name, v := range ids {
		if v.got != v.want {
			t.Errorf("%s = %#02X, want %#02X", name, v.got, v.want)
		}
	}
}

type errConn struct{ err error }

func (e errConn) Send(*Message) error { return e.err }
func (e errConn) Close() error        { return nil }

type scriptConn struct {
	t  *testing.T
	fn func(*Message) error
}

func (s scriptConn) Send(m *Message) error { return s.fn(m) }
func (s scriptConn) Close() error          { return nil }

func TestHelpersBuildCorrectMessages(t *testing.T) {
	c := scriptConn{t: t, fn: func(m *Message) error {
		switch m.MsgID {
		case MsgGetProtoVer:
			if m.NumArgs != 0 || m.ResponseSz != 1 {
				t.Errorf("proto ver signature = {%d,%d}, want {0,1}", m.NumArgs, m.ResponseSz)
			}
			m.Args[0] = 7
		case MsgGetBoostLimit:
			if m.NumArgs != 1 || m.ResponseSz != 1 {
				t.Errorf("boost limit signature = {%d,%d}, want {1,1}", m.NumArgs, m.ResponseSz)
			}
			if m.SockInd != 1 || m.Args[0] != 0x85 {
				t.Errorf("boost limit sock/apic = %d/%#X, want 1/0x85", m.SockInd, m.Args[0])
			}
			m.Args[0] = 3400
		case MsgGetProcHot:
			if m.NumArgs != 0 || m.ResponseSz != 1 {
				t.Errorf("prochot signature = {%d,%d}, want {0,1}", m.NumArgs, m.ResponseSz)
			}
			m.Args[0] = 1
		case MsgGetRaplUnits:
			if m.NumArgs != 0 || m.ResponseSz != 1 {
				t.Errorf("rapl units signature = {%d,%d}, want {0,1}", m.NumArgs, m.ResponseSz)
			}
			m.Args[0] = 10<<16 | 16<<8
		case MsgGetRaplCoreCounter:
			// response_sz must be 2; see the doc-inconsistency note on
			// MsgGetRaplCoreCounter.
			if m.NumArgs != 1 || m.ResponseSz != 2 {
				t.Errorf("rapl core counter signature = {%d,%d}, want {1,2}", m.NumArgs, m.ResponseSz)
			}
			m.Args[0] = 0xDDCCBBAA
			m.Args[1] = 0x11223344
		default:
			t.Errorf("unexpected message id %#02X", m.MsgID)
		}
		return nil
	}}

	if v, err := ProtoVersion(c); err != nil || v != 7 {
		t.Errorf("ProtoVersion = %d, %v; want 7, nil", v, err)
	}
	if v, err := BoostLimitMHz(c, 1, 0x85); err != nil || v != 3400 {
		t.Errorf("BoostLimitMHz = %d, %v; want 3400, nil", v, err)
	}
	if v, err := ProcHot(c, 0); err != nil || !v {
		t.Errorf("ProcHot = %v, %v; want true, nil", v, err)
	}
	units, err := RaplUnits(c, 0)
	if err != nil || EnergyStatusUnit(units) != 16 {
		t.Errorf("EnergyStatusUnit(RaplUnits) = %d, %v; want 16, nil", EnergyStatusUnit(units), err)
	}
	if v, err := RaplCoreCounter(c, 0, 0x85); err != nil || v != 0x11223344DDCCBBAA {
		t.Errorf("RaplCoreCounter = %#X, %v; want 0x11223344DDCCBBAA, nil", v, err)
	}
}

func TestRaplCoreCounterMasksAPICID(t *testing.T) {
	var gotArg uint32
	c := scriptConn{t: t, fn: func(m *Message) error {
		gotArg = m.Args[0]
		return nil
	}}
	// APIC id argument is defined as args[0] = apic id[15:0].
	if _, err := RaplCoreCounter(c, 0, 0x1F0085); err != nil {
		t.Fatalf("RaplCoreCounter: %v", err)
	}
	if gotArg != 0x0085 {
		t.Errorf("apic id arg = %#X, want 0x0085 (masked to 16 bits)", gotArg)
	}
}

func TestIsUnsupported(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{unix.ENOMSG, true},
		{unix.EOPNOTSUPP, true},
		{unix.EINVAL, true},
		{unix.ETIMEDOUT, false},
		{unix.EBUSY, false},
		{unix.EIO, false},
		{nil, false},
	}
	for _, tc := range cases {
		c := errConn{err: nil}
		if tc.err != nil {
			c.err = tc.err
		}
		_, err := ProtoVersion(c)
		if tc.err == nil {
			if err != nil {
				t.Errorf("ProtoVersion with nil error: %v", err)
			}
			continue
		}
		if got := IsUnsupported(err); got != tc.want {
			t.Errorf("IsUnsupported(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
