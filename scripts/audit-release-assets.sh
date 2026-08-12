#!/bin/sh
set -eu

dist=${1:?usage: audit-release-assets.sh DIST_DIR}
work=$(mktemp -d "${TMPDIR:-/tmp}/quackridge-release-audit.XXXXXX")
trap 'rm -rf "$work"' EXIT HUP INT TERM

for archive in "$dist"/*.tar.gz "$dist"/*.zip; do
  test -f "$archive" || continue
  target="$work/$(basename "$archive")"
  mkdir "$target"
  case "$archive" in
    *.zip) unzip -q "$archive" -d "$target" ;;
    *) tar -xf "$archive" -C "$target" ;;
  esac
done

if find "$work" -type f \( -name 'config.json' -o -name '.env' -o -name 'control.sock' -o -name '*.trace' -o -name '*.png' \) | grep .; then
  echo "release contains a forbidden runtime or private artifact" >&2
  exit 1
fi

pattern='(/home/runner/|/Users/runner/|[A-Za-z]:\\a\\|postgres(ql)?://[^[:space:]]+@|QUACKRIDGE_SECRET_[A-Z0-9_]+=[^[:space:]]+|Bearer[[:space:]]+[A-Za-z0-9_.-]{24,})'
if command -v rg >/dev/null 2>&1; then
  if rg --text --line-number "$pattern" "$work" "$dist" \
    --glob '*.md' --glob '*.json' --glob '*.yaml' --glob '*.rb' \
    --glob '*.sha256' --glob '*.txt' --glob '*.upstream'; then
    echo "release contains a private path or credential-shaped value" >&2
    exit 1
  fi
else
  for file in $(find "$work" "$dist" -type f \( -name '*.md' -o -name '*.json' -o -name '*.yaml' -o -name '*.rb' -o -name '*.sha256' -o -name '*.txt' -o -name '*.upstream' \)); do
    if grep -EIna "$pattern" "$file"; then
      echo "release contains a private path or credential-shaped value" >&2
      exit 1
    fi
  done
fi
