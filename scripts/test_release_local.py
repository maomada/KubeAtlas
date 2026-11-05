import base64
import hashlib
import json
import os
import subprocess
import tempfile
import unittest
from datetime import timedelta
from pathlib import Path
from unittest.mock import patch

import release_approval
import release_local


SOURCE_SHA = "a" * 40


def make_keypair(root: Path) -> tuple[Path, Path]:
    private_key = root / "test-private.pem"
    public_key = root / release_approval.PUBLIC_KEY
    public_key.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(
        ["openssl", "genpkey", "-algorithm", "Ed25519", "-out", str(private_key)],
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["openssl", "pkey", "-in", str(private_key), "-pubout", "-out", str(public_key)],
        check=True,
        capture_output=True,
    )
    return private_key, public_key


def make_bundle(root: Path, include_gates: bool = True):
    root.mkdir(parents=True, exist_ok=True)
    repo_root = root / "checkout"
    repo_root.mkdir()
    private_key, _ = make_keypair(repo_root)
    bundle = root / "release-bundle"
    for directory in (
        "binaries",
        "ghcr",
        "evidence/logs",
        "evidence/receipts",
        "evidence/reports",
        "evidence/semgrep",
        "evidence/imports",
    ):
        (bundle / directory).mkdir(parents=True, exist_ok=True)
    for channel in ("binaries", "ghcr"):
        for relative in release_approval.expected_artifact_paths(channel, release_local.VERSION):
            path = bundle / channel / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(f"test-only:{channel}:{relative}".encode())
    now = release_local._now()
    context = {
        "schema": "kubeatlas-local-build/v1",
        "repository": release_local.REPOSITORY,
        "version": release_local.VERSION,
        "candidateOnly": True,
        "sourceSha": SOURCE_SHA,
        "toolingSha": SOURCE_SHA,
        "buildId": "2f8d3cce-72ed-4b26-84c7-9f38ab36ef26",
        "createdAt": release_local._format_time(now - timedelta(minutes=2)),
        "completedAt": release_local._format_time(now - timedelta(minutes=1)),
        "status": "built",
    }
    release_local._exclusive_write(
        release_local._context_path(bundle), release_local._canonical_json(context)
    )
    inventories = release_local.artifact_inventories(bundle)
    if include_gates:
        for gate in sorted(release_local.REQUIRED_GATES):
            log_relative = f"logs/{gate}.log"
            log_path = bundle / "evidence" / log_relative
            log_path.write_text(f"test-only evidence for {gate}\n", encoding="utf-8")
            reports = [release_local._evidence_record(bundle, log_relative, "log")]
            acceptance = None
            execution = {"system": "Linux", "machine": "x86_64"}
            commands = [
                {
                    "name": "test-only command",
                    "argv": ["fixture", gate],
                    "cwd": ".",
                    "exitStatus": 0,
                }
            ]
            if gate in release_local.EXTERNAL_GATES:
                acceptance, execution, commands = external_acceptance(gate, inventories)
            receipt = release_local._receipt(
                gate,
                context,
                inventories,
                commands,
                reports,
                execution_platform=execution,
                acceptance=acceptance,
            )
            release_local._write_receipt_file(bundle, gate, receipt)
    return repo_root, bundle, private_key, context, inventories


def external_acceptance(gate: str, inventories: dict):
    scenarios = [
        {"name": name, "status": "passed", "details": "test-only fixture"}
        for name in release_local.ACCEPTANCE_SCENARIOS[gate]
    ]
    versions = {"kubeatlas": release_local.VERSION}
    hashes = {}
    if gate == "windows-native":
        versions.update({"powershell_5_1": "5.1", "powershell_7": "7.5", "helm": "4.2.3"})
        execution = {
            "system": "Windows",
            "machine": "AMD64",
            "emulation": False,
            "isolation": "host",
        }
        commands = [
            {
                "name": "windows_zip_smoke",
                "argv": [
                    "python",
                    "release_windows_smoke.py",
                    "--manifest",
                    "binary-manifest.json",
                    "--archive",
                    "kubeatlas_windows.zip",
                    "--version",
                    release_local.VERSION,
                ],
                "cwd": ".",
                "exitStatus": 0,
            },
            {
                "name": "powershell_5_1_installer_fixtures",
                "argv": ["powershell", "scripts/test-installers.ps1"],
                "cwd": ".",
                "exitStatus": 0,
            },
            {
                "name": "powershell_7_installer_fixtures",
                "argv": ["pwsh", "scripts/test-installers.ps1"],
                "cwd": ".",
                "exitStatus": 0,
            },
            {
                "name": "helm_refresh_windows",
                "argv": [
                    "go",
                    "test",
                    "./internal/helm/repository_cache_windows.go",
                    "./internal/helm/repository_cache_windows_test.go",
                ],
                "cwd": ".",
                "exitStatus": 0,
            },
        ]
        hashes = {
            name: digest
            for name, digest in (
                (item["path"], item["sha256"]) for item in inventories["binaries"]
            )
            if name.endswith(".zip") and "portal" not in name
        }
    elif gate == "real-cluster":
        versions.update({"kubeatlas-portal": release_local.VERSION, "kubernetes": "1.33.1"})
        execution = {
            "system": "Linux",
            "machine": "x86_64",
            "emulation": False,
            "isolation": "host",
        }
        commands = [
            {
                "name": name,
                "argv": ["kubectl", "get", "pods"],
                "cwd": ".",
                "exitStatus": 0,
            }
            for name in release_local.ACCEPTANCE_SCENARIOS[gate]
        ]
    else:
        remainder = gate.removeprefix("package-install-")
        component = next(
            name
            for name in ("kubeatlas-portal", "kubeatlas")
            if remainder.startswith(name + "-")
        )
        _, arch, package = remainder.removeprefix(component + "-").split("-")
        versions.update({"distribution": "Debian 12" if package == "deb" else "Fedora 41"})
        versions[component] = release_local.VERSION
        execution = {
            "system": "Linux",
            "machine": "x86_64" if arch == "amd64" else "aarch64",
            "emulation": "native",
            "isolation": "vm",
        }
        artifact = f"{component}_{release_local.VERSION}_linux_{arch}.{package}"
        hashes = {
            artifact: next(
                item["sha256"]
                for item in inventories["binaries"]
                if item["path"] == artifact
            )
        }
        commands = [
            {
                "name": name,
                "argv": ["package-test", name],
                "cwd": ".",
                "exitStatus": 0,
            }
            for name in release_local.ACCEPTANCE_SCENARIOS[gate]
        ]
    acceptance = {
        "context": "test-only fixture host/context",
        "componentVersions": versions,
        "limitations": ["test-only fixture; not real acceptance"],
        "scenarios": scenarios,
        "execution": execution,
        "artifactHashes": hashes,
    }
    return acceptance, execution, commands


class LocalReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        (
            self.repo_root,
            self.bundle,
            self.private_key,
            self.context,
            self.inventories,
        ) = make_bundle(self.root)

    def _prepare(self):
        with patch.object(release_local, "assert_authority", return_value=SOURCE_SHA):
            manifest_path = release_local.prepare_approval(
                self.bundle,
                SOURCE_SHA,
                self.repo_root,
                main_sha_getter=lambda: SOURCE_SHA,
            )
        raw_signature = self.root / "signature.raw"
        subprocess.run(
            [
                "openssl",
                "pkeyutl",
                "-sign",
                "-rawin",
                "-inkey",
                str(self.private_key),
                "-in",
                str(manifest_path),
                "-out",
                str(raw_signature),
            ],
            check=True,
            capture_output=True,
        )
        (self.bundle / "approval.sig").write_bytes(
            base64.b64encode(raw_signature.read_bytes())
        )
        return manifest_path

    def test_complete_bundle_round_trips_through_real_openssl_signature(self):
        manifest_path = self._prepare()
        with patch.object(release_local, "assert_authority", return_value=SOURCE_SHA):
            verified = release_local.verify_approval(
                self.bundle,
                SOURCE_SHA,
                self.repo_root,
                main_sha_getter=lambda: SOURCE_SHA,
            )
        self.assertEqual(verified["schema"], release_local.MANIFEST_SCHEMA)
        self.assertEqual(verified["origin"], "local")
        self.assertEqual(verified["toolingSha"], SOURCE_SHA)
        self.assertNotIn("runId", verified)
        self.assertEqual(manifest_path.read_bytes()[-1:], b"\n")

    def test_modified_artifact_and_evidence_are_rejected_after_signing(self):
        self._prepare()
        artifact = self.bundle / "binaries" / self.inventories["binaries"][0]["path"]
        artifact.write_bytes(artifact.read_bytes() + b"tampered")
        with patch.object(release_local, "assert_authority", return_value=SOURCE_SHA):
            with self.assertRaisesRegex(release_local.LocalReleaseError, "inventory"):
                release_local.verify_approval(
                    self.bundle,
                    SOURCE_SHA,
                    self.repo_root,
                    main_sha_getter=lambda: SOURCE_SHA,
                )

    def test_stale_main_dirty_checkout_and_missing_receipts_fail_closed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / ".github").mkdir()
            private_key, _ = make_keypair(root)
            private_key.unlink()
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            subprocess.run(["git", "-C", str(root), "config", "user.name", "test"], check=True)
            subprocess.run(
                ["git", "-C", str(root), "config", "user.email", "test@example.invalid"],
                check=True,
            )
            subprocess.run(
                ["git", "-C", str(root), "add", ".github/release-signing-public.pem"],
                check=True,
            )
            subprocess.run(["git", "-C", str(root), "commit", "-qm", "test fixture"], check=True)
            head = subprocess.check_output(
                ["git", "-C", str(root), "rev-parse", "HEAD"], text=True
            ).strip()
            with self.assertRaisesRegex(release_local.LocalReleaseError, "HEAD"):
                release_local.assert_authority("b" * 40, root)
            with self.assertRaisesRegex(release_local.LocalReleaseError, "fresh main"):
                release_local.assert_authority(
                    head,
                    root,
                    require_main=True,
                    main_sha_getter=lambda: "c" * 40,
                )
            (root / "dirty.txt").write_text("dirty", encoding="utf-8")
            with self.assertRaisesRegex(release_local.LocalReleaseError, "clean"):
                release_local.assert_authority(head, root)

        _, empty_bundle, _, _, _ = make_bundle(self.root / "missing", include_gates=False)
        with patch.object(release_local, "assert_authority", return_value=SOURCE_SHA):
            with self.assertRaisesRegex(release_local.LocalReleaseError, "missing"):
                release_local.prepare_approval(
                    empty_bundle,
                    SOURCE_SHA,
                    self.repo_root,
                    main_sha_getter=lambda: SOURCE_SHA,
                )

    def test_untested_external_acceptance_is_imported_but_cannot_pass_gate(self):
        _, bundle, _, context, inventories = make_bundle(self.root / "untested", include_gates=False)
        gate = "package-install-kubeatlas-linux-amd64-deb"
        log = self.root / "host-evidence" / "run.log"
        log.parent.mkdir(parents=True)
        log.write_text("Installer acceptance not run on this host.\n", encoding="utf-8")
        artifact = "kubeatlas_0.1.0_linux_amd64.deb"
        digest = next(item["sha256"] for item in inventories["binaries"] if item["path"] == artifact)
        now = release_local._format_time(release_local._now())
        report = {
            "schema": release_local.ACCEPTANCE_SCHEMA,
            "origin": "operator-attested",
            "repository": release_local.REPOSITORY,
            "gate": gate,
            "version": release_local.VERSION,
            "sourceSha": SOURCE_SHA,
            "toolingSha": SOURCE_SHA,
            "buildId": context["buildId"],
            "artifactInventorySha256": release_local.artifact_inventory_digest(inventories),
            "executionPlatform": {
                "system": "Linux",
                "machine": "x86_64",
                "emulation": "native",
                "isolation": "vm",
            },
            "issuedAt": now,
            "completedAt": now,
            "commands": [],
            "exitStatus": 0,
            "result": "untested",
            "context": "Debian 12 test VM",
            "componentVersions": {"kubeatlas": release_local.VERSION, "distribution": "Debian 12"},
            "limitations": ["Installation not performed."],
            "scenarios": [
                {"name": name, "status": "untested", "details": "Not run."}
                for name in release_local.ACCEPTANCE_SCENARIOS[gate]
            ],
            "evidence": [
                {
                    "path": "run.log",
                    "size": log.stat().st_size,
                    "sha256": hashlib.sha256(log.read_bytes()).hexdigest(),
                    "kind": "log",
                }
            ],
            "artifactHashes": {artifact: digest},
        }
        report_path = self.root / "acceptance.json"
        report_path.write_text(json.dumps(report), encoding="utf-8")
        with patch.object(release_local, "_clean_head", return_value=SOURCE_SHA):
            release_local.import_acceptance(bundle, report_path, log.parent, self.repo_root)
        self.assertTrue((bundle / "evidence" / "imports" / gate / "acceptance-report.json").is_file())
        with self.assertRaisesRegex(release_local.LocalReleaseError, "does not bind"):
            release_local.validate_receipts(
                bundle,
                context,
                inventories,
                required_gates=frozenset({gate}),
            )


if __name__ == "__main__":
    unittest.main()
