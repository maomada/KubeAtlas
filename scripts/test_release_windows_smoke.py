import json
import tempfile
import unittest
import zipfile
from pathlib import Path

import release_artifacts
import release_windows_smoke as smoke


class ReleaseWindowsSmokeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.version = "1.2.3"
        self.source_sha = "a" * 40
        self.archive = self.root / f"kubeatlas_v{self.version}_windows_amd64.zip"
        self.manifest = self.root / "binary-manifest.json"
        with zipfile.ZipFile(self.archive, "w") as output:
            for name in sorted(smoke.WINDOWS_ARCHIVE_MEMBERS):
                output.writestr(name, b"fixture")
        artifacts = []
        for filename in sorted(release_artifacts.expected_names(self.version)):
            item = release_artifacts._artifact_metadata(filename, self.version, self.source_sha)
            item["checksum"] = "0" * 64
            item["evidenceUrls"] = [
                f"https://github.com/maomada/KubeAtlas/commit/{self.source_sha}"
            ]
            if filename == self.archive.name:
                item["checksum"] = smoke._sha256(self.archive)
            artifacts.append(item)
        self.manifest.write_text(
            json.dumps(
                {
                    "schemaVersion": 1,
                    "repository": "maomada/KubeAtlas",
                    "version": self.version,
                    "sourceSha": self.source_sha,
                    "artifacts": artifacts,
                },
                indent=2,
                sort_keys=True,
            )
            + "\n"
        )

    def test_manifest_hash_and_safe_extraction(self):
        executable = smoke.verify_and_extract(
            self.manifest,
            self.archive,
            self.root / "extracted",
            self.version,
        )
        self.assertEqual(executable.name, "kubeatlas.exe")
        self.assertEqual(executable.read_bytes(), b"fixture")

    def test_tampered_zip_is_rejected_by_manifest_hash(self):
        self.archive.write_bytes(self.archive.read_bytes() + b"tampered")
        with self.assertRaisesRegex(smoke.WindowsSmokeError, "checksum"):
            smoke.verify_and_extract(
                self.manifest,
                self.archive,
                self.root / "extracted",
                self.version,
            )

    def test_unsafe_or_duplicate_zip_members_are_rejected(self):
        with zipfile.ZipFile(self.archive, "w") as output:
            for name in sorted(smoke.WINDOWS_ARCHIVE_MEMBERS - {"docs/packaging.md"}):
                output.writestr(name, b"fixture")
            output.writestr("../escape", b"unsafe")
        item = next(
            item for item in json.loads(self.manifest.read_text())["artifacts"]
            if item["filename"] == self.archive.name
        )
        item["checksum"] = smoke._sha256(self.archive)
        manifest = json.loads(self.manifest.read_text())
        manifest["artifacts"] = [
            item if artifact["filename"] == self.archive.name else artifact
            for artifact in manifest["artifacts"]
        ]
        self.manifest.write_text(json.dumps(manifest))
        with self.assertRaisesRegex(smoke.WindowsSmokeError, "unsafe"):
            smoke.verify_and_extract(
                self.manifest,
                self.archive,
                self.root / "extracted",
                self.version,
            )


if __name__ == "__main__":
    unittest.main()
