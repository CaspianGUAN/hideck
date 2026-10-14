"""Exercise installer functions with isolated package/tool doubles, never install packages."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
INSTALLER = (ROOT / "deploy-binary.sh").read_text()
FUNCTIONS, MAIN = INSTALLER.split("\ndetect_os\n", 1)
RECORDER = r"""
is_openwrt() { [ "$TEST_OPENWRT" = 1 ]; }
command() {
  [ "$1" = -v ] || return 2
  case " $TEST_COMMANDS " in
    *" $2 "*) return 0 ;;
    *) return 1 ;;
  esac
}
maybe_sudo() {
  printf 'PACKAGE %s\n' "$*"
  case " $* " in
    *" $TEST_FAIL "*) return 1 ;;
  esac
}
adb() {
  case "$1" in
    help) printf '%s\n' "$TEST_ADB_HELP" ;;
    version) printf 'ADB version test fixture\n' ;;
    *) printf 'Unexpected ADB operation: %s\n' "$*" >&2; return 1 ;;
  esac
}
arecord() { [ "$1" = --version ] && [ "$TEST_AUDIO_OK" = 1 ]; }
aplay() { [ "$1" = --version ] && [ "$TEST_AUDIO_OK" = 1 ]; }
modinfo() { [ "$1" = snd_usb_audio ] && [ "$TEST_KERNEL_OK" = 1 ]; }
"""


class DependencyTests(unittest.TestCase):
    def run_shell(self, script, **overrides):
        environment = dict(os.environ, TEST_OPENWRT="0", TEST_COMMANDS="", HIDECK_MODEM_VOICE="auto",
                           TEST_FAIL="never-installed-package", TEST_AUDIO_OK="1",
                           TEST_KERNEL_OK="1", TEST_ADB_HELP=" -t ID transport\n -L SOCKET server")
        environment.update(overrides)
        with tempfile.TemporaryDirectory(prefix="hideck-deps-test-") as directory:
            # Do not depend on whether the CI host has a loaded USB audio driver.
            functions = FUNCTIONS.replace("/sys/bus/usb/drivers/snd-usb-audio",
                                          str(Path(directory) / "unloaded-driver"))
            return subprocess.run(["sh", "-c", functions + RECORDER + script],
                                  env=environment, capture_output=True, text=True, timeout=10)

    def assert_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_linux_package_names(self):
        managers = {
            "apt-get": ("install -y", "adb"),
            "dnf": ("install -y", "android-tools"),
            "yum": ("install -y", "android-tools"),
            "apk": ("add --no-cache", "android-tools-adb"),
            "pacman": ("-S --needed --noconfirm", "android-tools"),
        }
        for manager, (operation, adb_package) in managers.items():
            with self.subTest(manager=manager):
                result = self.run_shell("install_runtime_dependencies_with_pkg",
                                        TEST_COMMANDS=manager)
                self.assert_success(result)
                for package in (adb_package, "alsa-utils", "kmod"):
                    self.assertIn(f"PACKAGE {manager} {operation} {package}\n", result.stdout)

    def test_openwrt_default_does_not_install_voice_tools(self):
        for manager, operation in (("opkg", "install"), ("apk", "add")):
            with self.subTest(manager=manager):
                result = self.run_shell("install_runtime_dependencies_with_pkg",
                                        TEST_OPENWRT="1", TEST_COMMANDS=manager)
                self.assert_success(result)
                for package in ("libqmi",
                                "kmod-usb-net-qmi-wwan", "kmod-usb-serial-option",
                                "lame-lib", "libopencore-amrnb"):
                    self.assertIn(f"PACKAGE {manager} {operation} {package}\n", result.stdout)
                self.assertNotIn("android-tools-adb", result.stdout)
                for package in ("adb", "hideck-adb", "alsa-utils", "kmod-usb-audio"):
                    self.assertNotIn(f" {package}\n", result.stdout)

    def test_openwrt_explicit_voice_uses_private_adb_package(self):
        for manager, operation in (("opkg", "install"), ("apk", "add")):
            with self.subTest(manager=manager):
                result = self.run_shell("install_runtime_dependencies_with_pkg", TEST_OPENWRT="1",
                                        TEST_COMMANDS=manager, HIDECK_MODEM_VOICE="1")
                self.assert_success(result)
                for package in ("hideck-adb", "alsa-utils", "kmod-usb-audio"):
                    self.assertIn(f"PACKAGE {manager} {operation} {package}\n", result.stdout)
                self.assertNotIn(f"PACKAGE {manager} {operation} adb\n", result.stdout)

    def test_openwrt_without_voice_does_not_require_audio_hardware(self):
        result = self.run_shell("install_runtime_dependencies", TEST_OPENWRT="1", TEST_COMMANDS="opkg")
        self.assert_success(result)
        self.assertIn("未选择模组直拨依赖", result.stdout)

    def test_missing_recording_library_does_not_skip_requested_voice_packages(self):
        result = self.run_shell("install_runtime_dependencies_with_pkg", TEST_OPENWRT="1",
                                TEST_COMMANDS="opkg", HIDECK_MODEM_VOICE="1",
                                TEST_FAIL="libopencore-amrnb")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("未安装：libopencore-amrnb", result.stdout)
        for package in ("hideck-adb", "alsa-utils", "kmod-usb-audio"):
            self.assertIn(f"PACKAGE opkg install {package}\n", result.stdout)

    def test_explicit_opt_out_skips_linux_voice_packages_and_checks(self):
        result = self.run_shell("install_runtime_dependencies", TEST_COMMANDS="apt-get",
                                HIDECK_MODEM_VOICE="0")
        self.assert_success(result)
        self.assertNotIn("install -y adb\n", result.stdout)
        self.assertNotIn("install -y alsa-utils\n", result.stdout)

    def test_repository_refresh_failure_stops_package_install(self):
        for manager, openwrt in (("apt-get", "0"), ("opkg", "1"), ("apk", "1")):
            with self.subTest(manager=manager):
                result = self.run_shell("install_runtime_dependencies_with_pkg",
                                        TEST_COMMANDS=manager, TEST_OPENWRT=openwrt,
                                        TEST_FAIL="update")
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout.count("PACKAGE "), 1)

    def test_package_failure_is_reported_even_when_tools_exist(self):
        result = self.run_shell("install_runtime_dependencies",
                                TEST_COMMANDS="apt-get adb arecord aplay modinfo",
                                TEST_FAIL="alsa-utils")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("未安装：alsa-utils", result.stdout)
        self.assertNotIn("检查通过", result.stdout)

    def test_unknown_package_manager_fails(self):
        result = self.run_shell("install_runtime_dependencies_with_pkg")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("未识别的包管理器", result.stderr)

    def test_missing_tools_fail(self):
        for missing in ("adb", "arecord", "aplay"):
            with self.subTest(missing=missing):
                commands = " ".join(tool for tool in ("adb", "arecord", "aplay") if tool != missing)
                result = self.run_shell("check_modem_voice_tools", TEST_COMMANDS=commands)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(f"缺少 {missing}", result.stderr)

    def test_old_adb_is_not_accepted(self):
        for help_text, missing in ((" -L SOCKET", "transport-id"), (" -t ID", "server socket")):
            with self.subTest(missing=missing):
                result = self.run_shell("check_modem_voice_tools",
                                        TEST_COMMANDS="adb arecord aplay", TEST_ADB_HELP=help_text)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(missing, result.stderr)

    def test_audio_execution_failure_is_not_accepted(self):
        result = self.run_shell("check_modem_voice_tools", TEST_COMMANDS="adb arecord aplay",
                                TEST_AUDIO_OK="0")
        self.assertNotEqual(result.returncode, 0)

    def test_unloaded_but_installed_driver_is_accepted(self):
        self.assert_success(self.run_shell("check_usb_audio_support", TEST_COMMANDS="modinfo"))

    def test_absent_driver_is_reported(self):
        result = self.run_shell("check_usb_audio_support", TEST_COMMANDS="modinfo", TEST_KERNEL_OK="0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("snd_usb_audio", result.stderr)

    def test_success_does_not_require_connected_modem(self):
        result = self.run_shell("install_runtime_dependencies",
                                TEST_COMMANDS="apt-get adb arecord aplay modinfo")
        self.assert_success(result)
        self.assertIn("检查通过", result.stdout)
        self.assertIn("未执行真实通话测试", result.stdout)

    def test_main_exposes_partial_installation_failure(self):
        self.assertIn("if ! install_runtime_dependencies; then\n  dependency_status=1", MAIN)
        self.assertIn('exit "$dependency_status"', MAIN)
        self.assertNotIn("install_recording_libraries || true", MAIN)


class PackagingTests(unittest.TestCase):
    def test_docker_variants_check_all_tools_and_adb_capabilities(self):
        for name in ("Dockerfile", "Dockerfile.github", "Dockerfile.runtime", "Dockerfile.release"):
            with self.subTest(dockerfile=name):
                source = (ROOT / name).read_text()
                adb_package = "adb" if name == "Dockerfile.release" else "android-tools-adb"
                for requirement in (adb_package, "alsa-utils", "adb version", "arecord --version",
                                    "aplay --version", "*-t[[:space:]]", "*-L[[:space:]]"):
                    self.assertIn(requirement, source)

    def test_openwrt_package_declares_audio_dependencies(self):
        source = (ROOT / "packaging/openwrt/hideck/Makefile").read_text()
        main_package = source.split("define Package/hideck\n", 1)[1].split("endef", 1)[0]
        for dependency in ("+hideck-adb", "+alsa-utils", "+kmod-usb-audio"):
            self.assertIn(dependency, source)
            self.assertNotIn(dependency, main_package)

    def test_compose_preserves_hotplug_device_access(self):
        source = (ROOT / "docker-compose.yml").read_text()
        self.assertIn("privileged: true", source)
        self.assertIn("/dev:/dev", source)


if __name__ == "__main__":
    unittest.main()
