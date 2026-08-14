#!/bin/sh
set -eu

if [ "$(uname -s)" != Darwin ]; then
  echo "macOS ODBC packaging spike requires a macOS host" >&2
  exit 2
fi

arch=${ARCH:-}
case "$arch" in
  arm64|amd64) ;;
  "")
    case "$(uname -m)" in arm64) arch=arm64 ;; x86_64) arch=amd64 ;; *) exit 2 ;; esac
    ;;
  *) echo "ARCH must be arm64 or amd64" >&2; exit 2 ;;
esac

expected_native=$arch
if [ "$arch" = amd64 ]; then expected_native=x86_64; fi
if [ "$(uname -m)" != "$expected_native" ]; then
  echo "BLOCKED: ODBC release proof must run on native $arch; current host is $(uname -m)" >&2
  exit 3
fi

command -v odbcinst >/dev/null 2>&1 || {
  echo "BLOCKED: unixODBC and odbcinst are not installed" >&2
  exit 3
}

postgres_dsn=${QUACKRIDGE_ODBC_POSTGRES_DSN:-}
mysql_dsn=${QUACKRIDGE_ODBC_MYSQL_DSN:-}
if [ -z "$postgres_dsn" ] || [ -z "$mysql_dsn" ]; then
  echo "BLOCKED: set non-secret QUACKRIDGE_ODBC_POSTGRES_DSN and QUACKRIDGE_ODBC_MYSQL_DSN" >&2
  exit 3
fi

root=build/macos-packaging-spike/$arch
dmg=$root/QuackRidge-spike-$arch.dmg
test -f "$dmg" || {
  echo "BLOCKED: exact signed, notarized Task 0 DMG is unavailable: $dmg" >&2
  exit 3
}
(cd "$root" && shasum -a 256 -c "$(basename "$dmg").sha256")
mountpoint=$(mktemp -d "/private/tmp/quackridge-odbc-mount.XXXXXX")
trap 'hdiutil detach "$mountpoint" -quiet 2>/dev/null || true; rmdir "$mountpoint" 2>/dev/null || true' EXIT HUP INT TERM
hdiutil attach -quiet -readonly -nobrowse -mountpoint "$mountpoint" "$dmg"
app="$mountpoint/QuackRidge Gate ü.app"
helper="$app/Contents/Helpers/quackridge"
extensions="$app/Contents/Resources/Backend/extensions"
test -x "$helper"
xcrun stapler validate "$dmg"
xcrun stapler validate "$app"
codesign --verify --strict --verbose=2 "$helper"
codesign --verify --strict --deep --verbose=2 "$app"

temporary=$(mktemp -d "/private/tmp/quackridge-odbc-spike.XXXXXX")
trap 'kill -TERM "${backend_pid:-}" 2>/dev/null || true; wait "${backend_pid:-}" 2>/dev/null || true; rm -rf "$temporary"; hdiutil detach "$mountpoint" -quiet 2>/dev/null || true; rmdir "$mountpoint" 2>/dev/null || true' EXIT HUP INT TERM

"$helper" source add odbc --config "$temporary/config.json" \
  --id packaging-postgres --name Packaging --alias packaging_postgres \
  --dsn "$postgres_dsn" --database-type postgres --json
"$helper" source add odbc --config "$temporary/config.json" \
  --id packaging-mysql --name Packaging --alias packaging_mysql \
  --dsn "$mysql_dsn" --database-type mysql --json

readiness="$temporary/readiness"
mkfifo "$readiness"
"$helper" serve --config "$temporary/config.json" --extensions "$extensions" \
  --control "$temporary/control.sock" --credential-provider environment --json \
  >"$readiness" 2>/dev/null &
backend_pid=$!
ready=$(perl -e 'alarm 60; open my $pipe, "<", $ARGV[0] or die; my $line = <$pipe>; print $line // ""' "$readiness")
case "$ready" in
  *'"endpoint"'*) ;;
  *) echo "packaged helper did not attach both ODBC sources" >&2; exit 1 ;;
esac
status=$("$helper" status --control "$temporary/control.sock" --json)
printf '%s\n' "$status"
printf '%s' "$status" | grep -F '"id":"packaging-postgres"' >/dev/null
printf '%s' "$status" | grep -F '"id":"packaging-mysql"' >/dev/null
healthy_count=$(printf '%s' "$status" | grep -o '"health":"healthy"' | wc -l | tr -d ' ')
if [ "$healthy_count" -lt 2 ]; then
  echo "packaged helper did not report both ODBC sources healthy" >&2
  exit 1
fi
kill -TERM "$backend_pid"
wait "$backend_pid"
backend_pid=
rm -rf "$temporary"
hdiutil detach "$mountpoint" -quiet
rmdir "$mountpoint"
trap - EXIT HUP INT TERM

echo "ODBC PACKAGED HELPER PASS ($arch)"
