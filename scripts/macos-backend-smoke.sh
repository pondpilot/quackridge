#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ]; then echo "macOS backend smoke requires macOS" >&2; exit 2; fi
arch=$(uname -m); case "$arch" in arm64) goarch=arm64; platform=osx_arm64 ;; x86_64) goarch=amd64; platform=osx_amd64 ;; *) exit 2 ;; esac
root="build/macos-backend-smoke/$goarch"; helper="$root/quackridge"; extensions="$root/extensions"
mkdir -p "$root"
if [ ! -f "$extensions/extensions.sha256" ]; then QUACKRIDGE_EXTENSION_PLATFORM="$platform" ./scripts/fetch-extensions.sh "$extensions"; fi
env GOCACHE="$(pwd -P)/$root/go-cache" CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=13.0 go build -trimpath -buildvcs=false -o "$helper" ./cmd/quackridge
(cd "$extensions" && shasum -a 256 -c extensions.sha256)
go run ./cmd/releasesmoke --binary "$helper" --extensions "$extensions"
go run ./cmd/lifecycleharness --binary "$helper" --extensions "$extensions"
