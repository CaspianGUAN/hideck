#!/usr/bin/env python3
"""Download pinned OpenWrt SDKs; only verified archives enter the shared cache."""

import argparse
from dataclasses import dataclass
import hashlib
from pathlib import Path
import re
import subprocess
import sys
import time
from urllib.parse import urlsplit


CONNECT_TIMEOUT_SECONDS = 20
DEFAULT_ATTEMPT_SECONDS = 600
# Release runners have sustained only 80 KB/s for SDK archives over 200 MB.
DEFAULT_TOTAL_SECONDS = 3600
DEFAULT_ATTEMPTS = 6
RETRY_DELAY_SECONDS = 5
HASH_BLOCK_BYTES = 1024 * 1024
# Transport failures only. HTTP, certificate and unsupported Range errors surface.
RETRYABLE_CURL_ERRORS = {6, 7, 18, 28, 35, 52, 55, 56, 92}


@dataclass(frozen=True)
class Download:
    url: str
    sha256: str
    cache_dir: Path
    seed_dir: Path | None = None
    attempt_seconds: int = DEFAULT_ATTEMPT_SECONDS
    total_seconds: int = DEFAULT_TOTAL_SECONDS
    attempts: int = DEFAULT_ATTEMPTS
    retry_delay: float = RETRY_DELAY_SECONDS


def matches_digest(path, digest):
    result = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(HASH_BLOCK_BYTES), b""):
            result.update(block)
    return result.hexdigest() == digest


def checked_cache(directory, digest):
    archive = directory / f"{digest}.tar.zst"
    if not archive.exists():
        return None
    if not matches_digest(archive, digest):
        raise RuntimeError(f"SDK cache SHA-256 mismatch: {archive}; remove the invalid cache before retrying")
    print(f"Verified SDK cache: {archive}", file=sys.stderr)
    return archive


def curl_attempt(spec, partial, remaining):
    timeout = min(spec.attempt_seconds, remaining)
    command = [
        "curl", "--disable", "--http1.1", "--fail", "--location", "--silent", "--show-error",
        "--proto", "=https", "--proto-redir", "=https", "--retry", "0",
        "--continue-at", "-", "--connect-timeout", str(min(CONNECT_TIMEOUT_SECONDS, timeout)),
        "--max-time", str(timeout), "--output", str(partial), spec.url,
    ]
    try:
        return subprocess.run(command, check=False, timeout=remaining).returncode
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(f"SDK download total deadline exceeded; partial file retained: {partial}") from error


def resume_download(spec, archive):
    partial = archive.with_suffix(archive.suffix + ".part")
    if partial.exists() and matches_digest(partial, spec.sha256):
        partial.replace(archive)
        return archive
    deadline = time.monotonic() + spec.total_seconds
    for attempt in range(1, spec.attempts + 1):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        offset = partial.stat().st_size if partial.exists() else 0
        print(f"SDK download attempt {attempt}/{spec.attempts}, resume offset {offset} bytes", file=sys.stderr)
        code = curl_attempt(spec, partial, remaining)
        if partial.exists() and matches_digest(partial, spec.sha256):
            partial.replace(archive)
            return archive
        if code == 0:
            raise RuntimeError(f"SDK SHA-256 mismatch; unverified file retained: {partial}")
        if code not in RETRYABLE_CURL_ERRORS:
            raise RuntimeError(f"SDK download failed (curl {code}); partial file retained: {partial}")
        if attempt == spec.attempts:
            break
        delay = min(spec.retry_delay, max(0, deadline - time.monotonic()))
        print(f"SDK transport failure (curl {code}); retrying from saved bytes in {delay:.1f}s", file=sys.stderr)
        time.sleep(delay)
    raise RuntimeError(f"SDK download retry/time budget exhausted; partial file retained: {partial}")


def download(spec):
    spec.cache_dir.mkdir(parents=True, exist_ok=True)
    directories = [spec.cache_dir]
    if spec.seed_dir is not None:
        directories.append(spec.seed_dir)
    for directory in directories:
        cached = checked_cache(directory, spec.sha256)
        if cached is not None:
            return cached
    archive = spec.cache_dir / f"{spec.sha256}.tar.zst"
    return resume_download(spec, archive)


def positive_integer(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return number


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--cache-dir", required=True, type=Path)
    parser.add_argument("--seed-dir", type=Path, help="Optional read-only verified archive directory")
    parser.add_argument("--attempt-seconds", type=positive_integer, default=DEFAULT_ATTEMPT_SECONDS)
    parser.add_argument("--total-seconds", type=positive_integer, default=DEFAULT_TOTAL_SECONDS)
    parser.add_argument("--attempts", type=positive_integer, default=DEFAULT_ATTEMPTS)
    args = parser.parse_args()
    url = urlsplit(args.url)
    valid_path = re.fullmatch(r"/releases/[A-Za-z0-9._/-]+/openwrt-sdk-[A-Za-z0-9._-]+\.tar\.zst", url.path)
    if url.scheme != "https" or url.netloc != "downloads.openwrt.org" or not valid_path or url.query or url.fragment:
        parser.error("URL must identify an official HTTPS OpenWrt release SDK")
    if not re.fullmatch(r"[a-f0-9]{64}", args.sha256):
        parser.error("SHA-256 must be 64 lowercase hexadecimal characters")
    return Download(**vars(args))


def main():
    try:
        print(download(parse_args()).resolve())
    except (RuntimeError, OSError) as error:
        print(f"SDK download error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
