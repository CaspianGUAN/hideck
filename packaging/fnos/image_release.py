"""Resolve a published multiarch image and its matching Git configuration."""

from dataclasses import dataclass
import json
import subprocess

from validation import image_digest, release_version, source_revision


IMAGE_REPOSITORY = "yibaiba/hideck"
SOURCE_URL = "https://github.com/yibaiba/hideck"
ARCHITECTURES = ("amd64", "arm64")
COMMAND_TIMEOUT_SECONDS = 60


@dataclass(frozen=True)
class PlatformImage:
    architecture: str
    digest: str


@dataclass(frozen=True)
class Release:
    version: str
    digest: str
    revision: str
    platforms: tuple
    config: bytes
    license: bytes

    @property
    def image(self):
        return f"{IMAGE_REPOSITORY}:{self.version}@{self.digest}"


def platform_images(manifest):
    entries = manifest.get("manifests")
    if not isinstance(entries, list) or any(not isinstance(entry, dict) for entry in entries):
        raise ValueError("Registry did not return a multiarch manifest list")
    selected = []
    for architecture in ARCHITECTURES:
        candidates = [entry for entry in entries
                      if isinstance(entry.get("platform"), dict) and entry["platform"].get("os") == "linux"
                      and entry["platform"].get("architecture") == architecture]
        if len(candidates) != 1:
            raise ValueError(f"Expected exactly one linux/{architecture} image")
        selected.append(PlatformImage(architecture, image_digest(candidates[0].get("digest"))))
    return tuple(selected)


def validate_image_config(config, *, platform, version, revision):
    if config.get("os") != "linux" or config.get("architecture") != platform.architecture:
        raise ValueError(f"Image configuration architecture differs: {platform.architecture}")
    image_config = config.get("config")
    labels = image_config.get("Labels") if isinstance(image_config, dict) else None
    if not isinstance(labels, dict):
        raise ValueError(f"linux/{platform.architecture} image has no provenance labels")
    expected = {"org.opencontainers.image.revision": revision,
                "org.opencontainers.image.source": SOURCE_URL}
    for key, value in expected.items():
        if labels.get(key) != value:
            raise ValueError(f"linux/{platform.architecture} image {key} does not match release")
    if labels.get("org.opencontainers.image.version") not in (version, f"v{version}"):
        raise ValueError(f"linux/{platform.architecture} image version does not match release")


class ReleaseSource:
    def __init__(self, *, root, run):
        self.root = root
        self.run = run

    def command(self, arguments):
        try:
            return self.run(arguments, cwd=self.root, check=True, capture_output=True,
                            timeout=COMMAND_TIMEOUT_SECONDS).stdout
        except subprocess.CalledProcessError as error:
            detail = (error.stderr or b"").decode(errors="replace").strip()
            raise RuntimeError(f"Release metadata lookup failed: {detail or error}") from error

    def inspect(self, reference, field):
        result = self.command(["docker", "buildx", "imagetools", "inspect", reference,
                               "--format", "{{json ." + field + "}}"])
        parsed = json.loads(result)
        if not isinstance(parsed, dict):
            raise ValueError(f"Invalid image {field} response")
        return parsed

    def resolve(self, version, *, digest=None, revision=None):
        version = release_version(version)
        tag = f"refs/tags/v{version}^{{commit}}"
        commit = source_revision(self.command(["git", "rev-parse", "--verify", tag]).decode().strip())
        if revision is not None and source_revision(revision) != commit:
            raise ValueError("Published source revision does not match the release tag")
        reference = f"{IMAGE_REPOSITORY}:{version}"
        if digest is not None:
            reference = f"{IMAGE_REPOSITORY}@{image_digest(digest)}"
        manifest = self.inspect(reference, "Manifest")
        resolved_digest = image_digest(manifest.get("digest", ""))
        if digest is not None and resolved_digest != digest:
            raise ValueError("Registry returned a different image digest")
        platforms = platform_images(manifest)
        # Every subsequent inspection uses immutable child digests, not the tag.
        for platform in platforms:
            config = self.inspect(f"{IMAGE_REPOSITORY}@{platform.digest}", "Image")
            validate_image_config(config, platform=platform, version=version, revision=commit)
        return Release(version, resolved_digest, commit, platforms,
                       self.command(["git", "show", f"{commit}:config/config.example.yaml"]),
                       self.command(["git", "show", f"{commit}:LICENSE"]))
