import copy
import tempfile
from pathlib import Path
import unittest

import security_dispositions as gate


class DispositionTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.file = self.root / "example.go"
        self.file.write_text("package example\n")
        self.finding = {"check_id": "audit.rule", "path": str(self.file),
                        "start": {"line": 1, "col": 1}, "end": {"line": 1, "col": 16}}
        self.report = {"results": [self.finding]}
        self.entry = {"id": "SAST-001", "finding": {**self.finding, "path": "example.go"},
                      "disposition": "false-positive", "evidence": "Test fixture only",
                      "reviewedSourceSha": "a" * 40,
                      "approval": {"approved": True, "actor": "reviewer", "reference": "test"}}
        self.policy = {"schemaVersion": 1, "engine": "pinned", "rulesLockSha256": "lock",
                       "sourceTreeSha256": gate.source_digest(self.root, [self.file]),
                       "entries": [self.entry]}

    def evaluate(self, report=None, policy=None):
        return gate.evaluate(report or self.report, policy or self.policy, self.root,
                             [self.file], "pinned", "lock")

    def test_approved_exact_finding_passes_without_mutating_raw_report(self):
        original = copy.deepcopy(self.report)
        result = self.evaluate()
        self.assertTrue(result["passed"])
        self.assertEqual(len(result["approved"]), 1)
        self.assertEqual(self.report, original)

    def test_new_finding_and_explicit_hold_still_block(self):
        extra = {**self.finding, "check_id": "new.rule"}
        result = self.evaluate({"results": [self.finding, extra]})
        self.assertFalse(result["passed"])
        self.assertEqual(len(result["unresolved"]), 1)
        self.entry["disposition"] = "hold"
        self.assertFalse(self.evaluate()["passed"])

    def test_mitigated_finding_retains_classification_and_requires_evidence(self):
        self.entry["disposition"] = "mitigated"
        result = self.evaluate()
        self.assertTrue(result["passed"])
        self.assertEqual(result["approved"][0]["disposition"], "mitigated")
        self.entry["evidence"] = ""
        with self.assertRaises(ValueError):
            self.evaluate()

    def test_source_and_rule_changes_require_new_review(self):
        for field in ("engine", "rulesLockSha256", "sourceTreeSha256"):
            with self.subTest(field=field):
                policy = {**self.policy, field: "changed"}
                with self.assertRaises(ValueError):
                    self.evaluate(policy=policy)
        self.file.write_text("package changed\n")
        with self.assertRaises(ValueError):
            self.evaluate()

    def test_missing_approval_evidence_stale_and_duplicate_entries_fail_closed(self):
        for edit in (lambda e: e.update(evidence=""),
                     lambda e: e["approval"].update(approved=False),
                     lambda e: e.update(disposition="ignore"),
                     lambda e: e["finding"].update(check_id="absent.rule"),
                     lambda e: e["finding"].update(path="../outside.go")):
            policy = copy.deepcopy(self.policy)
            edit(policy["entries"][0])
            with self.assertRaises(ValueError):
                self.evaluate(policy=policy)
        self.policy["entries"].append(copy.deepcopy(self.entry))
        with self.assertRaises(ValueError):
            self.evaluate()


if __name__ == "__main__":
    unittest.main()
