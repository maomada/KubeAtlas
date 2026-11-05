import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import release_local
import release_local_publisher as publisher
import release_registry


class LocalPublisherTests(unittest.TestCase):
    def test_missing_github_credentials_blocks_before_any_command(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            bundle.mkdir()
            commands = []

            def runner(argv, **kwargs):
                commands.append(argv)
                return subprocess.CompletedProcess(argv, 0, "", "")

            with patch.dict(os.environ, {}, clear=True):
                with self.assertRaisesRegex(publisher.PublisherError, "GH_TOKEN"):
                    publisher.publish_release(bundle, "a" * 40, runner=runner)
            self.assertEqual(commands, [])

    def test_registry_collision_blocks_before_remote_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            bundle.mkdir()
            authfile = root / "auth.json"
            authfile.write_text("{}", encoding="utf-8")
            authfile.chmod(0o600)
            commands = []

            def runner(argv, **kwargs):
                commands.append(argv)
                return subprocess.CompletedProcess(argv, 0, "tool version", "")

            with (
                patch.dict(os.environ, {"GH_TOKEN": "test-only-token"}, clear=True),
                patch.object(release_local, "verify_approval", return_value={}),
                patch.object(
                    release_registry,
                    "inspect_targets",
                    side_effect=release_registry.RegistryPreflightError("collision"),
                ),
            ):
                with self.assertRaisesRegex(publisher.PublisherError, "preflight"):
                    publisher.publish_release(
                        bundle,
                        "a" * 40,
                        runner=runner,
                        journal_dir=root / "journal",
                        authfile_path=authfile,
                    )
            forbidden = {"copy", "push", "create", "upload", "edit"}
            self.assertFalse(
                any(
                    argv[0] in {"skopeo", "helm", "gh"}
                    and any(argument in forbidden for argument in argv[1:])
                    for argv in commands
                )
            )
            self.assertFalse((root / "journal" / "publish-v0.1.0.jsonl").exists())

    def test_any_existing_version_journal_blocks_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            journal_dir = root / "journal"
            bundle.mkdir()
            journal, lock = publisher._create_attempt(journal_dir, bundle, "a" * 40)
            lock.unlink()
            self.assertTrue(journal.is_file())
            with self.assertRaisesRegex(publisher.PublisherError, "retries are forbidden"):
                publisher._create_attempt(journal_dir, bundle, "a" * 40)

    def test_asset_map_is_exactly_the_signed_whitelist(self):
        expected = release_local.release_approval.expected_artifact_paths(
            "binaries", release_local.VERSION
        )
        manifest = {
            "artifacts": {
                "binaries": [
                    {"path": path, "sha256": "0" * 64}
                    for path in expected
                ]
            }
        }
        mapped = publisher._artifact_map(manifest, Path("/tmp/test-bundle"), "binaries")
        self.assertEqual(set(mapped), set(expected))
        with self.assertRaises(publisher.PublisherError):
            publisher._artifact_map(
                {"artifacts": {"binaries": [{"path": "../unsafe", "sha256": "0" * 64}]}},
                Path("/tmp/test-bundle"),
                "binaries",
            )


if __name__ == "__main__":
    unittest.main()
