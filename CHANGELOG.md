# Changelog

All notable changes to this project are documented in this file. The
format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Metric names
are not yet considered stable before v1.0.0.

## [Unreleased]

## [0.1.0] - 2026-09-01

### Added

- Prometheus exporter for the three AMD EPYC telemetry values
  node_exporter cannot provide, read from the kernel amd_hsmp driver's
  `/dev/hsmp` ioctl interface: `amd_hsmp_core_energy_joules_total`,
  `amd_hsmp_core_boost_limit_hertz` and `amd_hsmp_prochot_active`, plus
  `amd_hsmp_up`, `amd_hsmp_protocol_version`, scrape meta metrics and
  build info.
- APIC-id-based core addressing from `/proc/cpuinfo` and sysfs, with SMT
  siblings deduplicated to one series per physical core.
- Opt-in MSR fallback (`MSR_AMD_CORE_ENERGY_STATUS`) for per-core energy
  on HSMP protocol versions below 7, with wrap-safe accumulation.
- Graceful degradation: absent series (never sentinels) for unavailable
  data, `amd_hsmp_up 0` for an unusable device, per-scrape deadlines from
  `X-Prometheus-Scrape-Timeout-Seconds`, serialized mailbox access.
- ABI assertions locking `struct hsmp_message` (44 bytes) and
  `HSMP_IOCTL_CMD` (0xC02CF800), topology fixtures, golden-exposition
  and lint tests, race-clean test suite.
- EPEL9-conformant RPM packaging: hardened systemd unit, sysusers.d
  user, udev rule for `/dev/hsmp` group access, modules-load.d drop-in,
  `ExclusiveArch: x86_64`, fully offline vendored build.
- CI: lint/vet/race tests, CGO-free static build assertion, offline RPM
  builds on Rocky 9 (required) plus Rocky 10 and Fedora (advisory),
  commitlint and self-contained-commit-message enforcement; release
  workflow producing RPM, SRPM, static tarball and SHA256SUMS.

[Unreleased]: https://github.com/GSI-HPC/amd-hsmp-exporter/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/GSI-HPC/amd-hsmp-exporter/releases/tag/v0.1.0
