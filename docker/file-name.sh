#!/bin/sh
set -eu

fail() {
    printf 'Fluxgate Docker artifact: %s\n' "$*" >&2
    exit 1
}

case "${TARGETPLATFORM:-}" in
    "linux/amd64")
        arch="amd64-v1"
        ;;
    "linux/386")
        arch="386"
        ;;
    "linux/arm64")
        arch="arm64"
        ;;
    "linux/arm/v7")
        arch="armv7"
        ;;
    "linux/riscv64")
        arch="riscv64"
        ;;
    *)
        fail "unsupported target platform: ${TARGETPLATFORM:-unset}"
        ;;
esac

[ -r bin/version.txt ] || fail "missing bin/version.txt"
version=$(cat bin/version.txt)
case "$version" in
    ""|*[!A-Za-z0-9._-]*) fail "invalid version in bin/version.txt" ;;
esac
file_name="bin/fluxgate-linux-$arch-$version.gz"
[ -f "$file_name" ] && [ -r "$file_name" ] || fail "missing artifact: $file_name"
printf '%s\n' "$file_name"
