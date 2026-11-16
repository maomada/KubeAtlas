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

    def accepted_risk_policy(self):
        policy = copy.deepcopy(self.policy)
        policy["schemaVersion"] = 2
        entry = policy["entries"][0]
        entry["disposition"] = "accepted-risk"
        entry["approval"]["kind"] = "maintainer-risk-acceptance"
        entry["riskAcceptance"] = {
            "acceptedBy": entry["approval"]["actor"], "decision": "accept",
            "scope": "Explicitly configured test-only HTTP origin",
            "residualRisk": "The test session is sent without TLS encryption",
            "conditions": ["Exact configured authority only"],
        }
        return policy

    def test_accepted_risk_is_visible_separately_and_never_relabels_raw_findings(self):
        original = copy.deepcopy(self.report)
        result = self.evaluate(policy=self.accepted_risk_policy())
        self.assertTrue(result["passed"])
        self.assertEqual(result["approved"], [])
        self.assertEqual(len(result["acceptedRisks"]), 1)
        self.assertEqual(result["acceptedRisks"][0]["disposition"], "accepted-risk")
        self.assertIn("without TLS", result["acceptedRisks"][0]["riskAcceptance"]["residualRisk"])
        self.assertEqual(self.report, original)
        self.assertNotIn("acceptedRisks", self.evaluate())  # v1 receipts stay compatible.

    def test_risk_acceptance_cannot_reuse_a_generic_approval_or_empty_scope(self):
        edits = (
            lambda p: p.update(schemaVersion=1),
            lambda p: p["entries"][0]["approval"].pop("kind"),
            lambda p: p["entries"][0]["approval"].update(approved=False),
            lambda p: p["entries"][0]["approval"].update(reference="   "),
            lambda p: p["entries"][0]["approval"].update(reference={"url": "test"}),
            lambda p: (p["entries"][0]["approval"].update(actor=["reviewer"]),
                       p["entries"][0]["riskAcceptance"].update(acceptedBy=["reviewer"])),
            lambda p: (p["entries"][0]["approval"].update(actor="   "),
                       p["entries"][0]["riskAcceptance"].update(acceptedBy="   ")),
            lambda p: p["entries"][0].pop("riskAcceptance"),
            lambda p: p["entries"][0]["riskAcceptance"].update(acceptedBy="someone-else"),
            lambda p: p["entries"][0]["riskAcceptance"].update(decision="pending"),
            lambda p: p["entries"][0]["riskAcceptance"].update(scope=""),
            lambda p: p["entries"][0]["riskAcceptance"].update(residualRisk=""),
            lambda p: p["entries"][0]["riskAcceptance"].update(conditions=[]),
        )
        for edit in edits:
            with self.subTest(edit=edit):
                policy = self.accepted_risk_policy()
                edit(policy)
                with self.assertRaises(ValueError):
                    self.evaluate(policy=policy)

    def test_risk_acceptance_does_not_cover_new_findings_or_changed_source(self):
        policy = self.accepted_risk_policy()
        extra = {**self.finding, "check_id": "new.rule"}
        result = self.evaluate({"results": [self.finding, extra]}, policy)
        self.assertFalse(result["passed"])
        self.assertEqual(len(result["acceptedRisks"]), 1)
        self.assertEqual(len(result["unresolved"]), 1)
        self.file.write_text("package changed\n")
        with self.assertRaisesRegex(ValueError, "production source changes"):
            self.evaluate(policy=policy)

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
