import tempfile
import unittest
from pathlib import Path

import release_manifest as manifest


class ReleaseManifestTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        images = self.root / "images"
        charts = self.root / "charts"
        name = "kubeatlas-portal"
        directory = images / f"release-image-{name}"
        directory.mkdir(parents=True)
        (directory / f"{name}.oci.tar").write_bytes(name.encode())
        (directory / f"{name}.oci.tar.sha256").write_text(
            manifest._sha256(directory / f"{name}.oci.tar") + "  archive\n"
        )
        (directory / f"{name}.metadata.json").write_text(
            '{"sourceSha":"%s","version":"1.2.3","buildDigest":"sha256:%s"}'
            % ("a" * 40, "b" * 64)
        )
        charts.mkdir()
        chart = charts / f"{name}-1.2.3.tgz"
        chart.write_bytes(name.encode())
        (charts / "SHA256SUMS").write_text(f"{manifest._sha256(chart)}  {chart.name}\n")

    def test_manifest_records_build_evidence_and_artifact_checksums(self):
        result = manifest.create_manifest(
            self.root,
            "a" * 40,
            "1.2.3",
            "maintainer",
            "42",
            "https://github.com/maomada/KubeAtlas/actions/runs/42",
            "https://github.com/maomada/KubeAtlas/actions/runs/ci",
            "https://github.com/maomada/KubeAtlas/actions/runs/security",
            False,
        )
        self.assertEqual(result["publication"]["status"], "dry-run")
        self.assertEqual(result["schemaVersion"], 2)
        self.assertEqual(result["checks"], {
            "ci": "https://github.com/maomada/KubeAtlas/actions/runs/ci",
            "security": "https://github.com/maomada/KubeAtlas/actions/runs/security",
        })
        self.assertIn("Semgrep CE evidence", manifest.summary(result))
        self.assertNotIn("CodeQL", manifest.summary(result))
        self.assertEqual(set(result["images"]), {"kubeatlas-portal"})
        self.assertEqual(set(result["charts"]), {"kubeatlas-portal"})
        self.assertEqual(result["images"]["kubeatlas-portal"]["buildDigest"], "sha256:" + "b" * 64)
        self.assertEqual(result["charts"]["kubeatlas-portal"]["reference"], "ghcr.io/maomada/charts/kubeatlas-portal:1.2.3")

    def test_partial_publication_is_explicit_and_summary_warns(self):
        output = self.root / "manifest.json"
        result = manifest.create_manifest(
            self.root,
            "a" * 40,
            "1.2.3",
            "maintainer",
            "42",
            "https://github.com/maomada/KubeAtlas/actions/runs/42",
            "https://github.com/maomada/KubeAtlas/actions/runs/ci",
            "https://github.com/maomada/KubeAtlas/actions/runs/security",
            True,
        )
        manifest.write_manifest(output, result)
        manifest.record_target(output, "image", "kubeatlas-portal", "published", "sha256:" + "c" * 64)
        manifest.record_target(output, "chart", "kubeatlas-portal", "push-failed-or-unknown")
        result = manifest.finalize_manifest(output, "failure", "success", "success")
        self.assertEqual(result["publication"]["status"], "partial-or-unknown")
        self.assertIn("choose a fresh version", manifest.summary(result))

    def test_dry_run_summary_does_not_claim_registry_validation(self):
        result = manifest.create_manifest(
            self.root,
            "a" * 40,
            "1.2.3",
            "maintainer",
            "42",
            "https://github.com/maomada/KubeAtlas/actions/runs/42",
            "https://github.com/maomada/KubeAtlas/actions/runs/ci",
            "https://github.com/maomada/KubeAtlas/actions/runs/security",
            False,
        )
        self.assertIn("does not prove registry permissions, collision checks, or a successful push", manifest.summary(result))


if __name__ == "__main__":
    unittest.main()
