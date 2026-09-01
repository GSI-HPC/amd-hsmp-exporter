// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package msr

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeMSRFixture creates <root>/<cpu>/msr as a sparse file with the given
// MSR values written at their MSR-number offsets, mimicking the pread
// interface of the msr driver.
//
// Note the emulation's limit: in the real device the offset selects a
// 64-bit register, so adjacent MSR numbers do not overlap — in a regular
// file they do. Each fixture therefore holds MSRs at least 8 apart, and
// tests for adjacent MSRs use separate fixtures.
func writeMSRFixture(t *testing.T, root string, cpu int, values map[uint32]uint64) {
	t.Helper()
	cpuDir := filepath.Join(root, strconv.Itoa(cpu))
	if err := os.MkdirAll(cpuDir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(cpuDir, "msr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for msr, val := range values {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], val)
		if _, err := f.WriteAt(buf[:], int64(msr)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMSRConstants(t *testing.T) {
	// From arch/x86/include/asm/msr-index.h.
	if RaplPowerUnit != 0xC0010299 {
		t.Errorf("RaplPowerUnit = %#x, want 0xC0010299", RaplPowerUnit)
	}
	if CoreEnergyStatus != 0xC001029A {
		t.Errorf("CoreEnergyStatus = %#x, want 0xC001029A", CoreEnergyStatus)
	}
	if PkgEnergyStatus != 0xC001029B {
		t.Errorf("PkgEnergyStatus = %#x, want 0xC001029B", PkgEnergyStatus)
	}
}

func TestEnergyStatusUnit(t *testing.T) {
	root := t.TempDir()
	writeMSRFixture(t, root, 0, map[uint32]uint64{
		// ESU 16 in bits [12:8], plus unrelated bits that must be masked.
		RaplPowerUnit: 0xA0000 | 16<<8 | 0x3,
	})
	esu, err := EnergyStatusUnit(Dev{Root: root}, 0)
	if err != nil {
		t.Fatalf("EnergyStatusUnit: %v", err)
	}
	if esu != 16 {
		t.Errorf("ESU = %d, want 16", esu)
	}
}

func TestCoreEnergyRaw(t *testing.T) {
	root := t.TempDir()
	writeMSRFixture(t, root, 2, map[uint32]uint64{
		// 32-bit accumulator with garbage in the upper half that
		// CoreEnergyRaw must discard.
		CoreEnergyStatus: 0xDEADBEEF00010000,
	})
	raw, err := CoreEnergyRaw(Dev{Root: root}, 2)
	if err != nil {
		t.Fatalf("CoreEnergyRaw: %v", err)
	}
	if raw != 0x00010000 {
		t.Errorf("raw counter = %#x, want 0x00010000", raw)
	}
}

func TestDevReadMissingDevice(t *testing.T) {
	d := Dev{Root: t.TempDir()}
	if _, err := d.Read(3, RaplPowerUnit); err == nil {
		t.Error("Read on missing msr node: got nil error")
	}
}
