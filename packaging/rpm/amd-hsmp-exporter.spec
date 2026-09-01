# SPDX-License-Identifier: Apache-2.0
# Packaged following the Fedora Go and systemd packaging guidelines.
# The build is fully offline: the release tarball carries the vendor/
# directory and GOPROXY is forced off, so rpmbuild works in a
# network-isolated mock chroot.

%global goipath         github.com/GSI-HPC/amd-hsmp-exporter
%global gomodulesmode   GO111MODULE=on
Version:                0.1.0

%gometa

%global common_description %{expand:
Prometheus exporter for the three AMD EPYC telemetry values that
node_exporter cannot provide: the per-core energy counter, the per-core
boost (maximum frequency) limit and the per-socket PROCHOT status, read
from the kernel amd_hsmp driver's character device (/dev/hsmp). It is a
companion to node_exporter, not a replacement, and deliberately exports
nothing that node_exporter's hwmon, rapl, cpufreq or thermal_zone
collectors already cover.}

Name:           amd-hsmp-exporter
Release:        1%{?dist}
Summary:        Prometheus exporter for AMD EPYC HSMP core telemetry

# The exporter's own code is Apache-2.0; the effective binary license
# includes the vendored Go modules (Apache-2.0, BSD-3-Clause, MIT).
License:        Apache-2.0 AND BSD-3-Clause AND MIT
URL:            %{gourl}
Source0:        %{gosource}

# HSMP is an AMD x86-64-only interface; the kernel driver binds only CPU
# families 0x19 (Milan/Genoa) and 0x1A (Turin).
ExclusiveArch:  x86_64

BuildRequires:  golang >= 1.23
BuildRequires:  systemd-rpm-macros
%if 0%{?fedora}
BuildRequires:  go-rpm-macros
%else
BuildRequires:  go-rpm-macros-epel
%endif

# Vendored modules (keep in sync with vendor/modules.txt; regenerate with:
#   grep '^# ' vendor/modules.txt |
#     awk '{printf "Provides:       bundled(golang(%%s)) = %%s\n", $2, substr($3,2)}' )
Provides:       bundled(golang(github.com/beorn7/perks)) = 1.0.1
Provides:       bundled(golang(github.com/cespare/xxhash/v2)) = 2.3.0
Provides:       bundled(golang(github.com/kr/text)) = 0.2.0
Provides:       bundled(golang(github.com/kylelemons/godebug)) = 1.1.0
Provides:       bundled(golang(github.com/munnerz/goautoneg)) = 0.0.0-20191010083416-a7dc8b61c822
Provides:       bundled(golang(github.com/prometheus/client_golang)) = 1.23.2
Provides:       bundled(golang(github.com/prometheus/client_model)) = 0.6.2
Provides:       bundled(golang(github.com/prometheus/common)) = 0.66.1
Provides:       bundled(golang(github.com/prometheus/procfs)) = 0.16.1
Provides:       bundled(golang(go.yaml.in/yaml/v2)) = 2.4.2
Provides:       bundled(golang(golang.org/x/sys)) = 0.35.0
Provides:       bundled(golang(google.golang.org/protobuf)) = 1.36.8

%description %{common_description}

%prep
# -k keeps the committed vendor/ directory; the build must never touch the
# network (this is enforced in CI by rebuilding the SRPM inside a network
# namespace with no interfaces).
%goprep -k

%build
export GOPROXY=off
export GOFLAGS="${GOFLAGS:-} -mod=vendor"
export LDFLAGS="-X main.version=%{version} -X main.revision=%{version}-%{release}"
%gobuild -o %{gobuilddir}/bin/amd-hsmp-exporter %{goipath}/cmd/amd-hsmp-exporter

%install
install -m 0755 -vd %{buildroot}%{_bindir}
install -m 0755 -vp %{gobuilddir}/bin/amd-hsmp-exporter %{buildroot}%{_bindir}/
install -D -m 0644 -vp packaging/systemd/amd-hsmp-exporter.service %{buildroot}%{_unitdir}/amd-hsmp-exporter.service
install -D -m 0644 -vp packaging/systemd/amd-hsmp-exporter.sysusers %{buildroot}%{_sysusersdir}/amd-hsmp-exporter.conf
install -D -m 0644 -vp packaging/udev/60-amd-hsmp-exporter.rules %{buildroot}%{_udevrulesdir}/60-amd-hsmp-exporter.rules
install -D -m 0644 -vp packaging/modules-load/amd-hsmp-exporter.conf %{buildroot}%{_modulesloaddir}/amd-hsmp-exporter.conf
install -D -m 0644 -vp packaging/sysconfig/amd-hsmp-exporter %{buildroot}%{_sysconfdir}/sysconfig/amd-hsmp-exporter

%check
%gocheck

%pre
%sysusers_create_compat packaging/systemd/amd-hsmp-exporter.sysusers

%post
%systemd_post amd-hsmp-exporter.service
%udev_rules_update

%preun
%systemd_preun amd-hsmp-exporter.service

%postun
%systemd_postun_with_restart amd-hsmp-exporter.service
%udev_rules_update

%files
%license LICENSE NOTICE
%doc README.md CHANGELOG.md
%{_bindir}/amd-hsmp-exporter
%{_unitdir}/amd-hsmp-exporter.service
%{_sysusersdir}/amd-hsmp-exporter.conf
%{_udevrulesdir}/60-amd-hsmp-exporter.rules
%{_modulesloaddir}/amd-hsmp-exporter.conf
%config(noreplace) %{_sysconfdir}/sysconfig/amd-hsmp-exporter

%changelog
* Tue Sep 01 2026 Dennis Klein <d.klein@gsi.de> - 0.1.0-1
- Initial package: per-core energy, per-core boost limit and per-socket
  PROCHOT from /dev/hsmp; hardened systemd unit, sysusers, udev rule and
  modules-load drop-in; fully offline vendored build
