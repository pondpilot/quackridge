#!/bin/sh
set -eu

duckdb_version=1.5.5
httpfs_version=827222f
postgres_scanner_version=41223e5
mysql_scanner_version=7267164
sqlite_scanner_version=f79b1db
odbc_scanner_version=274a330
quack_version=c154811
output_dir=${1:-extensions}
platform=${QUACKRIDGE_EXTENSION_PLATFORM:-}

if [ -z "$platform" ]; then
  os=$(uname -s)
  arch=$(uname -m)
  case "$os/$arch" in
    Linux/x86_64) platform=linux_amd64 ;;
    Darwin/x86_64) platform=osx_amd64 ;;
    Darwin/arm64) platform=osx_arm64 ;;
    MINGW*/*|MSYS*/*|CYGWIN*/*) echo "Windows MinGW extension bundles are not available for DuckDB $duckdb_version" >&2; exit 2 ;;
    *) echo "unsupported extension platform: $os/$arch" >&2; exit 2 ;;
  esac
fi

expected_hash() {
  case "$platform/$1" in
    linux_amd64/httpfs) echo 7cdd52a3135388718884a9b71e3987ba723002121e8e9de399c4ed619d824a05 ;;
    linux_amd64/postgres_scanner) echo e0f631a5535f165468bc8a20501f8bc1490adbc877d38fcdff2f8d05531e1e5b ;;
    linux_amd64/mysql_scanner) echo 191855fa6dcb0972d9194700b152615e86ab76b29b4573aa369d88f2ca28682c ;;
    linux_amd64/sqlite_scanner) echo 01292812092200c2d0b76324df9568d336ddaa5a198e7cc8fed124e84088e14e ;;
    linux_amd64/odbc_scanner) echo f8a19a1c58ac3d69c60bd4fd3cd4def33bae2db17e4786fe8a1aa9cc05405d39 ;;
    linux_amd64/quack) echo 7b2c417e3797c2d85673655dea420ead9bbbb24e686ee8dbe37bef9fa8768207 ;;
    osx_amd64/httpfs) echo f445c2692f863bff82609c7061e6e273a4d9fd3b6695e56a6ebc18bd502ed464 ;;
    osx_amd64/postgres_scanner) echo b8764ed496be635fbac3e5e6a6c8e3e3c2dbfcaa862ce0422c21cad6fcc6c353 ;;
    osx_amd64/mysql_scanner) echo 8066f3a8545ade296bd5883682d9f9b8ace26ebd40d9c9457ae7048a1c8f1b78 ;;
    osx_amd64/sqlite_scanner) echo 1b96e4ac03a4394708166f75236614a80fd1f9ab810fb3f35ea7aa5a9a833501 ;;
    osx_amd64/odbc_scanner) echo 889632486fe610c1ece683708c24350e92b589145e184c61ba783dde1b77ba9a ;;
    osx_amd64/quack) echo db551622587e8678f935aff276a4d8c42b6b0d909da854e071ab94db92eb49d1 ;;
    osx_arm64/httpfs) echo 758acc0b0c4fbf09506f387ff6f52826b1038b7b6849ded39928d2f992945230 ;;
    osx_arm64/postgres_scanner) echo 4fb5079e67b00e6643e6ee91545a355010004d1dad50b43f1c060de0cb789c8e ;;
    osx_arm64/mysql_scanner) echo 653cdf0780e0a399017f9accc011998ace3055c5e86bf742537bacf3748e9b70 ;;
    osx_arm64/sqlite_scanner) echo d7514249b0cce24bb63856b4c752a889ef2f739c6fd821109988e4e13afd7058 ;;
    osx_arm64/odbc_scanner) echo 5c1d9a7966fb4ea3b8c7b179de51c2a3cb8030cfb8f9925fc17f22d1afcdb960 ;;
    osx_arm64/quack) echo a551db5ca9db6964a48f3c1f77076be0875bbdb0f335b139f77798c8fa92df51 ;;
    *) echo "missing pinned checksum for $platform/$1" >&2; exit 2 ;;
  esac
}

digest() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

mkdir -p "$output_dir"
: > "$output_dir/extensions.sha256"
: > "$output_dir/extensions.upstream"
cat > "$output_dir/extensions.versions" <<EOF
duckdb $duckdb_version
httpfs $httpfs_version
postgres_scanner $postgres_scanner_version
mysql_scanner $mysql_scanner_version
sqlite_scanner $sqlite_scanner_version
odbc_scanner $odbc_scanner_version
quack $quack_version
EOF
for extension in httpfs mysql_scanner odbc_scanner postgres_scanner quack sqlite_scanner; do
  if [ "$extension" = odbc_scanner ]; then
    url="https://nightly-extensions.duckdb.org/odbc_scanner/${odbc_scanner_version}/v1.2.0/${platform}/${extension}.duckdb_extension.gz"
  else
    url="https://extensions.duckdb.org/v${duckdb_version}/${platform}/${extension}.duckdb_extension.gz"
  fi
  archive="$output_dir/${extension}.duckdb_extension.gz"
  temporary="$archive.download"
  curl --fail --location --silent --show-error "$url" --output "$temporary"
  actual=$(digest "$temporary")
  expected=$(expected_hash "$extension")
  if [ "$actual" != "$expected" ]; then
    rm -f "$temporary"
    echo "checksum drift for $url: expected $expected, got $actual" >&2
    exit 1
  fi
  mv "$temporary" "$archive"
  gzip -dc "$archive" > "$output_dir/${extension}.duckdb_extension"
  decompressed_hash=$(digest "$output_dir/${extension}.duckdb_extension")
  printf '%s  %s\n' "$decompressed_hash" "${extension}.duckdb_extension" >> "$output_dir/extensions.sha256"
  printf '%s\t%s\t%s\n' "$extension" "$url" "$expected" >> "$output_dir/extensions.upstream"
  printf '%s  %s\n' "$expected" "${extension}.duckdb_extension.gz"
done
versions_hash=$(digest "$output_dir/extensions.versions")
printf '%s  %s\n' "$versions_hash" "extensions.versions" >> "$output_dir/extensions.sha256"
