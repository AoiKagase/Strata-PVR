#!/usr/bin/env sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"

output=${1:-strata-pvr}
count=$(git rev-list --count HEAD)
commit=$(git rev-parse --short=12 HEAD)
version="0.1.0-dev.${count}+${commit}"
if ! git diff --quiet; then
  version="${version}.dirty"
fi
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)

go build \
  -ldflags "-X strata-pvr/internal/version.Number=${version} -X strata-pvr/internal/version.Commit=${commit} -X strata-pvr/internal/version.Date=${date}" \
  -o "$output" ./cmd/strata-pvr
printf 'Built Strata PVR %s -> %s\n' "$version" "$output"
