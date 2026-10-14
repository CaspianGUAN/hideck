"""Check image tools without starting HiDeck or exposing host devices/network."""

import subprocess
import uuid

from image_release import IMAGE_REPOSITORY


PULL_TIMEOUT_SECONDS = 120
CHECK_TIMEOUT_SECONDS = 30
CLEANUP_TIMEOUT_SECONDS = 15
TOOL_CHECK = """
for tool in adb arecord aplay wget; do
  command -v "$tool" >/dev/null || { echo "Missing image dependency: $tool" >&2; exit 1; }
done
adb version
adb help > /tmp/adb-help 2>&1
grep -Eq '^[[:space:]]*-t[[:space:]]' /tmp/adb-help
grep -Eq '^[[:space:]]*-L[[:space:]]' /tmp/adb-help
arecord --version
aplay --version
test -x /usr/local/bin/hideck
"""


def check_platform(platform, *, run):
    reference = f"{IMAGE_REPOSITORY}@{platform.digest}"
    architecture = f"linux/{platform.architecture}"
    run(["docker", "pull", "--platform", architecture, reference],
        check=True, timeout=PULL_TIMEOUT_SECONDS)
    name = "hideck-fnos-check-" + uuid.uuid4().hex
    arguments = ["docker", "run", "--rm", "--name", name, "--platform", architecture,
                 "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL",
                 "--security-opt=no-new-privileges", "--user=65534:65534",
                 "--tmpfs=/tmp:rw,noexec,nosuid,size=16m", "--pids-limit=64",
                 "--memory=128m", "--cpus=1", "--entrypoint=/bin/sh",
                 reference, "-eu", "-c", TOOL_CHECK]
    try:
        run(arguments, check=True, timeout=CHECK_TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired:
        # Only this invocation's uniquely named check container may be removed.
        run(["docker", "rm", "--force", name], check=True, timeout=CLEANUP_TIMEOUT_SECONDS)
        raise
    except subprocess.CalledProcessError as error:
        raise RuntimeError(f"{architecture} image dependency check failed; see the tool error above") from error


def check_release(release, *, run):
    for platform in release.platforms:
        check_platform(platform, run=run)
