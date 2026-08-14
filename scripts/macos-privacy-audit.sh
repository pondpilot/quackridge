#!/bin/sh
set -eu

swift_root=macos/QuackRidge
audit_root=$(pwd -P)/build/macos-audit
mkdir -p "$audit_root/gocache" "$audit_root/gopath"

if rg -n '(^|[^A-Za-z])(print|debugPrint|dump|NSLog|os_log)\s*\(|Logger\s*\(' "$swift_root"; then
  echo "privacy audit: direct application logging is not allowed" >&2
  exit 1
fi
if rg -n 'ProcessInfo\.processInfo\.environment|child\.environment\s*=\s*ProcessInfo' "$swift_root"; then
  echo "privacy audit: inherited process environments are not allowed" >&2
  exit 1
fi

rg -q 'child\.standardOutput = stdout\.fileHandleForWriting' "$swift_root/Services/ServiceSupervisor.swift"
rg -q 'child\.standardError = stderr\.fileHandleForWriting' "$swift_root/Services/ServiceSupervisor.swift"
rg -q 'Self\.discard\(stdout\.fileHandleForReading\)' "$swift_root/Services/ServiceSupervisor.swift"
rg -q 'Self\.discard\(stderr\.fileHandleForReading\)' "$swift_root/Services/ServiceSupervisor.swift"
rg -q 'managementFrameLimit = 64 \* 1024' "$swift_root/Models/ManagementProtocol.swift"

env TMPDIR=/private/tmp GOCACHE="$audit_root/gocache" GOPATH="$audit_root/gopath" \
  go test ./internal/config ./internal/control ./internal/source/odbc \
  -run 'Secret|Credential|Sensitive|ODBC|Frame' -count=1

echo "macOS privacy audit passed"
