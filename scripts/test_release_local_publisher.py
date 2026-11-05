import hashlib
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import release_local
import release_local_publisher as publisher
import release_registry
import release_github


class LocalPublisherTests(unittest.TestCase):
    def test_successful_publication_preserves_order_and_exact_asset_whitelist(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            authfile = root / "auth.json"
            (bundle / "evidence").mkdir(parents=True)
            (bundle / "binaries").mkdir()
            (bundle / "ghcr").mkdir()
            authfile.write_text("{}", encoding="utf-8")
            authfile.chmod(0o600)
            for channel in ("binaries", "ghcr"):
                for relative in release_local.expected_artifact_paths(
                    channel, release_local.VERSION
                ):
                    path = bundle / channel / relative
                    path.parent.mkdir(parents=True, exist_ok=True)
                    if relative.endswith(".metadata.json"):
                        component = "kubeatlas-portal" if "portal" in relative else "kubeatlas"
                        raw = b'{"schemaVersion":2,"manifests":[]}'
                        digest = hashlib.sha256(raw).hexdigest()
                        path.write_text(
                            json.dumps(
                                {
                                    "component": component,
                                    "sourceSha": "a" * 40,
                                    "version": release_local.VERSION,
                                    "buildDigest": "sha256:" + digest,
                                }
                            ),
                            encoding="utf-8",
                        )
                    else:
                        path.write_bytes(f"test-only:{relative}".encode())
            inventories = release_local.artifact_inventories(bundle)
            manifest = {"artifacts": inventories}
            events = []
            copied_images = set()
            published_charts = set()
            remote_commands = []
            binaries = {
                item["path"]: bundle / "binaries" / item["path"]
                for item in inventories["binaries"]
            }
            image_raw = b'{"schemaVersion":2,"manifests":[]}'

            def runner(argv, **_kwargs):
                remote_commands.append(argv)
                if argv[:2] == ["gh", "--version"] or argv[:3] == ["helm", "version", "--short"]:
                    return subprocess.CompletedProcess(argv, 0, "version\n", "")
                if argv[:2] == ["skopeo", "--version"]:
                    return subprocess.CompletedProcess(argv, 0, "skopeo version\n", "")
                if argv[:2] == ["skopeo", "inspect"]:
                    reference = argv[-1]
                    if reference.startswith("oci-archive:"):
                        return subprocess.CompletedProcess(argv, 0, image_raw.decode(), "")
                    if reference.startswith("docker://"):
                        ref = reference.removeprefix("docker://")
                        if ref in copied_images:
                            return subprocess.CompletedProcess(argv, 0, image_raw.decode(), "")
                        if ref in published_charts:
                            return subprocess.CompletedProcess(argv, 0, "chart manifest", "")
                        return subprocess.CompletedProcess(argv, 1, "", "manifest unknown")
                if argv[:2] == ["skopeo", "copy"]:
                    events.append("image-copy")
                    copied_images.add(argv[-1].removeprefix("docker://"))
                    return subprocess.CompletedProcess(argv, 0, "", "")
                if argv[:2] == ["helm", "push"]:
                    events.append("chart-push")
                    published_charts.add(
                        f"ghcr.io/maomada/charts/{Path(argv[2]).name.removesuffix(f'-{release_local.VERSION}.tgz')}:{release_local.VERSION}"
                    )
                    return subprocess.CompletedProcess(argv, 0, "", "")
                if argv[:2] == ["helm", "pull"]:
                    chart = argv[2].rsplit("/", 1)[-1]
                    destination = Path(argv[argv.index("--destination") + 1])
                    source = bundle / "ghcr" / "charts" / f"{chart}-{release_local.VERSION}.tgz"
                    (destination / source.name).write_bytes(source.read_bytes())
                    return subprocess.CompletedProcess(argv, 0, "", "")
                if argv[:2] == ["gh", "api"] and "--method" in argv:
                    if argv[argv.index("--method") + 1] == "POST":
                        events.append("draft")
                        return subprocess.CompletedProcess(
                            argv,
                            0,
                            json.dumps(
                                {
                                    "id": 7,
                                    "draft": True,
                                    "tag_name": f"v{release_local.VERSION}",
                                    "target_commitish": "a" * 40,
                                }
                            ),
                            "",
                        )
                    events.append("expose")
                    return subprocess.CompletedProcess(
                        argv,
                        0,
                        json.dumps(
                            {
                                "draft": False,
                                "tag_name": f"v{release_local.VERSION}",
                                "target_commitish": "a" * 40,
                            }
                        ),
                        "",
                    )
                if argv[:3] == ["gh", "release", "upload"]:
                    events.append(f"upload:{Path(argv[4]).name}")
                    self.assertNotIn("--clobber", argv)
                    self.assertNotIn("delete", argv)
                    return subprocess.CompletedProcess(argv, 0, "", "")
                if argv[:3] == ["gh", "release", "download"]:
                    name = argv[argv.index("--pattern") + 1]
                    destination = Path(argv[argv.index("--dir") + 1])
                    (destination / name).write_bytes(binaries[name].read_bytes())
                    events.append(f"download:{name}")
                    return subprocess.CompletedProcess(argv, 0, "", "")
                return subprocess.CompletedProcess(argv, 0, "", "")

            def create_tag(path, _token, body):
                events.append("tag")
                self.assertEqual(path, "repos/maomada/KubeAtlas/git/refs")
                self.assertEqual(body, {"ref": f"refs/tags/v{release_local.VERSION}", "sha": "a" * 40})
                return 201

            with (
                patch.dict(os.environ, {"GH_TOKEN": "test-only-token"}, clear=True),
                patch.object(release_local, "verify_approval", return_value=manifest),
                patch.object(
                    release_github,
                    "preflight",
                    return_value=[
                        {"path": "repo", "status": "authenticated"},
                        {"path": "tag", "status": "absent"},
                        {"path": "release", "status": "absent"},
                    ],
                ),
                patch.object(release_github, "github_post", side_effect=create_tag),
            ):
                publisher.publish_release(
                    bundle,
                    "a" * 40,
                    main_sha_getter=lambda: "a" * 40,
                    runner=runner,
                    journal_dir=root / "journal",
                    authfile_path=authfile,
                )

            expected_assets = sorted(
                release_local.expected_artifact_paths("binaries", release_local.VERSION)
            )
            uploads = [event.removeprefix("upload:") for event in events if event.startswith("upload:")]
            downloads = [event.removeprefix("download:") for event in events if event.startswith("download:")]
            self.assertEqual(uploads, expected_assets)
            self.assertEqual(downloads, expected_assets)
            self.assertEqual(len(expected_assets), 10)
            self.assertEqual(
                sum(name.endswith((".deb", ".rpm")) for name in expected_assets), 8
            )
            self.assertFalse(any(name.endswith(".zip") for name in expected_assets))
            self.assertEqual(events.count("image-copy"), 2)
            image_copies = [argv for argv in remote_commands if argv[:2] == ["skopeo", "copy"]]
            self.assertEqual(len(image_copies), 2)
            for argv in image_copies:
                self.assertIn("--all", argv)
                self.assertIn("--preserve-digests", argv)
            self.assertEqual(events.count("chart-push"), 2)
            self.assertLess(events.index("image-copy"), events.index("chart-push"))
            self.assertLess(events.index("chart-push"), events.index("tag"))
            self.assertLess(events.index("tag"), events.index("draft"))
            self.assertEqual(events[-1], "expose")
            self.assertTrue(all("--clobber" not in argv and "delete" not in argv for argv in remote_commands))

    def test_extra_windows_zip_is_rejected_before_any_remote_write(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            bundle.mkdir()
            authfile = root / "auth.json"
            authfile.write_text("{}", encoding="utf-8")
            authfile.chmod(0o600)
            expected = release_local.expected_artifact_paths(
                "binaries", release_local.VERSION
            )
            manifest = {
                "artifacts": {
                    "binaries": [
                        {"path": path, "sha256": "0" * 64} for path in expected
                    ]
                    + [
                        {
                            "path": f"kubeatlas_v{release_local.VERSION}_windows_amd64.zip",
                            "sha256": "f" * 64,
                        }
                    ],
                    "ghcr": [],
                }
            }
            commands = []

            def runner(argv, **kwargs):
                commands.append(argv)
                return subprocess.CompletedProcess(argv, 0, "test version\n", "")

            with (
                patch.dict(os.environ, {"GH_TOKEN": "test-only-token"}, clear=True),
                patch.object(release_local, "verify_approval", return_value=manifest),
                patch.object(
                    release_registry,
                    "inspect_targets",
                    return_value=[{"status": "absent"}] * 4,
                ),
                patch.object(
                    release_github,
                    "preflight",
                    return_value=[
                        {"path": "repo", "status": "authenticated"},
                        {"path": "tag", "status": "absent"},
                        {"path": "release", "status": "absent"},
                    ],
                ),
            ):
                with self.assertRaisesRegex(
                    publisher.PublisherError, "exact supported publication profile"
                ):
                    publisher.publish_release(
                        bundle,
                        "a" * 40,
                        runner=runner,
                        journal_dir=root / "journal",
                        authfile_path=authfile,
                    )

            self.assertEqual(
                commands,
                [
                    ["gh", "--version"],
                    ["skopeo", "--version"],
                    ["helm", "version", "--short"],
                ],
            )
            self.assertFalse((root / "journal").exists())

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

    def test_unknown_mutation_is_journaled_and_blocks_retry(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            bundle = root / "bundle"
            bundle.mkdir()
            journal, _lock = publisher._create_attempt(root / "journal", bundle, "a" * 40)
            with self.assertRaises(publisher.PublisherError):
                publisher._run_and_journal(
                    lambda *_args, **_kwargs: None,
                    lambda: None,
                    journal,
                    "a" * 40,
                    "test-write",
                    "remote-target",
                    lambda: (_ for _ in ()).throw(RuntimeError("unknown")),
                )
            self.assertIn(b'"unknown:test-write"', journal.read_bytes())
            with self.assertRaisesRegex(
                publisher.PublisherError, "publisher lock already exists|retries are forbidden"
            ):
                publisher._create_attempt(root / "journal", bundle, "a" * 40)

    def test_source_gate_or_artifact_change_blocks_later_mutation(self):
        for changed in ("source", "gate", "artifact"):
            with self.subTest(changed=changed), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                bundle = root / "bundle"
                evidence = bundle / "evidence"
                evidence.mkdir(parents=True)
                artifact = bundle / "artifact.bin"
                artifact.write_bytes(b"approved artifact")
                gate = evidence / "gate.log"
                gate.write_bytes(b"approved gate evidence")
                journal, _lock = publisher._create_attempt(root / "journal", bundle, "a" * 40)
                writes = []
                source = ["a" * 40]
                artifact_hash = hashlib.sha256(artifact.read_bytes()).hexdigest()
                gate_hash = hashlib.sha256(gate.read_bytes()).hexdigest()

                def verify():
                    if source[0] != "a" * 40:
                        raise publisher.PublisherError("source changed after prior write")
                    if hashlib.sha256(artifact.read_bytes()).hexdigest() != artifact_hash:
                        raise publisher.PublisherError("artifact changed after prior write")
                    if hashlib.sha256(gate.read_bytes()).hexdigest() != gate_hash:
                        raise publisher.PublisherError("gate evidence changed after prior write")

                def first_write():
                    writes.append("first")
                    if changed == "source":
                        source[0] = "b" * 40
                    elif changed == "gate":
                        gate.write_bytes(b"changed gate evidence")
                    else:
                        artifact.write_bytes(b"changed artifact")

                publisher._run_and_journal(
                    lambda *_args, **_kwargs: None,
                    verify,
                    journal,
                    "a" * 40,
                    "first-write",
                    "first-target",
                    first_write,
                )
                with self.assertRaises(publisher.PublisherError):
                    publisher._run_and_journal(
                        lambda *_args, **_kwargs: None,
                        verify,
                        journal,
                        "a" * 40,
                        "later-write",
                        "later-target",
                        lambda: writes.append("later"),
                    )
                self.assertEqual(writes, ["first"])

    def test_gate_signature_expiry_foreign_signature_tamper_and_native_fail_before_writes(self):
        failures = (
            "gate",
            "signature",
            "expiry",
            "foreign-mode signature",
            "tampered evidence",
            "native platform",
        )
        for failure in failures:
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                bundle = root / "bundle"
                bundle.mkdir()
                authfile = root / "auth.json"
                authfile.write_text("{}", encoding="utf-8")
                authfile.chmod(0o600)
                commands = []

                def runner(argv, **kwargs):
                    commands.append(argv)
                    return subprocess.CompletedProcess(argv, 0, "", "")

                with (
                    patch.dict(os.environ, {"GH_TOKEN": "test-only-token"}, clear=True),
                    patch.object(
                        release_local,
                        "verify_approval",
                        side_effect=release_local.LocalReleaseError(failure),
                    ),
                ):
                    with self.assertRaises(
                        (publisher.PublisherError, release_local.LocalReleaseError)
                    ):
                        publisher.publish_release(
                            bundle,
                            "a" * 40,
                            runner=runner,
                            journal_dir=root / "journal",
                            authfile_path=authfile,
                        )
                self.assertEqual(commands, [])
                self.assertFalse((root / "journal").exists())

    def test_asset_map_is_exactly_the_signed_whitelist(self):
        expected = release_local.expected_artifact_paths("binaries", release_local.VERSION)
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
