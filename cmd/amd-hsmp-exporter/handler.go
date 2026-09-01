// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH

package main

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/GSI-HPC/amd-hsmp-exporter/internal/collector"
)

// defaultScrapeTimeout applies when Prometheus does not announce one.
const defaultScrapeTimeout = 10 * time.Second

// timeoutOffset is subtracted from the announced scrape timeout so the
// exporter finishes (and reports what it has) before Prometheus gives up
// on the whole scrape.
const timeoutOffset = 250 * time.Millisecond

// scrapeContext derives the collection deadline from Prometheus's
// X-Prometheus-Scrape-Timeout-Seconds header.
func scrapeContext(r *http.Request) (context.Context, context.CancelFunc) {
	timeout := defaultScrapeTimeout
	if v := r.Header.Get("X-Prometheus-Scrape-Timeout-Seconds"); v != "" {
		if s, err := strconv.ParseFloat(v, 64); err == nil && s > 0 {
			timeout = time.Duration(s * float64(time.Second))
		}
	}
	timeout -= timeoutOffset
	if timeout < time.Second {
		timeout = time.Second
	}
	return context.WithTimeout(r.Context(), timeout)
}

// scrapeHandler binds the HSMP collector to a fresh registry per request
// so every scrape runs under its own deadline, gathered together with the
// long-lived base registry (build info, process and Go metrics).
type scrapeHandler struct {
	baseReg *prometheus.Registry
	col     *collector.Collector
	logger  *slog.Logger
}

func newScrapeHandler(baseReg *prometheus.Registry, col *collector.Collector, logger *slog.Logger) http.Handler {
	return scrapeHandler{baseReg: baseReg, col: col, logger: logger}
}

// ctxCollector adapts a per-request context onto the shared Collector.
type ctxCollector struct {
	ctx context.Context
	col *collector.Collector
}

func (c ctxCollector) Describe(ch chan<- *prometheus.Desc) { c.col.Describe(ch) }
func (c ctxCollector) Collect(ch chan<- prometheus.Metric) {
	c.col.CollectWithContext(c.ctx, ch)
}

func (h scrapeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := scrapeContext(r)
	defer cancel()

	scrapeReg := prometheus.NewRegistry()
	scrapeReg.MustRegister(ctxCollector{ctx: ctx, col: h.col})

	promhttp.HandlerFor(
		prometheus.Gatherers{h.baseReg, scrapeReg},
		promhttp.HandlerOpts{
			ErrorLog:      slog.NewLogLogger(h.logger.Handler(), slog.LevelError),
			ErrorHandling: promhttp.ContinueOnError,
		},
	).ServeHTTP(w, r.WithContext(ctx))
}
