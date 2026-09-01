#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 GSI Helmholtzzentrum für Schwerionenforschung GmbH
#
# Fail unless the given release tag (vX.Y.Z), the RPM spec's Version and
# the newest CHANGELOG.md entry all agree. Run by the release workflow
# before any artifact is built.
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: $0 <tag>" >&2; exit 2; }

tag="$1"
case "$tag" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "tag '$tag' is not of the form vX.Y.Z" >&2; exit 1 ;;
esac
ver="${tag#v}"

spec="packaging/rpm/amd-hsmp-exporter.spec"
spec_ver="$(sed -n 's/^Version:[[:space:]]*//p' "$spec" | head -1)"
if [ "$spec_ver" != "$ver" ]; then
  echo "version mismatch: tag $tag vs $spec Version: $spec_ver" >&2
  exit 1
fi

if ! grep -qE "^## \[$(printf '%s' "$ver" | sed 's/\./\\./g')\]" CHANGELOG.md; then
  echo "version mismatch: CHANGELOG.md has no '## [$ver]' entry for tag $tag" >&2
  exit 1
fi

echo "version consistency OK: tag $tag == spec $spec_ver == CHANGELOG [$ver]"
