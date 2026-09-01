// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

//go:build hsmp_hardware

package hsmp

// Hardware-dependent tests. These require a real EPYC node with the
// amd_hsmp driver loaded and /dev/hsmp readable, and are excluded from CI.
// Run them on such a node with:
//
//	go test -tags hsmp_hardware ./internal/hsmp/

import "testing"

func TestDeviceProtoVersion(t *testing.T) {
	d, err := Open("/dev/hsmp")
	if err != nil {
		t.Fatalf("open /dev/hsmp: %v", err)
	}
	defer d.Close()

	v, err := ProtoVersion(d)
	if err != nil {
		t.Fatalf("HSMP_GET_PROTO_VER: %v", err)
	}
	if v == 0 {
		t.Errorf("protocol version = 0, want > 0")
	}
	t.Logf("HSMP protocol version: %d", v)
}

func TestDeviceProcHot(t *testing.T) {
	d, err := Open("/dev/hsmp")
	if err != nil {
		t.Fatalf("open /dev/hsmp: %v", err)
	}
	defer d.Close()

	hot, err := ProcHot(d, 0)
	if err != nil {
		t.Fatalf("HSMP_GET_PROC_HOT: %v", err)
	}
	t.Logf("socket 0 PROCHOT: %v", hot)
}
