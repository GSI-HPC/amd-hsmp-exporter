#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH
#
# Guard for the RPM's bundled-library metadata: the static
# `Provides: bundled(golang(<module>)) = <version>` lines in the spec must
# match vendor/modules.txt exactly, otherwise the package misreports what
# it ships. A dependency bump (Dependabot's included) rewrites go.mod,
# go.sum and vendor/ but never the spec, so this check turns that drift
# into a CI failure instead of a wrong RPM.
#
# Usage:
#   check-bundled-provides.sh          # verify; non-zero exit on drift
#   check-bundled-provides.sh --fix    # rewrite the Provides block in place
set -euo pipefail

spec=packaging/rpm/amd-hsmp-exporter.spec
modules=vendor/modules.txt

# One line per vendored module, in vendor/modules.txt order. The version
# loses its leading "v", and every "-" of a Go pseudo-version becomes "~":
# rpm allows at most one "-" in a version and "~" is its pre-release
# separator, so v0.0.0-20191010083416-a7dc8b61c822 sorts below 0.0.0 as
# 0.0.0~20191010083416~a7dc8b61c822, the same mapping Fedora uses.
expected="$(grep '^# ' "$modules" \
  | awk '{v = substr($3, 2); gsub("-", "~", v);
          printf "Provides:       bundled(golang(%s)) = %s\n", $2, v}')"
actual="$(grep '^Provides: *bundled(golang(' "$spec" || true)"

if [ "${1:-}" = "--fix" ]; then
  # Replace the contiguous block of bundled Provides with the expected one;
  # the first matching line is where the new block goes, the rest are
  # dropped.
  tmp="$(mktemp)"
  awk -v block="$expected" '
    /^Provides: *bundled\(golang\(/ { if (!done) { print block; done = 1 }; next }
    { print }
  ' "$spec" > "$tmp"
  cat "$tmp" > "$spec"
  rm -f "$tmp"
  exit 0
fi

if [ "$expected" != "$actual" ]; then
  echo "$spec: bundled(golang(...)) Provides are out of sync with $modules:" >&2
  diff <(printf '%s\n' "$actual") <(printf '%s\n' "$expected") >&2 || true
  echo "run: $0 --fix" >&2
  exit 1
fi
