"""fnOS package lifecycle checks; Docker calls use isolated command doubles."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[2]
PACKAGING = ROOT / "packaging/fnos"
sys.path.insert(0, str(PACKAGING))
SPEC = importlib.util.spec_from_file_location("fnos_build", PACKAGING / "build.py")
BUILD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BUILD)
from image_release import PlatformImage, Release


class FnOSTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="hideck-fnos-test-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        self.stage = self.directory / "package"
        self.release = Release("2.1.23", "sha256:" + "a" * 64, "b" * 40,
                               (PlatformImage("amd64", "sha256:" + "c" * 64),),
                               (ROOT / "config/config.example.yaml").read_bytes(), b"test license\n")
        BUILD.prepare_package(self.stage, release=self.release, port=17575)
        self.environment = dict(os.environ, TRIM_APPDEST=str(self.stage / "app"),
                                TRIM_PKGETC=str(self.directory / "config"),
                                TRIM_PKGVAR=str(self.directory / "var"),
                                TRIM_TEMP_LOGFILE=str(self.directory / "error.log"), TRIM_SERVICE_PORT="17575")
        self.tools = self.directory / "tools"
        self.tools.mkdir()
        self.environment.update(PATH=f"{self.tools}:{os.environ['PATH']}", TEST_STATE="", TEST_ID="", TEST_FAILURE="")
        self.write_command("timeout", 'test "$1" = -k && test "$2" = 1 && test "$3" = 5 || exit 2\n'
                           '[ "${TEST_TIMEOUT:-}" != yes ] || exit 124\nshift 3\nexec "$@"\n')
        self.write_command("docker", 'case "$1" in\n'
                           'info|compose) [ "$TEST_FAILURE" != "$1" ] || exit 1 ;;\n'
                           'container) [ "$TEST_FAILURE" != list ] || exit 1; printf "%s\\n" "$TEST_ID" ;;\n'
                           'inspect) [ "$TEST_FAILURE" != inspect ] || exit 1; printf "%s\\n" "$TEST_STATE" ;;\n'
                           '*) exit 1 ;;\nesac\n')

    def write_command(self, name, body):
        path = self.tools / name
        path.write_text("#!/bin/sh\nset -eu\n" + body)
        path.chmod(0o755)

    def run_script(self, path, *arguments, **environment):
        return subprocess.run(["sh", str(self.stage / path), *arguments],
                              env=dict(self.environment, **environment), text=True,
                              capture_output=True, timeout=10)

    def test_release_and_port_validation(self):
        self.assertEqual(BUILD.release_version("v2.1.23"), "2.1.23")
        self.assertEqual(BUILD.release_version("2.1.23-rc.1"), "2.1.23-rc.1")
        for value in ("latest", "dev", "2.1.23\n", "2.1.23;id", "../2.1.23"):
            with self.assertRaises(argparse.ArgumentTypeError):
                BUILD.release_version(value)
        for value in ("0", "65536", "port", "-1"):
            with self.assertRaises(argparse.ArgumentTypeError):
                BUILD.http_port(value)

    def test_release_workflow_uploads_fpk_to_existing_release(self):
        workflow = (ROOT / ".github/workflows/fnos-package.yml").read_text()
        caller = (ROOT / ".github/workflows/docker-publish.yml").read_text()

        self.assertIn('release_tag="v${PACKAGE_VERSION#v}"', workflow)
        self.assertIn('gh release view "$release_tag"', workflow)
        self.assertIn('gh release upload "$release_tag"', workflow)
        self.assertIn("dist/fnos/*.fpk.sha256 --clobber", workflow)
        self.assertIn("fnos:\n    needs: build", caller)
        self.assertIn("fnos:\n    needs: build\n    if:", caller)
        self.assertIn("permissions:\n      contents: write\n    uses:", caller)

    def build_options(self):
        return argparse.Namespace(version="2.1.23", http_port=7575,
                                  fnpack="fnpack", output=self.directory / "output",
                                  image_digest=None, source_revision=None)

    def test_build_does_not_overwrite_existing_package_or_checksum(self):
        options = self.build_options()
        options.output.mkdir()
        for suffix in (".fpk", ".fpk.sha256"):
            with self.subTest(suffix=suffix):
                existing = options.output / f"hideck_2.1.23_fnos{suffix}"
                existing.write_bytes(b"existing artifact")
                with patch.object(BUILD.shutil, "which", return_value="/fnpack"):
                    with self.assertRaises(FileExistsError):
                        BUILD.build_package(options)
                self.assertEqual(existing.read_bytes(), b"existing artifact")
                existing.unlink()

    def test_success_exit_without_fpk_is_a_build_failure(self):
        # fnpack may exit zero after reporting a missing required file.
        options = self.build_options()
        resolver = patch.object(BUILD.ReleaseSource, "resolve", return_value=self.release)
        checker = patch.object(BUILD, "check_release")
        resolver.start()
        checker.start()
        self.addCleanup(resolver.stop)
        self.addCleanup(checker.stop)
        with patch.object(BUILD.shutil, "which", return_value="/fnpack"):
            with patch.object(BUILD.subprocess, "run") as run:
                with self.assertRaisesRegex(RuntimeError, "nonempty FPK"):
                    BUILD.build_package(options)
        self.assertEqual(run.call_args.kwargs["timeout"], BUILD.BUILD_TIMEOUT_SECONDS)
        self.assertEqual(list(options.output.iterdir()), [])

    def test_rendered_package_agrees_on_identity_and_port(self):
        manifest = dict(line.split("=", 1) for line in (self.stage / "manifest").read_text().splitlines())
        entry = json.loads((self.stage / "app/ui/config").read_text())[".url"]["hideck.main"]
        self.assertEqual(manifest["desktop_applaunchname"], "hideck.main")
        self.assertEqual(manifest["service_port"], "17575")
        self.assertEqual(entry["port"], manifest["service_port"])
        self.assertEqual(entry["type"], "url")
        self.assertFalse(entry["allUsers"])
        compose = (self.stage / "app/docker/docker-compose.yaml").read_text()
        self.assertIn(f'image: "{self.release.image}"', compose)
        self.assertIn("network_mode: host", compose)
        self.assertIn("privileged: true", compose)
        self.assertIn("/dev:/dev", compose)
        self.assertNotIn("ports:", compose)
        self.assertNotIn("docker.sock", compose)
        self.assertNotIn("latest", compose)
        self.assertIn("${TRIM_PKGETC:?", compose)
        self.assertIn("${TRIM_PKGVAR:?", compose)
        self.assertIn("${TRIM_SERVICE_PORT:?", compose)

    def test_assets_and_executable_modes(self):
        for size, name in ((64, "ICON.PNG"), (256, "ICON_256.PNG")):
            data = (self.stage / name).read_bytes()
            self.assertEqual(data[:8], b"\x89PNG\r\n\x1a\n")
            self.assertEqual(struct.unpack(">II", data[16:24]), (size, size))
            self.assertEqual(data, (self.stage / f"app/ui/images/icon_{size}.png").read_bytes())
        for script in [*(self.stage / "cmd").iterdir(), *(self.stage / "app/bin").iterdir()]:
            self.assertTrue(os.access(script, os.X_OK))
            subprocess.run(["sh", "-n", str(script)], check=True, timeout=10)
        self.assertEqual((self.stage / "app/defaults/config.example.yaml").read_bytes(),
                         (ROOT / "config/config.example.yaml").read_bytes())

    def test_consent_is_explicit(self):
        for choice in ("", "no", "true"):
            result = self.run_script("cmd/install_init", wizard_hardware_access=choice)
            self.assertEqual(result.returncode, 1)
            self.assertIn("权限", Path(self.environment["TRIM_TEMP_LOGFILE"]).read_text())
        self.assertEqual(self.run_script("cmd/install_init", wizard_hardware_access="yes").returncode, 0)

    def test_first_install_initializes_private_persistent_directories(self):
        result = self.run_script("cmd/install_callback")
        self.assertEqual(result.returncode, 0, result.stderr)
        config = Path(self.environment["TRIM_PKGETC"]) / "config.yaml"
        self.assertEqual(config.read_bytes(), (ROOT / "config/config.example.yaml").read_bytes())
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertTrue((self.directory / "var/data").is_dir())
        self.assertTrue((self.directory / "var/logs").is_dir())

    def test_upgrade_and_start_preserve_config_notifications_and_database(self):
        self.assertEqual(self.run_script("cmd/install_callback").returncode, 0)
        config = self.directory / "config/config.yaml"
        state = self.directory / "config/notification-state.json"
        database = self.directory / "var/data/hideck.db"
        for path in (config, state, database):
            path.write_bytes(b"preserve existing user bytes\n")
        for action in (("cmd/upgrade_callback",), ("cmd/main", "start")):
            result = self.run_script(*action)
            self.assertEqual(result.returncode, 0, result.stderr)
        for path in (config, state, database):
            self.assertEqual(path.read_bytes(), b"preserve existing user bytes\n")

    def test_invalid_config_path_is_not_replaced(self):
        config_dir = self.directory / "config"
        config_dir.mkdir()
        config = config_dir / "config.yaml"
        config.symlink_to(config_dir / "missing-target")
        result = self.run_script("cmd/install_callback")
        self.assertEqual(result.returncode, 1)
        self.assertTrue(config.is_symlink())
        self.assertIn("未覆盖", result.stderr)

    def test_missing_platform_paths_fail(self):
        for variable in ("TRIM_APPDEST", "TRIM_PKGETC", "TRIM_PKGVAR"):
            result = self.run_script("app/bin/initialize", **{variable: ""})
            self.assertEqual(result.returncode, 1)

    def status_result(self, *, state="", identifier="abc123", failure=""):
        return self.run_script("cmd/main", "status", TEST_STATE=state, TEST_ID=identifier, TEST_FAILURE=failure)

    def test_status_only_accepts_our_running_container(self):
        self.assertEqual(self.status_result(state="hideck-fnos|hideck|running").returncode, 0)
        for state in ("running", "other|hideck|running"):
            self.assertEqual(self.status_result(state=state).returncode, 1)
        for state in ("created", "exited", "restarting", "dead"):
            result = self.status_result(state=f"hideck-fnos|hideck|{state}")
            self.assertEqual(result.returncode, 3)
        script = (self.stage / "cmd/main").read_text()
        self.assertNotIn(".State.Health", script)
        self.assertIn("healthcheck:", (self.stage / "app/docker/docker-compose.yaml").read_text())

    def test_status_distinguishes_missing_container_from_docker_failure(self):
        self.assertEqual(self.status_result(identifier="").returncode, 3)
        for failure in ("list", "inspect"):
            result = self.status_result(failure=failure)
            self.assertEqual(result.returncode, 1)
            self.assertTrue(result.stderr)

    def test_preflight_rejects_daemon_or_compose_failure(self):
        for failure in ("info", "compose"):
            for hook in ("cmd/install_init", "cmd/upgrade_init"):
                result = self.run_script(hook, wizard_hardware_access="yes", TEST_FAILURE=failure)
                self.assertEqual(result.returncode, 1)
                self.assertIn("Docker 查询失败", result.stderr)
        result = self.run_script("cmd/main", "start", TEST_FAILURE="info")
        self.assertEqual(result.returncode, 1)
        self.assertFalse((self.directory / "config/config.yaml").exists())

    def test_status_timeout_is_error_not_stopped_or_success(self):
        result = self.run_script("cmd/main", "status", TEST_TIMEOUT="yes")
        self.assertEqual(result.returncode, 1)
        self.assertIn("超时", result.stderr)

    @unittest.skipUnless(shutil.which("timeout"), "Actual coreutils timeout is tested on Linux")
    def test_real_status_deadline_terminates_hung_docker(self):
        (self.tools / "timeout").unlink()
        (self.tools / "timeout").symlink_to(shutil.which("timeout"))
        self.write_command("docker", "exec sleep 30\n")
        result = self.run_script("cmd/main", "status")
        self.assertEqual(result.returncode, 1)
        self.assertIn("超时", result.stderr)

    def test_entry_configuration_does_not_change_server_and_survives_upgrade(self):
        result = self.run_script("cmd/install_callback", wizard_entry_protocol="https", wizard_entry_port="7576")
        self.assertEqual(result.returncode, 0, result.stderr)
        original = (self.directory / "config/config.yaml").read_bytes()
        # Simulate replacement of package assets and stale platform wizard defaults.
        (self.stage / "app/ui/config").write_text('{}')
        for action in (("cmd/upgrade_callback",), ("cmd/main", "start")):
            result = self.run_script(*action, wizard_entry_protocol="http", wizard_entry_port="17575")
            self.assertEqual(result.returncode, 0, result.stderr)
        entry = json.loads((self.stage / "app/ui/config").read_text())[".url"]["hideck.main"]
        self.assertEqual((entry["protocol"], entry["port"]), ("https", "7576"))
        self.assertEqual((self.directory / "config/config.yaml").read_bytes(), original)

    def test_invalid_entry_is_rejected_without_changes(self):
        self.assertEqual(self.run_script("cmd/install_callback").returncode, 0)
        saved = (self.stage / "app/ui/config").read_bytes()
        for protocol, port in (("javascript", "7576"), ("https", "65536"), ("https", '443"}'), ("https", "")):
            result = self.run_script("cmd/config_init", wizard_entry_protocol=protocol, wizard_entry_port=port)
            self.assertEqual(result.returncode, 1)
            self.assertEqual((self.stage / "app/ui/config").read_bytes(), saved)

    def test_entry_path_directory_is_not_treated_as_success(self):
        (self.directory / "config/fnos-entry.conf").mkdir(parents=True)
        result = self.run_script("cmd/config_callback", wizard_entry_protocol="https", wizard_entry_port="7576")
        self.assertEqual(result.returncode, 1)
        self.assertIn("未覆盖", result.stderr)
        self.assertEqual(list((self.directory / "config/fnos-entry.conf").iterdir()), [])

    def test_default_config_is_from_resolved_release(self):
        stage = self.directory / "different-release"
        release = Release("2.1.24", self.release.digest, self.release.revision,
                          self.release.platforms, b"version-bound config\n", b"release license\n")
        BUILD.prepare_package(stage, release=release, port=17575)
        self.assertEqual((stage / "app/defaults/config.example.yaml").read_bytes(), release.config)
        metadata = json.loads((stage / "app/defaults/release.json").read_text())
        self.assertEqual(metadata["source_revision"], release.revision)

    def test_stop_does_not_operate_other_containers_or_remove_data(self):
        self.assertEqual(self.run_script("cmd/install_callback").returncode, 0)
        config = self.directory / "config/config.yaml"
        original = config.read_bytes()
        self.assertEqual(self.run_script("cmd/main", "stop", PATH="/usr/bin:/bin").returncode, 0)
        for hook in ("uninstall_init", "uninstall_callback"):
            self.assertEqual(self.run_script(f"cmd/{hook}", PATH="/usr/bin:/bin").returncode, 0)
        self.assertEqual(config.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
