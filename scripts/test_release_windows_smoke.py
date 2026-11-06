import json
import tempfile
import unittest
import zipfile
from pathlib import Path

import release_artifacts
import release_windows_smoke as smoke


class ReleaseWindowsSmokeTests(unittest.TestCase):
    @staticmethod
    def _synthetic_pe(machine=0x8664, optional_magic=0x20B, pe_offset=0x80):
        data = bytearray(pe_offset + smoke.PE_HEADER_SIZE)
        data[:2] = b"MZ"
        data[0x3C:0x40] = pe_offset.to_bytes(4, "little")
        data[pe_offset : pe_offset + 4] = b"PE\0\0"
        data[pe_offset + 4 : pe_offset + 6] = machine.to_bytes(2, "little")
        data[pe_offset + 20 : pe_offset + 22] = (0xF0).to_bytes(2, "little")
        data[pe_offset + 24 : pe_offset + 26] = optional_magic.to_bytes(2, "little")
        return bytes(data)

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.version = "1.2.3"
        self.source_sha = "a" * 40
        self.archive = self.root / f"kubeatlas_v{self.version}_windows_amd64.zip"
        self.manifest = self.root / "binary-manifest.json"
        self.executable_bytes = self._synthetic_pe()
        with zipfile.ZipFile(self.archive, "w") as output:
            for name in sorted(smoke.WINDOWS_ARCHIVE_MEMBERS):
                output.writestr(
                    name,
                    self.executable_bytes if name == "kubeatlas.exe" else b"fixture",
                )
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

    def _replace_executable(self, executable_bytes):
        with zipfile.ZipFile(self.archive, "w") as output:
            for name in sorted(smoke.WINDOWS_ARCHIVE_MEMBERS):
                output.writestr(
                    name,
                    executable_bytes if name == "kubeatlas.exe" else b"fixture",
                )
        manifest = json.loads(self.manifest.read_text())
        windows_artifact = next(
            item
            for item in manifest["artifacts"]
            if item["filename"] == self.archive.name
        )
        windows_artifact["checksum"] = smoke._sha256(self.archive)
        self.manifest.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")

    def test_manifest_hash_and_safe_extraction(self):
        executable = smoke.verify_and_extract(
            self.manifest,
            self.archive,
            self.root / "extracted",
            self.version,
        )
        self.assertEqual(executable.name, "kubeatlas.exe")
        self.assertEqual(executable.read_bytes(), self.executable_bytes)

    def test_non_amd64_and_truncated_pe_headers_are_rejected(self):
        truncated = bytearray(self.executable_bytes)
        truncated[0x3C:0x40] = (len(truncated) + 1).to_bytes(4, "little")
        oversized_offset = bytearray(self.executable_bytes)
        oversized_offset[0x3C:0x40] = (smoke.MAX_PE_HEADER_OFFSET + 1).to_bytes(4, "little")
        cases = (
            ("32-bit x86", self._synthetic_pe(machine=0x014C, optional_magic=0x010B)),
            ("ARM64", self._synthetic_pe(machine=0xAA64)),
            ("truncated", bytes(truncated)),
            ("oversized offset", bytes(oversized_offset)),
        )
        for label, executable_bytes in cases:
            with self.subTest(header=label):
                self._replace_executable(executable_bytes)
                with self.assertRaisesRegex(smoke.WindowsSmokeError, "AMD64 PE32\\+"):
                    smoke.verify_and_extract(
                        self.manifest,
                        self.archive,
                        self.root / f"extracted-{label.replace(' ', '-')}",
                        self.version,
                    )

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
                output.writestr(
                    name,
                    self.executable_bytes if name == "kubeatlas.exe" else b"fixture",
                )
            output.writestr("../escape", b"unsafe")
        manifest = json.loads(self.manifest.read_text())
        windows_artifact = next(
            item
            for item in manifest["artifacts"]
            if item["filename"] == self.archive.name
        )
        windows_artifact["checksum"] = smoke._sha256(self.archive)
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
