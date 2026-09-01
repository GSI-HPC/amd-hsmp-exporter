// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package collector

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/sys/unix"

	"github.com/GSI-HPC/amd-hsmp-exporter/internal/hsmp"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/msr"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/topology"
)

// comparableFamilies are the deterministic metric families used in golden
// comparisons; amd_hsmp_scrape_duration_seconds is excluded because its
// value depends on wall time.
var comparableFamilies = []string{
	"amd_hsmp_up",
	"amd_hsmp_protocol_version",
	"amd_hsmp_core_energy_joules_total",
	"amd_hsmp_core_boost_limit_hertz",
	"amd_hsmp_prochot_active",
	"amd_hsmp_scrape_errors_total",
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testTopo is a small dual-socket model with a socket-offset APIC map.
func testTopo() *topology.Topology {
	return &topology.Topology{
		Family:  26,
		Sockets: []int{0, 1},
		Cores: []topology.Core{
			{Socket: 0, CoreID: 0, SamplingCPU: 0, APICID: 0},
			{Socket: 0, CoreID: 1, SamplingCPU: 1, APICID: 2},
			{Socket: 1, CoreID: 0, SamplingCPU: 2, APICID: 16},
		},
	}
}

// fakeConn routes messages to per-ID handlers.
type fakeConn struct {
	mu       sync.Mutex
	handlers map[uint32]func(*hsmp.Message) error
	closed   bool
}

func (f *fakeConn) Send(m *hsmp.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.handlers[m.MsgID]
	if !ok {
		return unix.ENOMSG
	}
	return h(m)
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// happyConn implements every message like a protocol-version-7 (Turin)
// part: RAPL with ESU 16, per-APIC boost limits and PROCHOT on socket 1.
func happyConn() *fakeConn {
	boost := map[uint32]uint32{0: 3400, 2: 3500, 16: 3700}
	energy := map[uint32]uint64{0: 1 << 16, 2: 2 << 16, 16: 3 << 16} // 1, 2, 3 J at ESU 16
	return &fakeConn{handlers: map[uint32]func(*hsmp.Message) error{
		hsmp.MsgGetProtoVer: func(m *hsmp.Message) error {
			m.Args[0] = 7
			return nil
		},
		hsmp.MsgGetRaplUnits: func(m *hsmp.Message) error {
			m.Args[0] = 10<<16 | 16<<8 // TU 10, ESU 16
			return nil
		},
		hsmp.MsgGetRaplCoreCounter: func(m *hsmp.Message) error {
			v, ok := energy[m.Args[0]]
			if !ok {
				return unix.EINVAL
			}
			m.Args[0] = uint32(v)
			m.Args[1] = uint32(v >> 32)
			return nil
		},
		hsmp.MsgGetBoostLimit: func(m *hsmp.Message) error {
			v, ok := boost[m.Args[0]]
			if !ok {
				return unix.EINVAL
			}
			m.Args[0] = v
			return nil
		},
		hsmp.MsgGetProcHot: func(m *hsmp.Message) error {
			if m.SockInd == 1 {
				m.Args[0] = 1
			} else {
				m.Args[0] = 0
			}
			return nil
		},
	}}
}

type fakeMSR struct {
	mu     sync.Mutex
	unit   uint64
	energy map[int]uint32
	err    error
}

func (f *fakeMSR) Read(cpu int, reg uint32) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	switch reg {
	case msr.RaplPowerUnit:
		return f.unit, nil
	case msr.CoreEnergyStatus:
		return uint64(f.energy[cpu]), nil
	}
	return 0, errors.New("unexpected msr read")
}

func (f *fakeMSR) set(cpu int, raw uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.energy[cpu] = raw
}

func newTestCollector(cfg Config, conn hsmp.Conn, openErr error, reader msr.Reader) *Collector {
	cfg.Logger = discardLogger()
	c := New(cfg, testTopo())
	c.openConn = func() (hsmp.Conn, error) {
		if openErr != nil {
			return nil, openErr
		}
		return conn, nil
	}
	if reader != nil {
		c.msrReader = reader
	}
	return c
}

func golden(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func allEnabled() Config {
	return Config{
		DevicePath: "/dev/hsmp-test",
		CoreEnergy: true,
		BoostLimit: true,
		ProcHot:    true,
	}
}

func TestCollectAll(t *testing.T) {
	c := newTestCollector(allEnabled(), happyConn(), nil, nil)
	if err := testutil.CollectAndCompare(c, golden(t, "all.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

func TestCollectAndLint(t *testing.T) {
	c := newTestCollector(allEnabled(), happyConn(), nil, nil)
	problems, err := testutil.CollectAndLint(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint: %s: %s", p.Metric, p.Text)
	}
}

func TestDescribeMatchesCollect(t *testing.T) {
	// The registry itself enforces Describe/Collect consistency; a
	// registration + gather with the strictest settings must not error.
	reg := prometheus.NewPedanticRegistry()
	c := newTestCollector(allEnabled(), happyConn(), nil, nil)
	if err := reg.Register(c); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Gather(); err != nil {
		t.Fatal(err)
	}
}

// TestCollectAllUnsupported is the degradation contract for a device whose
// every message errors: amd_hsmp_up 0, no telemetry families, no panic —
// and specifically no sentinel values.
func TestCollectAllUnsupported(t *testing.T) {
	conn := &fakeConn{handlers: map[uint32]func(*hsmp.Message) error{}} // ENOMSG for everything
	c := newTestCollector(allEnabled(), conn, nil, nil)
	if err := testutil.CollectAndCompare(c, golden(t, "unsupported.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

// TestCollectDeviceMissing covers /dev/hsmp being absent: up 0, exporter
// keeps serving.
func TestCollectDeviceMissing(t *testing.T) {
	c := newTestCollector(allEnabled(), nil, unix.ENOENT, nil)
	if err := testutil.CollectAndCompare(c, golden(t, "device_missing.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

// TestCollectDeviceAppearsLate proves the device is retried per scrape:
// the first scrape fails to open it, the second succeeds.
func TestCollectDeviceAppearsLate(t *testing.T) {
	conn := happyConn()
	var openErr error = unix.ENOENT
	cfg := allEnabled()
	cfg.Logger = discardLogger()
	c := New(cfg, testTopo())
	c.openConn = func() (hsmp.Conn, error) {
		if openErr != nil {
			err := openErr
			openErr = nil // next open succeeds
			return nil, err
		}
		return conn, nil
	}
	if got := testutil.CollectAndCount(c, "amd_hsmp_protocol_version"); got != 0 {
		t.Errorf("first scrape: %d protocol_version series, want 0", got)
	}
	if got := testutil.CollectAndCount(c, "amd_hsmp_protocol_version"); got != 1 {
		t.Errorf("second scrape: %d protocol_version series, want 1", got)
	}
}

// TestCollectSingleCoreFailure: one failing core omits that one series and
// counts one error; everything else is unaffected. The failing series must
// be absent — never a -1 or any other sentinel.
func TestCollectSingleCoreFailure(t *testing.T) {
	conn := happyConn()
	inner := conn.handlers[hsmp.MsgGetRaplCoreCounter]
	conn.handlers[hsmp.MsgGetRaplCoreCounter] = func(m *hsmp.Message) error {
		if m.Args[0] == 2 { // core 1 on socket 0
			return unix.EIO
		}
		return inner(m)
	}
	c := newTestCollector(allEnabled(), conn, nil, nil)
	if err := testutil.CollectAndCompare(c, golden(t, "one_core_failed.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

// TestCollectRaplUnsupportedNoFallback: HSMP RAPL missing (pre-proto-7
// firmware) and MSR fallback disabled → the energy family is omitted
// entirely while boost and PROCHOT still work.
func TestCollectRaplUnsupportedNoFallback(t *testing.T) {
	conn := happyConn()
	delete(conn.handlers, hsmp.MsgGetRaplUnits)
	delete(conn.handlers, hsmp.MsgGetRaplCoreCounter)
	conn.handlers[hsmp.MsgGetProtoVer] = func(m *hsmp.Message) error {
		m.Args[0] = 5
		return nil
	}
	c := newTestCollector(allEnabled(), conn, nil, nil)
	if err := testutil.CollectAndCompare(c, golden(t, "no_rapl.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

// TestCollectMSRFallback: HSMP RAPL missing, MSR fallback enabled. The
// exported counter accumulates the wrapping 32-bit MSR, including across a
// wrap.
func TestCollectMSRFallback(t *testing.T) {
	conn := happyConn()
	delete(conn.handlers, hsmp.MsgGetRaplUnits)
	delete(conn.handlers, hsmp.MsgGetRaplCoreCounter)
	conn.handlers[hsmp.MsgGetProtoVer] = func(m *hsmp.Message) error {
		m.Args[0] = 5
		return nil
	}
	reader := &fakeMSR{
		unit: 16 << 8, // ESU 16
		energy: map[int]uint32{
			0: 0,
			1: 0,
			2: 0xFFFF0000, // one 65536-J short of wrapping
		},
	}
	cfg := allEnabled()
	cfg.BoostLimit = false
	cfg.ProcHot = false
	cfg.MSRFallback = true
	c := newTestCollector(cfg, conn, nil, reader)

	if err := testutil.CollectAndCompare(c, golden(t, "msr_first.metrics"), comparableFamilies...); err != nil {
		t.Errorf("first scrape: %v", err)
	}

	reader.set(0, 1<<16)      // +1 J
	reader.set(1, 2<<16)      // +2 J
	reader.set(2, 0x00010000) // wraps: +2^17 raw = +2 J
	if err := testutil.CollectAndCompare(c, golden(t, "msr_second.metrics"), comparableFamilies...); err != nil {
		t.Errorf("second scrape: %v", err)
	}
}

// TestCollectMSROnlyWithoutDevice: no /dev/hsmp at all (e.g. family 0x17)
// but the MSR fallback enabled — core energy is still exported, up stays 0.
func TestCollectMSROnlyWithoutDevice(t *testing.T) {
	reader := &fakeMSR{unit: 16 << 8, energy: map[int]uint32{0: 0, 1: 0, 2: 0}}
	cfg := allEnabled()
	cfg.MSRFallback = true
	c := newTestCollector(cfg, nil, unix.ENOENT, reader)
	if err := testutil.CollectAndCompare(c, golden(t, "msr_only.metrics"), comparableFamilies...); err != nil {
		t.Error(err)
	}
}

// TestCollectContextExpired: a scrape whose context is already done while
// another scrape holds the mailbox must return promptly with only the meta
// metrics — goroutines must not queue behind a wedged ioctl.
func TestCollectContextExpired(t *testing.T) {
	c := newTestCollector(allEnabled(), happyConn(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c.sem <- struct{}{} // simulate a scrape in progress
	defer func() { <-c.sem }()

	ch := make(chan prometheus.Metric, 64)
	done := make(chan struct{})
	go func() {
		c.CollectWithContext(ctx, ch)
		close(ch)
		close(done)
	}()
	<-done

	n := 0
	for range ch {
		n++
	}
	// scrape_duration + the 5 pre-seeded error counters.
	if n != 6 {
		t.Errorf("got %d metrics from an expired-context scrape, want 6", n)
	}
	if got := testutil.ToFloat64(c.scrapeErrors.WithLabelValues("scrape")); got != 1 {
		t.Errorf("scrape errors = %v, want 1", got)
	}
}

// TestCollectConcurrent exercises the scrape serialization under -race.
func TestCollectConcurrent(t *testing.T) {
	c := newTestCollector(allEnabled(), happyConn(), nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := make(chan prometheus.Metric, 128)
			c.Collect(ch)
		}()
	}
	wg.Wait()
}

func TestBuildInfo(t *testing.T) {
	g := BuildInfo("1.2.3", "abcdef")
	if v := testutil.ToFloat64(g); v != 1 {
		t.Errorf("build info value = %v, want 1", v)
	}
	problems, err := testutil.CollectAndLint(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Errorf("lint: %s: %s", p.Metric, p.Text)
	}
}
