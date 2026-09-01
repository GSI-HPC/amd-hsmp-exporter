// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

// Package topology builds the CPU ↔ core ↔ socket model of the machine
// from /proc/cpuinfo and sysfs.
//
// Two properties of this model matter for correctness of the exporter:
//
//  1. HSMP_GET_BOOST_LIMIT and HSMP_GET_RAPL_CORE_COUNTER take an APIC id,
//     not a Linux CPU number. The two coincide on some topologies and
//     diverge on others (multi-socket and SMT-enabled EPYC systems in
//     particular); assuming they are equal silently reads another core's
//     telemetry. The per-processor "apicid" field of /proc/cpuinfo is the
//     pure-Go source of truth for the mapping.
//
//  2. Core energy and boost limit are core-scoped. Iterating hardware
//     threads would publish duplicate series for SMT siblings, doubling
//     series count for no information. One physical core therefore yields
//     one Core entry, sampled through its lowest-numbered hardware thread.
package topology

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Core is one physical core.
type Core struct {
	// Socket is the physical_package_id from sysfs.
	Socket int
	// CoreID is the core_id from sysfs, unique within a socket.
	CoreID int
	// SamplingCPU is the lowest-numbered Linux CPU (hardware thread) of
	// this core; per-core telemetry is read through it.
	SamplingCPU int
	// APICID is the APIC id of SamplingCPU, from /proc/cpuinfo.
	APICID uint32
}

// Topology is the discovered machine model.
type Topology struct {
	// Family is the x86 CPU family from /proc/cpuinfo (decimal: 25 =
	// 0x19 Milan/Genoa, 26 = 0x1A Turin, 23 = 0x17 Rome/Naples).
	Family int
	// Cores holds one entry per physical core, sorted by (Socket, CoreID).
	Cores []Core
	// Sockets holds the distinct physical_package_id values, sorted.
	Sockets []int
}

// Discover reads procRoot/cpuinfo and sysRoot/devices/system/cpu to build
// the topology. Production callers pass "/proc" and "/sys"; tests pass
// fixture directories.
func Discover(procRoot, sysRoot string) (*Topology, error) {
	apicIDs, family, err := parseCPUInfo(filepath.Join(procRoot, "cpuinfo"))
	if err != nil {
		return nil, err
	}
	if len(apicIDs) == 0 {
		return nil, fmt.Errorf("topology: no processors found in %s/cpuinfo", procRoot)
	}

	type coreKey struct{ pkg, core int }
	best := map[coreKey]int{} // core -> lowest-numbered CPU seen
	pkgs := map[int]struct{}{}

	cpus := make([]int, 0, len(apicIDs))
	for cpu := range apicIDs {
		cpus = append(cpus, cpu)
	}
	sort.Ints(cpus)

	for _, cpu := range cpus {
		topoDir := filepath.Join(sysRoot, "devices", "system", "cpu",
			fmt.Sprintf("cpu%d", cpu), "topology")
		pkg, err := readInt(filepath.Join(topoDir, "physical_package_id"))
		if err != nil {
			// The CPU may have gone offline between reading /proc/cpuinfo
			// and sysfs; skip it rather than failing discovery.
			continue
		}
		coreID, err := readInt(filepath.Join(topoDir, "core_id"))
		if err != nil {
			continue
		}
		pkgs[pkg] = struct{}{}
		k := coreKey{pkg, coreID}
		if existing, ok := best[k]; !ok || cpu < existing {
			best[k] = cpu
		}
	}
	if len(best) == 0 {
		return nil, fmt.Errorf("topology: no core topology found under %s", sysRoot)
	}

	t := &Topology{Family: family}
	for k, cpu := range best {
		t.Cores = append(t.Cores, Core{
			Socket:      k.pkg,
			CoreID:      k.core,
			SamplingCPU: cpu,
			APICID:      apicIDs[cpu],
		})
	}
	sort.Slice(t.Cores, func(i, j int) bool {
		if t.Cores[i].Socket != t.Cores[j].Socket {
			return t.Cores[i].Socket < t.Cores[j].Socket
		}
		return t.Cores[i].CoreID < t.Cores[j].CoreID
	})
	for pkg := range pkgs {
		t.Sockets = append(t.Sockets, pkg)
	}
	sort.Ints(t.Sockets)
	return t, nil
}

// parseCPUInfo extracts the per-processor "apicid" values and the CPU
// family from an x86 /proc/cpuinfo.
func parseCPUInfo(path string) (map[int]uint32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("topology: %w", err)
	}
	defer func() { _ = f.Close() }()

	apicIDs := map[int]uint32{}
	family := 0
	cur := -1

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "processor":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, 0, fmt.Errorf("topology: bad processor number %q in %s", val, path)
			}
			cur = n
		case "apicid":
			if cur < 0 {
				return nil, 0, fmt.Errorf("topology: apicid before processor in %s", path)
			}
			n, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return nil, 0, fmt.Errorf("topology: bad apicid %q in %s", val, path)
			}
			apicIDs[cur] = uint32(n)
		case "cpu family":
			if n, err := strconv.Atoi(val); err == nil {
				family = n
			}
		}
	}
	if err := s.Err(); err != nil {
		return nil, 0, fmt.Errorf("topology: read %s: %w", path, err)
	}
	return apicIDs, family, nil
}

func readInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("topology: bad integer in %s: %w", path, err)
	}
	return n, nil
}
