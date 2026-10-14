"""Registry/config binding and isolated image-tool validation contracts."""

import json
from pathlib import Path
import subprocess
import sys
import unittest
from unittest.mock import Mock


ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "packaging/fnos"))
from check_image import CHECK_TIMEOUT_SECONDS, check_platform
from image_release import PlatformImage, ReleaseSource, SOURCE_URL, platform_images, validate_image_config


class ImageReleaseTests(unittest.TestCase):
    def setUp(self):
        self.revision = "b" * 40
        self.digest = "sha256:" + "a" * 64
        self.children = {"amd64": "sha256:" + "c" * 64, "arm64": "sha256:" + "d" * 64}
        self.manifest = {"digest": self.digest, "manifests": [
            {"digest": digest, "platform": {"os": "linux", "architecture": architecture}}
            for architecture, digest in self.children.items()]}
        self.calls = []

    def image_config(self, architecture):
        return {"architecture": architecture, "os": "linux", "config": {"Labels": {
            "org.opencontainers.image.source": SOURCE_URL,
            "org.opencontainers.image.revision": self.revision,
            "org.opencontainers.image.version": "v2.1.24"}}}

    def run_command(self, arguments, **options):
        self.calls.append(arguments)
        self.assertLessEqual(options["timeout"], 60)
        if arguments[0:2] == ["git", "rev-parse"]:
            data = self.revision.encode()
        elif arguments[0:2] == ["git", "show"]:
            self.assertTrue(arguments[2].startswith(self.revision + ":"))
            data = b"release bytes"
        elif arguments[-1] == "{{json .Manifest}}":
            data = json.dumps(self.manifest).encode()
        else:
            architecture = next(arch for arch, digest in self.children.items() if arguments[4].endswith(digest))
            data = json.dumps(self.image_config(architecture)).encode()
        return subprocess.CompletedProcess(arguments, 0, stdout=data)

    def test_resolve_binds_config_and_both_child_images_to_one_commit(self):
        source = ReleaseSource(root=ROOT, run=self.run_command)
        release = source.resolve("2.1.24")
        self.assertEqual(release.digest, self.digest)
        self.assertEqual(release.config, b"release bytes")
        self.assertEqual(len(release.platforms), 2)
        image_calls = [call for call in self.calls if call[-1] == "{{json .Image}}"]
        self.assertTrue(all("@sha256:" in call[4] for call in image_calls))

    def test_publisher_digest_is_used_instead_of_mutable_tag(self):
        ReleaseSource(root=ROOT, run=self.run_command).resolve("2.1.24", digest=self.digest, revision=self.revision)
        manifest_call = next(call for call in self.calls if call[-1] == "{{json .Manifest}}")
        self.assertEqual(manifest_call[4], "yibaiba/hideck@" + self.digest)

    def test_publisher_revision_and_digest_mismatch_are_rejected(self):
        for options in ({"revision": "e" * 40}, {"digest": "sha256:" + "e" * 64}):
            with self.assertRaises(ValueError):
                ReleaseSource(root=ROOT, run=self.run_command).resolve("2.1.24", **options)

    def test_missing_and_ambiguous_platforms_are_rejected(self):
        for entries in (self.manifest["manifests"][:1], self.manifest["manifests"] * 2):
            with self.assertRaises(ValueError):
                platform_images({"manifests": entries})

    def test_each_architecture_must_match_release(self):
        for field in ("revision", "version", "source"):
            config = self.image_config("arm64")
            config["config"]["Labels"]["org.opencontainers.image." + field] = "wrong"
            with self.assertRaises(ValueError):
                validate_image_config(config, platform=PlatformImage("arm64", self.children["arm64"]),
                                      version="2.1.24", revision=self.revision)

    def test_tool_checks_have_no_host_access_and_do_not_launch_hideck(self):
        run = Mock()
        check_platform(PlatformImage("amd64", self.children["amd64"]), run=run)
        command = run.call_args_list[-1].args[0]
        for option in ("--network=none", "--read-only", "--cap-drop=ALL", "--entrypoint=/bin/sh"):
            self.assertIn(option, command)
        for option in ("--privileged", "--device", "--mount", "--volume"):
            self.assertNotIn(option, command)
        self.assertNotIn("/usr/local/bin/hideck -", command[-1])
        self.assertIn("arecord --version", command[-1])
        self.assertEqual(run.call_args.kwargs["timeout"], CHECK_TIMEOUT_SECONDS)

    def test_missing_image_dependency_propagates_failure(self):
        run = Mock(side_effect=[None, subprocess.CalledProcessError(1, "docker run")])
        with self.assertRaisesRegex(RuntimeError, "dependency check failed"):
            check_platform(PlatformImage("amd64", self.children["amd64"]), run=run)

    def test_malformed_registry_metadata_is_reported(self):
        for entries in (None, {}, [None]):
            with self.assertRaisesRegex(ValueError, "manifest list"):
                platform_images({"manifests": entries})
        config = self.image_config("amd64")
        config["config"]["Labels"] = None
        with self.assertRaisesRegex(ValueError, "provenance labels"):
            validate_image_config(config, platform=PlatformImage("amd64", self.children["amd64"]),
                                  version="2.1.24", revision=self.revision)

    def test_lookup_failure_keeps_actionable_diagnostic(self):
        run = Mock(side_effect=subprocess.CalledProcessError(128, "git", stderr=b"release tag not found"))
        with self.assertRaisesRegex(RuntimeError, "release tag not found"):
            ReleaseSource(root=ROOT, run=run).resolve("2.1.24")

    def test_timeout_cleans_only_the_named_check_container(self):
        run = Mock(side_effect=[None, subprocess.TimeoutExpired("docker run", 30), None])
        with self.assertRaises(subprocess.TimeoutExpired):
            check_platform(PlatformImage("amd64", self.children["amd64"]), run=run)
        command = run.call_args_list[1].args[0]
        name = command[command.index("--name") + 1]
        self.assertEqual(run.call_args_list[2].args[0], ["docker", "rm", "--force", name])
