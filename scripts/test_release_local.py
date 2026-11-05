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
        for relative in release_local.expected_artifact_paths(channel, release_local.VERSION):
            path = bundle / channel / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            if relative.endswith(".metadata.json"):
                component = "kubeatlas-portal" if "portal" in relative else "kubeatlas"
                path.write_bytes(
                    release_local._canonical_json(
                        {
                            "component": component,
                            "sourceSha": SOURCE_SHA,
                            "version": release_local.VERSION,
                            "buildDigest": "sha256:" + "b" * 64,
                        }
                    )
                )
            else:
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
            commands = []
            tool_versions = {}
            if gate == "build":
                commands = [
                    {
                        "name": "Build full release matrix and selected local profile",
                        "argv": [
                            "make",
                            "release-binaries-dry",
                            f"KUBEATLAS_RELEASE_VERSION={release_local.VERSION}",
                            f"SOURCE_SHA={SOURCE_SHA}",
                        ],
                        "cwd": ".",
                        "exitStatus": 0,
                    }
                ]
                full_report = bundle / "evidence" / "reports" / "full-matrix-inventory.json"
                full_report.write_bytes(
                    release_local._canonical_json(
                        {
                            "schema": "kubeatlas-full-matrix/v1",
                            "repository": release_local.REPOSITORY,
                            "version": release_local.VERSION,
                            "sourceSha": SOURCE_SHA,
                            "artifacts": [
                                {"path": name, "size": 1, "sha256": "c" * 64}
                                for name in release_local.release_artifacts.expected_names(
                                    release_local.VERSION
                                )
                            ],
                        }
                    )
                )
                reports.append(
                    release_local._evidence_record(
                        bundle, "reports/full-matrix-inventory.json", "report"
                    )
                )
            elif gate in release_local.AUTOMATED_GATES:
                rule_source = Path("/tmp/test-semgrep-rules")
                specs, report_paths, _ = release_local._gate_specs(bundle, rule_source, gate)
                if gate.startswith("semgrep-"):
                    output_dir = report_paths[0].parent
                    output_dir.mkdir(parents=True, exist_ok=True)
                    report_paths = [output_dir / path.name for path in report_paths]
                    if gate == "semgrep-self-test":
                        clean_target = output_dir / "clean.go"
                        unsafe_targets = [
                            output_dir / name for name in ("unsafe.go", "unsafe.js", "unsafe.ts")
                        ]
                        clean_target.write_text("package main\n", encoding="utf-8")
                        for target in unsafe_targets:
                            target.write_text("fixture\n", encoding="utf-8")
                        report_paths[0].write_bytes(
                            release_local._canonical_json(
                                {"errors": [], "paths": {"scanned": [str(clean_target)]}, "results": []}
                            )
                        )
                        report_paths[1].write_bytes(
                            release_local._canonical_json(
                                {
                                    "errors": [],
                                    "paths": {"scanned": [str(path) for path in unsafe_targets]},
                                    "results": [{"path": str(path)} for path in unsafe_targets],
                                }
                            )
                        )
                    else:
                        targets = sorted(
                            release_local.ROOT / name
                            for name in subprocess.check_output(
                                ["git", "-C", str(release_local.ROOT), "ls-files", "-z"]
                            ).decode().split("\0")
                            if name and release_local.security_scan.production_source(name)
                        )
                        lock_path = release_local.ROOT / ".github/security-rules.lock.json"
                        policy_path = release_local.ROOT / ".github/security-dispositions.json"
                        lock = release_local._load_json(lock_path, "rules")
                        lock_hash = hashlib.sha256(lock_path.read_bytes()).hexdigest()
                        policy = release_local._load_json(policy_path, "policy")
                        findings = {
                            "errors": [],
                            "paths": {"scanned": [str(path) for path in targets]},
                            "results": [
                                entry["finding"] for entry in policy["entries"]
                            ],
                        }
                        decision = release_local.security_dispositions.evaluate(
                            findings,
                            policy,
                            release_local.ROOT,
                            targets,
                            release_local.security_scan.ENGINE_VERSION,
                            lock_hash,
                        )
                        report_paths[0].write_bytes(release_local._canonical_json(findings))
                        report_paths[1].write_bytes(
                            release_local._canonical_json(
                                {
                                    "sourceSha": SOURCE_SHA,
                                    "engine": release_local.security_scan.ENGINE_VERSION,
                                    "rulesCommit": lock["commit"],
                                    "lockSha256": lock_hash,
                                    "targets": [
                                        path.relative_to(release_local.ROOT).as_posix()
                                        for path in targets
                                    ],
                                }
                            )
                        )
                        report_paths[2].write_bytes(
                            release_local._canonical_json(
                                {
                                    **decision,
                                    "sourceSha": SOURCE_SHA,
                                    "policySha256": hashlib.sha256(policy_path.read_bytes()).hexdigest(),
                                    "rawReportSha256": hashlib.sha256(report_paths[0].read_bytes()).hexdigest(),
                                }
                            )
                        )
                for report_path in report_paths:
                    if not report_path.exists():
                        report_path.parent.mkdir(parents=True, exist_ok=True)
                        report_path.write_text("test-only report\n", encoding="utf-8")
                    reports.append(
                        release_local._evidence_record(
                            bundle, report_path.relative_to(bundle / "evidence").as_posix(), "report"
                        )
                    )
                commands = [
                    {
                        "name": spec["name"],
                        "argv": release_local._display_argv(
                            spec["argv"], bundle, release_local.ROOT
                        ),
                        "cwd": spec.get("cwd", "."),
                        "exitStatus": 0,
                    }
                    for spec in specs
                ]
                required_tools = {
                    "linux-ci": ("go",), "frontend": ("node",), "settings-e2e": ("node",),
                    "helm": ("helm", "helm3", "helmfile"), "release-security": ("go",),
                    "posix-installers": ("shellcheck",),
                    "semgrep-self-test": ("semgrep",), "semgrep-full": ("semgrep",),
                    "package-oci": ("python",),
                }[gate]
                version_values = {
                    "go": "go1.26.test",
                    "node": "v24.test",
                    "helm": "v4.2.3",
                    "helm3": "v3.19.0",
                    "helmfile": "test-only",
                    "shellcheck": "test-only",
                    "semgrep": "1.180.0",
                    "python": "3.12.test",
                }
                tool_versions = {name: version_values[name] for name in required_tools}
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
                tool_versions=tool_versions,
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
    deployed = {}
    executables = {}
    if gate == "windows-native":
        versions.update(
            {
                "powershell_5_1": "5.1.22621.3235",
                "powershell_7": "7.5.0",
                "helm": "4.2.3",
            }
        )
        executables = {
            "powershell_5_1": r"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe",
            "powershell_7": r"C:\Program Files\PowerShell\7\pwsh.exe",
        }
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
                "argv": [executables["powershell_5_1"], "scripts/test-installers.ps1"],
                "cwd": ".",
                "exitStatus": 0,
            },
            {
                "name": "powershell_7_installer_fixtures",
                "argv": [executables["powershell_7"], "scripts/test-installers.ps1"],
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
        hashes = {
            item["path"]: item["sha256"]
            for item in inventories["ghcr"]
            if item["path"].endswith((".oci.tar", ".tgz"))
        }
        deployed = {
            "kubeatlas": "sha256:" + "b" * 64,
            "kubeatlas-portal": "sha256:" + "b" * 64,
        }
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
        "deployedImageDigests": deployed,
        "executables": executables,
    }
    return acceptance, execution, commands


def external_report(receipt: dict) -> dict:
    acceptance = receipt["acceptance"]
    report = {
        key: value
        for key, value in receipt.items()
        if key not in {"schema", "origin", "reports", "acceptance"}
    }
    report.update(
        {
            "schema": release_local.ACCEPTANCE_SCHEMA,
            "origin": "operator-attested",
            "evidence": receipt["reports"],
            **{key: value for key, value in acceptance.items() if key != "execution"},
        }
    )
    return report


class SkopeoCapabilityTests(unittest.TestCase):
    def test_copy_help_must_succeed_and_advertise_both_required_flags(self):
        supported = subprocess.CompletedProcess(
            ["skopeo", "copy", "--help"],
            0,
            "Usage: skopeo copy [options]\n  --all\n  --preserve-digests\n",
            "",
        )
        release_local.require_skopeo_copy_capabilities(
            lambda argv, **_kwargs: supported
        )

        for result in (
            subprocess.CompletedProcess(
                ["skopeo", "copy", "--help"], 0, "--all\n", ""
            ),
            subprocess.CompletedProcess(
                ["skopeo", "copy", "--help"], 1, "--all --preserve-digests", ""
            ),
        ):
            with self.subTest(result=result.returncode, stdout=result.stdout):
                with self.assertRaisesRegex(
                    release_local.LocalReleaseError, "must support --all"
                ):
                    release_local.require_skopeo_copy_capabilities(
                        lambda argv, **_kwargs: result
                    )

        with self.assertRaisesRegex(
            release_local.LocalReleaseError, "cannot inspect required Skopeo"
        ):
            release_local.require_skopeo_copy_capabilities(
                lambda *_args, **_kwargs: (_ for _ in ()).throw(OSError("unavailable"))
            )


class BuildPreflightTests(unittest.TestCase):
    def test_missing_package_tools_stop_before_bundle_creation(self):
        versions = {"go": "go version go1.26.10", "goreleaser": "2.13.3",
                    "node": "v24.19.0", "helm": "v4.2.3"}
        for missing in ("rpm", "dpkg-deb", "cpio", "rpm2cpio", "npx"):
            with self.subTest(missing=missing), tempfile.TemporaryDirectory() as directory:
                bundle = Path(directory) / "not-created"

                def run(argv, **kwargs):
                    if argv[0] == missing:
                        raise FileNotFoundError(missing)
                    return subprocess.CompletedProcess(argv, 0, versions.get(argv[0], "test version"), "")

                with (
                    patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
                    patch.object(release_local.subprocess, "run", side_effect=run) as commands,
                    patch.object(release_local.shutil, "which", side_effect=lambda tool: None if tool == missing else f"/usr/bin/{tool}"),
                    self.assertRaisesRegex(release_local.LocalReleaseError, f"required tool is unavailable: {missing}"),
                ):
                    release_local.build_bundle(SOURCE_SHA, bundle)
                self.assertFalse(bundle.exists())
                self.assertFalse(any(call.args[0][:2] == ["make", "release-binaries-dry"] for call in commands.call_args_list))

    def test_unsupported_skopeo_fails_before_bundle_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            bundle = Path(directory) / "not-created"
            versions = {
                "go": "go version go1.26.10",
                "goreleaser": "2.13.3",
                "node": "v24.19.0",
                "helm": "v4.2.3",
            }

            def run(argv, **kwargs):
                if argv == ["skopeo", "copy", "--help"]:
                    return subprocess.CompletedProcess(argv, 0, "Usage: copy --all\n", "")
                return subprocess.CompletedProcess(
                    argv, 0, versions.get(argv[0], "test version"), ""
                )

            with (
                patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
                patch.object(release_local.subprocess, "run", side_effect=run) as commands,
                patch.object(release_local.shutil, "which", return_value="/usr/bin/tool"),
                self.assertRaisesRegex(
                    release_local.LocalReleaseError, "Skopeo copy must support"
                ),
            ):
                release_local.build_bundle(SOURCE_SHA, bundle)

            self.assertFalse(bundle.exists())
            self.assertFalse(
                any(call.args[0][:2] == ["make", "release-binaries-dry"] for call in commands.call_args_list)
            )


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

    def test_local_amd64_profile_keeps_fifteen_gates_and_exact_targets(self):
        expected_packages = {
            f"{component}_{release_local.VERSION}_linux_{architecture}.{package}"
            for component in ("kubeatlas", "kubeatlas-portal")
            for architecture in ("amd64",)
            for package in ("deb", "rpm")
        }
        expected_metadata = {"binary-manifest.json", "checksums.txt"}
        expected_package_gates = {
            f"package-install-{component}-linux-{architecture}-{package}"
            for component in ("kubeatlas", "kubeatlas-portal")
            for architecture in ("amd64",)
            for package in ("deb", "rpm")
        }
        expected_gates = {
            "build",
            "linux-ci",
            "frontend",
            "settings-e2e",
            "helm",
            "release-security",
            "posix-installers",
            "semgrep-self-test",
            "semgrep-full",
            "package-oci",
            "real-cluster",
            *expected_package_gates,
        }

        self.assertEqual(release_local.PUBLICATION_PROFILE, "local-linux-amd64")
        self.assertEqual(release_local.IMAGE_PLATFORMS, {("linux", "amd64")})
        self.assertEqual(release_local.REQUIRED_GATES, expected_gates)
        self.assertEqual(len(release_local.REQUIRED_GATES), 15)
        self.assertNotIn("windows-native", release_local.REQUIRED_GATES)
        self.assertNotIn("windows-native", release_local.EXTERNAL_GATES)
        self.assertIn("windows-native", release_local.ACCEPTANCE_SCENARIOS)
        selected_paths = set(
            release_local.expected_artifact_paths("binaries", release_local.VERSION)
        )
        self.assertEqual(selected_paths, expected_packages | expected_metadata)
        self.assertEqual(len(expected_packages), 4)
        self.assertEqual(len(expected_metadata), 2)
        self.assertFalse(any(path.endswith(".zip") for path in selected_paths))
        self.assertFalse(any("arm64" in path for path in selected_paths))
        self.assertFalse(any("arm64" in gate for gate in release_local.REQUIRED_GATES))
        self.assertEqual(
            release_local._expected_targets()[-len(selected_paths):],
            sorted(selected_paths),
        )

    def test_package_acceptance_requires_native_amd64_execution(self):
        gate = "package-install-kubeatlas-linux-amd64-deb"
        acceptance, execution, commands = external_acceptance(gate, self.inventories)
        receipt = {"acceptance": acceptance, "commands": commands}
        release_local._validate_acceptance(receipt, gate)
        for mode, machine in (("emulated", "x86_64"), ("native", "aarch64")):
            with self.subTest(mode=mode, machine=machine):
                execution.update(emulation=mode, machine=machine)
                with self.assertRaisesRegex(release_local.LocalReleaseError, "platform or version"):
                    release_local._validate_acceptance(receipt, gate)

    def _prepare(self):
        with (
            patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
            patch.object(release_local, "_validate_bundle_content", return_value={}),
        ):
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
        with (
            patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
            patch.object(release_local, "_validate_bundle_content", return_value={}),
        ):
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
        with (
            patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
            patch.object(release_local, "_validate_bundle_content", return_value={}),
        ):
            with self.assertRaisesRegex(release_local.LocalReleaseError, "inventory"):
                release_local.verify_approval(
                    self.bundle,
                    SOURCE_SHA,
                    self.repo_root,
                    main_sha_getter=lambda: SOURCE_SHA,
                )

    def test_evidence_tampering_is_rejected_after_signing(self):
        self._prepare()
        log_path = self.bundle / "evidence" / "logs" / "build.log"
        log_path.write_text("tampered evidence\n", encoding="utf-8")
        with (
            patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
            patch.object(release_local, "_validate_bundle_content", return_value={}),
        ):
            with self.assertRaises(release_local.LocalReleaseError):
                release_local.verify_approval(
                    self.bundle,
                    SOURCE_SHA,
                    self.repo_root,
                    main_sha_getter=lambda: SOURCE_SHA,
                )

    def test_invalid_bundle_content_blocks_prepare_and_verify(self):
        with patch.object(release_local, "assert_authority", return_value=SOURCE_SHA):
            with self.assertRaises(release_local.LocalReleaseError):
                release_local.prepare_approval(
                    self.bundle, SOURCE_SHA, self.repo_root, main_sha_getter=lambda: SOURCE_SHA
                )
            with self.assertRaises(release_local.LocalReleaseError):
                release_local.verify_approval(
                    self.bundle, SOURCE_SHA, self.repo_root, main_sha_getter=lambda: SOURCE_SHA
                )

    def test_arbitrary_automated_command_cannot_replace_fixed_gate_contract(self):
        receipt_path = self.bundle / "evidence" / "receipts" / "linux-ci.json"
        receipt = release_local._load_json(receipt_path, "receipt")
        receipt["commands"][0]["argv"] = ["fixture", "gate"]
        receipt_path.write_bytes(release_local._canonical_json(receipt))
        with self.assertRaisesRegex(release_local.LocalReleaseError, "fixed gate"):
            release_local.validate_receipts(
                self.bundle,
                self.context,
                self.inventories,
                required_gates=frozenset({"linux-ci"}),
            )

    def test_windows_acceptance_requires_real_version_formats_and_executables(self):
        acceptance, execution, commands = external_acceptance(
            "windows-native", self.inventories
        )
        receipt = {"acceptance": acceptance, "commands": commands}
        release_local._validate_acceptance(receipt, "windows-native", require_passed=False)
        acceptance["componentVersions"]["powershell_5_1"] = "PowerShell 5.1"
        with self.assertRaisesRegex(release_local.LocalReleaseError, "Windows native"):
            release_local._validate_acceptance(
                receipt, "windows-native", require_passed=False
            )

    def test_real_cluster_report_import_preserves_commands_and_runtime_evidence(self):
        gate = "real-cluster"
        receipt_path = self.bundle / "evidence" / "receipts" / f"{gate}.json"
        receipt = release_local._load_json(receipt_path, "fixture receipt")
        acceptance = receipt["acceptance"]
        report = external_report(receipt)
        report_path = self.root / "cluster-host-report.json"
        report_path.write_bytes(release_local._canonical_json(report))
        receipt_path.unlink()
        with patch.object(release_local, "_clean_head", return_value=SOURCE_SHA):
            release_local.import_acceptance(
                self.bundle, report_path, self.bundle / "evidence", self.repo_root
            )
        imported = release_local.validate_receipts(
            self.bundle, self.context, self.inventories,
            required_gates=frozenset({gate}),
        )[gate]
        self.assertEqual(imported["commands"], receipt["commands"])
        self.assertEqual(imported["acceptance"], acceptance)
        self.assertEqual(imported["result"], "passed")

    def test_windows_report_import_is_rejected_without_creating_a_receipt(self):
        receipt = release_local._load_json(
            self.bundle / "evidence" / "receipts" / "real-cluster.json",
            "fixture receipt",
        )
        report = external_report(receipt)
        report["gate"] = "windows-native"
        report_path = self.root / "windows-host-report.json"
        report_path.write_bytes(release_local._canonical_json(report))
        receipt_path = self.bundle / "evidence" / "receipts" / "windows-native.json"
        import_path = self.bundle / "evidence" / "imports" / "windows-native"

        with (
            patch.object(release_local, "_clean_head", return_value=SOURCE_SHA),
            self.assertRaisesRegex(release_local.LocalReleaseError, "unapproved gate"),
        ):
            release_local.import_acceptance(
                self.bundle, report_path, self.bundle / "evidence", self.repo_root
            )

        self.assertFalse(receipt_path.exists())
        self.assertFalse(import_path.exists())

    @unittest.skipUnless(hasattr(os, "mkfifo"), "FIFO tests require POSIX")
    def test_fifo_path_is_rejected_without_blocking(self):
        fifo = self.root / "nonregular.fifo"
        os.mkfifo(fifo)
        with self.assertRaisesRegex(release_local.LocalReleaseError, "regular file"):
            release_local._open_regular(fifo)

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
        with (
            patch.object(release_local, "assert_authority", return_value=SOURCE_SHA),
            patch.object(release_local, "_validate_bundle_content", return_value={}),
        ):
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
            "deployedImageDigests": {},
            "executables": {},
            "toolVersions": {"test": "test-only"},
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
