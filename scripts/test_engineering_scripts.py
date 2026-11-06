import json
import os
from pathlib import Path
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import unittest

from test_engineering_rbac import FAKE_KUBECTL


ROOT = Path(__file__).resolve().parents[1]
RBAC = ROOT / "scripts/test-rbac.sh"
VISUAL = ROOT / "scripts/visual-test-start.sh"


FAKE_CURL = r"""#!/usr/bin/env python3
import os
import sys
import time

url = sys.argv[-1]
with open(os.environ["FAKE_CURL_LOG"], "a", encoding="utf-8") as stream:
    stream.write(url + "\n")
mode = os.environ.get("FAKE_CURL_MODE", "failure")
if url.endswith("/api/health"):
    if mode == "delay-success":
        time.sleep(0.2)
        raise SystemExit(0)
    if mode in ("success", "startup-only"):
        for _ in range(50):
            if os.path.exists(os.environ["FAKE_APP_ARGS"]):
                raise SystemExit(0)
            time.sleep(0.01)
    raise SystemExit(22)
if url.endswith("/api/connection"):
    print('{"state":"connected"}')
    raise SystemExit(0)
raise SystemExit(22)
"""


FAKE_APP = r"""#!/usr/bin/env python3
import json
import os
import sys
import time

if sys.argv[1:] == ["--version"]:
    print("kubeatlas test-version")
    raise SystemExit(0)
with open(os.environ["FAKE_APP_ARGS"], "w", encoding="utf-8") as stream:
    json.dump(sys.argv[1:], stream)
mode = os.environ.get("FAKE_APP_MODE", "serve")
if mode == "exit":
    time.sleep(0.05)
    raise SystemExit(9)
while True:
    time.sleep(1)
"""


FAKE_GIT = r"""#!/usr/bin/env bash
if [[ "$1" == "rev-parse" ]]; then
  printf '%040d\n' 1
elif [[ "$1" == "status" ]]; then
  exit 0
else
  exit 2
fi
"""


FAKE_SLEEP = "#!/usr/bin/env bash\nexit 0\n"
FAKE_MAKE = "#!/usr/bin/env bash\nexit 0\n"


class EngineeringScriptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.kubectl_log = self.root / "kubectl.jsonl"
        self.kubectl_state = self.root / "kubectl-state.json"
        self.kubectl_count = self.root / "kubectl-count"
        self.curl_log = self.root / "curl.log"
        self.app_args = self.root / "app-args.json"
        self.app = self.bin / "kubeatlas-test"
        self.write_executable(self.bin / "kubectl", FAKE_KUBECTL)
        self.write_executable(self.bin / "curl", FAKE_CURL)
        self.write_executable(self.bin / "git", FAKE_GIT)
        self.write_executable(self.bin / "sleep", FAKE_SLEEP)
        self.write_executable(self.bin / "make", FAKE_MAKE)
        self.write_executable(self.app, FAKE_APP)
        port_socket = socket.socket()
        port_socket.bind(("127.0.0.1", 0))
        self.port = port_socket.getsockname()[1]
        port_socket.close()
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}",
            "KUBEATLAS_BIN": str(self.app),
            "KUBEATLAS_TEST_CONTEXT": "selected-context",
            "FAKE_KUBECTL_LOG": str(self.kubectl_log),
            "FAKE_KUBECTL_STATE": str(self.kubectl_state),
            "FAKE_KUBECTL_COUNT": str(self.kubectl_count),
            "FAKE_CURL_LOG": str(self.curl_log),
            "FAKE_APP_ARGS": str(self.app_args),
            "FAKE_CURL_MODE": "failure",
            "NS_ALICE": "rbac-alice",
            "NS_BOB": "rbac-bob",
            "KUBEATLAS_RBAC_EVIDENCE_DIR": str(self.root / "rbac-evidence"),
            "PORT": str(self.port),
        }

    def write_executable(self, path, content):
        path.write_text(content, encoding="utf-8")
        path.chmod(0o755)

    def run_rbac(self, args=("--skip-build",), input_text="", env=None, path=None):
        return subprocess.run(
            ["/bin/bash", str(RBAC), *args],
            input=input_text,
            text=True,
            capture_output=True,
            env={**self.env, **(env or {}), **({"PATH": path} if path else {})},
            check=False,
        )

    def kubectl_calls(self):
        if not self.kubectl_log.exists():
            return []
        return [json.loads(line) for line in self.kubectl_log.read_text().splitlines()]

    @staticmethod
    def option_value(args, name):
        for index, value in enumerate(args):
            if value == name:
                return args[index + 1]
            if value.startswith(name + "="):
                return value.split("=", 1)[1]
        raise AssertionError(f"missing {name} option in {args!r}")

    def test_help_refusal_eof_and_missing_context_never_call_kubectl(self):
        help_result = self.run_rbac(("--help",), env={"KUBEATLAS_TEST_CONTEXT": ""})
        self.assertEqual(help_result.returncode, 0, help_result.stderr)
        self.assertIn("Usage:", help_result.stdout)
        self.assertEqual(self.kubectl_calls(), [])

        missing_context = self.run_rbac(env={"KUBEATLAS_TEST_CONTEXT": ""})
        self.assertNotEqual(missing_context.returncode, 0)
        self.assertEqual(self.kubectl_calls(), [])

        for answer in ("n\n", ""):
            result = self.run_rbac(input_text=answer)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(self.kubectl_calls(), [])
            self.assertFalse(self.app_args.exists())

    def test_missing_dependency_or_binary_never_calls_kubectl(self):
        minimal_path = self.root / "minimal-bin"
        minimal_path.mkdir()
        self.write_executable(minimal_path / "kubectl", FAKE_KUBECTL)
        for name in ("curl", "python3", "sha256sum", "git", "dirname"):
            executable = shutil.which(name)
            if executable:
                (minimal_path / name).symlink_to(executable)
        missing_dependency = self.run_rbac(path=str(minimal_path))
        self.assertNotEqual(missing_dependency.returncode, 0)
        self.assertIn("missing required dependency: jq", missing_dependency.stderr)
        self.assertEqual(self.kubectl_calls(), [])

        missing_binary = self.run_rbac(env={"KUBEATLAS_BIN": str(self.root / "missing")})
        self.assertNotEqual(missing_binary.returncode, 0)
        self.assertEqual(self.kubectl_calls(), [])

    def test_namespace_collision_is_refused_without_adoption_or_cleanup(self):
        result = self.run_rbac(
            args=("--skip-build", "--yes"),
            env={"NS_ALICE": "existing-namespace", "FAKE_EXISTING_NAMESPACE": "existing-namespace"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Refusing to adopt existing namespace", result.stderr)
        calls = self.kubectl_calls()
        self.assertTrue(any("get" in call for call in calls))
        self.assertFalse(any("create" in call or "delete" in call for call in calls))

    def test_rbac_pins_context_and_binary_kubeconfig_and_flags(self):
        result = self.run_rbac(
            args=("--skip-build", "--yes"),
            env={"FAKE_CURL_MODE": "startup-only"},
        )
        self.assertNotEqual(result.returncode, 0)
        calls = self.kubectl_calls()
        self.assertGreater(len(calls), 5)
        self.assertIn("--context", calls[0])
        self.assertIn("selected-context", calls[0])
        frozen_paths = []
        for call in calls[1:]:
            self.assertEqual(self.option_value(call, "--context"), "selected-context")
            frozen_paths.append(self.option_value(call, "--kubeconfig"))
        self.assertEqual(len(set(frozen_paths)), 1)
        self.assertNotIn("apply", " ".join(" ".join(call) for call in calls))
        self.assertNotIn("--all", " ".join(" ".join(call) for call in calls))
        self.assertTrue(self.app_args.exists())
        args = json.loads(self.app_args.read_text())
        self.assertIn("--no-browser", args)
        self.assertIn("unknown (--skip-build)", result.stdout)
        self.assertIn("Binary SHA-256:", result.stdout)
        self.assertIn("Binary version: kubeatlas test-version", result.stdout)
        self.assertIn("--kubeconfig", args)
        app_config = args[args.index("--kubeconfig") + 1]
        self.assertEqual(app_config, frozen_paths[0])
        self.assertFalse(Path(app_config).exists())
        self.assertNotIn("rbac-test-secret-do-not-use-in-prod", result.stdout + result.stderr)
        self.assertIn('KUBEATLAS_BIN="${KUBEATLAS_BIN:-./kubeatlas}"', RBAC.read_text())
        run_dir = next(line.split(": ", 1)[1] for line in result.stdout.splitlines()
                       if line.startswith("RBAC evidence directory retained: "))
        self.assertTrue((Path(run_dir) / "ledger.jsonl").is_file())
        self.assertTrue((Path(run_dir) / "kubeatlas.log").is_file())

    def test_rbac_busy_port_has_no_cluster_calls_or_server_launch(self):
        port_socket = socket.socket()
        port_socket.bind(("127.0.0.1", 0))
        port_socket.listen()
        try:
            port = port_socket.getsockname()[1]
            result = self.run_rbac(
                args=("--skip-build", "--yes"),
                env={"PORT": str(port)},
            )
        finally:
            port_socket.close()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(f"Port {port} is already in use", result.stderr)
        self.assertEqual(self.kubectl_calls(), [])
        self.assertFalse(self.app_args.exists())

    def test_rbac_rechecks_child_after_http_readiness(self):
        result = self.run_rbac(
            args=("--skip-build", "--yes"),
            env={"FAKE_APP_MODE": "exit", "FAKE_CURL_MODE": "delay-success"},
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("exited during readiness", result.stderr)
        self.assertEqual(
            self.curl_log.read_text(encoding="utf-8").splitlines(),
            [f"http://localhost:{self.env['PORT']}/api/health"],
        )

    def test_rbac_external_binary_override_has_unknown_build_provenance(self):
        source = RBAC.read_text(encoding="utf-8").split("MUTATIONS_STARTED=true", 1)[0]
        helper_dir = str(ROOT / "scripts")
        source = source.replace(
            'SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"',
            f'SCRIPT_DIR="{helper_dir}"',
        )
        fixture_script = self.root / "rbac-binary-provenance.sh"
        fixture_script.write_text(source + "\nexit 0\n", encoding="utf-8")
        result = subprocess.run(
            ["/bin/bash", str(fixture_script), "--yes"],
            text=True,
            capture_output=True,
            env={**self.env, "KUBEATLAS_BIN": str(self.app)},
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Building KubeAtlas...", result.stdout)
        self.assertIn("unknown (custom KUBEATLAS_BIN override)", result.stdout)
        self.assertNotIn("make build from checkout", result.stdout)
        self.assertIn("Binary SHA-256:", result.stdout)
        self.assertIn("Binary version: kubeatlas test-version", result.stdout)
        self.assertFalse(self.app_args.exists())
        self.assertFalse(any("create" in call or "delete" in call for call in self.kubectl_calls()))

    def test_rbac_cleanup_failure_changes_successful_test_exit_to_nonzero(self):
        source = RBAC.read_text(encoding="utf-8").split("# --- Helpers ---", 1)[0]
        helper_dir = str(ROOT / "scripts")
        source = source.replace(
            'SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"',
            f'SCRIPT_DIR="{helper_dir}"',
        )
        fixture_script = self.root / "rbac-cleanup-success.sh"
        fixture_script.write_text(source + "\nexit 0\n", encoding="utf-8")
        result = subprocess.run(
            ["/bin/bash", str(fixture_script), "--skip-build", "--yes"],
            text=True,
            capture_output=True,
            env={
                **self.env,
                "FAKE_CURL_MODE": "startup-only",
                "FAKE_FAIL_DELETE": "1",
            },
            check=False,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Cleanup failed or residue is uncertain", result.stderr)

    def visual_env(self, **values):
        return {
            **self.env,
            "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}",
            "FAKE_CURL_MODE": "success",
            **values,
        }

    def run_visual(self, cwd, env):
        return subprocess.run(
            ["/bin/bash", str(VISUAL), "--skip-build"],
            cwd=cwd,
            text=True,
            capture_output=True,
            env=env,
            check=False,
        )

    def prepare_visual_cwd(self, name):
        cwd = self.root / name
        cwd.mkdir()
        visual_binary = cwd / "kubeatlas"
        visual_binary.write_text(FAKE_APP, encoding="utf-8")
        visual_binary.chmod(0o755)
        return cwd

    def test_visual_start_refuses_existing_running_state(self):
        cwd = self.prepare_visual_cwd("visual-existing")
        state = cwd / ".playwright-mcp/visual-test-state.env"
        state.parent.mkdir()
        state.write_text(f"KUBEATLAS_PID={os.getpid()}\n", encoding="utf-8")
        result = self.run_visual(cwd, self.visual_env())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("already recorded", result.stderr)
        self.assertFalse(self.app_args.exists())

    def test_visual_early_exit_and_http_failure_do_not_write_state(self):
        for name, mode, curl_mode in (
            ("early-exit", "exit", "delay-success"),
            ("http-error", "serve", "failure"),
        ):
            cwd = self.prepare_visual_cwd(name)
            self.app_args.unlink(missing_ok=True)
            result = self.run_visual(
                cwd,
                self.visual_env(FAKE_APP_MODE=mode, FAKE_CURL_MODE=curl_mode),
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((cwd / ".playwright-mcp/visual-test-state.env").exists())

    def test_visual_uses_kubeatlas_no_browser_and_reports_unknown_skip_build_provenance(self):
        cwd = self.prepare_visual_cwd("visual-success")
        result = self.run_visual(cwd, self.visual_env())
        self.assertEqual(result.returncode, 0, result.stderr)
        args = json.loads(self.app_args.read_text())
        self.assertIn("--no-browser", args)
        self.assertIn("--port", args)
        self.assertIn("unknown (--skip-build)", result.stdout)
        self.assertIn("Binary SHA-256:", result.stdout)
        self.assertIn("Binary version: kubeatlas test-version", result.stdout)
        state = cwd / ".playwright-mcp/visual-test-state.env"
        values = dict(line.split("=", 1) for line in state.read_text().splitlines())
        pid = int(values["KUBEATLAS_PID"])
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            pass


if __name__ == "__main__":
    unittest.main()
