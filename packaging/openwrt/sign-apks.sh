#!/bin/sh
# APK v3 packages need a trusted signature; never distribute an unsigned bundle.
set -eu

SDK_DIR=${SDK_DIR:?SDK_DIR is required}
OUTPUT_DIR=${OUTPUT_DIR:?OUTPUT_DIR is required}
set -- "$OUTPUT_DIR"/*.apk
[ -f "$1" ] || exit 0
apk_tool="$SDK_DIR/staging_dir/host/bin/apk"
test -x "$apk_tool"

sign_dir=$(mktemp -d)
trap 'rm -f "$sign_dir/key.pem"; rmdir "$sign_dir"' EXIT
trap 'exit 1' HUP INT TERM
umask 077
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$sign_dir/key.pem"
mkdir -p "$OUTPUT_DIR/keys"
openssl pkey -in "$sign_dir/key.pem" -pubout -out "$OUTPUT_DIR/keys/hideck-build.pem"
chmod 755 "$OUTPUT_DIR/keys"
chmod 644 "$OUTPUT_DIR/keys/hideck-build.pem"
# SDK mkpkg output is unsigned input. This flag is scoped to adding signatures,
# never installation or the mandatory verification below.
# apk-tools 3.0.5 keeps signatures_written across input files. Sign each file
# in its own process so every package gets this build's signature.
for package in "$@"; do
  "$apk_tool" adbsign --allow-untrusted --sign-key "$sign_dir/key.pem" "$package"
done
"$apk_tool" --keys-dir "$OUTPUT_DIR/keys" verify "$@"
