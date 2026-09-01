// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/GSI-HPC/amd-hsmp-exporter/internal/collector"
	"github.com/GSI-HPC/amd-hsmp-exporter/internal/topology"
)

func TestScrapeContext(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"default", "", defaultScrapeTimeout - timeoutOffset},
		{"announced", "5", 5*time.Second - timeoutOffset},
		{"floor", "0.5", time.Second},
		{"garbage", "not-a-number", defaultScrapeTimeout - timeoutOffset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.header != "" {
				r.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", tc.header)
			}
			before := time.Now()
			ctx, cancel := scrapeContext(r)
			defer cancel()
			dl, ok := ctx.Deadline()
			if !ok {
				t.Fatal("context has no deadline")
			}
			got := dl.Sub(before)
			if diff := got - tc.want; diff < -100*time.Millisecond || diff > 100*time.Millisecond {
				t.Errorf("deadline in %v, want about %v", got, tc.want)
			}
		})
	}
}

// TestScrapeHandlerServesDegraded exercises the full HTTP path with an
// unusable device: the scrape must succeed and carry amd_hsmp_up 0 plus
// the base-registry families. No network socket is opened (offline-build
// friendly).
func TestScrapeHandlerServesDegraded(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	col := collector.New(collector.Config{
		DevicePath: filepath.Join(t.TempDir(), "no-such-hsmp"),
		CoreEnergy: true,
		BoostLimit: true,
		ProcHot:    true,
		Logger:     logger,
	}, &topology.Topology{})
	defer func() { _ = col.Close() }()

	baseReg := prometheus.NewRegistry()
	baseReg.MustRegister(collector.BuildInfo("test", "deadbeef"))

	h := newScrapeHandler(baseReg, col, logger)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"amd_hsmp_up 0",
		`amd_hsmp_build_info{goversion=`,
		"amd_hsmp_scrape_duration_seconds",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape body missing %q", want)
		}
	}
	if strings.Contains(body, "amd_hsmp_core_energy_joules_total") {
		t.Error("scrape body contains core energy series despite unusable device")
	}
}

func TestNewLogger(t *testing.T) {
	if _, err := newLogger(os.Stderr, "info", "text"); err != nil {
		t.Errorf("valid logger config rejected: %v", err)
	}
	if _, err := newLogger(os.Stderr, "loud", "text"); err == nil {
		t.Error("invalid level accepted")
	}
	if _, err := newLogger(os.Stderr, "info", "xml"); err == nil {
		t.Error("invalid format accepted")
	}
}

func TestRunVersionFlag(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	if code := run([]string{"--version"}, out); code != 0 {
		t.Fatalf("run --version = %d, want 0", code)
	}
	b, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "amd-hsmp-exporter, version") {
		t.Errorf("version output = %q", string(b))
	}
}

func TestRunBadFlags(t *testing.T) {
	out, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	if code := run([]string{"--no-such-flag"}, out); code != 2 {
		t.Errorf("run with unknown flag = %d, want 2", code)
	}
	if code := run([]string{"--log.level=loud"}, out); code != 2 {
		t.Errorf("run with bad log level = %d, want 2", code)
	}
}
