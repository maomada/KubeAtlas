import copy
import unittest
from unittest.mock import patch

import release_policy as policy


SHA = "a" * 40


def run(run_id=1, **changes):
    result = {
        "id": run_id, "run_attempt": 1, "head_sha": SHA,
        "event": "push", "head_branch": "main",
        "head_repository": {"full_name": policy.REPOSITORY},
        "path": ".github/workflows/ci.yml", "status": "completed",
        "conclusion": "success", "html_url": "https://example.test/run",
    }
    result.update(changes)
    return result


class ReleasePolicyTests(unittest.TestCase):
    def test_exact_repository_ref_sha_and_semver(self):
        for version in ("0.1.0", "2.0.0-rc.1"):
            policy.validate_inputs(policy.REPOSITORY, "refs/heads/main", SHA, version)
        for repository, ref, sha, version in (
            ("skyhook-io/radar", "refs/heads/main", SHA, "1.0.0"),
            ("maomada/radar", "refs/heads/main", SHA, "1.0.0"),
            (policy.REPOSITORY, "refs/heads/test", SHA, "1.0.0"),
            (policy.REPOSITORY, "refs/heads/main", "main", "1.0.0"),
            (policy.REPOSITORY, "refs/heads/main", SHA, "v1.0.0"),
            (policy.REPOSITORY, "refs/heads/main", SHA, "1.0.0+metadata"),
            (policy.REPOSITORY, "refs/heads/main", SHA, "1.0.0-01"),
            (policy.REPOSITORY, "refs/heads/main", SHA, "1.0.0\ncommand"),
        ):
            with self.subTest(repository=repository, ref=ref, sha=sha, version=version):
                with self.assertRaises(ValueError):
                    policy.validate_inputs(repository, ref, sha, version)

    def test_latest_attempt_must_pass_not_historical_success(self):
        for workflow in policy.WORKFLOWS:
            path = f".github/workflows/{workflow}"
            for changes in (
                {"status": "in_progress", "conclusion": None},
                {"conclusion": "failure"}, {"conclusion": "cancelled"},
                {"conclusion": "skipped"}, {"conclusion": "neutral"},
                {"conclusion": "timed_out"},
            ):
                with self.subTest(workflow=workflow, changes=changes):
                    with self.assertRaises(ValueError):
                        policy.require_success([{"workflow_runs": [run(path=path), run(2, path=path, **changes)]}], SHA, workflow)
            self.assertEqual(
                policy.require_success([{"workflow_runs": [run(1, path=path, conclusion="failure"), run(2, path=path)]}], SHA, workflow),
                "https://example.test/run",
            )

    def test_other_sha_event_branch_repository_or_workflow_is_not_evidence(self):
        for workflow in policy.WORKFLOWS:
            for key, value in (
                ("head_sha", "b" * 40), ("event", "pull_request"),
                ("head_branch", "feature"), ("head_repository", {"full_name": "other/radar"}),
                ("path", ".github/workflows/other.yml"),
            ):
                with self.subTest(workflow=workflow, key=key):
                    args = {"path": f".github/workflows/{workflow}", key: value}
                    with self.assertRaises(ValueError):
                        policy.require_success([{"workflow_runs": [run(**args)]}], SHA, workflow)
            with self.assertRaises(ValueError):
                policy.require_success([{"workflow_runs": []}], SHA, workflow)

    def test_standalone_security_is_required_and_codeql_is_not_a_substitute(self):
        self.assertEqual(policy.WORKFLOWS, ("ci.yml", "security.yml"))
        codeql = run(path=".github/workflows/codeql.yml")
        with self.assertRaises(ValueError):
            policy.require_success([{"workflow_runs": [codeql]}], SHA, "security.yml")
        security = run(path=".github/workflows/security.yml")
        self.assertEqual(policy.require_success([{"workflow_runs": [security]}], SHA, "security.yml"), security["html_url"])

    @patch.dict("os.environ", {"GITHUB_REPOSITORY": policy.REPOSITORY, "GITHUB_REF": "refs/heads/main"})
    @patch("release_policy.subprocess.run")
    def test_requires_both_workflows_and_main_ancestry(self, execute):
        import json
        import subprocess

        ci = run()
        security = copy.deepcopy(ci)
        security["path"] = ".github/workflows/security.yml"
        execute.side_effect = [
            subprocess.CompletedProcess([], 0),
            subprocess.CompletedProcess([], 0, json.dumps([{"workflow_runs": [ci]}])),
            subprocess.CompletedProcess([], 0, json.dumps([{"workflow_runs": [security]}])),
        ]
        self.assertEqual(set(policy.validate_source("source", SHA, "1.0.0")["checks"]), set(policy.WORKFLOWS))
        self.assertEqual(execute.call_args_list[0].args[0], ["git", "-C", "source", "merge-base", "--is-ancestor", SHA, "origin/main"])
        self.assertIn("/security.yml/runs?", execute.call_args_list[2].args[0][-1])
        execute.side_effect = subprocess.CalledProcessError(1, ["git"])
        with self.assertRaises(subprocess.CalledProcessError):
            policy.validate_source("source", SHA, "1.0.0")


if __name__ == "__main__":
    unittest.main()
