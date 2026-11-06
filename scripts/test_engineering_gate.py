import copy
import unittest

from engineering_gate import CI_JOBS, SELECTORS, aggregate, ci_results, package_results, receipt


class EngineeringGateTests(unittest.TestCase):
    def needs(self, selected=True):
        needs = {job: {"result": "success"} for job in CI_JOBS}
        needs["changes"]["outputs"] = {key: str(selected).lower() for key in SELECTORS}
        for job, selector in CI_JOBS.items():
            if selector is not None:
                required = not selected if selector.startswith("!") else selected
                needs[job]["result"] = "success" if required else "skipped"
        return needs

    def env(self):
        return {"GITHUB_REPOSITORY": "maomada/KubeAtlas", "GITHUB_EVENT_NAME": "pull_request",
                "HEAD_SHA": "a" * 40, "GITHUB_SHA": "b" * 40,
                "GITHUB_RUN_ID": "12", "GITHUB_RUN_ATTEMPT": "1"}

    def bundle(self):
        needs = self.needs()
        package = {"changes": {"result": "success", "outputs": {"packages": "true"}},
                   "validate": {"result": "success"}}
        receipts = [receipt("ci", needs, self.env()),
                    receipt("security", {"sast": {"result": "success"}}, self.env()),
                    receipt("packages", package, self.env())]
        plan = {"schema": "kubeatlas-engineering-plan/v1", "repository": "maomada/KubeAtlas",
                "origin": "github-actions", "profile": "pr-ready", "head_sha": "a" * 40,
                "tested_sha": "b" * 40, "gates": {}}
        for evidence in receipts:
            plan["gates"][evidence["gate"]] = {
                "run_id": "12", "run_attempt": "1",
                "required_jobs": {job: value != "not_required_by_plan"
                                  for job, value in evidence["results"].items()}}
        return plan, receipts

    def test_required_jobs_fail_closed(self):
        for state in ("skipped", "failure", "cancelled", None, "neutral"):
            with self.subTest(state=state):
                needs = self.needs()
                needs["backend"]["result"] = state
                self.assertEqual(ci_results(needs)["backend"], "failed")
        needs = self.needs()
        del needs["backend"]
        with self.assertRaises(ValueError):
            ci_results(needs)

    def test_only_plan_can_exempt_jobs(self):
        self.assertEqual(ci_results(self.needs(False))["backend"], "not_required_by_plan")
        needs = self.needs(False)
        needs["backend"]["result"] = "failure"
        self.assertEqual(ci_results(needs)["backend"], "failed")
        del needs["changes"]["outputs"]["go"]
        with self.assertRaises(ValueError):
            ci_results(needs)

    def test_draft_deferred_package_is_not_pass(self):
        needs = {"changes": {"result": "success", "outputs": {"packages": "true"}},
                 "validate": {"result": "skipped"}}
        self.assertEqual(package_results(needs)["validate"], "failed")
        needs["changes"]["outputs"]["packages"] = "false"
        self.assertEqual(package_results(needs)["validate"], "not_required_by_plan")

    def test_matching_receipts_pass(self):
        self.assertEqual(aggregate(*self.bundle())["status"], "passed")

    def test_wrong_identity_and_results_block(self):
        for key, wrong in (("head_sha", "c" * 40), ("tested_sha", "c" * 40),
                           ("origin", "local"), ("event", "push"), ("run_id", "11"),
                           ("run_attempt", "2"), ("status", "blocked_external")):
            with self.subTest(key=key):
                plan, receipts = self.bundle()
                receipts[0][key] = wrong
                with self.assertRaises(ValueError):
                    aggregate(plan, receipts)

    def test_missing_duplicate_and_incomplete_receipts_block(self):
        plan, receipts = self.bundle()
        for bad in (receipts[:-1], [receipts[0], receipts[0], receipts[2]]):
            with self.assertRaises(ValueError):
                aggregate(plan, bad)
        for value in ("failed", "untested", "blocked_external", "not_required_by_plan"):
            bad = copy.deepcopy(receipts)
            bad[0]["results"]["backend"] = value
            with self.assertRaises(ValueError):
                aggregate(plan, bad)

    def test_plan_cannot_omit_mandatory_workflow_or_job(self):
        plan, receipts = self.bundle()
        plan["gates"]["security"]["required_jobs"]["sast"] = False
        with self.assertRaises(ValueError):
            aggregate(plan, receipts)
        plan, receipts = self.bundle()
        del plan["gates"]["security"]
        with self.assertRaises(ValueError):
            aggregate(plan, receipts)


if __name__ == "__main__":
    unittest.main()
