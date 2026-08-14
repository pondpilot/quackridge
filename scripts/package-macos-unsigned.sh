#!/bin/sh
set -eu

if [ "$(uname -s)" != Darwin ]; then echo "macOS app packaging requires macOS" >&2; exit 2; fi
if ! xcodebuild -version >/dev/null 2>&1; then echo "BLOCKED: full Xcode with a matching macOS SDK is required" >&2; exit 3; fi

arch=${ARCH:-}
case "$arch" in
  arm64) goarch=arm64; xcode_arch=arm64; extension_platform=osx_arm64 ;;
  amd64) goarch=amd64; xcode_arch=x86_64; extension_platform=osx_amd64 ;;
  "") case "$(uname -m)" in arm64) arch=arm64; goarch=arm64; xcode_arch=arm64; extension_platform=osx_arm64 ;; x86_64) arch=amd64; goarch=amd64; xcode_arch=x86_64; extension_platform=osx_amd64 ;; *) exit 2 ;; esac ;;
  *) echo "ARCH must be arm64 or amd64" >&2; exit 2 ;;
esac
if [ "$xcode_arch" != "$(uname -m)" ]; then echo "BLOCKED: $arch app builds run natively; host is $(uname -m)" >&2; exit 3; fi

version=${VERSION:-0.1.0-dev}
root="build/macos-app/$arch"
derived="$root/DerivedData"
app="$root/QuackRidge.app"
contents="$app/Contents"
extensions="$contents/Resources/Backend/extensions"
go_cache="$root/go-cache"

rm -rf "$root"
mkdir -p "$root" "$go_cache"
xcodebuild build -project macos/QuackRidge.xcodeproj -scheme QuackRidge -configuration Release \
  -derivedDataPath "$derived" -destination "platform=macOS,arch=$xcode_arch" \
  ARCHS="$xcode_arch" ONLY_ACTIVE_ARCH=YES CODE_SIGNING_ALLOWED=NO CODE_SIGNING_REQUIRED=NO \
  MARKETING_VERSION="$version"
ditto "$derived/Build/Products/Release/QuackRidge.app" "$app"
mkdir -p "$contents/Helpers" "$extensions" "$contents/Resources/Licenses"

cc=clang
if [ "$goarch" = amd64 ]; then cc="clang -arch x86_64 -mmacosx-version-min=13.0"; fi
env GOCACHE="$(pwd -P)/$go_cache" GOOS=darwin GOARCH="$goarch" CGO_ENABLED=1 CC="$cc" MACOSX_DEPLOYMENT_TARGET=13.0 \
  go build -trimpath -buildvcs=false -ldflags="-s -w -X github.com/pondpilot/quackridge.Version=$version" \
  -o "$contents/Helpers/quackridge" ./cmd/quackridge

QUACKRIDGE_EXTENSION_PLATFORM="$extension_platform" ./scripts/fetch-extensions.sh "$extensions"
(cd "$extensions" && shasum -a 256 -c extensions.sha256)
cp LICENSE THIRD_PARTY_NOTICES.md "$contents/Resources/Licenses/"
cp third_party/duckdb/LICENSE "$contents/Resources/Licenses/DuckDB-LICENSE"
cp third_party/quack/LICENSE "$contents/Resources/Licenses/Quack-LICENSE"
cp third_party/odbc-scanner/LICENSE "$contents/Resources/Licenses/ODBC-Scanner-LICENSE"

go run ./cmd/backendmanifest --root "$contents" --architecture "$arch" --version "$version" \
  --output "$contents/Resources/backend-manifest.json"
go run ./cmd/releasesmoke --binary "$contents/Helpers/quackridge" --extensions "$extensions"
go run ./cmd/lifecycleharness --binary "$contents/Helpers/quackridge" --extensions "$extensions"

minos=$(otool -l "$contents/Helpers/quackridge" | awk '/LC_BUILD_VERSION/{found=1;next} found && $1=="minos"{print $2;exit}')
if [ "$minos" != 13.0 ]; then echo "backend minimum macOS is $minos, expected 13.0" >&2; exit 1; fi
if find "$app" -type f -perm +111 -exec codesign -dv {} \; 2>&1 | grep -q 'Authority='; then echo "unexpected signed nested artifact in unsigned package" >&2; exit 1; fi

dmg_root="$root/dmg-root"
mkdir -p "$dmg_root"
ditto "$app" "$dmg_root/QuackRidge.app"
hdiutil create -quiet -fs HFS+ -srcfolder "$dmg_root" -volname QuackRidge "$root/QuackRidge-$version-$arch-unsigned.dmg"
echo "$app"
echo "$root/QuackRidge-$version-$arch-unsigned.dmg"
