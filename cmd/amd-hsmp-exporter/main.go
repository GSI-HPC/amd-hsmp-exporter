// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

// Command amd-hsmp-exporter exports the three AMD EPYC telemetry values
// that node_exporter cannot provide — per-core energy, per-core boost
// limit and per-socket PROCHOT — from the kernel's AMD HSMP character
// device, as Prometheus metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/GSI-HPC/amd-hsmp-exporter/internal/collector"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/topology"
)

// version and revision are injected at build time via
// -ldflags "-X main.version=... -X main.revision=...".
var (
	version  = "0.0.0-dev"
	revision = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, errOut *os.File) int {
	fs := flag.NewFlagSet("amd-hsmp-exporter", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var (
		listenAddress = fs.String("web.listen-address", ":10054",
			"Address to listen on for telemetry.")
		metricsPath = fs.String("web.telemetry-path", "/metrics",
			"Path under which to expose metrics.")
		devicePath = fs.String("hsmp.device", "/dev/hsmp",
			"Path of the AMD HSMP character device.")
		coreEnergy = fs.Bool("collector.core-energy", true,
			"Export the per-core energy counter (amd_hsmp_core_energy_joules_total).")
		boostLimit = fs.Bool("collector.boost-limit", true,
			"Export the per-core boost limit (amd_hsmp_core_boost_limit_hertz).")
		procHot = fs.Bool("collector.prochot", true,
			"Export the per-socket PROCHOT status (amd_hsmp_prochot_active).")
		msrFallback = fs.Bool("collector.core-energy.msr-fallback", false,
			"Read core energy from /dev/cpu/<N>/msr when the HSMP RAPL messages are unavailable. Requires the msr module and CAP_SYS_RAWIO.")
		logLevel = fs.String("log.level", "info",
			"Log level: debug, info, warn, error.")
		logFormat = fs.String("log.format", "text",
			"Log format: text or json.")
		showVersion = fs.Bool("version", false,
			"Print version information and exit.")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if *showVersion {
		_, _ = fmt.Fprintf(errOut, "amd-hsmp-exporter, version %s (revision %s, %s)\n",
			version, revision, runtime.Version())
		return 0
	}

	logger, err := newLogger(errOut, *logLevel, *logFormat)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return 2
	}
	logger.Info("starting amd-hsmp-exporter",
		"version", version, "revision", revision, "goversion", runtime.Version())

	// Topology discovery failing is not fatal: the exporter still serves
	// /metrics with amd_hsmp_up so the failure is observable.
	topo, err := topology.Discover("/proc", "/sys")
	if err != nil {
		logger.Error("topology discovery failed; per-core collectors will export nothing", "err", err)
		topo = &topology.Topology{}
	} else {
		logger.Info("discovered topology", "cpu_family", topo.Family,
			"cores", len(topo.Cores), "sockets", len(topo.Sockets))
		if topo.Family != 0 && topo.Family < 0x19 {
			logger.Warn("CPU family predates HSMP driver support (families 0x19/0x1A); /dev/hsmp will not exist",
				"cpu_family", topo.Family)
		}
	}

	col := collector.New(collector.Config{
		DevicePath:  *devicePath,
		CoreEnergy:  *coreEnergy,
		BoostLimit:  *boostLimit,
		ProcHot:     *procHot,
		MSRFallback: *msrFallback,
		Logger:      logger,
	}, topo)
	defer func() { _ = col.Close() }()

	// Meta metrics (build info, process, Go runtime) live in a base
	// registry gathered on every scrape; the HSMP collector is bound to a
	// per-request registry so each scrape carries its own deadline.
	baseReg := prometheus.NewRegistry()
	baseReg.MustRegister(
		collector.BuildInfo(version, revision),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)

	mux := http.NewServeMux()
	mux.Handle(*metricsPath, newScrapeHandler(baseReg, col, logger))
	if *metricsPath != "/" {
		landing := []byte(`<html><head><title>amd-hsmp-exporter</title></head><body>
<h1>amd-hsmp-exporter</h1>
<p><a href="` + *metricsPath + `">Metrics</a></p>
</body></html>
`)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(landing)
		})
	}

	srv := &http.Server{
		Addr:              *listenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "address", *listenAddress, "path", *metricsPath)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		logger.Error("http server failed", "err", err)
		return 1
	case <-ctx.Done():
		logger.Info("shutting down on signal")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("graceful shutdown incomplete", "err", err)
		}
	}
	return 0
}

func newLogger(w *os.File, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid --log.level %q (want debug, info, warn or error)", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	default:
		return nil, fmt.Errorf("invalid --log.format %q (want text or json)", format)
	}
}
