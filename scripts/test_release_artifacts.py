import gzip
import json
import struct
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
        self.rpm_normalizer = patch.object(artifacts, "_normalize_rpm_payload_size")
        self.rpm_normalizer.start()
        self.addCleanup(self.rpm_normalizer.stop)

    def test_package_version_uses_native_prerelease_separator(self):
        self.assertEqual(artifacts._package_version("1.2.3"), "1.2.3")
        self.assertEqual(artifacts._package_version("1.2.3-rc.1"), "1.2.3~rc.1")

    def test_native_hub_package_has_the_matching_catalog(self):
        import io
        import tarfile

        version, source_sha = "1.2.3", "a" * 40
        files = {
            "manifest.json": json.dumps({"version": version, "sourceRevision": source_sha}).encode(),
            "install-hub-agent.sh": b"installer",
            "bootstrap-hub-agent.sh": b"bootstrap",
        }
        bundle = self.dist / "catalog.tar.gz"
        with tarfile.open(bundle, "w:gz") as archive:
            for name, data in files.items():
                member = tarfile.TarInfo(f"hub-agent-bundle/{name}")
                member.size = len(data)
                archive.addfile(member, io.BytesIO(data))
        catalog = artifacts._bundle_inventory(bundle, version, source_sha)
        with self.assertRaisesRegex(ValueError, "version and source"):
            artifacts._bundle_inventory(bundle, version, "b" * 40)

        paths = {"usr/bin/kubeatlas-portal"} | {
            f"usr/share/doc/kubeatlas-portal/{name}"
            for name in ("LICENSE", "NOTICE", "README.md", "packaging.md")
        }
        with self.assertRaisesRegex(ValueError, "native Hub payload missing"):
            artifacts._package_layout(paths, "kubeatlas-portal", "fixture.deb", catalog)
        paths |= {artifacts.HUB_SERVICE, artifacts.HUB_ENV}
        paths |= {f"{artifacts.HUB_CATALOG}/{name}" for name in files}
        self.assertTrue(artifacts._package_layout(paths, "kubeatlas-portal", "fixture.deb", catalog))

        embedded = self.dist / artifacts.HUB_CATALOG
        embedded.mkdir(parents=True)
        for name, data in files.items():
            (embedded / name).write_bytes(data)
        artifacts._validate_catalog(self.dist, version, "fixture.deb", catalog)
        (embedded / "bootstrap-hub-agent.sh").write_bytes(b"unmatched bootstrap")
        with self.assertRaisesRegex(ValueError, "differs from the release bundle"):
            artifacts._validate_catalog(self.dist, version, "fixture.deb", catalog)

    def test_full_matrix_preserves_linux_and_darwin_without_windows(self):
        version = "1.2.3"
        expected = {
            f"kubeatlas_v{version}_{platform}_{architecture}.tar.gz"
            for platform in ("linux", "darwin")
            for architecture in ("amd64", "arm64")
        }
        expected.update(
            f"kubeatlas-portal_v{version}_linux_{architecture}.tar.gz"
            for architecture in ("amd64", "arm64")
        )
        expected.update(
            f"{component}_{version}_linux_{architecture}.{suffix}"
            for component in ("kubeatlas", "kubeatlas-portal")
            for architecture in ("amd64", "arm64")
            for suffix in ("deb", "rpm")
        )
        self.assertEqual(artifacts.expected_names(version), expected)
        self.assertEqual(len(expected), 14)

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
        self.assertEqual(len(manifest["artifacts"]), 14)
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

    def test_default_local_profile_selects_linux_packages_and_keeps_full_matrix_diagnostic(self):
        version = "1.2.3"
        source_sha = "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "local"
        manifest = artifacts.create_local_staged(full, output, source_sha, version)
        names = set(artifacts.local_asset_names(version))
        self.assertEqual(len(names), 8)
        self.assertEqual(len(manifest["artifacts"]), 8)
        self.assertEqual(manifest["schemaVersion"], 2)
        self.assertEqual(manifest["profile"], "local-linux")
        self.assertEqual(
            {path.name for path in output.iterdir()},
            set(artifacts.local_publication_names(version)),
        )
        self.assertEqual(len((output / "checksums.txt").read_text().splitlines()), 9)
        full_manifest = json.loads((full / "binary-manifest.json").read_text())
        self.assertEqual(full_manifest["schemaVersion"], 1)
        self.assertEqual(len(full_manifest["artifacts"]), 14)
        self.assertEqual(artifacts.validate_staged(output, profile="local-linux")["profile"], "local-linux")
        with self.assertRaisesRegex(ValueError, "publication profile"):
            artifacts.validate_staged(output, profile="local")
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

        self.assertEqual(len(artifacts.expected_names(version)), 14)
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
        (output / windows_zip).write_bytes(b"retired Windows archive")
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile="local-linux")

    def test_hub_linux_profile_stages_four_hub_packages_and_rejects_standalone(self):
        version, source_sha = "1.2.3", "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "hub-linux"
        profile = artifacts.HUB_LINUX_PROFILE
        manifest = artifacts.create_local_staged(full, output, source_sha, version, profile=profile)
        expected = {
            f"kubeatlas-portal_{version}_linux_{architecture}.{suffix}"
            for architecture in ("amd64", "arm64")
            for suffix in ("deb", "rpm")
        }

        self.assertEqual({item["filename"] for item in manifest["artifacts"]}, expected)
        self.assertEqual({path.name for path in output.iterdir()}, expected | {"binary-manifest.json", "checksums.txt"})
        artifacts.validate_staged(output, profile=profile)
        with self.assertRaisesRegex(ValueError, "publication profile"):
            artifacts.validate_staged(output, profile="local-linux")
        (output / f"kubeatlas_{version}_linux_amd64.deb").write_bytes(b"retired standalone package")
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile=profile)

    def test_local_amd64_profile_excludes_deferred_platforms(self):
        version, source_sha = "1.2.3", "a" * 40
        full = self._create_full_release(version, source_sha)
        output = Path(self.temporary.name) / "local-linux-amd64"
        profile = "local-linux-amd64"
        manifest = artifacts.create_local_staged(full, output, source_sha, version, profile=profile)
        expected = {
            f"{component}_{version}_linux_amd64.{suffix}"
            for component in ("kubeatlas", "kubeatlas-portal")
            for suffix in ("deb", "rpm")
        }
        self.assertEqual(set(artifacts.local_asset_names(version, profile)), expected)
        self.assertEqual({p.name for p in output.iterdir()}, expected | {"binary-manifest.json", "checksums.txt"})
        self.assertEqual(manifest["profile"], profile)
        self.assertEqual(len(manifest["artifacts"]), 4)
        artifacts.validate_staged(output, profile=profile)
        for deferred in (f"kubeatlas_{version}_linux_arm64.deb", f"kubeatlas_v{version}_windows_amd64.zip"):
            with self.subTest(deferred=deferred):
                extra = output / deferred
                extra.write_bytes(b"unsupported artifact")
                with self.assertRaisesRegex(ValueError, "artifact set"):
                    artifacts.validate_staged(output, profile=profile)
                extra.unlink()
        with self.assertRaisesRegex(ValueError, "publication profile"):
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
            artifacts.validate_staged(output, profile="local-linux")
        artifact.write_bytes(original)
        (output / "extra").write_text("extra")
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output, profile="local-linux")
        (output / "extra").unlink()
        (output / "symlink").symlink_to(artifact)
        with self.assertRaisesRegex(ValueError, "files only"):
            artifacts.validate_staged(output, profile="local-linux")
        (output / "symlink").unlink()
        artifact.write_bytes(b"modified")
        with self.assertRaises(ValueError):
            artifacts.validate_staged(output, profile="local-linux")

    def test_full_matrix_rejects_retired_windows_asset(self):
        output = self._create_full_release()
        (output / "kubeatlas_v1.2.3_windows_amd64.zip").write_bytes(b"retired Windows archive")
        with self.assertRaisesRegex(ValueError, "artifact set"):
            artifacts.validate_staged(output)

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


class RpmPayloadSizeNormalizerTests(unittest.TestCase):
    _HEADER_MAGIC = b"\x8e\xad\xe8\x01\x00\x00\x00\x00"
    _SIGNATURE_TAGS = (62, 273, 1000, 1007)

    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)

    @classmethod
    def _header(cls, entries, store):
        indexes = b"".join(struct.pack(">IIII", *entry) for entry in entries)
        return cls._HEADER_MAGIC + struct.pack(">II", len(entries), len(store)) + indexes + store

    @classmethod
    def _synthetic_rpm(cls, payload, installed_size, declared_size, signature_tags=None):
        tags = cls._SIGNATURE_TAGS if signature_tags is None else signature_tags
        store = bytearray()
        entries = []
        for tag in sorted(tags):
            offset = len(store)
            value = declared_size if tag == 1007 else tag
            store.extend(struct.pack(">I", value))
            entries.append((tag, 4, offset, 1))
        signature = cls._header(entries, store)
        lead = b"\xed\xab\xee\xdb" + b"\0" * 92
        padding = b"\0" * (-(len(lead) + len(signature)) % 8)
        main_header = cls._header([], b"")
        return lead + signature + padding + main_header + gzip.compress(payload, mtime=0)

    @staticmethod
    def _archive_size_info(path):
        raw = path.read_bytes()
        count, _ = struct.unpack(">II", raw[104:112])
        indexes_start = 112
        store_start = indexes_start + count * 16
        for index in range(count):
            tag, _, offset, _ = struct.unpack_from(">IIII", raw, indexes_start + index * 16)
            if tag == 1007:
                position = store_start + offset
                return struct.unpack_from(">I", raw, position)[0], position
        raise AssertionError("synthetic RPM has no archive-size tag")

    @staticmethod
    def _native_run(installed_size, fail_digest_check=None):
        digest_checks = 0

        def run(command):
            nonlocal digest_checks
            if command[:2] == ["rpm", "--checksig"]:
                digest_checks += 1
                if digest_checks == fail_digest_check:
                    raise ValueError("RPM digest check failed")
                return "digests OK"
            if command[:3] == ["rpm", "-qp", "--queryformat"]:
                return f"{installed_size}\ngzip\n"
            raise AssertionError(command)

        return run

    def _write_synthetic_rpm(self, name, payload, installed_size, declared_size, signature_tags=None):
        path = self.directory / name
        path.write_bytes(
            self._synthetic_rpm(payload, installed_size, declared_size, signature_tags)
        )
        return path

    def test_known_installed_size_mismatch_is_corrected_and_idempotent(self):
        installed_size = 1024
        payload = b"synthetic cpio payload" + b"x" * 2048
        path = self._write_synthetic_rpm(
            "known-mismatch.rpm", payload, installed_size, installed_size
        )
        original = path.read_bytes()
        original_size, size_position = self._archive_size_info(path)
        self.assertEqual(original_size, installed_size)

        with patch.object(artifacts, "_run", side_effect=self._native_run(installed_size)) as run:
            artifacts._normalize_rpm_payload_size(path)
            corrected = path.read_bytes()
            corrected_size, _ = self._archive_size_info(path)
            self.assertEqual(corrected_size, len(payload))
            expected = bytearray(original)
            expected[size_position : size_position + 4] = struct.pack(">I", len(payload))
            self.assertEqual(corrected, bytes(expected))

            artifacts._normalize_rpm_payload_size(path)
            self.assertEqual(path.read_bytes(), corrected)

        self.assertEqual(run.call_count, 5)

    def test_unexpected_mismatch_is_rejected_unchanged(self):
        path = self._write_synthetic_rpm(
            "unexpected-mismatch.rpm", b"x" * 2048, 1024, 512
        )
        original = path.read_bytes()

        with patch.object(artifacts, "_run", side_effect=self._native_run(1024)):
            with self.assertRaisesRegex(ValueError, "unexpected RPM archive-size mismatch"):
                artifacts._normalize_rpm_payload_size(path)

        self.assertEqual(path.read_bytes(), original)

    def test_signed_or_unrecognized_signature_tags_are_rejected_unchanged(self):
        for tags in (
            (62, 273, 1000, 1002, 1007),
            (62, 1000, 1007),
        ):
            with self.subTest(tags=tags):
                path = self._write_synthetic_rpm(
                    f"signature-{len(tags)}.rpm",
                    b"x" * 2048,
                    1024,
                    1024,
                    signature_tags=tags,
                )
                original = path.read_bytes()

                with patch.object(artifacts, "_run", side_effect=self._native_run(1024)):
                    with self.assertRaisesRegex(ValueError, "unsigned rpmpack headers"):
                        artifacts._normalize_rpm_payload_size(path)

                self.assertEqual(path.read_bytes(), original)

    def test_truncated_gzip_and_oversized_header_are_rejected_unchanged(self):
        installed_size = 1024
        payload = b"x" * 2048
        truncated = self._synthetic_rpm(payload, installed_size, installed_size)[:-3]
        truncated_path = self.directory / "truncated-gzip.rpm"
        truncated_path.write_bytes(truncated)
        with patch.object(artifacts, "_run", side_effect=self._native_run(installed_size)):
            with self.assertRaises((EOFError, OSError)):
                artifacts._normalize_rpm_payload_size(truncated_path)
        self.assertEqual(truncated_path.read_bytes(), truncated)

        oversized = (
            b"\xed\xab\xee\xdb"
            + b"\0" * 92
            + self._HEADER_MAGIC
            + struct.pack(">II", 4097, 0)
        )
        oversized_path = self.directory / "oversized-header.rpm"
        oversized_path.write_bytes(oversized)
        with patch.object(artifacts, "_run", side_effect=self._native_run(installed_size)):
            with self.assertRaisesRegex(ValueError, "RPM header exceeds inspection limits"):
                artifacts._normalize_rpm_payload_size(oversized_path)
        self.assertEqual(oversized_path.read_bytes(), oversized)

    def test_digest_failures_before_and_after_correction_are_rejected_unchanged(self):
        installed_size = 1024
        payload = b"x" * 2048
        for fail_digest_check in (1, 2):
            with self.subTest(fail_digest_check=fail_digest_check):
                path = self._write_synthetic_rpm(
                    f"digest-failure-{fail_digest_check}.rpm",
                    payload,
                    installed_size,
                    installed_size,
                )
                original = path.read_bytes()
                with patch.object(
                    artifacts,
                    "_run",
                    side_effect=self._native_run(installed_size, fail_digest_check),
                ):
                    with self.assertRaisesRegex(ValueError, "RPM digest check failed"):
                        artifacts._normalize_rpm_payload_size(path)
                self.assertEqual(path.read_bytes(), original)


if __name__ == "__main__":
    unittest.main()
