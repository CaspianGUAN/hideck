#!/usr/bin/env python3
"""Real fnpack/Compose contract test; fixture packages are temporary and never published."""

import argparse
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

from build import prepare_package
from image_release import Release


def inspect_archive(path, *, release):
    with tarfile.open(path) as archive:
        files = {member.name.removeprefix("./"): member for member in archive.getmembers()}
        assert files["cmd/main"].mode & 0o111, "Lifecycle script lost its executable mode"
        app = archive.extractfile(files["app.tgz"]).read()
    with tarfile.open(fileobj=io.BytesIO(app)) as archive:
        files = {member.name.removeprefix("./"): member for member in archive.getmembers()}
        assert archive.extractfile(files["defaults/config.example.yaml"]).read() == release.config
        compose = archive.extractfile(files["docker/docker-compose.yaml"]).read().decode()
        assert release.image in compose, "FPK did not retain the immutable image reference"


def check_package(tool):
    fixture = Release("0.0.0-fpk-test", "sha256:" + "a" * 64, "b" * 40, (),
                      b"# fnpack format test, not a release\nserver:\n  port: 17575\n", b"Test fixture\n")
    with tempfile.TemporaryDirectory(prefix="hideck-fnpack-test-") as directory:
        root = Path(directory)
        stage = root / "hideck"
        prepare_package(stage, release=fixture, port=17575)
        subprocess.run([tool, "build", "--directory", str(stage)], cwd=root, check=True, timeout=60)
        packages = list(root.rglob("*.fpk"))
        assert len(packages) == 1, "fnpack did not produce one package"
        inspect_archive(packages[0], release=fixture)
        result = subprocess.run(["docker", "compose", "-f", str(stage / "app/docker/docker-compose.yaml"),
                                 "config", "--format", "json"], check=True, capture_output=True,
                                timeout=30, env=dict(os.environ, TRIM_PKGETC=str(root / "config"),
                                                    TRIM_PKGVAR=str(root / "var"), TRIM_SERVICE_PORT="17575"))
        service = json.loads(result.stdout)["services"]["hideck"]
        assert service["image"] == fixture.image
        assert service["environment"]["PROXY_SERVER_PORT"] == "17575"
        assert service["healthcheck"]["test"][-1] == "http://127.0.0.1:17575/ping"
    print("Official fnpack archive and Compose checks passed; no release artifact retained")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fnpack", required=True)
    check_package(str(Path(parser.parse_args().fnpack).resolve()))
