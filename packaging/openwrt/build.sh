#!/bin/sh
# Build a musl HiDeck binary for OpenWrt. Do not UPX: UPX 5 stubs
# need glibc and fail on musl with "Not a valid dynamic program".
#
# Required env: OUT
# Optional: VERSION BUILD_TIME GOARCH GOARM CC LINK_MODE (static or dynamic)
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo unknown)}
BUILD_TIME=${BUILD_TIME:-$(date "+%Y-%m-%d %H:%M:%S")}
GOARCH=${GOARCH:-amd64}
GOARM=${GOARM:-}
OUT=${OUT:?OUT is required}
CC=${CC:-musl-gcc}
LINK_MODE=${LINK_MODE:-static}
case "$LINK_MODE" in
  static) external_flags='-extldflags -static' ;;
  dynamic) external_flags='' ;;
  *) printf 'LINK_MODE must be static or dynamic\n' >&2; exit 1 ;;
esac

if [ ! -d internal/web/dist ]; then
  printf 'missing internal/web/dist; build frontend first\n' >&2
  exit 1
fi

mkdir -p "$(dirname -- "$OUT")"

export GOWORK=off
export CGO_ENABLED=1
export GOOS=linux
export GOARCH
if [ -n "${GOARM:-}" ]; then
  export GOARM
else
  unset GOARM
fi
export CC

go build -trimpath -buildvcs=false -tags "with_utls nomsgpack netgo osusergo" \
  -ldflags "-s -w -linkmode external ${external_flags} -X 'github.com/yibaiba/hideck/internal/global.Version=${VERSION}' -X 'github.com/yibaiba/hideck/internal/global.BuildTime=${BUILD_TIME}'" \
  -o "$OUT" ./cmd/hideck

if command -v file >/dev/null 2>&1; then
  file "$OUT"
  binary_info=$(file -b "$OUT")
  case "$LINK_MODE:$binary_info" in
    static:*ELF*statically\ linked*) ;;
    dynamic:*ELF*dynamically\ linked*interpreter\ /lib/ld-musl-*) ;;
    *)
      printf 'OpenWrt binary does not match musl LINK_MODE=%s:\n%s\n' "$LINK_MODE" "$binary_info" >&2
      exit 1
      ;;
  esac
fi
