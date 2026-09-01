// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package hsmp

// Typed helpers for the individual HSMP messages this exporter uses.
// Each helper builds the message with the num_args/response_sz signature
// from the kernel's message descriptor table and decodes the response.

// ProtoVersion returns the HSMP interface (protocol) version reported by
// the SMU firmware. The version is not socket-specific; socket 0 is queried.
func ProtoVersion(c Conn) (uint32, error) {
	m := Message{MsgID: MsgGetProtoVer, NumArgs: 0, ResponseSz: 1}
	if err := c.Send(&m); err != nil {
		return 0, err
	}
	return m.Args[0], nil
}

// BoostLimitMHz returns the current boost (maximum frequency) limit in MHz
// of the core identified by apicID on socket sock.
//
// The argument is an APIC id, not a Linux CPU number; see
// internal/topology for the mapping.
func BoostLimitMHz(c Conn, sock uint16, apicID uint32) (uint32, error) {
	m := Message{MsgID: MsgGetBoostLimit, NumArgs: 1, ResponseSz: 1, SockInd: sock}
	m.Args[0] = apicID
	if err := c.Send(&m); err != nil {
		return 0, err
	}
	return m.Args[0], nil
}

// ProcHot reports whether socket sock currently asserts PROCHOT
// (processor-hot throttling).
func ProcHot(c Conn, sock uint16) (bool, error) {
	m := Message{MsgID: MsgGetProcHot, NumArgs: 0, ResponseSz: 1, SockInd: sock}
	if err := c.Send(&m); err != nil {
		return false, err
	}
	return m.Args[0]&1 == 1, nil
}

// RaplUnits returns the raw RAPL units word for socket sock. The word
// mirrors the layout of MSR_AMD_RAPL_POWER_UNIT: energy status unit (ESU)
// in bits [12:8], time unit in bits [19:16].
func RaplUnits(c Conn, sock uint16) (uint32, error) {
	m := Message{MsgID: MsgGetRaplUnits, NumArgs: 0, ResponseSz: 1, SockInd: sock}
	if err := c.Send(&m); err != nil {
		return 0, err
	}
	return m.Args[0], nil
}

// EnergyStatusUnit extracts the energy status unit (ESU) from a RAPL units
// word. Energy in joules = raw counter * 0.5^ESU.
func EnergyStatusUnit(units uint32) uint8 {
	return uint8((units >> 8) & 0x1F)
}

// RaplCoreCounter returns the 64-bit cumulative core energy counter of the
// core identified by apicID on socket sock. The value is in units of
// 0.5^ESU joules, with ESU from RaplUnits.
//
// response_sz is 2 (not the 1 claimed by the comment in the kernel uapi
// header — see the note on MsgGetRaplCoreCounter): the counter comes back
// split across Args[0] (low 32 bits) and Args[1] (high 32 bits).
func RaplCoreCounter(c Conn, sock uint16, apicID uint32) (uint64, error) {
	m := Message{MsgID: MsgGetRaplCoreCounter, NumArgs: 1, ResponseSz: 2, SockInd: sock}
	m.Args[0] = apicID & 0xFFFF
	if err := c.Send(&m); err != nil {
		return 0, err
	}
	return uint64(m.Args[1])<<32 | uint64(m.Args[0]), nil
}
