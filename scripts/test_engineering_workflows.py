import fnmatch
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

from engineering_gate import CI_JOBS, package_results


ROOT = Path(__file__).resolve().parents[1]
PACKAGE_PATHS = ROOT / ".github/package-paths.yml"
PACKAGE_WORKFLOW = ROOT / ".github/workflows/package-validation.yml"
CI_WORKFLOW = ROOT / ".github/workflows/ci.yml"
SECURITY_WORKFLOW = ROOT / ".github/workflows/security.yml"

EXPECTED_PACKAGE_PATHS = [
    "cmd/**", "internal/**", "pkg/**", "web/**", "packages/**", "**/*.go",
    "go.mod", "go.sum", "go.work*", "package.json", "package-lock.json",
    ".npmrc", ".goreleaser.yaml", "Makefile", "LICENSE", "NOTICE", "README.md",
    "docs/packaging.md", "scripts/release*.py", "scripts/test_release*.py",
    "scripts/engineering_gate.py", "scripts/test_engineering*.py",
    ".github/package-paths.yml",
    ".github/workflows/{ci,package-validation,release-binaries}.yml",
    ".github/actions/**",
]


def job_block(workflow, job):
    section = workflow.split(f"  {job}:\n", 1)[1]
    return re.split(r"\n  [a-z][a-z0-9-]*:\n", section, maxsplit=1)[0]


def workflow_step_script(workflow, name):
    lines = workflow.splitlines()
    marker = f"- name: {name}"
    start = next(index for index, line in enumerate(lines) if line.strip() == marker)
    run = next(index for index in range(start + 1, len(lines)) if lines[index].strip() == "run: |")
    script = []
    for line in lines[run + 1:]:
        if not line.strip():
            script.append("")
            continue
        if len(line) - len(line.lstrip()) < 10:
            break
        script.append(line[10:])
    return "\n".join(script).rstrip() + "\n"


def run_workflow_script(script, cwd, env):
    return subprocess.run(
        ["bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", script],
        cwd=cwd,
        env=env,
        text=True,
        capture_output=True,
        check=False,
    )


class EngineeringWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.paths_text = PACKAGE_PATHS.read_text(encoding="utf-8")
        self.package = PACKAGE_WORKFLOW.read_text(encoding="utf-8")
        self.ci = CI_WORKFLOW.read_text(encoding="utf-8")
        self.security = SECURITY_WORKFLOW.read_text(encoding="utf-8")

    def patterns(self):
        return re.findall(r"^\s+- '([^']+)'$", self.paths_text, re.MULTILINE)

    def selected_by_real_config(self, path):
        return any(fnmatch.fnmatchcase(path, pattern) for pattern in self.patterns())

    def test_central_package_paths_cover_build_inputs(self):
        self.assertEqual(self.patterns(), EXPECTED_PACKAGE_PATHS)
        for path in (
            "cmd/explorer/main.go",
            "internal/server/server.go",
            "pkg/auth/auth.go",
            "pkg/go.mod",
            "pkg/go.sum",
            "package-lock.json",
            "web/package-lock.json",
            "packages/k8s-ui/package-lock.json",
            "packages/k8s-ui/src/index.tsx",
        ):
            with self.subTest(path=path):
                self.assertTrue(self.selected_by_real_config(path))

    def test_package_pr_main_and_draft_selection_semantics(self):
        on_block = self.package.split("on:\n", 1)[1].split("\npermissions:", 1)[0]
        self.assertIn("pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]", on_block)
        self.assertIn("push:\n    branches: [main]", on_block)
        self.assertNotIn("paths:", on_block)
        changes = job_block(self.package, "changes")
        self.assertIn("contents: read", changes)
        self.assertIn("pull-requests: read", changes)
        self.assertIn("github.event_name == 'push' && 'true' || steps.filter.outputs.packages", changes)
        self.assertIn("dorny/paths-filter@ceb8a2b8f2d89434be7ff52d3de7ec3738c5cc9d", changes)
        self.assertIn("filters: .github/package-paths.yml", changes)
        validate = job_block(self.package, "validate")
        self.assertIn("needs: changes", validate)
        self.assertIn(
            "if: needs.changes.outputs.packages == 'true' && "
            "(github.event_name != 'pull_request' || github.event.pull_request.draft == false)",
            validate,
        )

        cases = (
            ("main-push", "true", "success", "passed"),
            ("ready-pr-selected", "true", "success", "passed"),
            ("ready-pr-unselected", "false", "skipped", "not_required_by_plan"),
            ("draft-pr-selected", "true", "skipped", "failed"),
            ("draft-pr-unselected", "false", "skipped", "not_required_by_plan"),
        )
        for name, selected, result, expected in cases:
            with self.subTest(name=name):
                needs = {
                    "changes": {"result": "success", "outputs": {"packages": selected}},
                    "validate": {"result": result},
                }
                self.assertEqual(package_results(needs)["validate"], expected)

    def test_ci_gate_is_terminal_and_requires_the_frozen_job_inventory(self):
        gate = job_block(self.ci, "required-gates")
        needs = re.findall(r"^\s+- ([a-z][a-z0-9-]+)$", gate, re.MULTILINE)
        self.assertEqual(needs, list(CI_JOBS))
        self.assertIn("name: CI required gates", gate)
        self.assertIn("if: always()", gate)
        self.assertIn("timeout-minutes: 5", gate)
        self.assertIn("NEEDS_JSON: ${{ toJSON(needs) }}", gate)
        self.assertIn("HEAD_SHA: ${{ github.event.pull_request.head.sha || github.sha }}", gate)
        self.assertIn("receipt --gate ci --output \"$RUNNER_TEMP/ci.json\"", gate)
        self.assertIn("engineering-ci-${{ github.run_id }}-${{ github.run_attempt }}", gate)
        self.assertIn("if-no-files-found: error", gate)
        changes = job_block(self.ci, "changes")
        for selector in ("scripts/engineering_gate.py", "scripts/test_engineering*.py", ".github/package-paths.yml"):
            self.assertIn(selector, changes)
        tests = job_block(self.ci, "engineering-tests")
        self.assertIn("python3 -m unittest discover -s scripts -p 'test_engineering*.py'", tests)
        self.assertIn("shellcheck \"${changed_shell[@]}\"", tests)
        self.assertIn("git diff --diff-filter=ACMR --name-only -z", tests)
        self.assertIn("mapfile -d '' -t changed_shell < \"$changed_shell_file\"", tests)

    def test_ci_shell_syntax_step_checks_second_script(self):
        script = workflow_step_script(self.ci, "Check shell syntax")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            scripts = root / "scripts"
            scripts.mkdir()
            (scripts / "test-rbac.sh").write_text("echo valid\n", encoding="utf-8")
            (scripts / "visual-test-start.sh").write_text("if broken\n", encoding="utf-8")
            result = run_workflow_script(script, root, os.environ.copy())
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("syntax error", result.stderr)

    def test_ci_shellcheck_fails_when_git_diff_fails(self):
        script = workflow_step_script(self.ci, "ShellCheck changed shell scripts")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            bin_dir = root / "bin"
            runner_temp = root / "runner-temp"
            bin_dir.mkdir()
            runner_temp.mkdir()
            shellcheck_marker = root / "shellcheck-called"
            fake_git = bin_dir / "git"
            fake_git.write_text(
                "#!/usr/bin/env bash\n"
                "echo 'git diff failed: invalid base' >&2\n"
                "exit 128\n",
                encoding="utf-8",
            )
            fake_git.chmod(0o755)
            fake_shellcheck = bin_dir / "shellcheck"
            fake_shellcheck.write_text(
                "#!/usr/bin/env bash\n"
                'touch "$SHELLCHECK_MARKER"\n',
                encoding="utf-8",
            )
            fake_shellcheck.chmod(0o755)
            env = {
                **os.environ,
                "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
                "BASE_SHA": "invalid-base",
                "HEAD_SHA": "valid-head",
                "RUNNER_TEMP": str(runner_temp),
                "SHELLCHECK_MARKER": str(shellcheck_marker),
            }
            result = run_workflow_script(script, root, env)
            self.assertFalse(shellcheck_marker.exists())
            self.assertEqual(list(runner_temp.iterdir()), [])
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("invalid base", result.stderr)

    def test_ci_shellcheck_preserves_filenames_with_spaces(self):
        script = workflow_step_script(self.ci, "ShellCheck changed shell scripts")
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            bin_dir = root / "bin"
            runner_temp = root / "runner-temp"
            bin_dir.mkdir()
            runner_temp.mkdir()
            shellcheck_args = root / "shellcheck-args"
            fake_git = bin_dir / "git"
            fake_git.write_text(
                "#!/usr/bin/env bash\n"
                "printf 'scripts/with space.sh\\0'\n",
                encoding="utf-8",
            )
            fake_git.chmod(0o755)
            fake_shellcheck = bin_dir / "shellcheck"
            fake_shellcheck.write_text(
                "#!/usr/bin/env bash\n"
                "printf '%s\\n' \"$@\" > \"$SHELLCHECK_ARGS_FILE\"\n",
                encoding="utf-8",
            )
            fake_shellcheck.chmod(0o755)
            env = {
                **os.environ,
                "PATH": f"{bin_dir}{os.pathsep}{os.environ['PATH']}",
                "BASE_SHA": "base",
                "HEAD_SHA": "head",
                "RUNNER_TEMP": str(runner_temp),
                "SHELLCHECK_ARGS_FILE": str(shellcheck_args),
            }
            result = run_workflow_script(script, root, env)
            shellcheck_output = shellcheck_args.read_text(encoding="utf-8")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(shellcheck_output, "scripts/with space.sh\n")

    def test_package_and_security_gates_are_mandatory_and_terminal(self):
        package_gate = job_block(self.package, "required-gates")
        self.assertIn("name: Package required gates", package_gate)
        self.assertIn("if: always()", package_gate)
        self.assertEqual(re.findall(r"^\s+- ([a-z][a-z0-9-]+)$", package_gate, re.MULTILINE),
                         ["changes", "validate"])
        self.assertIn("receipt --gate packages", package_gate)
        self.assertIn("engineering-packages-${{ github.run_id }}-${{ github.run_attempt }}", package_gate)

        self.assertIn("types: [opened, synchronize, reopened, ready_for_review]", self.security)
        security_gate = job_block(self.security, "required-gates")
        self.assertIn("name: Security required gates", security_gate)
        self.assertIn("if: always()", security_gate)
        self.assertEqual(re.findall(r"^\s+- ([a-z][a-z0-9-]+)$", security_gate, re.MULTILINE),
                         ["sast"])
        self.assertIn("receipt --gate security", security_gate)
        self.assertIn("engineering-security-${{ github.run_id }}-${{ github.run_attempt }}", security_gate)
        self.assertIn("sast:", self.security)


if __name__ == "__main__":
    unittest.main()
