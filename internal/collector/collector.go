// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

// Package collector implements the prometheus.Collector that turns HSMP
// telemetry into the amd_hsmp_* metric families.
//
// Degradation contract (the exporter must never fail to start or crash a
// scrape):
//
//  1. /dev/hsmp missing or unusable → serve /metrics normally with
//     amd_hsmp_up 0, log the reason once per failure class.
//  2. A message unsupported on this platform → omit that metric family
//     entirely. Never export a placeholder or sentinel value: an absent
//     series is the correct representation of "no data".
//  3. A per-core read failure → omit that series, increment
//     amd_hsmp_scrape_errors_total, keep going.
package collector

import (
	"context"
	"log/slog"
	"math"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/GSI-HPC/amd-hsmp-exporter/internal/hsmp"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/msr"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/topology"
)

const namespace = "amd_hsmp"

var (
	upDesc = prometheus.NewDesc(
		namespace+"_up",
		"1 if the HSMP device is usable (open succeeds and HSMP_GET_PROTO_VER answers), 0 otherwise.",
		nil, nil,
	)
	protoVerDesc = prometheus.NewDesc(
		namespace+"_protocol_version",
		"HSMP interface (protocol) version reported by the SMU firmware.",
		nil, nil,
	)
	coreEnergyDesc = prometheus.NewDesc(
		namespace+"_core_energy_joules_total",
		"Cumulative energy consumed by the physical core in joules, from the HSMP RAPL core counter or, if enabled, the MSR fallback.",
		[]string{"core", "socket"}, nil,
	)
	boostLimitDesc = prometheus.NewDesc(
		namespace+"_core_boost_limit_hertz",
		"Current maximum-frequency (boost) limit of the physical core in hertz.",
		[]string{"core", "socket"}, nil,
	)
	prochotDesc = prometheus.NewDesc(
		namespace+"_prochot_active",
		"1 if the socket currently asserts PROCHOT (processor-hot throttling), 0 otherwise.",
		[]string{"socket"}, nil,
	)
	scrapeDurationDesc = prometheus.NewDesc(
		namespace+"_scrape_duration_seconds",
		"Duration of the last HSMP scrape in seconds.",
		nil, nil,
	)
)

// Config selects which collectors run and where the devices live.
type Config struct {
	// DevicePath is the HSMP character device, /dev/hsmp in production.
	DevicePath string
	// CoreEnergy, BoostLimit and ProcHot enable the three metric families.
	CoreEnergy bool
	BoostLimit bool
	ProcHot    bool
	// MSRFallback permits reading core energy via /dev/cpu/<N>/msr when
	// the HSMP RAPL messages are unavailable. Requires the msr module and
	// CAP_SYS_RAWIO; off by default.
	MSRFallback bool
	// MSRDevRoot overrides the msr device directory (tests only).
	MSRDevRoot string
	Logger     *slog.Logger
}

// energySource is the resolved origin of the core energy counter.
type energySource int

const (
	energyUndecided energySource = iota
	energyHSMP
	energyMSR
	energyNone
)

// supportState tracks whether a probed HSMP message family is implemented
// on this platform.
type supportState int

const (
	supportUnknown supportState = iota
	supported
	unsupported
)

// Collector implements prometheus.Collector for all amd_hsmp_* families.
//
// A capacity-1 semaphore serializes scrapes: HSMP is a serialized mailbox,
// and a wedged ioctl must not pile up goroutines. A scrape that cannot
// acquire the semaphore before its context deadline returns with only the
// meta metrics.
type Collector struct {
	cfg  Config
	topo *topology.Topology
	log  *slog.Logger

	sem chan struct{}

	// openConn opens the HSMP device; swapped out by tests.
	openConn func() (hsmp.Conn, error)
	// msrReader reads MSRs; swapped out by tests.
	msrReader msr.Reader

	// State below is guarded by the semaphore.
	conn         hsmp.Conn
	protoVer     uint32
	protoKnown   bool
	energySrc    energySource
	hsmpMult     float64 // joules per HSMP RAPL counter unit
	msrMult      float64 // joules per MSR counter unit
	boostState   supportState
	prochotState supportState
	msrAccum     map[int]*msrCounter // keyed by sampling CPU

	// loggedOnce deduplicates failure-class log lines: one line per
	// distinct failure class, not one per scrape.
	loggedOnce map[string]struct{}

	scrapeErrors *prometheus.CounterVec
}

// msrCounter accumulates the wrapping 32-bit MSR energy counter into a
// monotonic 64-bit total.
type msrCounter struct {
	last  uint32
	total uint64
	seen  bool
}

func (m *msrCounter) update(raw uint32) uint64 {
	if m.seen {
		// Unsigned subtraction handles wraparound.
		m.total += uint64(raw - m.last)
	}
	m.last = raw
	m.seen = true
	return m.total
}

// New builds a Collector. It never fails: device problems surface as
// amd_hsmp_up 0 at scrape time, not as startup errors.
func New(cfg Config, topo *topology.Topology) *Collector {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if topo == nil {
		topo = &topology.Topology{}
	}
	c := &Collector{
		cfg:  cfg,
		topo: topo,
		log:  cfg.Logger,
		sem:  make(chan struct{}, 1),
		openConn: func() (hsmp.Conn, error) {
			return hsmp.Open(cfg.DevicePath)
		},
		msrReader:  msr.Dev{Root: cfg.MSRDevRoot},
		msrAccum:   map[int]*msrCounter{},
		loggedOnce: map[string]struct{}{},
		scrapeErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: namespace + "_scrape_errors_total",
			Help: "Number of failed telemetry reads since exporter start, by collector.",
		}, []string{"collector"}),
	}
	// Pre-seed the error counters so every label combination exists (and
	// rate() has a starting point) from the first scrape on.
	for _, name := range []string{"hsmp", "core-energy", "boost-limit", "prochot", "scrape"} {
		c.scrapeErrors.WithLabelValues(name)
	}
	return c
}

// logOnce logs msg with args exactly once per key for the lifetime of the
// process.
func (c *Collector) logOnce(level slog.Level, key, msg string, args ...any) {
	if _, dup := c.loggedOnce[key]; dup {
		return
	}
	c.loggedOnce[key] = struct{}{}
	c.log.Log(context.Background(), level, msg, args...)
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- upDesc
	ch <- protoVerDesc
	ch <- coreEnergyDesc
	ch <- boostLimitDesc
	ch <- prochotDesc
	ch <- scrapeDurationDesc
	c.scrapeErrors.Describe(ch)
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.CollectWithContext(context.Background(), ch)
}

// CollectWithContext is Collect with a deadline, typically derived from
// the X-Prometheus-Scrape-Timeout-Seconds header.
func (c *Collector) CollectWithContext(ctx context.Context, ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue,
			time.Since(start).Seconds())
		c.scrapeErrors.Collect(ch)
	}()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		// Another scrape still holds the mailbox; give up rather than
		// queueing goroutines behind a possibly wedged ioctl.
		c.scrapeErrors.WithLabelValues("scrape").Inc()
		return
	}

	up := c.ensureDevice()
	ch <- prometheus.MustNewConstMetric(upDesc, prometheus.GaugeValue, boolToFloat(up))
	if up {
		ch <- prometheus.MustNewConstMetric(protoVerDesc, prometheus.GaugeValue,
			float64(c.protoVer))
	}
	if up && c.cfg.BoostLimit {
		c.collectBoostLimit(ctx, ch)
	}
	if up && c.cfg.ProcHot {
		c.collectProcHot(ctx, ch)
	}
	if c.cfg.CoreEnergy {
		c.collectCoreEnergy(ctx, ch, up)
	}
}

// ensureDevice opens the HSMP device on demand and confirms it answers
// HSMP_GET_PROTO_VER. The device is retried on every scrape until it
// works, so amd_hsmp appearing after exporter start (e.g. a late modprobe)
// is picked up without a restart.
func (c *Collector) ensureDevice() bool {
	if c.conn == nil {
		conn, err := c.openConn()
		if err != nil {
			c.logOnce(slog.LevelWarn, "open:"+err.Error(),
				"HSMP device unusable; exporting amd_hsmp_up 0",
				"device", c.cfg.DevicePath, "err", err)
			c.scrapeErrors.WithLabelValues("hsmp").Inc()
			return false
		}
		c.conn = conn
	}
	if !c.protoKnown {
		v, err := hsmp.ProtoVersion(c.conn)
		if err != nil {
			c.logOnce(slog.LevelWarn, "protover:"+err.Error(),
				"HSMP_GET_PROTO_VER failed; exporting amd_hsmp_up 0",
				"device", c.cfg.DevicePath, "err", err)
			c.scrapeErrors.WithLabelValues("hsmp").Inc()
			return false
		}
		c.protoVer = v
		c.protoKnown = true
		c.log.Info("HSMP device usable", "device", c.cfg.DevicePath,
			"protocol_version", v, "cpu_family", c.topo.Family,
			"cores", len(c.topo.Cores), "sockets", len(c.topo.Sockets))
	}
	return true
}

func (c *Collector) collectBoostLimit(ctx context.Context, ch chan<- prometheus.Metric) {
	if c.boostState == unsupported {
		return
	}
	okCount, unsupportedCount := 0, 0
	for i := range c.topo.Cores {
		core := &c.topo.Cores[i]
		if ctx.Err() != nil {
			c.scrapeErrors.WithLabelValues("scrape").Inc()
			return
		}
		mhz, err := hsmp.BoostLimitMHz(c.conn, uint16(core.Socket), core.APICID)
		if err != nil {
			if hsmp.IsUnsupported(err) && c.boostState == supportUnknown {
				unsupportedCount++
				continue
			}
			c.scrapeErrors.WithLabelValues("boost-limit").Inc()
			continue
		}
		okCount++
		ch <- prometheus.MustNewConstMetric(boostLimitDesc, prometheus.GaugeValue,
			float64(mhz)*1e6,
			strconv.Itoa(core.CoreID), strconv.Itoa(core.Socket))
	}
	c.resolveSupport(&c.boostState, okCount, unsupportedCount, len(c.topo.Cores),
		"boost-limit", "HSMP_GET_BOOST_LIMIT")
}

func (c *Collector) collectProcHot(ctx context.Context, ch chan<- prometheus.Metric) {
	if c.prochotState == unsupported {
		return
	}
	okCount, unsupportedCount := 0, 0
	for _, sock := range c.topo.Sockets {
		if ctx.Err() != nil {
			c.scrapeErrors.WithLabelValues("scrape").Inc()
			return
		}
		hot, err := hsmp.ProcHot(c.conn, uint16(sock))
		if err != nil {
			if hsmp.IsUnsupported(err) && c.prochotState == supportUnknown {
				unsupportedCount++
				continue
			}
			c.scrapeErrors.WithLabelValues("prochot").Inc()
			continue
		}
		okCount++
		ch <- prometheus.MustNewConstMetric(prochotDesc, prometheus.GaugeValue,
			boolToFloat(hot), strconv.Itoa(sock))
	}
	c.resolveSupport(&c.prochotState, okCount, unsupportedCount, len(c.topo.Sockets),
		"prochot", "HSMP_GET_PROC_HOT")
}

// resolveSupport settles a message family's support state after its first
// full pass: any success marks it supported; a pass where every read
// reported "unsupported" disables the family for the process lifetime, so
// the metric family is omitted entirely rather than exported empty-ish.
func (c *Collector) resolveSupport(state *supportState, ok, unsup, total int, name, msg string) {
	if *state != supportUnknown {
		return
	}
	switch {
	case ok > 0:
		*state = supported
	case total > 0 && unsup == total:
		*state = unsupported
		c.logOnce(slog.LevelInfo, "unsupported:"+name,
			"HSMP message not implemented on this platform; omitting metric family",
			"message", msg, "protocol_version", c.protoVer)
	}
}

func (c *Collector) collectCoreEnergy(ctx context.Context, ch chan<- prometheus.Metric, up bool) {
	c.decideEnergySource(up)
	switch c.energySrc {
	case energyHSMP:
		if !up {
			return
		}
		for i := range c.topo.Cores {
			core := &c.topo.Cores[i]
			if ctx.Err() != nil {
				c.scrapeErrors.WithLabelValues("scrape").Inc()
				return
			}
			raw, err := hsmp.RaplCoreCounter(c.conn, uint16(core.Socket), core.APICID)
			if err != nil {
				c.scrapeErrors.WithLabelValues("core-energy").Inc()
				continue
			}
			ch <- prometheus.MustNewConstMetric(coreEnergyDesc, prometheus.CounterValue,
				float64(raw)*c.hsmpMult,
				strconv.Itoa(core.CoreID), strconv.Itoa(core.Socket))
		}
	case energyMSR:
		for i := range c.topo.Cores {
			core := &c.topo.Cores[i]
			if ctx.Err() != nil {
				c.scrapeErrors.WithLabelValues("scrape").Inc()
				return
			}
			raw, err := msr.CoreEnergyRaw(c.msrReader, core.SamplingCPU)
			if err != nil {
				c.scrapeErrors.WithLabelValues("core-energy").Inc()
				continue
			}
			acc := c.msrAccum[core.SamplingCPU]
			if acc == nil {
				acc = &msrCounter{}
				c.msrAccum[core.SamplingCPU] = acc
			}
			// The MSR counter is 32 bits and wraps (65536 J per core at
			// the typical ESU of 16); accumulate it into a monotonic
			// total so rate() sees a well-behaved counter that starts
			// near zero at exporter start.
			ch <- prometheus.MustNewConstMetric(coreEnergyDesc, prometheus.CounterValue,
				float64(acc.update(raw))*c.msrMult,
				strconv.Itoa(core.CoreID), strconv.Itoa(core.Socket))
		}
	}
}

// decideEnergySource picks where core energy comes from, once, on the
// first scrape that can decide:
//
//   - HSMP RAPL when the firmware implements it (probed directly — the
//     mainline uapi header has no HSMP_GET_ENABLED_HSMP_CMDS to ask, and
//     firmware answers ENOMSG for unimplemented messages; in practice the
//     RAPL messages exist from HSMP protocol version 7, i.e. Turin).
//   - The MSR fallback when enabled and readable.
//   - Otherwise none: the family is omitted entirely.
func (c *Collector) decideEnergySource(up bool) {
	if c.energySrc != energyUndecided {
		return
	}
	if up {
		units, err := hsmp.RaplUnits(c.conn, uint16(c.firstSocket()))
		if err == nil && len(c.topo.Cores) > 0 {
			// Also probe the core counter itself: units and counter are
			// separate messages and only together prove the HSMP path.
			first := c.topo.Cores[0]
			_, err = hsmp.RaplCoreCounter(c.conn, uint16(first.Socket), first.APICID)
		}
		switch {
		case err == nil:
			esu := hsmp.EnergyStatusUnit(units)
			c.hsmpMult = math.Pow(0.5, float64(esu))
			c.energySrc = energyHSMP
			c.log.Info("core energy source: HSMP RAPL counters",
				"protocol_version", c.protoVer, "energy_status_unit", esu)
			return
		case hsmp.IsUnsupported(err):
			c.logOnce(slog.LevelInfo, "rapl-unsupported",
				"HSMP RAPL messages not implemented on this platform (expected below protocol version 7)",
				"protocol_version", c.protoVer, "err", err)
			// fall through to the MSR fallback
		default:
			// Transient failure: stay undecided and retry next scrape.
			c.scrapeErrors.WithLabelValues("core-energy").Inc()
			return
		}
	} else if !c.cfg.MSRFallback {
		// No device yet and no fallback: stay undecided so a device that
		// appears later can still enable the HSMP path.
		return
	}
	if c.cfg.MSRFallback {
		cpu := c.firstSamplingCPU()
		esu, err := msr.EnergyStatusUnit(c.msrReader, cpu)
		if err != nil {
			c.logOnce(slog.LevelWarn, "msr-unavailable",
				"MSR fallback enabled but unreadable (msr module loaded? CAP_SYS_RAWIO granted? kernel lockdown?)",
				"cpu", cpu, "err", err)
			c.scrapeErrors.WithLabelValues("core-energy").Inc()
			c.energySrc = energyNone
			return
		}
		c.msrMult = math.Pow(0.5, float64(esu))
		c.energySrc = energyMSR
		c.log.Info("core energy source: MSR fallback (MSR_AMD_CORE_ENERGY_STATUS)",
			"energy_status_unit", esu)
		return
	}
	c.logOnce(slog.LevelInfo, "energy-none",
		"core energy unavailable: HSMP RAPL unsupported and MSR fallback disabled; omitting metric family")
	c.energySrc = energyNone
}

func (c *Collector) firstSocket() int {
	if len(c.topo.Sockets) > 0 {
		return c.topo.Sockets[0]
	}
	return 0
}

func (c *Collector) firstSamplingCPU() int {
	if len(c.topo.Cores) > 0 {
		return c.topo.Cores[0].SamplingCPU
	}
	return 0
}

// Close releases the HSMP device.
func (c *Collector) Close() error {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// BuildInfo returns the amd_hsmp_build_info constant gauge.
func BuildInfo(version, revision string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: namespace + "_build_info",
		Help: "Build information of the running amd-hsmp-exporter; constant 1.",
		ConstLabels: prometheus.Labels{
			"version":   version,
			"revision":  revision,
			"goversion": runtime.Version(),
		},
	})
	g.Set(1)
	return g
}
