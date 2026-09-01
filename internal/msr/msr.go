// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

// Package msr reads AMD RAPL energy MSRs through /dev/cpu/<N>/msr.
//
// This is the fallback path for the per-core energy counter on systems
// whose HSMP firmware does not implement the RAPL messages (0x30–0x32).
// It requires the msr kernel module to be loaded and CAP_SYS_RAWIO, so it
// is opt-in and off by default.
//
// The hwmon-based amd_energy driver is NOT an alternative: it has been
// removed from mainline and is not a viable path on current kernels.
package msr

import (
	"encoding/binary"
	"fmt"
	"os"
)

// AMD RAPL MSRs, from arch/x86/include/asm/msr-index.h.
const (
	// RaplPowerUnit is MSR_AMD_RAPL_POWER_UNIT; the energy status unit
	// (ESU) lives in bits [12:8].
	RaplPowerUnit = 0xC0010299
	// CoreEnergyStatus is MSR_AMD_CORE_ENERGY_STATUS, the per-core energy
	// accumulator. The counter is 32 bits wide and wraps.
	CoreEnergyStatus = 0xC001029A
	// PkgEnergyStatus is MSR_AMD_PKG_ENERGY_STATUS. Unused by this
	// exporter (node_exporter's rapl collector covers package energy);
	// listed for completeness only.
	PkgEnergyStatus = 0xC001029B
)

// Reader reads one MSR on one CPU. It exists so the collector can be
// tested without /dev/cpu.
type Reader interface {
	Read(cpu int, msr uint32) (uint64, error)
}

// Dev is the real Reader backed by the msr character devices.
type Dev struct {
	// Root is the directory containing per-CPU msr device nodes,
	// "/dev/cpu" in production.
	Root string
}

var _ Reader = Dev{}

// Read reads MSR number msr on the given CPU via pread(2) at the MSR
// number as file offset, per the msr driver's interface. MSR device
// content is in the CPU's native byte order; this exporter is x86_64-only
// (little-endian), which the decode below assumes.
func (d Dev) Read(cpu int, msr uint32) (uint64, error) {
	root := d.Root
	if root == "" {
		root = "/dev/cpu"
	}
	path := fmt.Sprintf("%s/%d/msr", root, cpu)
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("msr: %w", err)
	}
	defer func() { _ = f.Close() }()

	var buf [8]byte
	if _, err := f.ReadAt(buf[:], int64(msr)); err != nil {
		return 0, fmt.Errorf("msr: read %#x on cpu %d: %w", msr, cpu, err)
	}
	return binary.LittleEndian.Uint64(buf[:]), nil
}

// EnergyStatusUnit reads the energy status unit (ESU) from
// MSR_AMD_RAPL_POWER_UNIT on the given CPU. Energy in joules =
// raw counter * 0.5^ESU.
func EnergyStatusUnit(r Reader, cpu int) (uint8, error) {
	v, err := r.Read(cpu, RaplPowerUnit)
	if err != nil {
		return 0, err
	}
	return uint8((v >> 8) & 0x1F), nil
}

// CoreEnergyRaw reads the raw 32-bit core energy accumulator of the given
// CPU. Callers must handle wraparound: at a typical ESU of 16 the counter
// wraps after 2^32 * 2^-16 J = 65536 J per core.
func CoreEnergyRaw(r Reader, cpu int) (uint32, error) {
	v, err := r.Read(cpu, CoreEnergyStatus)
	if err != nil {
		return 0, err
	}
	return uint32(v), nil
}
