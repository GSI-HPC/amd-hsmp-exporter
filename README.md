# amd-hsmp-exporter

A small, pure-Go Prometheus exporter for the three AMD EPYC CPU telemetry
values that [node_exporter](https://github.com/prometheus/node_exporter)
cannot provide: the **per-core energy counter**, the **per-core boost
(maximum frequency) limit** and the **per-socket PROCHOT status**. It reads
them directly from `/dev/hsmp`, the character device of the kernel's
`amd_hsmp` driver.

It is a companion to node_exporter, not a replacement. Both run on the
same node and are scraped as separate targets, and this exporter
deliberately exports nothing else. On AMD EPYC systems, socket power and
power limits, socket energy, temperatures, frequencies, C0 residency, DIMM
telemetry and xGMI/PCIe bandwidth are already exposed by node_exporter's
`hwmon`, `rapl`, `cpufreq`, `thermal_zone` and `drm` collectors. Enable
those instead of asking this exporter to grow. GPU metrics belong to a GPU
exporter such as
[ROCm/device-metrics-exporter](https://github.com/ROCm/device-metrics-exporter),
which is GPU-only and has declined CPU metrics upstream. The retired
[amd/amd_smi_exporter](https://github.com/amd/amd_smi_exporter) covered
some of this ground via `libamdsmi`. This project replaces the three of
its metrics that node_exporter cannot supply, with no C library, no cgo
(`CGO_ENABLED=0` builds a working static binary) and no duplicated series.

Metric names are not yet stable. This is a v0.x project and the
exposition may still change between minor versions.

## Requirements

| Requirement | Detail |
|---|---|
| CPU | AMD EPYC family 0x19 (Milan/Genoa/Bergamo/Siena) or 0x1A (Turin). Family 0x17 (Naples/Rome) is unsupported: the kernel driver's family gate (`legacy_hsmp_support()`) only binds 19h/1Ah, so `/dev/hsmp` never exists on Rome, whatever the BIOS settings. |
| Kernel | `CONFIG_AMD_HSMP` (module `amd_hsmp`). CentOS Stream 9 / Rocky 9 ship it as `=m`. The module has no modalias and is platform-probed, so it needs explicit loading; the RPM installs a `modules-load.d` drop-in for that. |
| BIOS | HSMP must be enabled in firmware. If it is not, the module may load but `/dev/hsmp` does not appear or every message errors. |
| OS | EL9 or later (packaged for Rocky Linux 9 + EPEL 9; also builds on EL10 and current Fedora). |
| Energy on pre-Turin parts | The HSMP RAPL messages exist from HSMP protocol version 7 (family 1Ah). On Milan/Genoa, per-core energy needs the opt-in MSR fallback: `msr` module plus `CAP_SYS_RAWIO` (see [Security model](#security-model)). |

## Install

```console
# dnf install ./amd-hsmp-exporter-<version>-1.el9.x86_64.rpm   # from the GitHub release
# systemctl enable --now amd-hsmp-exporter
# curl -s localhost:10054/metrics | grep ^amd_hsmp
```

The RPM ships the binary, a hardened systemd unit, a sysusers.d entry
creating the unprivileged `amd-hsmp-exporter` user, a udev rule granting
that user's group read access to `/dev/hsmp`, a modules-load.d drop-in for
`amd_hsmp`, and `/etc/sysconfig/amd-hsmp-exporter` for flags.

The exporter listens on `:10054` by default. The conventional exporter
range, 9100 to 9999, in the
[Prometheus default port allocations](https://github.com/prometheus/prometheus/wiki/Default-port-allocations)
is fully allocated, and 10054 is the first free port after it. Reserving
it in that list is planned before v1.0. The port is a flag
(`--web.listen-address`), not a constant, so a site can override it in
`OPTIONS=`.

## Exported metrics

| Name | Type | Labels | Unit / meaning |
|---|---|---|---|
| `amd_hsmp_core_energy_joules_total` | Counter | `core`, `socket` | Cumulative energy of the physical core in joules (HSMP RAPL counter, or MSR fallback if enabled) |
| `amd_hsmp_core_boost_limit_hertz` | Gauge | `core`, `socket` | Current maximum-frequency (boost) limit in hertz (converted from the firmware's MHz) |
| `amd_hsmp_prochot_active` | Gauge | `socket` | 1 while the socket asserts PROCHOT (processor-hot throttling), else 0 |
| `amd_hsmp_up` | Gauge | none | 1 if `/dev/hsmp` opens and answers `HSMP_GET_PROTO_VER` |
| `amd_hsmp_protocol_version` | Gauge | none | HSMP protocol version reported by the SMU firmware |
| `amd_hsmp_scrape_duration_seconds` | Gauge | none | Duration of the last HSMP scrape |
| `amd_hsmp_scrape_errors_total` | Counter | `collector` | Failed telemetry reads since start, by collector (`hsmp`, `core-energy`, `boost-limit`, `prochot`, `scrape`) |
| `amd_hsmp_build_info` | Gauge | `version`, `revision`, `goversion` | Constant 1 |

Standard `process_*` and `go_*` runtime metrics are also exposed.

Values are emitted per physical core, not per hardware thread. Core
energy and boost limit are core-scoped, and publishing them again for
each SMT sibling would double the series count without adding
information. The `core` label is the sysfs `core_id` and `socket` the
`physical_package_id`. Internally each core is read through the APIC id
of its lowest-numbered thread, because the HSMP messages take APIC ids
rather than Linux CPU numbers, and the two diverge on real topologies.

When a value is unavailable, its series is absent. The exporter never
emits `-1` or a stale placeholder in its place. A missing device yields
`amd_hsmp_up 0` with the telemetry families omitted. An unsupported
message (for example RAPL on a pre-protocol-7 part) omits just that
family. A single failed core read omits that one series and increments
`amd_hsmp_scrape_errors_total`. Use `absent()` or `amd_hsmp_up` in
alerting rather than testing for sentinel values. The exporter starts,
serves and exits 0 in all of these cases, so it never crash-loops because
hardware support is missing.

### Cardinality

Roughly `2 × cores + sockets + 10` series per node. A dual-socket
96-core Genoa node yields 192 energy series, 192 boost series, 2 PROCHOT
series and the meta metrics, about 390 series in total. Plan for that at
fleet scale.

## Configuration

Flags (via `OPTIONS=` in `/etc/sysconfig/amd-hsmp-exporter`, the unit's
`EnvironmentFile`):

| Flag | Default | Purpose |
|---|---|---|
| `--web.listen-address` | `:10054` | Listen address |
| `--web.telemetry-path` | `/metrics` | Metrics path |
| `--hsmp.device` | `/dev/hsmp` | HSMP character device |
| `--collector.core-energy` | `true` | Per-core energy family on/off |
| `--collector.boost-limit` | `true` | Per-core boost limit family on/off |
| `--collector.prochot` | `true` | Per-socket PROCHOT family on/off |
| `--collector.core-energy.msr-fallback` | `false` | Read core energy from `/dev/cpu/<N>/msr` when HSMP RAPL is unavailable |
| `--log.level` | `info` | `debug`, `info`, `warn`, `error` |
| `--log.format` | `text` | `text` or `json` |
| `--version` | none | Print version and exit |

Scrapes are serialized internally (HSMP is a serialized mailbox) and
honor the `X-Prometheus-Scrape-Timeout-Seconds` header, so a slow or
wedged device degrades a scrape rather than accumulating goroutines.

## Security model

The exporter runs as the dedicated unprivileged `amd-hsmp-exporter` user
under a hardened systemd unit (`NoNewPrivileges`, `ProtectSystem=strict`,
syscall filtering, empty capability set, `DevicePolicy=closed` with only
`/dev/hsmp` allowed). It touches exactly these interfaces:

- `/dev/hsmp`, opened read-only. The driver allows GET (telemetry)
  messages on a read-only descriptor and reserves SET messages for write
  access, which stays with root. Mainline kernels create the node with
  mode 0644 (`crw-r--r--`); the packaged udev rule
  (`60-amd-hsmp-exporter.rules`) additionally grants the service group
  read access so the exporter keeps working where stricter defaults or
  site policy apply.
- `/proc/cpuinfo` and `/sys/devices/system/cpu/*/topology/*`, read once
  at startup for the mapping from CPU to core, socket and APIC id.
- `/dev/cpu/<N>/msr`, only with `--collector.core-energy.msr-fallback`,
  which is off by default. MSR access requires the `msr` module,
  `CAP_SYS_RAWIO` and device permissions; the unit file documents the
  three-line drop-in, and the packaged udev/modules-load files carry
  commented entries. Kernel lockdown (Secure Boot) blocks MSR reads
  regardless of capabilities. The MSR energy counter is 32-bit and wraps
  (about 65536 J per core at the usual energy unit); the exporter
  accumulates it into a monotonic 64-bit counter internally, so the
  exported series survives wraps, and a restart costs only the usual
  counter reset.

SELinux: under the stock targeted policy the service runs as
`unconfined_service_t`. No custom policy module ships yet. Confining the
service with a small `.te` (read access to `hsmp_device_t`-labeled nodes,
`proc_t`, `sysfs_t`) is a documented follow-up rather than a half-tested
policy in the RPM.

## Troubleshooting

| Symptom | Likely cause | Check |
|---|---|---|
| `amd_hsmp_up 0`, log says `no such file or directory` | `amd_hsmp` not loaded, or unsupported CPU family | `lsmod \| grep amd_hsmp`; `modprobe amd_hsmp`; `lscpu \| grep -E 'CPU family'`: family 23 (0x17, Rome) can never work, 25 and 26 can |
| Module loads but `/dev/hsmp` absent | HSMP disabled in BIOS, or ACPI/platform probe failed | `dmesg \| grep -i hsmp` for probe errors; check BIOS/firmware settings |
| `amd_hsmp_up 0`, log says `permission denied` | udev rule not applied (device is root-only on this kernel) | `ls -l /dev/hsmp`; `udevadm trigger /dev/hsmp` after installing; group should be `amd-hsmp-exporter` or mode at least `0644` |
| No `amd_hsmp_core_energy_joules_total`, log says RAPL not implemented | HSMP protocol version < 7 (Milan/Genoa) | `curl -s localhost:10054/metrics \| grep protocol_version`; enable the MSR fallback if you need per-core energy on these parts |
| MSR fallback enabled but still no energy | `msr` module absent, missing `CAP_SYS_RAWIO`, or kernel lockdown | `lsmod \| grep '^msr'`; check the drop-in from the unit file's comment; `dmesg \| grep -i lockdown` |
| Scrapes time out | Wedged SMU mailbox (rare) | `amd_hsmp_scrape_duration_seconds`, `amd_hsmp_scrape_errors_total{collector="scrape"}`; the exporter serializes and deadline-bounds scrapes, so Prometheus sees a degraded scrape, not a pile-up |

## Example queries

Per-core power draw in watts, averaged over 5 minutes:

```promql
rate(amd_hsmp_core_energy_joules_total[5m])
```

Hottest cores by power across the fleet:

```promql
topk(10, rate(amd_hsmp_core_energy_joules_total[5m]))
```

Sockets currently throttling:

```promql
amd_hsmp_prochot_active == 1
```

Nodes where the exporter runs but the device is unusable:

```promql
amd_hsmp_up == 0
```

## Prometheus scrape configuration

```yaml
scrape_configs:
  - job_name: amd-hsmp
    static_configs:
      - targets: ['node1:10054', 'node2:10054']
```

## Verified kernel interface facts

Recorded here so operators and reviewers do not need to re-derive them.
The sources are the mainline kernel (`arch/x86/include/uapi/asm/amd_hsmp.h`,
`drivers/platform/x86/amd/hsmp/`) as of 2026. Re-verify against the
kernel actually deployed, since RHEL backports diverge;
`internal/hsmp/hsmp_test.go` locks the ABI in CI.

- `struct hsmp_message` is 44 bytes; `HSMP_IOCTL_CMD` is `0xC02CF800`.
- `HSMP_GET_RAPL_CORE_COUNTER` (0x31) has `response_sz = 2` per the
  driver's message descriptor table `{1, 2, HSMP_GET}`. The prose
  comment in the uapi header saying 1 is wrong; the 64-bit counter comes
  back split across two response words.
- `HSMP_GET_ENABLED_HSMP_CMDS` (0x37) does not exist in the mainline
  uapi header (the enum ends at 0x32), so feature detection probes the
  messages directly: the driver/firmware answer `ENOMSG` for
  unimplemented ones and the exporter omits those families.
- `/dev/hsmp` is a misc device created with mode 0644; GET messages are
  permitted read-only, SET messages require write access (root).
- The RAPL messages (0x30 to 0x32) are implemented from HSMP protocol
  version 7 (family 1Ah, Turin). Milan/Genoa report lower versions and
  need the MSR fallback for per-core energy. The exporter logs the
  detected protocol version and the energy source it chose at startup.
- The `amd_energy` hwmon driver was removed from mainline and is not an
  alternative path.
- To check the family gate in a deployed Rocky kernel:
  `rpm2cpio kernel-src.rpm | cpio -i --to-stdout '*hsmp/plat.c' | grep -A20 legacy_hsmp_support` (or read the matching CentOS Stream source).

## Running the hardware tests

Everything in CI runs against fakes and fixtures. The tests that need a
real EPYC node with a working `/dev/hsmp` are behind a build tag and
excluded from CI:

```console
$ go test -tags hsmp_hardware ./internal/hsmp/
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md), in particular the Conventional
Commits requirement, the self-contained-commit-message rule and the
rebase-merge policy. Licensed under [Apache-2.0](LICENSE); see
[NOTICE](NOTICE).
