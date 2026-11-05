import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import release_artifacts as artifacts


class ReleaseArtifactsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.dist = Path(self.temporary.name) / "dist"
        self.dist.mkdir()
        self.package_validators = [
            patch.object(artifacts, "_validate_deb"),
            patch.object(artifacts, "_validate_rpm"),
        ]
        for validator in self.package_validators:
            validator.start()
            self.addCleanup(validator.stop)

    def test_package_version_uses_native_prerelease_separator(self):
        self.assertEqual(artifacts._package_version("1.2.3"), "1.2.3")
        self.assertEqual(artifacts._package_version("1.2.3-rc.1"), "1.2.3~rc.1")

    def _make_archive(self, path: Path, zip_archive=False):
        component = path.name.split("_v", 1)[0]
        binary = component + (".exe" if zip_archive else "")
        names = {binary, "LICENSE", "NOTICE", "README.md", "docs/packaging.md"}
        arch = path.name.split("_")[-1].split(".")[0]
        binary_data = self._fake_elf(arch) if "_linux_" in path.name and not zip_archive else binary.encode()
        if zip_archive:
            import zipfile

            with zipfile.ZipFile(path, "w") as archive:
                for name in names:
                    archive.writestr(name, binary_data if name == binary else name.encode())
        else:
            import io
            import tarfile

            with tarfile.open(path, "w:gz") as archive:
                for name in names:
                    info = tarfile.TarInfo(name)
                    data = binary_data if name == binary else name.encode()
                    info.size = len(data)
                    archive.addfile(info, io.BytesIO(data))

    @staticmethod
    def _fake_elf(architecture):
        header = bytearray(20)
        header[:4] = b"\x7fELF"
        header[4] = 2
        header[5] = 1
        header[18:20] = {"amd64": 62, "arm64": 183}[architecture].to_bytes(2, "little")
        return bytes(header)

    def test_create_stages_exact_matrix_manifest_and_checksums(self):
        version = "1.2.3"
        source_sha = "a" * 40
        for name in artifacts.expected_names(version):
            path = self.dist / name
            path.parent.mkdir(parents=True, exist_ok=True)
            if name.endswith(".tar.gz"):
                self._make_archive(path)
            elif name.endswith(".zip"):
                self._make_archive(path, zip_archive=True)
            else:
                path.write_bytes(name.encode())
        output = self.dist / "release"
        manifest = artifacts.create_staged(self.dist, output, source_sha, version)
        self.assertEqual(len(manifest["artifacts"]), 15)
        self.assertEqual(artifacts.validate_staged(output)["sourceSha"], source_sha)
        (output / "unlisted").symlink_to(output / "checksums.txt")
        with self.assertRaisesRegex(ValueError, "files only"):
            artifacts.validate_staged(output)

    def _create_full_release(self, version="1.2.3", source_sha="a" * 40):
        for name in artifacts.expected_names(version):
            path = self.dist / name
            path.parent.mkdir(parents=True, exist_ok=True)
            if name.endswith(".tar.gz"):
                self._make_archive(path)
            elif name.endswith(".zip"):
                self._make_archive(path, zip_archive=True)
            else:
                path.write_bytes(name.encode())
        output = self.dist / "release"
        artifacts.create_staged(self.dist, output, source_sha, version)
        return output

    def test_local_profile_selects_nine_assets_and_keeps_full_matrix_diagnostic(self):
        version = "1.2.3"
        source_sha = "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "local"
        manifest = artifacts.create_local_staged(full, output, source_sha, version)
        names = set(artifacts.local_asset_names(version))
        self.assertEqual(len(names), 9)
        self.assertEqual(len(manifest["artifacts"]), 9)
        self.assertEqual(manifest["schemaVersion"], 2)
        self.assertEqual(manifest["profile"], "local")
        self.assertEqual(
            {path.name for path in output.iterdir()},
            set(artifacts.local_publication_names(version)),
        )
        self.assertEqual(len((output / "checksums.txt").read_text().splitlines()), 10)
        full_manifest = json.loads((full / "binary-manifest.json").read_text())
        self.assertEqual(full_manifest["schemaVersion"], 1)
        self.assertEqual(len(full_manifest["artifacts"]), 15)
        self.assertEqual(artifacts.validate_staged(output, profile="local")["profile"], "local")
        with self.assertRaisesRegex(ValueError, "publication profile"):
            artifacts.validate_staged(output, profile="local-linux")
        with self.assertRaisesRegex(ValueError, "schema"):
            artifacts.validate_staged(output)

    def test_local_linux_profile_stages_eight_packages_and_rejects_windows_zip(self):
        version = "1.2.3"
        source_sha = "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "local-linux"
        manifest = artifacts.create_local_staged(
            full, output, source_sha, version, profile="local-linux"
        )
        names = set(artifacts.local_asset_names(version, "local-linux"))

        self.assertEqual(len(artifacts.expected_names(version)), 15)
        self.assertEqual(len(names), 8)
        self.assertEqual(len(manifest["artifacts"]), 8)
        self.assertEqual(manifest["profile"], "local-linux")
        self.assertEqual(
            {path.name for path in output.iterdir()},
            set(artifacts.local_publication_names(version, "local-linux")),
        )
        self.assertEqual(len({path.name for path in output.iterdir()}), 10)
        self.assertEqual(len((output / "checksums.txt").read_text().splitlines()), 9)
        self.assertEqual(
            artifacts.validate_staged(output, profile="local-linux")["profile"],
            "local-linux",
        )
        with self.assertRaisesRegex(ValueError, "publication profile"):
            artifacts.validate_staged(output, profile="local")

        windows_zip = f"kubeatlas_v{version}_windows_amd64.zip"
        (output / windows_zip).write_bytes((full / windows_zip).read_bytes())
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile="local-linux")

    def test_local_profile_rejects_omitted_extra_symlink_and_modified_payloads(self):
        version = "1.2.3"
        source_sha = "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "local"
        artifacts.create_local_staged(full, output, source_sha, version)
        artifact = output / artifacts.local_asset_names(version)[0]
        original = artifact.read_bytes()
        artifact.unlink()
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile="local")
        artifact.write_bytes(original)
        (output / "extra").write_text("extra")
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile="local")
        (output / "extra").unlink()
        (output / "symlink").symlink_to(artifact)
        with self.assertRaisesRegex(ValueError, "files only"):
            artifacts.validate_staged(output, profile="local")
        (output / "symlink").unlink()
        artifact.write_bytes(b"modified")
        with self.assertRaises(ValueError):
            artifacts.validate_staged(output, profile="local")

    def test_linux_elf_architecture_is_not_trusted_from_filename(self):
        import io

        with self.assertRaisesRegex(ValueError, "architecture mismatch"):
            artifacts._validate_elf_stream(io.BytesIO(self._fake_elf("arm64")), "amd64", "test")

    def test_traversal_and_duplicate_archive_entries_are_rejected(self):
        path = self.dist / "kubeatlas_v1.2.3_linux_amd64.tar.gz"
        import io
        import tarfile

        with tarfile.open(path, "w:gz") as archive:
            for name in ("kubeatlas", "LICENSE", "NOTICE", "README.md", "../escape"):
                info = tarfile.TarInfo(name)
                info.size = 1
                archive.addfile(info, io.BytesIO(b"x"))
        with self.assertRaises(ValueError):
            artifacts._validate_archive(path, "kubeatlas", ".gz")

    def test_duplicate_archive_entries_are_rejected(self):
        path = self.dist / "kubeatlas_v1.2.3_linux_amd64.tar.gz"
        import io
        import tarfile

        with tarfile.open(path, "w:gz") as archive:
            for _ in range(2):
                info = tarfile.TarInfo("kubeatlas")
                info.size = 1
                archive.addfile(info, io.BytesIO(b"x"))
        with self.assertRaises(ValueError):
            artifacts._validate_archive(path, "kubeatlas", ".gz")

    def test_missing_and_extra_artifacts_are_rejected(self):
        with self.assertRaises(ValueError):
            artifacts.create_staged(self.dist, self.dist / "release", "a" * 40, "1.2.3")

    def test_extra_artifact_is_rejected(self):
        output = self.dist / "release"
        output.mkdir()
        (output / "unexpected").write_text("extra")
        with self.assertRaises(ValueError):
            artifacts.validate_staged(output)

    def test_metadata_and_hash_mismatch_are_rejected(self):
        version = "1.2.3"
        source_sha = "a" * 40
        for name in artifacts.expected_names(version):
            path = self.dist / name
            if name.endswith(".tar.gz"):
                self._make_archive(path)
            elif name.endswith(".zip"):
                self._make_archive(path, zip_archive=True)
            else:
                path.write_bytes(name.encode())
        output = self.dist / "release"
        artifacts.create_staged(self.dist, output, source_sha, version)
        manifest_path = output / "binary-manifest.json"
        manifest = __import__("json").loads(manifest_path.read_text())
        manifest["artifacts"].append(manifest["artifacts"][0])
        manifest_path.write_text(__import__("json").dumps(manifest))
        with self.assertRaisesRegex(ValueError, "duplicate manifest artifact"):
            artifacts.validate_staged(output)
        manifest["artifacts"].pop()
        manifest["schemaVersion"] = 2
        manifest_path.write_text(__import__("json").dumps(manifest))
        with self.assertRaisesRegex(ValueError, "schema"):
            artifacts.validate_staged(output)
        manifest["schemaVersion"] = 1
        manifest["artifacts"][0]["component"] = "wrong"
        manifest_path.write_text(__import__("json").dumps(manifest))
        with self.assertRaises(ValueError):
            artifacts.validate_staged(output)

    def test_hash_mismatch_is_rejected(self):
        version = "1.2.3"
        source_sha = "a" * 40
        for name in artifacts.expected_names(version):
            path = self.dist / name
            if name.endswith(".tar.gz"):
                self._make_archive(path)
            elif name.endswith(".zip"):
                self._make_archive(path, zip_archive=True)
            else:
                path.write_bytes(name.encode())
        output = self.dist / "release"
        artifacts.create_staged(self.dist, output, source_sha, version)
        (output / sorted(artifacts.expected_names(version))[0]).write_bytes(b"tampered")
        with self.assertRaises(ValueError):
            artifacts.validate_staged(output)

    def test_selected_source_sha_and_version_must_match(self):
        version = "1.2.3"
        source_sha = "a" * 40
        for name in artifacts.expected_names(version):
            path = self.dist / name
            if name.endswith(".tar.gz"):
                self._make_archive(path)
            elif name.endswith(".zip"):
                self._make_archive(path, zip_archive=True)
            else:
                path.write_bytes(name.encode())
        output = self.dist / "release"
        artifacts.create_staged(self.dist, output, source_sha, version)
        with self.assertRaisesRegex(ValueError, "source SHA"):
            artifacts.validate_staged(output, expected_source_sha="b" * 40)
        with self.assertRaisesRegex(ValueError, "version"):
            artifacts.validate_staged(output, expected_version="1.2.4")

    def test_deb_validator_rejects_maintainer_scripts(self):
        self.package_validators[0].stop()
        self.addCleanup(self.package_validators[0].start)
        package = self.dist / "kubeatlas.deb"
        def fake_run(command):
            if command[1:3] == ["--field", str(package)]:
                return {
                    "Package": "kubeatlas",
                    "Version": "1.2.3",
                    "Architecture": "amd64",
                }[command[3]]
            if command[1:3] == ["--contents", str(package)]:
                return "\n".join(
                    f"-rwxr-xr-x root/root 123 2024-01-01 00:00 ./{path}"
                    for path in (
                        "usr/bin/kubeatlas",
                        "usr/share/doc/kubeatlas/LICENSE",
                        "usr/share/doc/kubeatlas/NOTICE",
                        "usr/share/doc/kubeatlas/README.md",
                        "usr/share/doc/kubeatlas/packaging.md",
                    )
                )
            if command[1:3] == ["--control", str(package)]:
                Path(command[3], "control").write_text("Package: kubeatlas\n")
                Path(command[3], "postinst").write_text("#!/bin/sh\n")
                return ""
            raise AssertionError(command)

        with patch.object(artifacts, "_run", side_effect=fake_run), self.assertRaisesRegex(
            ValueError, "maintainer scripts"
        ):
            artifacts._validate_deb(package, "kubeatlas", "amd64", "1.2.3")


if __name__ == "__main__":
    unittest.main()
