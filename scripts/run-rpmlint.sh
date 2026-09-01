#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH
#
# Run rpmlint over the given spec/SRPM/RPM files and fail on any finding
# that is not explicitly justified below. This keeps "rpmlint clean" an
# enforced property rather than an aspiration, across the rpmlint versions
# shipped by EL9, EL10 and Fedora.
set -euo pipefail
[ $# -ge 1 ] || { echo "usage: $0 <spec-srpm-or-rpm>..." >&2; exit 2; }

# Justified findings (extended regular expression, one alternative per
# line-comment):
# - no-manual-page-for-binary: the exporter ships no man page; every flag
#   is documented in README.md and `amd-hsmp-exporter --help`.
# - spelling-error: rpmlint's dictionary trips over domain terms
#   (PROCHOT, sysfs, hwmon, udev, cpufreq, Prometheus label names, ...).
# - invalid-license: EL9's rpmlint predates Fedora's move to SPDX license
#   identifiers; `Apache-2.0 AND BSD-3-Clause AND MIT` is the correct
#   current form.
allow='no-manual-page-for-binary|spelling-error|invalid-license'

out="$(rpmlint "$@" 2>&1)" || true
printf '%s\n' "$out"

bad="$(printf '%s\n' "$out" | grep -E ' [EW]: ' | grep -vE "$allow" || true)"
if [ -n "$bad" ]; then
  echo >&2
  echo "unjustified rpmlint findings (fix them or justify them in $0):" >&2
  printf '%s\n' "$bad" >&2
  exit 1
fi
echo "rpmlint: clean (or justified)"
