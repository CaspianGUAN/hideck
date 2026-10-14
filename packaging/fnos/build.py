#!/usr/bin/env python3
"""Build a Docker-based fnOS package with the official fnpack tool."""

import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

from check_image import check_release
from image_release import ReleaseSource
from validation import DEFAULT_HTTP_PORT, http_port, release_version


PACKAGING = Path(__file__).resolve().parent
ROOT = PACKAGING.parents[1]
BUILD_TIMEOUT_SECONDS = 60


def prepare_package(destination, *, release, port):
    shutil.copytree(PACKAGING / "package", destination)
    defaults = destination / "app/defaults"
    defaults.mkdir()
    (defaults / "config.example.yaml").write_bytes(release.config)
    (destination / "LICENSE").write_bytes(release.license)
    metadata = {"version": release.version, "image": release.image,
                "source_revision": release.revision,
                "config_sha256": hashlib.sha256(release.config).hexdigest()}
    (defaults / "release.json").write_text(json.dumps(metadata, indent=2) + "\n")
    for relative in ("manifest", "app/ui/config", "app/docker/docker-compose.yaml",
                     "wizard/install", "wizard/config"):
        path = destination / relative
        content = (path.read_text().replace("@VERSION@", release.version)
                   .replace("@IMAGE@", release.image).replace("@HTTP_PORT@", str(port)))
        path.write_text(content)
    prepare_install_wizard(destination, port=port)
    for directory in (destination / "cmd", destination / "app/bin"):
        for script in directory.iterdir():
            script.chmod(0o755)
    images = destination / "app/ui/images"
    images.mkdir()
    for size, name in ((64, "ICON.PNG"), (256, "ICON_256.PNG")):
        source = PACKAGING / "assets" / name
        shutil.copyfile(source, destination / name)
        shutil.copyfile(source, images / f"icon_{size}.png")


def prepare_install_wizard(destination, *, port):
    installation = destination / "wizard/install"
    steps = json.loads(installation.read_text())
    entry_steps = json.loads((destination / "wizard/config").read_text())
    defaults = {"wizard_entry_protocol": "http", "wizard_entry_port": str(port)}
    for item in entry_steps[0]["items"]:
        if item.get("field") in defaults:
            item["initValue"] = defaults[item["field"]]
    installation.write_text(json.dumps(steps + entry_steps, ensure_ascii=False, indent=2) + "\n")


def build_package(options):
    tool = shutil.which(options.fnpack)
    if tool is None:
        raise FileNotFoundError("fnpack not found; install the official fnpack tool or pass --fnpack")
    output = options.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    artifact = output / f"hideck_{options.version}_fnos.fpk"
    checksums = artifact.with_suffix(".fpk.sha256")
    if artifact.exists() or checksums.exists():
        raise FileExistsError(f"Refusing to replace an existing package or checksum: {artifact}")
    source = ReleaseSource(root=ROOT, run=subprocess.run)
    release = source.resolve(options.version, digest=options.image_digest, revision=options.source_revision)
    check_release(release, run=subprocess.run)
    with tempfile.TemporaryDirectory(prefix="hideck-fnos-") as directory:
        stage = Path(directory) / "hideck"
        prepare_package(stage, release=release, port=options.http_port)
        subprocess.run([tool, "build", "--directory", str(stage)], cwd=directory,
                       check=True, timeout=BUILD_TIMEOUT_SECONDS)
        produced = list(Path(directory).rglob("*.fpk"))
        if len(produced) != 1 or produced[0].stat().st_size == 0:
            raise RuntimeError("fnpack did not produce exactly one nonempty FPK")
        # Exclusive create protects an output concurrently produced by another build.
        with produced[0].open("rb") as source, artifact.open("xb") as target:
            shutil.copyfileobj(source, target)
    digest = hashlib.sha256(artifact.read_bytes()).hexdigest()
    with checksums.open("x") as target:
        target.write(f"{digest}  {artifact.name}\n")
    print(f"Built {artifact}; pinned image {release.image}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, type=release_version)
    parser.add_argument("--http-port", type=http_port, default=DEFAULT_HTTP_PORT)
    parser.add_argument("--image-digest", help="Digest supplied by the Docker publishing job")
    parser.add_argument("--source-revision", help="Source commit supplied by the Docker publishing job")
    parser.add_argument("--fnpack", default="fnpack")
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    try:
        build_package(options)
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        parser.exit(1, f"fnOS package build failed: {error}\n")


if __name__ == "__main__":
    main()
