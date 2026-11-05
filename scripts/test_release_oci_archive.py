import hashlib
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path

import validate_oci_archive as oci


class OCIArchiveValidationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.blobs = {}
        self.index = self._make_index()
        self.raw_manifest = json.dumps(self.index, separators=(",", ":")).encode()

    def _add_blob(self, content):
        digest = hashlib.sha256(content).hexdigest()
        self.blobs[f"blobs/sha256/{digest}"] = content
        return {"digest": f"sha256:{digest}", "size": len(content)}

    def _make_index(self, config_architecture=None, labels_by_architecture=None):
        manifests = []
        for os_name, architecture in sorted(oci.EXPECTED_PLATFORMS):
            config_values = {}
            if labels_by_architecture is not None:
                config_values["Labels"] = labels_by_architecture[architecture]
            config = json.dumps(
                {
                    "os": os_name,
                    "architecture": config_architecture or architecture,
                    "config": config_values,
                },
                separators=(",", ":"),
            ).encode()
            config_descriptor = self._add_blob(config)
            manifest = json.dumps(
                {
                    "schemaVersion": 2,
                    "config": config_descriptor,
                    "layers": [],
                },
                separators=(",", ":"),
            ).encode()
            descriptor = self._add_blob(manifest)
            descriptor["platform"] = {"os": os_name, "architecture": architecture}
            manifests.append(descriptor)
        return {"schemaVersion": 2, "manifests": manifests}

    def _read_blob(self, name):
        return self.blobs[name]

    def _write_archive(self, index=None):
        archive_path = self.root / "image.oci.tar"
        layout = b'{"imageLayoutVersion":"1.0.0"}'
        index_bytes = json.dumps(index or self.index, separators=(",", ":")).encode()
        with tarfile.open(archive_path, "w") as archive:
            for name, content in [("oci-layout", layout), ("index.json", index_bytes), *self.blobs.items()]:
                info = tarfile.TarInfo(name)
                info.size = len(content)
                archive.addfile(info, io.BytesIO(content))
        return archive_path

    def test_index_requires_exact_platforms_and_matching_configs(self):
        result = oci.validate_index(self.raw_manifest, self._read_blob)
        self.assertEqual(set(result), oci.EXPECTED_PLATFORMS)

    def test_index_rejects_missing_platform(self):
        index = dict(self.index)
        index["manifests"] = index["manifests"][:1]
        with self.assertRaisesRegex(ValueError, "exactly"):
            oci.validate_index(json.dumps(index).encode(), self._read_blob)

    def test_explicit_amd64_matrix_rejects_multiarch_and_arm64_only(self):
        selected = {("linux", "amd64")}
        for manifests in (self.index["manifests"], self.index["manifests"][1:]):
            with self.subTest(manifests=manifests):
                index = {"schemaVersion": 2, "manifests": manifests}
                with self.assertRaisesRegex(ValueError, "exactly"):
                    oci.validate_index(json.dumps(index).encode(), self._read_blob, expected_platforms=selected)

    def test_single_platform_archive_and_raw_image_require_explicit_profile(self):
        descriptor = self.index["manifests"][0]
        raw = self.blobs["blobs/sha256/" + descriptor["digest"].split(":")[1]]
        raw_path = self.root / "raw.json"
        raw_path.write_bytes(raw)
        selected = {("linux", "amd64")}
        for include_platform in (True, False):
            with self.subTest(include_platform=include_platform):
                entry = dict(descriptor)
                if not include_platform:
                    del entry["platform"]
                archive = self._write_archive({"schemaVersion": 2, "manifests": [entry]})
                result = oci.validate_archive(archive, raw_path, expected_platforms=selected)
                self.assertEqual(result, {("linux", "amd64"): descriptor["digest"]})
                with self.assertRaisesRegex(ValueError, "exactly"):
                    oci.validate_archive(archive, raw_path)
                with self.assertRaisesRegex(ValueError, "provenance labels"):
                    oci.validate_archive(archive, raw_path, {"revision": "missing"}, expected_platforms=selected)

    def test_single_platform_nested_index_preserves_raw_descriptor_check(self):
        index = {"schemaVersion": 2, "manifests": self.index["manifests"][:1]}
        raw = json.dumps(index).encode()
        descriptor = self._add_blob(raw)
        descriptor["mediaType"] = "application/vnd.oci.image.index.v1+json"
        archive = self._write_archive({"schemaVersion": 2, "manifests": [descriptor]})
        raw_path = self.root / "raw.json"
        raw_path.write_bytes(raw)
        self.assertEqual(set(oci.validate_archive(archive, raw_path, expected_platforms={("linux", "amd64")})), {("linux", "amd64")})
        raw_path.write_bytes(self.raw_manifest)
        with self.assertRaisesRegex(ValueError, "exactly"):
            oci.validate_archive(archive, raw_path, expected_platforms={("linux", "amd64")})

    def test_index_rejects_extra_platform(self):
        index = json.loads(json.dumps(self.index))
        config = json.dumps(
            {"os": "linux", "architecture": "s390x"},
            separators=(",", ":"),
        ).encode()
        config_descriptor = self._add_blob(config)
        manifest = json.dumps(
            {
                "schemaVersion": 2,
                "config": config_descriptor,
                "layers": [],
            },
            separators=(",", ":"),
        ).encode()
        descriptor = self._add_blob(manifest)
        descriptor["platform"] = {"os": "linux", "architecture": "s390x"}
        index["manifests"].append(
            descriptor
        )
        with self.assertRaisesRegex(ValueError, "exactly"):
            oci.validate_index(json.dumps(index).encode(), self._read_blob)

    def test_index_rejects_config_architecture_mismatch(self):
        bad_index = self._make_index(config_architecture="amd64")
        with self.assertRaisesRegex(ValueError, "config does not match"):
            oci.validate_index(
                json.dumps(bad_index).encode(),
                self._read_blob,
            )

    def test_expected_labels_are_checked_in_each_platform_config(self):
        labels = {
            "amd64": {
                "org.opencontainers.image.source": "https://github.com/maomada/KubeAtlas",
                "org.opencontainers.image.revision": "a" * 40,
                "org.opencontainers.image.version": "0.1.0",
            },
            "arm64": {
                "org.opencontainers.image.source": "https://github.com/maomada/KubeAtlas",
                "org.opencontainers.image.revision": "a" * 40,
                "org.opencontainers.image.version": "0.1.0",
            },
        }
        index = self._make_index(labels_by_architecture=labels)
        expected = dict(labels["amd64"])
        self.assertEqual(
            set(oci.validate_index(json.dumps(index).encode(), self._read_blob, expected)),
            oci.EXPECTED_PLATFORMS,
        )
        labels["arm64"]["org.opencontainers.image.revision"] = "b" * 40
        invalid = self._make_index(labels_by_architecture=labels)
        with self.assertRaisesRegex(ValueError, "provenance labels"):
            oci.validate_index(json.dumps(invalid).encode(), self._read_blob, expected)

    def test_archive_validates_skopeo_manifest_and_platform_blobs(self):
        archive_path = self._write_archive()
        raw_path = self.root / "raw-manifest.json"
        raw_path.write_bytes(self.raw_manifest)
        result = oci.validate_archive(archive_path, raw_path)
        self.assertEqual(set(result), oci.EXPECTED_PLATFORMS)

    def test_archive_validates_nested_platform_indexes(self):
        raw_path = self.root / "raw-manifest.json"
        raw_path.write_bytes(self.raw_manifest)
        for media_type in sorted(oci.NESTED_INDEX_MEDIA_TYPES):
            descriptor = self._add_blob(self.raw_manifest)
            descriptor["mediaType"] = media_type
            descriptor["annotations"] = {oci.REF_NAME_ANNOTATION: "smoke"}
            archive_path = self._write_archive(
                {"schemaVersion": 2, "manifests": [descriptor]}
            )
            with self.subTest(media_type=media_type):
                result = oci.validate_archive(archive_path, raw_path)
                self.assertEqual(set(result), oci.EXPECTED_PLATFORMS)

    def test_archive_rejects_missing_or_corrupt_nested_index_blob(self):
        original_blobs = dict(self.blobs)
        for state in ("missing", "corrupt"):
            with self.subTest(state=state):
                self.blobs = dict(original_blobs)
                descriptor = self._add_blob(self.raw_manifest)
                descriptor["mediaType"] = "application/vnd.oci.image.index.v1+json"
                blob_path = f"blobs/sha256/{descriptor['digest'].removeprefix('sha256:')}"
                if state == "missing":
                    del self.blobs[blob_path]
                else:
                    corrupt = bytearray(self.blobs[blob_path])
                    corrupt[0] ^= 1
                    self.blobs[blob_path] = bytes(corrupt)
                archive_path = self._write_archive(
                    {"schemaVersion": 2, "manifests": [descriptor]}
                )
                with self.assertRaisesRegex(ValueError, "OCI archive root index blob"):
                    oci.validate_archive(archive_path)

    def test_archive_rejects_ambiguous_nested_indexes(self):
        descriptor = self._add_blob(self.raw_manifest)
        descriptor["mediaType"] = "application/vnd.oci.image.index.v1+json"
        archive_path = self._write_archive(
            {"schemaVersion": 2, "manifests": [descriptor, dict(descriptor)]}
        )
        with self.assertRaisesRegex(ValueError, "ambiguous nested image indexes"):
            oci.validate_archive(archive_path)

    def test_local_copy_descriptor_preserves_source_manifest_digest(self):
        layout = self.root / "layout"
        (layout / "blobs" / "sha256").mkdir(parents=True)
        digest = hashlib.sha256(self.raw_manifest).hexdigest()
        (layout / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        (layout / "blobs" / "sha256" / digest).write_bytes(self.raw_manifest)
        (layout / "index.json").write_text(
            json.dumps(
                {
                    "schemaVersion": 2,
                    "manifests": [
                        {
                            "digest": f"sha256:{digest}",
                            "size": len(self.raw_manifest),
                            "annotations": {oci.REF_NAME_ANNOTATION: "ci-validation"},
                        }
                    ],
                }
            )
        )
        raw_path = self.root / "raw-manifest.json"
        raw_path.write_bytes(self.raw_manifest)

        result = oci.validate_layout_copy(layout, raw_path, "ci-validation")
        self.assertEqual(result, f"sha256:{digest}")

    def test_local_copy_rejects_changed_manifest_digest(self):
        layout = self.root / "layout"
        layout.mkdir()
        (layout / "oci-layout").write_text('{"imageLayoutVersion":"1.0.0"}')
        (layout / "index.json").write_text(
            json.dumps(
                {
                    "schemaVersion": 2,
                    "manifests": [
                        {
                            "digest": "sha256:" + "0" * 64,
                            "annotations": {oci.REF_NAME_ANNOTATION: "ci-validation"},
                        }
                    ],
                }
            )
        )
        raw_path = self.root / "raw-manifest.json"
        raw_path.write_bytes(self.raw_manifest)

        with self.assertRaisesRegex(ValueError, "descriptor digest"):
            oci.validate_layout_copy(layout, raw_path, "ci-validation")


if __name__ == "__main__":
    unittest.main()
