"""Exercise the remote rollout script without contacting a cluster."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("rollout_web.sh")


class RolloutTest(unittest.TestCase):
    def run_rollout(self, mode):
        # Only the cluster boundary is substituted; run the actual shell script.
        root = Path(os.environ.get("XDG_CACHE_HOME", Path.home() / ".cache")) / "agent-startup"
        root.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="matrixqc-rollout-", dir=root) as work:
            work = Path(work)
            kubectl = work / "kubectl"
            kubectl.write_text("""#!/bin/sh
set -eu
printf '%s\\n' "$*" >> "$CALLS"
case "$*" in
  '-n default get deployment matrixqc --ignore-not-found -o name')
    case "$MODE" in
      absent) exit 0 ;;
      lookup-error) echo 'API unavailable' >&2; exit 7 ;;
      *) echo 'deployment.apps/matrixqc' ;;
    esac ;;
  '-n default rollout restart deployment/matrixqc')
    if [ "$MODE" = restart-error ]; then exit 8; fi ;;
  '-n default rollout status deployment/matrixqc --timeout=120s')
    if [ "$MODE" = rollout-error ]; then exit 9; fi ;;
  *) echo 'unexpected kubectl arguments' >&2; exit 10 ;;
esac
""")
            kubectl.chmod(0o755)
            calls = work / "calls"
            env = dict(os.environ, MODE=mode, CALLS=str(calls), PATH=f"{work}:{os.environ['PATH']}")
            result = subprocess.run(["sh", str(SCRIPT)], env=env, text=True, capture_output=True)
            return result, calls.read_text().splitlines() if calls.exists() else []

    def test_first_image_can_publish_before_manifests_exist(self):
        result, calls = self.run_rollout("absent")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, ["-n default get deployment matrixqc --ignore-not-found -o name"])
        self.assertIn("not installed", result.stdout)

    def test_existing_deployment_restarts_and_waits_for_readiness(self):
        result, calls = self.run_rollout("present")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls, [
            "-n default get deployment matrixqc --ignore-not-found -o name",
            "-n default rollout restart deployment/matrixqc",
            "-n default rollout status deployment/matrixqc --timeout=120s",
        ])

    def test_api_error_is_not_misreported_as_first_install(self):
        result, calls = self.run_rollout("lookup-error")
        self.assertEqual(result.returncode, 7)
        self.assertEqual(len(calls), 1)
        self.assertNotIn("not installed", result.stdout)

    def test_restart_error_does_not_continue_to_status(self):
        result, calls = self.run_rollout("restart-error")
        self.assertEqual(result.returncode, 8)
        self.assertEqual(len(calls), 2)

    def test_unhealthy_rollout_fails_the_workflow(self):
        result, calls = self.run_rollout("rollout-error")
        self.assertEqual(result.returncode, 9)
        self.assertEqual(len(calls), 3)


if __name__ == "__main__":
    unittest.main()
