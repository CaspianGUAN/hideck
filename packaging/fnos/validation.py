"""Validation at the FPK build input boundary."""

import argparse
import re


DEFAULT_HTTP_PORT = 7575
MAX_PORT = 65535


def release_version(value):
    version = value.removeprefix("v")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?", version):
        raise argparse.ArgumentTypeError("Use a release version, not latest/dev")
    return version


def http_port(value):
    try:
        port = int(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("HTTP port must be an integer") from error
    if not 1 <= port <= MAX_PORT:
        raise argparse.ArgumentTypeError("HTTP port must be between 1 and 65535")
    return port


def image_digest(value):
    if not isinstance(value, str) or not re.fullmatch(r"sha256:[a-f0-9]{64}", value):
        raise ValueError("Image digest must be sha256 followed by 64 lowercase hex digits")
    return value


def source_revision(value):
    if not isinstance(value, str) or not re.fullmatch(r"[a-f0-9]{40}", value):
        raise ValueError("Source revision must be a full Git commit SHA")
    return value
