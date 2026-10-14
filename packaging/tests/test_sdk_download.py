"""Exercise the real curl downloader against a local HTTPS server with Range."""

from dataclasses import replace
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import os
from pathlib import Path
import re
import socket
import ssl
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[1] / "openwrt/download_sdk.py"
MODULE_SPEC = importlib.util.spec_from_file_location("sdk_downloader", SCRIPT)
sdk = importlib.util.module_from_spec(MODULE_SPEC)
sys.modules[MODULE_SPEC.name] = sdk
MODULE_SPEC.loader.exec_module(sdk)
PAYLOAD = b"verified-sdk-test-data" * 4096
DIGEST = hashlib.sha256(PAYLOAD).hexdigest()


class SDKHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        server = self.server
        requested_range = self.headers.get("Range")
        server.ranges.append(requested_range)
        if server.mode == "not-found":
            self.send_error(404)
            return
        offset = int(requested_range.removeprefix("bytes=").removesuffix("-")) if requested_range else 0
        if server.mode == "ignore-range":
            offset = 0
        body = PAYLOAD[offset:]
        if server.mode == "bad-hash":
            body = b"x" * len(body)
        self.send_response(206 if offset else 200)
        self.send_header("Content-Length", str(len(body)))
        if offset:
            self.send_header("Content-Range", f"bytes {offset}-{len(PAYLOAD) - 1}/{len(PAYLOAD)}")
        self.end_headers()
        drop = server.mode == "always-drop" or server.mode == "drop-once" and len(server.ranges) == 1
        try:
            self.wfile.write(body[:len(body) // 2] if drop else body)
            self.wfile.flush()
        except (BrokenPipeError, ConnectionResetError):
            # Some curl versions reject ignored Range headers before reading the body.
            if server.mode != "ignore-range":
                raise
            self.close_connection = True
        if drop:
            self.close_connection = True
            self.connection.shutdown(socket.SHUT_RDWR)

    def log_message(self, *_args):
        pass


class SDKDownloadTests(unittest.TestCase):
    def test_workflow_deadlines_cover_download_and_build(self):
        workflows = SCRIPT.parents[2] / ".github/workflows"
        download_minutes = sdk.DEFAULT_TOTAL_SECONDS / 60
        build_headroom_minutes = {"binary-release.yml": 20, "openwrt-packages.yml": 90}
        for name, headroom in build_headroom_minutes.items():
            source = (workflows / name).read_text()
            if name == "binary-release.yml":
                source = source.split("  build-openwrt:", 1)[1]
            timeout = int(re.search(r"timeout-minutes: (\d+)", source).group(1))
            self.assertGreaterEqual(timeout, download_minutes + headroom, name)

    @classmethod
    def setUpClass(cls):
        cls.cert_directory = tempfile.TemporaryDirectory(prefix="hideck-sdk-tls-")
        cls.addClassCleanup(cls.cert_directory.cleanup)
        directory = Path(cls.cert_directory.name)
        cls.cert, cls.key = directory / "cert.pem", directory / "key.pem"
        subprocess.run([
            "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
            "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost",
            "-keyout", str(cls.key), "-out", str(cls.cert),
        ], check=True, capture_output=True, timeout=10)

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="hideck-sdk-download-")
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), SDKHandler)
        self.server.mode, self.server.ranges = "complete", []
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(self.cert, self.key)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(self.server.server_close)
        self.addCleanup(thread.join, 2)
        self.addCleanup(self.server.shutdown)
        environment = patch.dict(os.environ, {"CURL_CA_BUNDLE": str(self.cert), "NO_PROXY": "localhost"})
        environment.start()
        self.addCleanup(environment.stop)
        # Production CLI restricts URLs to the official host; exercise transport locally.
        self.spec = sdk.Download(
            url=f"https://localhost:{self.server.server_port}/sdk.tar.zst", sha256=DIGEST,
            cache_dir=self.root, attempt_seconds=3, total_seconds=10, attempts=2, retry_delay=0,
        )
        self.archive = self.root / f"{DIGEST}.tar.zst"
        self.partial = self.root / f"{DIGEST}.tar.zst.part"

    def test_interrupted_transfer_resumes_real_range_request(self):
        self.server.mode = "drop-once"
        self.assertEqual(sdk.download(self.spec), self.archive)
        self.assertEqual(self.archive.read_bytes(), PAYLOAD)
        self.assertEqual(self.server.ranges, [None, f"bytes={len(PAYLOAD) // 2}-"])
        self.assertFalse(self.partial.exists())

    def test_existing_partial_resumes_and_complete_cache_is_verified(self):
        self.partial.write_bytes(PAYLOAD[:8192])
        sdk.download(self.spec)
        sdk.download(self.spec)
        self.assertEqual(self.server.ranges, ["bytes=8192-"])
        self.assertEqual(self.archive.read_bytes(), PAYLOAD)

    def test_complete_partial_is_promoted_without_network(self):
        self.partial.write_bytes(PAYLOAD)
        sdk.download(self.spec)
        self.assertEqual(self.server.ranges, [])
        self.assertTrue(self.archive.exists())

    def test_unsupported_resume_fails_without_silent_restart(self):
        self.server.mode = "ignore-range"
        self.partial.write_bytes(PAYLOAD[:8192])
        with self.assertRaisesRegex(RuntimeError, "curl 33"):
            sdk.download(self.spec)
        self.assertEqual(self.server.ranges, ["bytes=8192-"])
        self.assertEqual(self.partial.read_bytes(), PAYLOAD[:8192])
        self.assertFalse(self.archive.exists())

    def test_hash_mismatch_is_not_promoted_or_retried(self):
        self.server.mode = "bad-hash"
        with self.assertRaisesRegex(RuntimeError, "SHA-256 mismatch"):
            sdk.download(self.spec)
        self.assertFalse(self.archive.exists())
        self.assertEqual(len(self.server.ranges), 1)

    def test_corrupt_cache_fails_instead_of_hiding_the_problem(self):
        self.archive.write_bytes(b"corrupt")
        with self.assertRaisesRegex(RuntimeError, "cache SHA-256 mismatch"):
            sdk.download(self.spec)
        self.assertEqual(self.server.ranges, [])

    def test_read_only_seed_is_verified_without_copy_or_download(self):
        seed = self.root / "seed"
        seed.mkdir()
        seeded_archive = seed / self.archive.name
        seeded_archive.write_bytes(PAYLOAD)
        self.assertEqual(sdk.download(replace(self.spec, seed_dir=seed)), seeded_archive)
        self.assertFalse(self.archive.exists())
        self.assertEqual(self.server.ranges, [])
        seeded_archive.write_bytes(b"corrupt")
        with self.assertRaisesRegex(RuntimeError, "cache SHA-256 mismatch"):
            sdk.download(replace(self.spec, seed_dir=seed))

    def test_attempt_budget_preserves_partial_for_manual_retry(self):
        self.server.mode = "always-drop"
        with self.assertRaisesRegex(RuntimeError, "budget exhausted"):
            sdk.download(self.spec)
        self.assertEqual(len(self.server.ranges), 2)
        self.assertFalse(self.archive.exists())
        offset = self.partial.stat().st_size
        self.server.mode = "complete"
        sdk.download(self.spec)
        self.assertEqual(self.server.ranges[-1], f"bytes={offset}-")

    def test_http_error_is_not_retried(self):
        self.server.mode = "not-found"
        with self.assertRaisesRegex(RuntimeError, "curl 22"):
            sdk.download(self.spec)
        self.assertEqual(len(self.server.ranges), 1)

    def test_total_deadline_stops_additional_attempts(self):
        with patch.object(sdk.time, "monotonic", side_effect=[0, 0, 2, 2]), \
                patch.object(sdk, "curl_attempt", return_value=28) as attempt:
            with self.assertRaisesRegex(RuntimeError, "budget exhausted"):
                sdk.download(replace(self.spec, total_seconds=1))
        attempt.assert_called_once()

    def test_cli_rejects_untrusted_url_or_digest_before_download(self):
        official = "https://downloads.openwrt.org/releases/24.10.8/targets/x86/64/openwrt-sdk-test.tar.zst"
        for url, digest in [("https://example.org/sdk.tar.zst", DIGEST), (official, "../invalid")]:
            with self.subTest(url=url):
                result = subprocess.run([
                    sys.executable, str(SCRIPT), "--url", url, "--sha256", digest,
                    "--cache-dir", str(self.root),
                ], capture_output=True, text=True, timeout=5)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("error:", result.stderr)
        self.assertEqual(list(self.root.iterdir()), [])


if __name__ == "__main__":
    unittest.main()
