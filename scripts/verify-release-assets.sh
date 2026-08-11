#!/bin/sh
set -eu

dist=${1:?usage: verify-release-assets.sh DIST_DIR}
manifest="$dist/manifest.json"
test -f "$manifest"
test -f "$dist/checksums.txt"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum -c checksums.txt)
else
  (cd "$dist" && shasum -a 256 -c checksums.txt)
fi
