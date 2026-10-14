#!/bin/sh
# Run inside a fresh OpenWrt SDK, with a previously built musl-static HiDeck.
set -eu

SOURCE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SDK_DIR=${SDK_DIR:?SDK_DIR is required}
HIDECK_BINARY=${HIDECK_BINARY:?HIDECK_BINARY is required}
HIDECK_VERSION=${HIDECK_VERSION:?HIDECK_VERSION is required}
OUTPUT_DIR=${OUTPUT_DIR:?OUTPUT_DIR is required}
JOBS=${JOBS:-2}
HIDECK_VERSION=${HIDECK_VERSION#v}
package_version=$HIDECK_VERSION
# Preserve the existing "dev" release workflow; APK requires a numeric version.
if [ "$package_version" = dev ]; then package_version=0.0.0; fi

case "$HIDECK_VERSION" in ""|*[!A-Za-z0-9._-]*) echo 'Invalid HiDeck version' >&2; exit 1 ;; esac
case "$JOBS" in ""|*[!0-9]*|0) echo 'Invalid JOBS' >&2; exit 1 ;; esac
[ "$JOBS" -gt 0 ] || { echo 'JOBS must be positive' >&2; exit 1; }
if [ ! -f "$SDK_DIR/include/package.mk" ]; then
  echo "Not an OpenWrt SDK: $SDK_DIR" >&2
  exit 1
fi
if [ ! -f "$HIDECK_BINARY" ]; then
  echo "HiDeck binary not found: $HIDECK_BINARY" >&2
  exit 1
fi
if [ ! -f "$SDK_DIR/.config" ]; then
  make -C "$SDK_DIR" defconfig
fi

# Reject host/glibc binaries before they can be wrapped in an OpenWrt package.
binary_info=$(file -b "$HIDECK_BINARY")
case "$binary_info" in *ELF*statically\ linked*) ;; *) echo 'Expected a static Linux ELF binary' >&2; exit 1 ;; esac
sdk_arch=$(sed -n 's/^CONFIG_ARCH="\([^"]*\)"$/\1/p' "$SDK_DIR/.config")
case "$sdk_arch:$binary_info" in
  x86_64:*ELF\ 64-bit*x86-64*) binarch=amd64 ;;
  aarch64:*ELF\ 64-bit*aarch64*) binarch=arm64 ;;
  arm:*ELF\ 32-bit*ARM*) binarch=armv7 ;;
  *) echo "Binary does not match SDK architecture: $sdk_arch" >&2; exit 1 ;;
esac
binary_hash=$(sha256sum "$HIDECK_BINARY" | cut -d ' ' -f 1)
binary_source="hideck_v${HIDECK_VERSION}_openwrt_${binarch}"

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR=$(CDPATH= cd -- "$OUTPUT_DIR" && pwd)
if [ -n "$(ls -A "$OUTPUT_DIR")" ]; then
  echo 'OUTPUT_DIR must be empty to avoid mixing SDK releases or old packages' >&2
  exit 1
fi
SDK_DIR=$(CDPATH= cd -- "$SDK_DIR" && pwd)
for package in hideck hideck-adb; do
  if [ -e "$SDK_DIR/package/$package" ]; then
    echo "Use a fresh SDK; refusing to overwrite package/$package" >&2
    exit 1
  fi
done
mkdir -p "$SDK_DIR/dl"
cp "$HIDECK_BINARY" "$SDK_DIR/dl/$binary_source"
cp -R "$SOURCE_DIR/hideck" "$SOURCE_DIR/hideck-adb" "$SDK_DIR/package/"
cd "$SDK_DIR"

# SDK feed revisions are pinned in its feeds.conf.default.
./scripts/feeds update base packages
./scripts/feeds install libqmi alsa-utils liblz4 libzstd protobuf libpcre2 libusb-1.0 zlib
printf '\nCONFIG_ALL=n\nCONFIG_PACKAGE_hideck=m\nCONFIG_PACKAGE_hideck-adb=m\nCONFIG_PACKAGE_hideck-modem-voice=m\n' >> .config
make HIDECK_VERSION="$HIDECK_VERSION" HIDECK_PACKAGE_VERSION="$package_version" HIDECK_BINARY_SHA256="$binary_hash" defconfig
make -j"$JOBS" HIDECK_ADB_JOBS="$JOBS" HIDECK_VERSION="$HIDECK_VERSION" HIDECK_PACKAGE_VERSION="$package_version" HIDECK_BINARY_SHA256="$binary_hash" \
  package/hideck-adb/compile V=s
# HiDeck is already a verified static executable and the voice package is a
# dependency manifest. Package them without rebuilding system QMI/kernel tools;
# their declared runtime dependencies remain enforced by opkg/apk on installation.
make HIDECK_VERSION="$HIDECK_VERSION" HIDECK_PACKAGE_VERSION="$package_version" HIDECK_BINARY_SHA256="$binary_hash" \
  NO_DEPS=1 package/hideck/compile V=s

package_count=0
for package in $(find bin/packages -type f \( -name 'hideck*.ipk' -o -name 'hideck*.apk' \)); do
  cp "$package" "$OUTPUT_DIR/"
  package_count=$((package_count + 1))
done
if [ "$package_count" -ne 3 ]; then
  echo "Expected hideck, hideck-adb and hideck-modem-voice packages; got $package_count" >&2
  exit 1
fi
SDK_DIR="$SDK_DIR" OUTPUT_DIR="$OUTPUT_DIR" sh "$SOURCE_DIR/sign-apks.sh"
cd "$OUTPUT_DIR"
find . -type f \( -name '*.ipk' -o -name '*.apk' -o -name '*.pem' \) -exec sha256sum {} \; > SHA256SUMS
printf 'Packages built in %s; install dependencies from the matching OpenWrt feeds.\n' "$OUTPUT_DIR"
