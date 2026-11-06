import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / "scripts/engineering_rbac.py"
RBAC_SCRIPT = ROOT / "scripts/test-rbac.sh"


FAKE_KUBECTL = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import re
import sys

args = sys.argv[1:]
log = Path(os.environ["FAKE_KUBECTL_LOG"])
state_path = Path(os.environ["FAKE_KUBECTL_STATE"])
with log.open("a", encoding="utf-8") as stream:
    stream.write(json.dumps(args) + "\n")
state = json.loads(state_path.read_text()) if state_path.exists() else []

def save():
    state_path.write_text(json.dumps(state))

def value_after(flag):
    return args[args.index(flag) + 1]

if "config" in args:
    if "view" in args:
        print("apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n")
    elif "current-context" in args:
        print(value_after("--context"))
    raise SystemExit(0)

if "get" in args and not any(item.startswith("--raw=") for item in args):
    index = args.index("get")
    resource = args[index + 1]
    name = args[index + 2]
    if os.environ.get("FAKE_EXISTING_NAMESPACE") == name and resource in ("namespace", "namespaces"):
        print("namespace/" + name)
    if os.environ.get("FAKE_EXISTING_CRD") == name and resource in ("crd", "customresourcedefinition"):
        print("customresourcedefinition.apiextensions.k8s.io/" + name)
    raise SystemExit(0)

if "wait" in args:
    raise SystemExit(0)

if "create" in args and "--dry-run=client" in args:
    manifest = sys.stdin.read()
    api_version = re.search(r"(?m)^apiVersion:\s*(.+)$", manifest).group(1)
    kind = re.search(r"(?m)^kind:\s*(.+)$", manifest).group(1)
    name = re.search(r"(?m)^\s+name:\s*(.+)$", manifest).group(1)
    namespace_match = re.search(r"(?m)^\s+namespace:\s*(.+)$", manifest)
    obj = {"apiVersion": api_version, "kind": kind,
           "metadata": {"name": name}}
    if namespace_match:
        obj["metadata"]["namespace"] = namespace_match.group(1)
    print(json.dumps(obj))
    raise SystemExit(0)

if "create" in args:
    count_file = Path(os.environ["FAKE_KUBECTL_COUNT"])
    count = int(count_file.read_text()) if count_file.exists() else 0
    count += 1
    count_file.write_text(str(count))
    obj = json.loads(sys.stdin.read())
    name = obj["metadata"]["name"]
    if os.environ.get("FAKE_FAIL_CREATE_NUMBER") == str(count):
        print("simulated create failure", file=sys.stderr)
        raise SystemExit(1)
    obj["metadata"]["uid"] = "uid-" + name
    obj["metadata"]["resourceVersion"] = "1"
    state.append(obj)
    save()
    print(json.dumps(obj))
    raise SystemExit(0)

raw = next((item.split("=", 1)[1] for item in args if item.startswith("--raw=")), None)
if "get" in args and raw:
    name = raw.rstrip("/").split("/")[-1]
    obj = next((item for item in state if item["metadata"]["name"] == name), None)
    if obj is None:
        print('Error from server (NotFound): object not found', file=sys.stderr)
        raise SystemExit(1)
    print(json.dumps(obj))
    raise SystemExit(0)

if "delete" in args and raw:
    options = json.loads(sys.stdin.read())
    if os.environ.get("FAKE_FAIL_DELETE") == "1":
        print("simulated delete failure", file=sys.stderr)
        raise SystemExit(1)
    name = raw.rstrip("/").split("/")[-1]
    item = next((item for item in state if item["metadata"]["name"] == name), None)
    if item is None:
        raise SystemExit(1)
    assert options["preconditions"]["uid"] == item["metadata"]["uid"]
    assert options["preconditions"]["resourceVersion"] == item["metadata"]["resourceVersion"]
    state.remove(item)
    save()
    print("{}")
    raise SystemExit(0)

print("unexpected fake kubectl invocation", file=sys.stderr)
raise SystemExit(2)
"""


class EngineeringRBACTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        fake = self.bin / "kubectl"
        fake.write_text(FAKE_KUBECTL, encoding="utf-8")
        fake.chmod(0o755)
        self.log = self.root / "kubectl.jsonl"
        self.state = self.root / "state.json"
        self.count = self.root / "create-count"
        self.ledger = self.root / "ledger.jsonl"
        self.kubeconfig = self.root / "frozen-kubeconfig"
        self.kubeconfig.write_text("apiVersion: v1\n", encoding="utf-8")
        self.env = {
            **os.environ,
            "PATH": f"{self.bin}{os.pathsep}{os.environ['PATH']}",
            "FAKE_KUBECTL_LOG": str(self.log),
            "FAKE_KUBECTL_STATE": str(self.state),
            "FAKE_KUBECTL_COUNT": str(self.count),
        }

    def call_helper(self, command, manifest=None, extra_env=None):
        return subprocess.run(
            [
                sys.executable,
                str(HELPER),
                command,
                "--ledger",
                str(self.ledger),
                "--kubeconfig",
                str(self.kubeconfig),
                "--context",
                "test-context",
                "--owner",
                "rbac-0123456789ab",
            ],
            input=manifest,
            text=True,
            capture_output=True,
            env={**self.env, **(extra_env or {})},
            check=False,
        )

    def create_manifest(self, name):
        return (
            "apiVersion: v1\nkind: Namespace\nmetadata:\n"
            f"  name: {name}\n"
        )

    def read_calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_collision_is_create_only_and_never_adopted(self):
        result = self.call_helper(
            "create",
            self.create_manifest("preexisting"),
            {"FAKE_FAIL_CREATE_NUMBER": "1"},
        )
        self.assertNotEqual(result.returncode, 0)
        records = [json.loads(line) for line in self.ledger.read_text().splitlines()]
        self.assertEqual([record["status"] for record in records], ["attempted", "uncertain"])
        cleanup = self.call_helper("cleanup")
        self.assertNotEqual(cleanup.returncode, 0)
        calls = self.read_calls()
        self.assertTrue(any("create" in call and "--dry-run=client" not in call for call in calls))
        self.assertFalse(any("delete" in call for call in calls))
        self.assertFalse(any(record.get("status") == "created" for record in records))

    def test_partial_success_cleans_only_ledgered_uid_with_server_preconditions(self):
        first = self.call_helper("create", self.create_manifest("owned-one"))
        second = self.call_helper(
            "create",
            self.create_manifest("uncertain-two"),
            {"FAKE_FAIL_CREATE_NUMBER": "2"},
        )
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertNotEqual(second.returncode, 0)
        cleanup = self.call_helper("cleanup")
        self.assertNotEqual(cleanup.returncode, 0)
        records = [json.loads(line) for line in self.ledger.read_text().splitlines()]
        created = next(record for record in records if record["status"] == "created")
        self.assertEqual(
            {key: created[key] for key in ("kind", "apiVersion", "namespace", "name", "uid")},
            {
                "kind": "Namespace",
                "apiVersion": "v1",
                "namespace": "",
                "name": "owned-one",
                "uid": "uid-owned-one",
            },
        )
        self.assertEqual(
            created["ownerLabel"],
            {"key": "kubeatlas.dev/engineering-test-run", "value": "rbac-0123456789ab"},
        )
        calls = self.read_calls()
        delete_calls = [call for call in calls if "delete" in call]
        self.assertEqual(len(delete_calls), 1)
        self.assertIn("--raw=/api/v1/namespaces/owned-one", delete_calls[0])
        self.assertEqual(delete_calls[0][-2:], ["-f", "-"])
        for call in calls:
            self.assertIn(f"--kubeconfig={self.kubeconfig}", call)
            self.assertIn("--context=test-context", call)
        delete_record = next(record for record in records if record["status"] == "created")
        self.assertEqual(delete_record["uid"], "uid-owned-one")
        self.assertIn("Uncertain residue retained", cleanup.stderr)

    def test_cleanup_deletes_created_objects_in_reverse_ledger_order(self):
        self.assertEqual(self.call_helper("create", self.create_manifest("first")).returncode, 0)
        self.assertEqual(self.call_helper("create", self.create_manifest("second")).returncode, 0)
        cleanup = self.call_helper("cleanup")
        self.assertEqual(cleanup.returncode, 0, cleanup.stderr)
        delete_calls = [call for call in self.read_calls() if "delete" in call]
        self.assertEqual(
            [next(item.split("=", 1)[1] for item in call if item.startswith("--raw="))
             for call in delete_calls],
            ["/api/v1/namespaces/second", "/api/v1/namespaces/first"],
        )

    def test_cleanup_failure_is_nonzero_and_shell_preserves_test_status(self):
        created = self.call_helper("create", self.create_manifest("cleanup-fails"))
        self.assertEqual(created.returncode, 0, created.stderr)
        cleanup = self.call_helper("cleanup", extra_env={"FAKE_FAIL_DELETE": "1"})
        self.assertNotEqual(cleanup.returncode, 0)
        script = RBAC_SCRIPT.read_text(encoding="utf-8")
        self.assertIn('[[ "$original_status" -ne 0 ]] || original_status=1', script)
        self.assertIn('python3 "$SCRIPT_DIR/engineering_rbac.py" cleanup', script)

    def test_helper_never_sends_user_config_or_current_context_to_mutations(self):
        created = self.call_helper("create", self.create_manifest("pinned"))
        self.assertEqual(created.returncode, 0, created.stderr)
        for call in self.read_calls():
            self.assertIn(f"--kubeconfig={self.kubeconfig}", call)
            self.assertIn("--context=test-context", call)
            self.assertNotIn("current-context", call)
        self.assertNotRegex(created.stdout, r"token|password|credential")


if __name__ == "__main__":
    unittest.main()
