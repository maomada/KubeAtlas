import hashlib
from pathlib import Path
import tempfile
import unittest

import security_scan as gate


class SecurityGateTests(unittest.TestCase):
    def test_all_production_languages_and_modules_are_selected(self):
        for path in ("internal/server/api.go", "pkg/k8score/cache.go", "cmd/explorer/main.go",
                     "web/src/main.tsx", "packages/k8s-ui/src/test-helper.ts", "web/src/lib.js"):
            self.assertTrue(gate.production_source(path), path)
        for path in ("internal/server/api_test.go", "pkg/testdata/unsafe.go", "cmd/testserver/main.go",
                     "web/e2e/settings.spec.ts", "web/src/example.test.tsx", "web/dist/app.js"):
            self.assertFalse(gate.production_source(path), path)

    def test_clean_passes_findings_fail_and_partial_scans_fail_closed(self):
        path = "/example.go"
        report = {"results": [], "errors": [], "paths": {"scanned": [path]}}
        self.assertTrue(gate.require_complete(report, [path], 0))
        finding = {**report, "results": [{"check_id": "unsafe"}]}
        self.assertFalse(gate.require_complete(finding, [path], 1))
        for candidate, targets, code in (
            ({**report, "errors": [{"type": "ParseError"}]}, [path], 0),
            ({**report, "paths": {"scanned": []}}, [path], 0),
            (report, [], 0), (report, [path], 2), (finding, [path], 0),
            ({}, [path], 0),
        ):
            with self.subTest(candidate=candidate, code=code):
                with self.assertRaises((ValueError, KeyError)):
                    gate.require_complete(candidate, targets, code)

    def test_missing_tampered_and_empty_rules_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "LICENSE").write_text("Test license")
            (root / "rule.yaml").write_text("rules: []\n")
            lock = {"files": {"rule.yaml": hashlib.sha256(b"rules: []\n").hexdigest()}}
            gate.prepare_rules(root, root / "selected", lock)
            (root / "rule.yaml").write_text("tampered")
            with self.assertRaises(ValueError):
                gate.prepare_rules(root, root / "selected", lock)
            (root / "rule.yaml").unlink()
            with self.assertRaises(FileNotFoundError):
                gate.prepare_rules(root, root / "selected", lock)
            with self.assertRaises(ValueError):
                gate.prepare_rules(root, root / "selected", {"files": {}})


if __name__ == "__main__":
    unittest.main()
