#!/bin/sh
set -eu

if [ "$(uname -s)" != Darwin ]; then
  echo "macOS packaging spike requires a macOS host" >&2
  exit 2
fi

arch=${ARCH:-}
case "$arch" in
  arm64) goarch=arm64; extension_platform=osx_arm64; swift_arch=arm64 ;;
  amd64) goarch=amd64; extension_platform=osx_amd64; swift_arch=x86_64 ;;
  "")
    case "$(uname -m)" in
      arm64) arch=arm64; goarch=arm64; extension_platform=osx_arm64; swift_arch=arm64 ;;
      x86_64) arch=amd64; goarch=amd64; extension_platform=osx_amd64; swift_arch=x86_64 ;;
      *) echo "unsupported native architecture: $(uname -m)" >&2; exit 2 ;;
    esac
    ;;
  *) echo "ARCH must be arm64 or amd64" >&2; exit 2 ;;
esac

if [ "$swift_arch" != "$(uname -m)" ]; then
  echo "BLOCKED: the $arch release gate must run natively; current host is $(uname -m)" >&2
  exit 3
fi

identity=${MACOS_SIGNING_IDENTITY:-}
adhoc=false
if [ -z "$identity" ]; then
  if [ "${ALLOW_ADHOC:-0}" != 1 ]; then
    echo "BLOCKED: MACOS_SIGNING_IDENTITY must name a Developer ID Application identity" >&2
    echo "Set ALLOW_ADHOC=1 only for the explicitly non-production local smoke." >&2
    exit 3
  fi
  identity=-
  adhoc=true
fi

if [ "$adhoc" = false ]; then
  security find-identity -v -p codesigning | grep -F "$identity" >/dev/null || {
    echo "BLOCKED: signing identity is not installed: $identity" >&2
    exit 3
  }
  if [ -z "${MACOS_NOTARY_PROFILE:-}" ]; then
    echo "BLOCKED: MACOS_NOTARY_PROFILE must name a notarytool Keychain profile" >&2
    exit 3
  fi
fi

command -v clang >/dev/null
command -v codesign >/dev/null
command -v otool >/dev/null

version=${VERSION:-0.0.0-spike}
root=build/macos-packaging-spike/$arch
app="$root/QuackRidge Gate ü.app"
contents="$app/Contents"
helper="$contents/Helpers/quackridge"
extensions="$contents/Resources/Backend/extensions"
go_path=${QUACKRIDGE_SPIKE_GOPATH:-/private/tmp/quackridge-spike-gopath}
go_cache="$(pwd -P)/$root/go-cache"

rm -rf "$root"
mkdir -p "$contents/MacOS" "$contents/Helpers" "$contents/Resources/Backend"
cp macos/PackagingSpike/Info.plist "$contents/Info.plist"

QUACKRIDGE_EXTENSION_PLATFORM=$extension_platform ./scripts/fetch-extensions.sh "$extensions"

cc=clang
if [ "$goarch" = amd64 ]; then
  cc="clang -arch x86_64 -mmacosx-version-min=13.0"
fi
env GOPATH="$go_path" GOCACHE="$go_cache" GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 \
  CC="$cc" MACOSX_DEPLOYMENT_TARGET=13.0 \
  go build -trimpath -buildvcs=false \
  -ldflags="-s -w -X github.com/pondpilot/quackridge.Version=$version" \
  -o "$helper" ./cmd/quackridge

clang -arch "$swift_arch" -mmacosx-version-min=13.0 \
  macos/PackagingSpike/SpikeHost.c -o "$contents/MacOS/QuackRidge"

minos=$(otool -l "$helper" | awk '/LC_BUILD_VERSION/{found=1;next} found && $1=="minos"{print $2;exit}')
if [ "$minos" != 13.0 ]; then
  echo "backend minimum macOS is $minos, expected 13.0" >&2
  exit 1
fi

(cd "$extensions" && shasum -a 256 -c extensions.sha256)

timestamp=--timestamp
if [ "$adhoc" = true ]; then timestamp=--timestamp=none; fi
backend_entitlements=macos/PackagingSpike/Backend.entitlements
if [ "${ALLOW_LIBRARY_VALIDATION_EXCEPTION:-0}" = 1 ]; then
  backend_entitlements=macos/PackagingSpike/BackendDisableLibraryValidation.entitlements
fi
codesign --force --sign "$identity" "$timestamp" --options runtime \
  --identifier io.pondpilot.quackridge.backend \
  --entitlements "$backend_entitlements" "$helper"
codesign --force --sign "$identity" "$timestamp" --options runtime \
  --identifier io.pondpilot.quackridge \
  --entitlements macos/PackagingSpike/App.entitlements "$app"

codesign --verify --strict --verbose=2 "$helper"
codesign --verify --strict --deep --verbose=2 "$app"
codesign -d --entitlements :- "$helper"
if codesign -d --entitlements :- "$app" 2>&1 | grep -F 'com.apple.security.cs.disable-library-validation' >/dev/null; then
  echo "GUI app must not disable library validation" >&2
  exit 1
fi
(cd "$extensions" && shasum -a 256 -c extensions.sha256)

"$contents/MacOS/QuackRidge"
env GOPATH="$go_path" GOCACHE="$go_cache" go run ./cmd/releasesmoke \
  --binary "$helper" --extensions "$extensions"

if [ "$adhoc" = true ]; then
  echo "PARTIAL PASS: native Hardened Runtime smoke passed with ad-hoc signing." >&2
  echo "Developer ID signing, notarization, Gatekeeper, and final-DMG claims remain BLOCKED." >&2
  exit 4
fi

archive="$root/QuackRidge-app.zip"
dmg="$root/QuackRidge-spike-$arch.dmg"
ditto -c -k --keepParent "$app" "$archive"
xcrun notarytool submit "$archive" --keychain-profile "$MACOS_NOTARY_PROFILE" --wait
xcrun stapler staple "$app"
xcrun stapler validate "$app"

dmg_root="$root/dmg-root"
mkdir -p "$dmg_root"
ditto "$app" "$dmg_root/$(basename "$app")"
hdiutil create -quiet -fs HFS+ -srcfolder "$dmg_root" -volname "QuackRidge" "$dmg"
codesign --force --sign "$identity" --timestamp "$dmg"
xcrun notarytool submit "$dmg" --keychain-profile "$MACOS_NOTARY_PROFILE" --wait
xcrun stapler staple "$dmg"
xcrun stapler validate "$dmg"
codesign --verify --strict --verbose=2 "$dmg"
spctl --assess --type open --context context:primary-signature --verbose=2 "$dmg"

mountpoint=$(mktemp -d "/private/tmp/quackridge-packaging-mount.XXXXXX")
trap 'hdiutil detach "$mountpoint" -quiet 2>/dev/null || true; rmdir "$mountpoint" 2>/dev/null || true' EXIT HUP INT TERM
hdiutil attach -quiet -readonly -nobrowse -mountpoint "$mountpoint" "$dmg"
mounted_app="$mountpoint/$(basename "$app")"
mounted_helper="$mounted_app/Contents/Helpers/quackridge"
mounted_extensions="$mounted_app/Contents/Resources/Backend/extensions"
xcrun stapler validate "$mounted_app"
codesign --verify --strict --deep --verbose=2 "$mounted_app"
codesign -dr - "$mounted_helper" 2>&1 | grep -F 'identifier "io.pondpilot.quackridge.backend"' >/dev/null
codesign -dr - "$mounted_app" 2>&1 | grep -F 'identifier "io.pondpilot.quackridge"' >/dev/null
if codesign -d --entitlements :- "$mounted_app" 2>&1 | grep -F 'com.apple.security.cs.disable-library-validation' >/dev/null; then
  echo "mounted GUI app must not disable library validation" >&2
  exit 1
fi
spctl --assess --type execute --verbose=2 "$mounted_app"
"$mounted_app/Contents/MacOS/QuackRidge"
env GOPATH="$go_path" GOCACHE="$go_cache" go run ./cmd/releasesmoke \
  --binary "$mounted_helper" --extensions "$mounted_extensions"
hdiutil detach "$mountpoint" -quiet
rmdir "$mountpoint"
trap - EXIT HUP INT TERM
(cd "$root" && shasum -a 256 "$(basename "$dmg")" > "$(basename "$dmg").sha256")

echo "PACKAGED EXTENSION GATE PASS: $dmg"
