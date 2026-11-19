import base64
import json
import os
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from unittest.mock import patch

import release_approval as approval


REPOSITORY = "maomada/KubeAtlas"
VERSION = "1.2.3"
SOURCE_SHA = "a" * 40
WORKFLOW_SHA = "b" * 40
RUN_ID = "123456"
NOW = datetime(2026, 2, 1, 12, 0, 0, tzinfo=timezone.utc)


class ReleaseApprovalTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.trusted_root = self.root / "trusted"
        (self.trusted_root / ".github").mkdir(parents=True)
        self.private_key = self.root / "private.pem"
        self.public_key = self.trusted_root / approval.PUBLIC_KEY
        self._make_key(self.private_key, self.public_key)
        self.trusted_patch = patch.object(approval, "TRUSTED_ROOT", self.trusted_root)
        self.trusted_patch.start()
        self.addCleanup(self.trusted_patch.stop)
        self.env = self._environment("ghcr")
        self.artifact_root = self._artifact_root("ghcr")
        self.manifest_path = self.root / "approval.json"
        self.signature_path = self.root / "approval.sig"
        self.run = self._build_run("ghcr")

    def _environment(self, channel):
        workflow = approval.WORKFLOWS[channel]
        return {
            "GITHUB_REPOSITORY": REPOSITORY,
            "GITHUB_REF": "refs/heads/main",
            "GITHUB_SHA": WORKFLOW_SHA,
            "GITHUB_RUN_ID": RUN_ID,
            "GITHUB_RUN_ATTEMPT": "1",
            "WORKFLOW_REF": f"{REPOSITORY}/{workflow}@refs/heads/main",
            "WORKFLOW_SHA": WORKFLOW_SHA,
            "GH_TOKEN": "fixture-token",
        }

    @staticmethod
    def _make_key(private_key, public_key):
        subprocess.run(
            ["openssl", "genpkey", "-algorithm", "Ed25519", "-out", str(private_key)],
            check=True,
            capture_output=True,
        )
        subprocess.run(
            [
                "openssl",
                "pkey",
                "-in",
                str(private_key),
                "-pubout",
                "-out",
                str(public_key),
            ],
            check=True,
            capture_output=True,
        )

    def _artifact_root(self, channel):
        root = self.root / f"artifacts-{channel}"
        root.mkdir()
        for relative in approval.expected_artifact_paths(channel, VERSION):
            path = root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(f"fixture:{relative}".encode())
        return root

    def _build_run(self, channel):
        return {
            "id": int(RUN_ID),
            "status": "completed",
            "conclusion": "success",
            "event": "workflow_dispatch",
            "head_branch": "main",
            "repository": {"full_name": REPOSITORY},
            "head_repository": {"full_name": REPOSITORY},
            "run_attempt": 1,
            "path": approval.WORKFLOWS[channel],
            "head_sha": WORKFLOW_SHA,
        }

    def _create(self, channel="ghcr", now=NOW, environment=None):
        artifact_root = self.artifact_root if channel == "ghcr" else self._artifact_root(channel)
        manifest_path = self.manifest_path if channel == "ghcr" else self.root / f"{channel}-approval.json"
        approval.create_approval(
            channel,
            artifact_root,
            manifest_path,
            SOURCE_SHA,
            VERSION,
            f"https://github.com/{REPOSITORY}/actions/runs/42",
            f"https://github.com/{REPOSITORY}/actions/runs/43",
            environment or self._environment(channel),
            now,
        )
        return manifest_path, artifact_root

    def _sign(self, manifest_path, private_key=None):
        signature_path = self.root / "fixture-signature.bin"
        subprocess.run(
            [
                "openssl",
                "pkeyutl",
                "-sign",
                "-rawin",
                "-inkey",
                str(private_key or self.private_key),
                "-in",
                str(manifest_path),
                "-out",
                str(signature_path),
            ],
            check=True,
            capture_output=True,
        )
        return base64.b64encode(signature_path.read_bytes()).decode("ascii")

    def _verify(
        self,
        manifest_path=None,
        artifact_root=None,
        channel="ghcr",
        signature=None,
        source_sha=SOURCE_SHA,
        version=VERSION,
        build_run_id=RUN_ID,
        environment=None,
        run=None,
        now=NOW,
    ):
        return approval.verify_approval(
            channel,
            manifest_path or self.manifest_path,
            artifact_root or self.artifact_root,
            source_sha,
            version,
            build_run_id,
            signature or self._sign(manifest_path or self.manifest_path),
            environment=environment or self._environment(channel),
            now=now,
            run_evidence=self.run if run is None else run,
            trusted_root=self.trusted_root,
        )

    def _resign(self, manifest_path, change):
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        change(manifest)
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        return self._sign(manifest_path)

    def test_real_openssl_signatures_succeed_for_both_channels(self):
        for channel in ("ghcr", "binaries"):
            with self.subTest(channel=channel):
                manifest_path, artifact_root = self._create(channel)
                signature = self._sign(manifest_path)
                result = self._verify(
                    manifest_path=manifest_path,
                    artifact_root=artifact_root,
                    channel=channel,
                    signature=signature,
                    run=self._build_run(channel),
                )
                self.assertEqual(result["channel"], channel)
                self.assertEqual(result["sourceSha"], SOURCE_SHA)

    def test_altered_bytes_and_wrong_signing_key_are_rejected(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        manifest_path.write_bytes(manifest_path.read_bytes() + b" ")
        with self.assertRaisesRegex(approval.ApprovalError, "signature verification failed"):
            self._verify(manifest_path, artifact_root, signature=signature)
        manifest_path.write_bytes(manifest_path.read_bytes().rstrip())
        wrong_private = self.root / "wrong-private.pem"
        wrong_public = self.root / "wrong-public.pem"
        self._make_key(wrong_private, wrong_public)
        wrong_signature = self._sign(manifest_path, wrong_private)
        with self.assertRaisesRegex(approval.ApprovalError, "signature verification failed"):
            self._verify(manifest_path, artifact_root, signature=wrong_signature)

    def test_expired_approval_is_rejected(self):
        manifest_path, artifact_root = self._create(now=NOW - timedelta(days=8))
        signature = self._sign(manifest_path)
        with self.assertRaisesRegex(approval.ApprovalError, "expired"):
            self._verify(manifest_path, artifact_root, signature=signature, now=NOW)

    def test_selected_values_and_current_workflow_binding_are_enforced(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        cases = (
            {"source_sha": "c" * 40},
            {"version": "1.2.4"},
            {"build_run_id": "123457"},
            {"channel": "binaries"},
        )
        for values in cases:
            with self.subTest(values=values), self.assertRaises(approval.ApprovalError):
                self._verify(
                    manifest_path,
                    artifact_root,
                    signature=signature,
                    **values,
                )
        for key, value in (
            ("GITHUB_REPOSITORY", "other/repo"),
            ("GITHUB_REF", "refs/heads/feature"),
            ("WORKFLOW_REF", f"{REPOSITORY}/.github/workflows/other.yml@refs/heads/main"),
            ("GITHUB_SHA", "c" * 40),
            ("WORKFLOW_SHA", "c" * 40),
            ("GITHUB_RUN_ATTEMPT", "2"),
        ):
            environment = dict(self.env)
            environment[key] = value
            with self.subTest(environment_key=key), self.assertRaises(approval.ApprovalError):
                self._verify(
                    manifest_path,
                    artifact_root,
                    signature=signature,
                    environment=environment,
                )

    def test_signed_wrong_workflow_and_channel_are_rejected(self):
        manifest_path, artifact_root = self._create()
        signature = self._resign(
            manifest_path,
            lambda manifest: manifest.update(workflow=approval.WORKFLOWS["binaries"]),
        )
        with self.assertRaisesRegex(approval.ApprovalError, "workflow mismatch"):
            self._verify(manifest_path, artifact_root, signature=signature)

        manifest_path, artifact_root = self._create("binaries")
        signature = self._sign(manifest_path)
        with self.assertRaisesRegex(approval.ApprovalError, "channel mismatch"):
            self._verify(
                manifest_path,
                artifact_root,
                channel="ghcr",
                signature=signature,
            )

    def test_artifact_inventory_rejects_missing_extra_symlink_and_modified_files(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        target = artifact_root / approval.expected_artifact_paths("ghcr", VERSION)[0]
        original = target.read_bytes()
        target.write_bytes(b"modified")
        with self.assertRaisesRegex(approval.ApprovalError, "inventory"):
            self._verify(manifest_path, artifact_root, signature=signature)
        target.write_bytes(original)
        target.unlink()
        with self.assertRaisesRegex(approval.ApprovalError, "artifact set mismatch"):
            self._verify(manifest_path, artifact_root, signature=signature)
        target.write_bytes(original)
        (artifact_root / "extra.txt").write_text("extra")
        with self.assertRaisesRegex(approval.ApprovalError, "artifact set mismatch"):
            self._verify(manifest_path, artifact_root, signature=signature)
        (artifact_root / "extra.txt").unlink()
        (artifact_root / "extra-link").symlink_to(target)
        with self.assertRaisesRegex(approval.ApprovalError, "symlink"):
            self._verify(manifest_path, artifact_root, signature=signature)

    def test_binaries_approval_binds_only_linux_packages(self):
        manifest_path, artifact_root = self._create("binaries")
        expected = {
            f"{component}_{VERSION}_linux_{architecture}.{extension}"
            for component in ("kubeatlas", "kubeatlas-portal")
            for architecture in ("amd64", "arm64")
            for extension in ("deb", "rpm")
        } | {"binary-manifest.json", "checksums.txt"}
        self.assertEqual({path.name for path in artifact_root.iterdir()}, expected)
        signature = self._sign(manifest_path)
        run = self._build_run("binaries")
        self._verify(manifest_path, artifact_root, channel="binaries", signature=signature, run=run)
        (artifact_root / f"kubeatlas_v{VERSION}_windows_amd64.zip").write_bytes(b"extra")
        with self.assertRaisesRegex(approval.ApprovalError, "artifact set mismatch"):
            self._verify(manifest_path, artifact_root, channel="binaries", signature=signature, run=run)

    def test_artifact_root_and_nested_directory_symlinks_are_rejected(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        root_link = self.root / "artifact-root-link"
        root_link.symlink_to(artifact_root, target_is_directory=True)
        with self.assertRaisesRegex(approval.ApprovalError, "real directory"):
            self._verify(manifest_path, root_link, signature=signature)
        image_dir = artifact_root / "images" / "release-image-kubeatlas"
        saved_dir = self.root / "saved-image-dir"
        image_dir.rename(saved_dir)
        image_dir.symlink_to(saved_dir, target_is_directory=True)
        with self.assertRaisesRegex(approval.ApprovalError, "symlink"):
            self._verify(manifest_path, artifact_root, signature=signature)

    def test_malformed_or_unavailable_build_run_api_evidence_is_rejected(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        for run in (
            None,
            {"id": int(RUN_ID)},
            {**self.run, "status": "in_progress"},
            {**self.run, "conclusion": "failure"},
            {**self.run, "event": "push"},
            {**self.run, "head_branch": "feature"},
            {**self.run, "repository": {"full_name": "fork/KubeAtlas"}},
            {**self.run, "head_repository": None},
            {**self.run, "run_attempt": 2},
            {**self.run, "path": ".github/workflows/other.yml"},
            {**self.run, "path": f"{approval.WORKFLOWS['ghcr']}@refs/heads/other"},
            {**self.run, "head_sha": "c" * 40},
        ):
            if run is None:
                continue
            with self.subTest(run=run), self.assertRaises(approval.ApprovalError):
                self._verify(manifest_path, artifact_root, signature=signature, run=run)
        with self.assertRaisesRegex(approval.ApprovalError, "API evidence"):
            self._verify(
                manifest_path,
                artifact_root,
                signature=signature,
                run={"id": int(RUN_ID)},
            )

        with patch.object(approval, "query_build_run", side_effect=approval.ApprovalError("API unavailable")):
            with self.assertRaisesRegex(approval.ApprovalError, "unavailable"):
                approval.verify_approval(
                    "ghcr",
                    manifest_path,
                    artifact_root,
                    SOURCE_SHA,
                    VERSION,
                    RUN_ID,
                    signature,
                    environment=self.env,
                    now=NOW,
                    trusted_root=self.trusted_root,
                )

    def test_valid_main_ref_suffix_in_workflow_run_path_is_accepted(self):
        manifest_path, artifact_root = self._create()
        signature = self._sign(manifest_path)
        run = {
            **self.run,
            "path": f"{approval.WORKFLOWS['ghcr']}@refs/heads/main",
        }
        self.assertEqual(
            self._verify(manifest_path, artifact_root, signature=signature, run=run)["channel"],
            "ghcr",
        )

    def test_duplicate_or_unknown_manifest_keys_are_rejected(self):
        with self.assertRaisesRegex(approval.ApprovalError, "duplicate"):
            approval._load_json(b'{"schemaVersion":1,"schemaVersion":1}')
        manifest_path, artifact_root = self._create()
        data = json.loads(manifest_path.read_text())
        data["unexpected"] = True
        manifest_path.write_text(json.dumps(data))
        signature = self._sign(manifest_path)
        with self.assertRaisesRegex(approval.ApprovalError, "schema"):
            self._verify(manifest_path, artifact_root, signature=signature)

    def test_nonfinite_oversized_and_boolean_integer_manifest_values_are_rejected(self):
        for payload in (
            b'{"value":NaN}',
            b'{"value":Infinity}',
            b'{"value":-Infinity}',
            b" " * (approval.MAX_MANIFEST_BYTES + 1),
        ):
            with self.subTest(payload=payload[:32]), self.assertRaises(approval.ApprovalError):
                approval._load_json(payload)

        manifest_path, artifact_root = self._create()
        signature = self._resign(
            manifest_path,
            lambda manifest: manifest["build"].update(runAttempt=True),
        )
        with self.assertRaises(approval.ApprovalError):
            self._verify(manifest_path, artifact_root, signature=signature)

    def test_oversized_manifest_read_is_bounded_before_parsing_or_signature_verification(self):
        manifest_path, artifact_root = self._create()
        manifest_path.write_bytes(b"x" * (approval.MAX_MANIFEST_BYTES + 128))
        read_sizes = []
        fdopen = os.fdopen

        class ReadTracker:
            def __init__(self, stream):
                self.stream = stream

            def __enter__(self):
                self.stream.__enter__()
                return self

            def __exit__(self, *args):
                return self.stream.__exit__(*args)

            def read(self, size=-1):
                read_sizes.append(size)
                return self.stream.read(size)

        with patch.object(
            approval.os,
            "fdopen",
            side_effect=lambda descriptor, *args, **kwargs: ReadTracker(
                fdopen(descriptor, *args, **kwargs)
            ),
        ), patch.object(approval, "_load_json") as load_json:
            with self.assertRaisesRegex(approval.ApprovalError, "exceeds 64 KiB"):
                self._verify(
                    manifest_path,
                    artifact_root,
                    signature="not-base64",
                )

        self.assertEqual(read_sizes, [approval.MAX_MANIFEST_BYTES + 1])
        load_json.assert_not_called()

    def test_missing_and_non_ed25519_production_keys_fail_closed(self):
        missing_root = self.root / "missing-trusted"
        (missing_root / ".github").mkdir(parents=True)
        with self.assertRaisesRegex(approval.ApprovalError, "missing"):
            approval.create_approval(
                "ghcr",
                self.artifact_root,
                self.root / "missing-approval.json",
                SOURCE_SHA,
                VERSION,
                f"https://github.com/{REPOSITORY}/actions/runs/42",
                f"https://github.com/{REPOSITORY}/actions/runs/43",
                self.env,
                NOW,
                missing_root,
            )
        rsa_private = self.root / "rsa-private.pem"
        rsa_public = self.trusted_root / approval.PUBLIC_KEY
        subprocess.run(
            ["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048", "-out", str(rsa_private)],
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["openssl", "pkey", "-in", str(rsa_private), "-pubout", "-out", str(rsa_public)],
            check=True,
            capture_output=True,
        )
        with self.assertRaisesRegex(approval.ApprovalError, "Ed25519"):
            approval._trusted_key(self.trusted_root)

    def test_creation_rejects_invalid_workflow_binding_and_output_inside_inventory(self):
        invalid_environment = dict(self.env)
        invalid_environment["GITHUB_REF"] = "refs/pull/12/merge"
        with self.assertRaisesRegex(approval.ApprovalError, "trusted main workflow"):
            self._create(environment=invalid_environment)
        with self.assertRaisesRegex(approval.ApprovalError, "outside"):
            approval.create_approval(
                "ghcr",
                self.artifact_root,
                self.artifact_root / "approval.json",
                SOURCE_SHA,
                VERSION,
                f"https://github.com/{REPOSITORY}/actions/runs/42",
                f"https://github.com/{REPOSITORY}/actions/runs/43",
                self.env,
                NOW,
                self.trusted_root,
            )


if __name__ == "__main__":
    unittest.main()
