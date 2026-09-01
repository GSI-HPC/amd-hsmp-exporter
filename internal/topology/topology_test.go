// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package topology

import (
	"path/filepath"
	"reflect"
	"testing"
)

func discover(t *testing.T, fixture string) *Topology {
	t.Helper()
	topo, err := Discover(
		filepath.Join("testdata", fixture, "proc"),
		filepath.Join("testdata", fixture, "sys"),
	)
	if err != nil {
		t.Fatalf("Discover(%s): %v", fixture, err)
	}
	return topo
}

func TestDiscover(t *testing.T) {
	cases := []struct {
		fixture     string
		family      int
		sockets     []int
		cores       []Core
		threadCount int // hardware threads in the fixture, to prove dedup
	}{
		{
			// Dual-socket, SMT on, sparse core_ids, per-socket APIC offset:
			// Linux CPU numbers and APIC ids diverge for every CPU past
			// socket 0 thread 0 (cpu4 has APIC id 16, not 4).
			fixture: "genoa",
			family:  25,
			sockets: []int{0, 1},
			cores: []Core{
				{Socket: 0, CoreID: 0, SamplingCPU: 0, APICID: 0},
				{Socket: 0, CoreID: 1, SamplingCPU: 1, APICID: 2},
				{Socket: 0, CoreID: 4, SamplingCPU: 2, APICID: 4},
				{Socket: 0, CoreID: 5, SamplingCPU: 3, APICID: 6},
				{Socket: 1, CoreID: 0, SamplingCPU: 4, APICID: 16},
				{Socket: 1, CoreID: 1, SamplingCPU: 5, APICID: 18},
				{Socket: 1, CoreID: 4, SamplingCPU: 6, APICID: 20},
				{Socket: 1, CoreID: 5, SamplingCPU: 7, APICID: 22},
			},
			threadCount: 16,
		},
		{
			// Single socket, SMT on: the thread-1 sibling of core 0 is
			// cpu4 with APIC id 1 — the identity assumption would read
			// core 2's telemetry (APIC id 4) for it.
			fixture: "milan",
			family:  25,
			sockets: []int{0},
			cores: []Core{
				{Socket: 0, CoreID: 0, SamplingCPU: 0, APICID: 0},
				{Socket: 0, CoreID: 1, SamplingCPU: 1, APICID: 2},
				{Socket: 0, CoreID: 2, SamplingCPU: 2, APICID: 4},
				{Socket: 0, CoreID: 3, SamplingCPU: 3, APICID: 6},
			},
			threadCount: 8,
		},
		{
			// Family 0x17 (Rome): no /dev/hsmp ever, but topology must
			// still parse so the exporter can report the family and, if
			// enabled, run the MSR fallback. SMT off, identity APIC map.
			fixture: "rome",
			family:  23,
			sockets: []int{0},
			cores: []Core{
				{Socket: 0, CoreID: 0, SamplingCPU: 0, APICID: 0},
				{Socket: 0, CoreID: 1, SamplingCPU: 1, APICID: 1},
				{Socket: 0, CoreID: 2, SamplingCPU: 2, APICID: 2},
				{Socket: 0, CoreID: 3, SamplingCPU: 3, APICID: 3},
			},
			threadCount: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			topo := discover(t, tc.fixture)
			if topo.Family != tc.family {
				t.Errorf("Family = %d, want %d", topo.Family, tc.family)
			}
			if !reflect.DeepEqual(topo.Sockets, tc.sockets) {
				t.Errorf("Sockets = %v, want %v", topo.Sockets, tc.sockets)
			}
			// SMT-sibling deduplication: one Core per physical core, not
			// one per hardware thread.
			if len(topo.Cores) != len(tc.cores) {
				t.Fatalf("got %d cores from %d hardware threads, want %d",
					len(topo.Cores), tc.threadCount, len(tc.cores))
			}
			if !reflect.DeepEqual(topo.Cores, tc.cores) {
				t.Errorf("Cores mismatch:\n got %+v\nwant %+v", topo.Cores, tc.cores)
			}
		})
	}
}

func TestDiscoverSamplingCPUIsLowestThread(t *testing.T) {
	topo := discover(t, "genoa")
	// In the genoa fixture, cpus 8-15 are the thread-1 siblings of cpus
	// 0-7. None of them may be selected as a sampling CPU.
	for _, c := range topo.Cores {
		if c.SamplingCPU >= 8 {
			t.Errorf("core (socket %d, core %d) sampled via SMT sibling cpu%d",
				c.Socket, c.CoreID, c.SamplingCPU)
		}
	}
}

func TestDiscoverErrors(t *testing.T) {
	if _, err := Discover("testdata/does-not-exist/proc", "testdata/does-not-exist/sys"); err == nil {
		t.Error("Discover on missing fixture: got nil error")
	}
	// cpuinfo present but sysfs missing must fail, not return an empty model.
	if _, err := Discover(filepath.Join("testdata", "genoa", "proc"), "testdata/does-not-exist/sys"); err == nil {
		t.Error("Discover with missing sysfs: got nil error")
	}
}
