#!/usr/bin/env python3
"""Prepare and verify a locally built, offline-approved KubeAtlas release."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path, PurePosixPath

import release_approval
import release_artifacts
import release_chart_package
import release_policy
import security_dispositions
import security_scan
import validate_oci_archive


REPOSITORY = "maomada/KubeAtlas"
ROOT = Path(__file__).resolve().parents[1]
VERSION = "0.1.0"
PUBLICATION_PROFILE = "local-linux"
MANIFEST_SCHEMA = "kubeatlas-local-release/v1"
RECEIPT_SCHEMA = "kubeatlas-local-receipt/v1"
ACCEPTANCE_SCHEMA = "kubeatlas-local-acceptance/v1"
MAX_VALIDITY = timedelta(days=7)
MAX_RECEIPT_AGE = timedelta(days=7)
TIMESTAMP_FORMAT = "%Y-%m-%dT%H:%M:%SZ"
SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
SHA256_PATTERN = re.compile(r"[0-9a-f]{64}")
MAX_JSON_FILE_BYTES = 64 * 1024 * 1024

AUTOMATED_GATES = (
    "linux-ci",
    "frontend",
    "settings-e2e",
    "helm",
    "release-security",
    "posix-installers",
    "semgrep-self-test",
    "semgrep-full",
    "package-oci",
)
EXTERNAL_GATES = ("real-cluster",) + tuple(
    f"package-install-{component}-linux-{arch}-{package}"
    for component in ("kubeatlas", "kubeatlas-portal")
    for arch in ("amd64", "arm64")
    for package in ("deb", "rpm")
)
REQUIRED_GATES = frozenset(("build", *AUTOMATED_GATES, *EXTERNAL_GATES))
EXPECTED_REQUIRED_GATES = frozenset(
    {
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
        *(f"package-install-{component}-linux-{arch}-{package}"
          for component in ("kubeatlas", "kubeatlas-portal")
          for arch in ("amd64", "arm64")
          for package in ("deb", "rpm")),
    }
)
ACCEPTANCE_SCENARIOS = {
    "windows-native": (
        "zip_hash_safe_extract_version_help",
        "powershell_5_1_installer_fixtures",
        "powershell_7_installer_fixtures",
        "helm_refresh_windows",
    ),
    "real-cluster": (
        "main_registration",
        "main_heartbeat",
        "main_persistence",
        "main_expiry",
        "portal_registration",
        "portal_heartbeat",
        "portal_persistence",
        "portal_expiry",
        "helm_main_installation",
        "helm_portal_flow",
        "worker_csp_security",
        "readonly_boundaries",
        "main_readonly_api",
        "main_readonly_mcp",
    ),
}
for _gate in EXTERNAL_GATES:
    if _gate.startswith("package-install-"):
        ACCEPTANCE_SCENARIOS[_gate] = ("install", "version", "uninstall", "no_autostart")
if REQUIRED_GATES != EXPECTED_REQUIRED_GATES or set(ACCEPTANCE_SCENARIOS) != set(EXTERNAL_GATES) | {"windows-native"}:
    raise RuntimeError("local release gate definitions are incomplete")

ALLOWED_EVIDENCE_DIRS = {"logs", "receipts", "reports", "semgrep", "imports"}
SECRET_MARKERS = (
    re.compile(rb"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
    re.compile(rb"(?i)authorization\s*[:=]\s*bearer\s+\S+"),
    re.compile(rb"\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b"),
)


class LocalReleaseError(ValueError):
    pass


def _now() -> datetime:
    return datetime.now(timezone.utc).replace(microsecond=0)


def _format_time(value: datetime) -> str:
    return value.astimezone(timezone.utc).replace(microsecond=0).strftime(TIMESTAMP_FORMAT)


def _parse_time(value: object, name: str) -> datetime:
    return release_approval._timestamp(value, name)


def _canonical_json(value: object) -> bytes:
    try:
        return json.dumps(value, sort_keys=True, indent=2, allow_nan=False).encode("utf-8") + b"\n"
    except (TypeError, ValueError) as error:
        raise LocalReleaseError("release data cannot be serialized safely") from error


def _load_json(path: Path, name: str = "JSON file") -> dict:
    try:
        descriptor, file_stat = _open_regular(path)
        if file_stat.st_size > MAX_JSON_FILE_BYTES:
            os.close(descriptor)
            raise LocalReleaseError(f"{name} exceeds its size limit")
        with os.fdopen(descriptor, "rb") as source:
            data = source.read(MAX_JSON_FILE_BYTES + 1)
    except LocalReleaseError:
        raise
    except OSError as error:
        raise LocalReleaseError(f"{name} cannot be read safely") from error
    try:
        def object_pairs(pairs):
            result = {}
            for key, value in pairs:
                if key in result:
                    raise ValueError(f"duplicate JSON key: {key}")
                result[key] = value
            return result

        value = json.loads(
            data.decode("utf-8"),
            object_pairs_hook=object_pairs,
            parse_constant=lambda constant: (_ for _ in ()).throw(
                ValueError(f"non-finite JSON constant: {constant}")
            ),
        )
    except (UnicodeDecodeError, json.JSONDecodeError, ValueError) as error:
        raise LocalReleaseError(f"{name} is not strict JSON") from error
    if not isinstance(value, dict):
        raise LocalReleaseError(f"{name} must be a JSON object")
    return value


def _assert_no_symlink_ancestors(path: Path) -> None:
    absolute = Path(os.path.abspath(path))
    for candidate in reversed((absolute, *absolute.parents)):
        try:
            mode = candidate.lstat().st_mode
        except FileNotFoundError:
            continue
        except OSError as error:
            raise LocalReleaseError("path cannot be inspected safely") from error
        if stat.S_ISLNK(mode):
            raise LocalReleaseError(f"symlink path component is not allowed: {candidate.name}")


def _open_regular(path: Path) -> tuple[int, os.stat_result]:
    _assert_no_symlink_ancestors(path)
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0)
    try:
        descriptor = os.open(path, flags)
        file_stat = os.fstat(descriptor)
    except OSError as error:
        raise LocalReleaseError(f"cannot safely open file: {path.name}") from error
    if not stat.S_ISREG(file_stat.st_mode):
        os.close(descriptor)
        raise LocalReleaseError(f"not a regular file: {path.name}")
    return descriptor, file_stat


def _hash_file(path: Path) -> tuple[int, str]:
    descriptor, file_stat = _open_regular(path)
    digest = hashlib.sha256()
    try:
        with os.fdopen(descriptor, "rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise LocalReleaseError(f"cannot hash file: {path.name}") from error
    return file_stat.st_size, digest.hexdigest()


def _read_regular(path: Path, max_size: int | None = None) -> bytes:
    descriptor, file_stat = _open_regular(path)
    if max_size is not None and file_stat.st_size > max_size:
        os.close(descriptor)
        raise LocalReleaseError(f"file exceeds its size limit: {path.name}")
    try:
        with os.fdopen(descriptor, "rb") as source:
            data = source.read((max_size + 1) if max_size is not None else file_stat.st_size + 1)
    except OSError as error:
        raise LocalReleaseError(f"cannot safely read file: {path.name}") from error
    if len(data) != file_stat.st_size:
        raise LocalReleaseError(f"file changed while being read: {path.name}")
    return data


def _safe_relative(value: object) -> str:
    if not isinstance(value, str):
        raise LocalReleaseError("evidence path must be a string")
    release_approval._safe_path(value)
    if value == "build-context.json":
        return value
    if PurePosixPath(value).parts[0] not in ALLOWED_EVIDENCE_DIRS:
        raise LocalReleaseError("evidence path is outside the approved evidence directories")
    return value


def _exclusive_write(path: Path, data: bytes, mode: int = 0o600) -> None:
    _assert_no_symlink_ancestors(path.parent)
    if not path.parent.is_dir():
        raise LocalReleaseError(f"output directory is missing: {path.parent.name}")
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags, mode)
        with os.fdopen(descriptor, "wb") as destination:
            destination.write(data)
            destination.flush()
            os.fsync(destination.fileno())
    except OSError as error:
        raise LocalReleaseError(f"refusing to overwrite or create output: {path.name}") from error


def _write_receipt_file(bundle: Path, name: str, value: dict) -> Path:
    path = bundle / "evidence" / "receipts" / f"{name}.json"
    _exclusive_write(path, _canonical_json(value))
    return path


def _git_output(repo_root: Path, *args: str) -> str:
    try:
        return subprocess.check_output(["git", "-C", str(repo_root), *args], text=True).strip()
    except (OSError, subprocess.CalledProcessError) as error:
        raise LocalReleaseError("Git source authority could not be verified") from error


def _clean_head(repo_root: Path, requested_sha: str) -> str:
    if not SHA_PATTERN.fullmatch(requested_sha):
        raise LocalReleaseError("source SHA must be a full lowercase 40-character SHA")
    _assert_no_symlink_ancestors(repo_root)
    try:
        head = _git_output(repo_root, "rev-parse", "HEAD")
        top = Path(_git_output(repo_root, "rev-parse", "--show-toplevel")).resolve()
        status = subprocess.check_output(
            ["git", "-C", str(repo_root), "status", "--porcelain=v1", "--untracked-files=all"],
            text=True,
        )
    except (OSError, subprocess.CalledProcessError) as error:
        raise LocalReleaseError("Git source authority could not be verified") from error
    if top != repo_root.resolve() or head != requested_sha:
        raise LocalReleaseError("local source and tooling HEAD must equal the requested SHA")
    if status:
        raise LocalReleaseError("source and tooling checkout must be clean")
    return head


def query_main_sha(environment: dict[str, str] | None = None) -> str:
    environment = os.environ if environment is None else environment
    if not (environment.get("GH_TOKEN") or environment.get("GITHUB_TOKEN")):
        raise LocalReleaseError("GH_TOKEN or GITHUB_TOKEN is required for a fresh main check")
    try:
        result = subprocess.run(
            ["gh", "api", f"repos/{REPOSITORY}/commits/main"],
            check=True,
            capture_output=True,
            text=True,
            env=dict(environment),
        )
    except (OSError, subprocess.CalledProcessError) as error:
        raise LocalReleaseError("fresh read-only main API query failed") from error
    try:
        response = release_approval._load_json(result.stdout.encode("utf-8"))
    except (UnicodeEncodeError, LocalReleaseError, release_approval.ApprovalError) as error:
        raise LocalReleaseError("main API response is malformed") from error
    sha = response.get("sha")
    if not isinstance(sha, str) or not SHA_PATTERN.fullmatch(sha):
        raise LocalReleaseError("main API response does not contain a full commit SHA")
    return sha


def assert_authority(
    requested_sha: str,
    repo_root: Path = ROOT,
    require_main: bool = False,
    main_sha_getter=None,
) -> str:
    head = _clean_head(repo_root, requested_sha)
    try:
        release_policy.validate_inputs(REPOSITORY, "refs/heads/main", requested_sha, VERSION)
        release_approval._trusted_key(repo_root)
    except (ValueError, release_approval.ApprovalError) as error:
        raise LocalReleaseError("trusted source, version, or public key is invalid") from error
    if require_main:
        current_main = (main_sha_getter or query_main_sha)()
        if current_main != requested_sha or current_main != head:
            raise LocalReleaseError("source and tooling SHA are not the fresh main HEAD")
    return head


def _bundle_path(path: Path, repo_root: Path = ROOT) -> Path:
    path = Path(os.path.abspath(path))
    _assert_no_symlink_ancestors(path)
    try:
        path.relative_to(repo_root.resolve())
    except ValueError:
        is_inside_repo = False
    else:
        is_inside_repo = True
    if is_inside_repo:
        raise LocalReleaseError("release bundle must be outside the source checkout")
    try:
        repo_root.resolve().relative_to(path.resolve())
    except ValueError:
        return path
    raise LocalReleaseError("release bundle must not contain the source checkout")


def _context_path(bundle: Path) -> Path:
    return bundle / "evidence" / "build-context.json"


def _load_context(bundle: Path) -> dict:
    context = _load_json(_context_path(bundle), "build context")
    if set(context) != {
        "schema",
        "repository",
        "version",
        "candidateOnly",
        "sourceSha",
        "toolingSha",
        "buildId",
        "createdAt",
        "completedAt",
        "status",
    }:
        raise LocalReleaseError("invalid local build context schema")
    if (
        context["schema"] != "kubeatlas-local-build/v1"
        or context["repository"] != REPOSITORY
        or context["version"] != VERSION
        or context["candidateOnly"] is not True
        or context["sourceSha"] != context["toolingSha"]
        or not isinstance(context["sourceSha"], str)
        or not SHA_PATTERN.fullmatch(context["sourceSha"])
        or context["status"] != "built"
    ):
        raise LocalReleaseError("local build context does not describe a completed trusted build")
    try:
        uuid.UUID(context["buildId"])
        created = _parse_time(context["createdAt"], "createdAt")
        completed = _parse_time(context["completedAt"], "completedAt")
    except (ValueError, TypeError, KeyError) as error:
        raise LocalReleaseError("local build context is malformed") from error
    if completed < created:
        raise LocalReleaseError("local build context timestamps are invalid")
    return context


def _ensure_bundle_layout(bundle: Path, signed: bool = False) -> None:
    expected = {"binaries", "ghcr", "evidence"}
    if (bundle / "approval.json").exists() or (bundle / "approval.json").is_symlink():
        expected.add("approval.json")
    if (bundle / "approval.sig").exists() or (bundle / "approval.sig").is_symlink():
        expected.add("approval.sig")
    if signed:
        expected.update(("approval.json", "approval.sig"))
    try:
        actual = {entry.name for entry in bundle.iterdir()}
    except OSError as error:
        raise LocalReleaseError("release bundle cannot be read") from error
    if actual != expected:
        raise LocalReleaseError("release bundle contains missing or unapproved top-level paths")
    for name in ("binaries", "ghcr", "evidence"):
        path = bundle / name
        _assert_no_symlink_ancestors(path)
        if path.is_symlink() or not path.is_dir():
            raise LocalReleaseError(f"release bundle directory is unsafe: {name}")


def expected_artifact_paths(channel: str, version: str = VERSION) -> list[str]:
    if channel == "binaries":
        return release_artifacts.local_publication_names(version, PUBLICATION_PROFILE)
    return release_approval.expected_artifact_paths(channel, version)


def _binary_inventory(root: Path) -> list[dict[str, object]]:
    _assert_no_symlink_ancestors(root)
    if not root.is_dir():
        raise LocalReleaseError("binary artifact root must be a real directory")
    paths = sorted(root.iterdir())
    if [path.name for path in paths] != expected_artifact_paths("binaries"):
        raise LocalReleaseError("binary artifact set does not match the Linux-only publication profile")
    inventory = []
    for path in paths:
        size, digest = _hash_file(path)
        inventory.append({"path": path.name, "size": size, "sha256": digest})
    return inventory


def artifact_inventories(bundle: Path) -> dict[str, list[dict[str, object]]]:
    _ensure_bundle_layout(bundle)
    return {
        "binaries": _binary_inventory(bundle / "binaries"),
        "ghcr": release_approval._inventory(bundle / "ghcr", "ghcr", VERSION),
    }


def artifact_inventory_digest(inventories: dict[str, list[dict[str, object]]]) -> str:
    return hashlib.sha256(_canonical_json(inventories)).hexdigest()


def evidence_inventory(bundle: Path) -> list[dict[str, object]]:
    root = bundle / "evidence"
    _assert_no_symlink_ancestors(root)
    if root.is_symlink() or not root.is_dir():
        raise LocalReleaseError("evidence directory is unsafe")
    result = []
    pending = [root]
    while pending:
        directory = pending.pop()
        for entry in sorted(os.scandir(directory), key=lambda item: item.name):
            path = Path(entry.path)
            mode = entry.stat(follow_symlinks=False).st_mode
            if stat.S_ISLNK(mode):
                raise LocalReleaseError("evidence must not contain symlinks")
            if stat.S_ISDIR(mode):
                pending.append(path)
                continue
            if not stat.S_ISREG(mode):
                raise LocalReleaseError("evidence must contain regular files only")
            relative = path.relative_to(root).as_posix()
            _safe_relative(relative)
            size, digest = _hash_file(path)
            if relative.startswith("logs/") and size == 0:
                raise LocalReleaseError("evidence logs must be nonempty")
            result.append({"path": relative, "size": size, "sha256": digest})
    if not result:
        raise LocalReleaseError("release evidence inventory is empty")
    return sorted(result, key=lambda item: item["path"])


def _evidence_record(bundle: Path, relative: str, kind: str) -> dict[str, object]:
    relative = _safe_relative(relative)
    path = bundle / "evidence" / relative
    size, digest = _hash_file(path)
    if kind == "log" and size == 0:
        raise LocalReleaseError("evidence logs must be nonempty")
    return {"path": relative, "size": size, "sha256": digest, "kind": kind}


def _validate_evidence_files(bundle: Path) -> list[dict[str, object]]:
    records = evidence_inventory(bundle)
    for item in records:
        path = bundle / "evidence" / item["path"]
        descriptor, _ = _open_regular(path)
        try:
            with os.fdopen(descriptor, "rb") as source:
                content = source.read()
        except OSError as error:
            raise LocalReleaseError("evidence cannot be inspected for secrets") from error
        if any(pattern.search(content) for pattern in SECRET_MARKERS):
            raise LocalReleaseError("evidence contains a credential or private-key marker")
    return records


def _receipt_commands_valid(
    commands: object, allow_failures: bool = False, allow_empty: bool = False
) -> bool:
    if not isinstance(commands, list) or not commands and not allow_empty:
        return False
    for command in commands:
        if (
            not isinstance(command, dict)
            or set(command) != {"name", "argv", "cwd", "exitStatus"}
            or not isinstance(command["name"], str)
            or not command["name"]
            or not isinstance(command["cwd"], str)
            or not isinstance(command["argv"], list)
            or not command["argv"]
            or any(not isinstance(arg, str) for arg in command["argv"])
            or type(command["exitStatus"]) is not int
            or command["exitStatus"] < 0
            or command["exitStatus"] > 255
            or not allow_failures and command["exitStatus"] != 0
        ):
            return False
    return True


def _validate_external_commands(receipt: dict, gate: str) -> None:
    names = {item["name"] for item in receipt["commands"]}
    acceptance = receipt.get("acceptance", receipt)
    scenario_statuses = {
        item["name"]: item["status"] for item in acceptance["scenarios"]
    }
    if gate == "windows-native":
        scenario_commands = {
            "zip_hash_safe_extract_version_help": "windows_zip_smoke",
            "powershell_5_1_installer_fixtures": "powershell_5_1_installer_fixtures",
            "powershell_7_installer_fixtures": "powershell_7_installer_fixtures",
            "helm_refresh_windows": "helm_refresh_windows",
        }
        required = {
            command
            for scenario, command in scenario_commands.items()
            if scenario_statuses[scenario] != "untested"
        }
        args = [arg for item in receipt["commands"] for arg in item["argv"]]
        if (
            not required.issubset(names)
            or (
                "windows_zip_smoke" in required
                and not any(
                    "release_windows_smoke.py" in arg
                    for item in receipt["commands"]
                    if item["name"] == "windows_zip_smoke"
                    for arg in item["argv"]
                )
            )
            or (
                "windows_zip_smoke" in required
                and not any(
                    "--version" in item["argv"]
                    and VERSION in item["argv"]
                    and "--archive" in item["argv"]
                    and "--manifest" in item["argv"]
                    for item in receipt["commands"]
                    if item["name"] == "windows_zip_smoke"
                )
            )
            or (
                "powershell_5_1_installer_fixtures" in required
                and not any(
                    "test-installers.ps1" in arg
                    for item in receipt["commands"]
                    if item["name"] == "powershell_5_1_installer_fixtures"
                    for arg in item["argv"]
                )
            )
            or (
                "powershell_7_installer_fixtures" in required
                and not any(
                    "test-installers.ps1" in arg
                    for item in receipt["commands"]
                    if item["name"] == "powershell_7_installer_fixtures"
                    for arg in item["argv"]
                )
            )
            or (
                "helm_refresh_windows" in required
                and not any("repository_cache_windows_test.go" in arg for arg in args)
            )
            or (
                "helm_refresh_windows" in required
                and not any(
                    "repository_cache_windows.go" in arg
                    for item in receipt["commands"]
                    if item["name"] == "helm_refresh_windows"
                    for arg in item["argv"]
                )
            )
        ):
            raise LocalReleaseError("Windows acceptance receipt omits a required command")
    elif gate.startswith("package-install-"):
        required = {
            name
            for name, status in scenario_statuses.items()
            if status != "untested"
        }
        if not required.issubset(names):
            raise LocalReleaseError(f"package installation receipt omits required commands: {gate}")
    elif gate == "real-cluster":
        required = {
            name
            for name, status in scenario_statuses.items()
            if status != "untested"
        }
        if not required.issubset(names):
            raise LocalReleaseError("real-cluster receipt omits an executed scenario command")


def _validate_acceptance(receipt: dict, gate: str, require_passed: bool = True) -> None:
    acceptance = receipt.get("acceptance")
    if not isinstance(acceptance, dict) or set(acceptance) != {
        "context",
        "componentVersions",
        "limitations",
        "scenarios",
        "execution",
        "artifactHashes",
        "deployedImageDigests",
        "executables",
    }:
        raise LocalReleaseError(f"operator-attested acceptance receipt is incomplete: {gate}")
    if (
        not isinstance(acceptance["context"], str)
        or not acceptance["context"].strip()
        or not isinstance(acceptance["componentVersions"], dict)
        or not acceptance["componentVersions"]
        or any(
            not isinstance(name, str)
            or not isinstance(version, str)
            or not version.strip()
            for name, version in acceptance["componentVersions"].items()
        )
        or not isinstance(acceptance["limitations"], list)
        or any(not isinstance(item, str) for item in acceptance["limitations"])
        or not isinstance(acceptance["artifactHashes"], dict)
        or not isinstance(acceptance["deployedImageDigests"], dict)
        or not isinstance(acceptance["executables"], dict)
    ):
        raise LocalReleaseError(f"operator-attested acceptance context is incomplete: {gate}")
    scenarios = acceptance["scenarios"]
    required = ACCEPTANCE_SCENARIOS[gate]
    if not isinstance(scenarios, list):
        raise LocalReleaseError(f"acceptance scenarios are invalid: {gate}")
    by_name = {}
    for item in scenarios:
        if not isinstance(item, dict) or set(item) != {"name", "status", "details"}:
            raise LocalReleaseError(f"acceptance scenario record is invalid: {gate}")
        name = item["name"]
        if (
            name not in required
            or name in by_name
            or item["status"] not in ("passed", "untested", "failed")
            or not isinstance(item["details"], str)
            or not item["details"].strip()
        ):
            raise LocalReleaseError(f"acceptance scenario is invalid: {gate}")
        by_name[name] = item
    if set(by_name) != set(required) or require_passed and any(
        by_name[name]["status"] != "passed" for name in required
    ):
        raise LocalReleaseError(f"required acceptance scenarios are untested or failed: {gate}")
    execution = acceptance["execution"]
    if not isinstance(execution, dict) or set(execution) != {
        "system",
        "machine",
        "emulation",
        "isolation",
    }:
        raise LocalReleaseError(f"acceptance execution platform is invalid: {gate}")
    system = execution["system"]
    machine = execution["machine"]
    if not isinstance(system, str) or not isinstance(machine, str) or not machine:
        raise LocalReleaseError(f"acceptance execution platform is invalid: {gate}")
    if gate == "windows-native":
        powershell_51 = acceptance["componentVersions"].get("powershell_5_1", "")
        powershell_7 = acceptance["componentVersions"].get("powershell_7", "")
        executables = acceptance["executables"]
        if (
            system != "Windows"
            or machine.upper() not in {"AMD64", "X86_64"}
            or execution["emulation"] is not False
            or acceptance["componentVersions"].get("kubeatlas") != VERSION
            or not re.fullmatch(r"5\.1\.\d+(?:\.\d+)?", powershell_51)
            or not re.fullmatch(r"7\.\d+(?:\.\d+)?", powershell_7)
            or not acceptance["componentVersions"].get("helm")
            or not isinstance(executables.get("powershell_5_1"), str)
            or not executables["powershell_5_1"].lower().endswith(
                r"\powershell.exe"
            )
            or not isinstance(executables.get("powershell_7"), str)
            or not executables["powershell_7"].lower().endswith(r"\pwsh.exe")
            or not any(
                item["name"] == "powershell_5_1_installer_fixtures"
                and item["argv"][0].lower() == executables["powershell_5_1"].lower()
                for item in receipt["commands"]
            )
            or not any(
                item["name"] == "powershell_7_installer_fixtures"
                and item["argv"][0].lower() == executables["powershell_7"].lower()
                for item in receipt["commands"]
            )
        ):
            raise LocalReleaseError("Windows native acceptance requires Windows AMD64 execution")
    elif gate == "real-cluster":
        if (
            system != "Linux"
            or execution["emulation"] is not False
            or acceptance["componentVersions"].get("kubeatlas") != VERSION
            or acceptance["componentVersions"].get("kubeatlas-portal") != VERSION
            or not acceptance["componentVersions"].get("kubernetes")
        ):
            raise LocalReleaseError("real-cluster acceptance must run on a native Linux host")
    else:
        remainder = gate.removeprefix("package-install-")
        component = next(
            name for name in ("kubeatlas-portal", "kubeatlas") if remainder.startswith(name + "-")
        )
        _, arch, _package = remainder.removeprefix(component + "-").split("-")
        expected_machine = {"amd64": {"x86_64", "amd64"}, "arm64": {"aarch64", "arm64"}}[arch]
        if (
            system != "Linux"
            or execution["emulation"] not in ("native", "emulated")
            or execution["isolation"] not in ("container", "vm")
            or acceptance["componentVersions"].get(component) != VERSION
            or not acceptance["componentVersions"].get("distribution")
            or (execution["emulation"] == "native" and machine.lower() not in expected_machine)
        ):
            raise LocalReleaseError(f"package acceptance platform or version mismatch: {gate}")


def _validate_external_artifact_hashes(
    receipt: dict,
    gate: str,
    inventories: dict[str, list[dict[str, object]]],
    bundle: Path,
) -> None:
    if gate == "real-cluster":
        ghcr_entries = {entry["path"]: entry["sha256"] for entry in inventories["ghcr"]}
        expected = {
            path: ghcr_entries[path]
            for path in release_approval.expected_artifact_paths("ghcr", VERSION)
            if path.endswith((".oci.tar", ".tgz"))
        }
        acceptance = receipt["acceptance"]
        if acceptance["artifactHashes"] != expected:
            raise LocalReleaseError("cluster acceptance must bind approved image and chart archives")
        deployed = acceptance["deployedImageDigests"]
        if set(deployed) != {"kubeatlas", "kubeatlas-portal"}:
            raise LocalReleaseError("cluster acceptance must report both deployed image digests")
        for component in ("kubeatlas", "kubeatlas-portal"):
            image_metadata_path = (
                bundle
                / "ghcr"
                / "images"
                / f"release-image-{component}"
                / f"{component}.metadata.json"
            )
            metadata = _load_json(image_metadata_path, "image metadata")
            if deployed.get(component) != metadata.get("buildDigest"):
                raise LocalReleaseError("observed cluster image digest differs from the approved build")
        return
    binary_entries = {entry["path"]: entry["sha256"] for entry in inventories["binaries"]}
    if gate == "windows-native":
        windows = [
            path
            for path in release_approval.expected_artifact_paths("binaries", VERSION)
            if path.endswith(".zip") and "portal" not in path
        ]
        expected = windows[0] if len(windows) == 1 else ""
    else:
        remainder = gate.removeprefix("package-install-")
        component = next(
            name for name in ("kubeatlas-portal", "kubeatlas") if remainder.startswith(name + "-")
        )
        _, arch, package = remainder.removeprefix(component + "-").split("-")
        expected = f"{component}_{VERSION}_linux_{arch}.{package}"
    artifact_hashes = receipt["acceptance"]["artifactHashes"]
    if (
        artifact_hashes != {expected: binary_entries.get(expected)}
        or not SHA256_PATTERN.fullmatch(artifact_hashes.get(expected, ""))
    ):
        raise LocalReleaseError(f"acceptance does not bind the exact binary artifact: {gate}")


def validate_receipts(
    bundle: Path,
    context: dict,
    inventories: dict[str, list[dict[str, object]]],
    now: datetime | None = None,
    required_gates: frozenset[str] = REQUIRED_GATES,
) -> dict[str, dict]:
    now = (now or _now()).astimezone(timezone.utc).replace(microsecond=0)
    receipts_dir = bundle / "evidence" / "receipts"
    actual = set()
    if receipts_dir.exists():
        if receipts_dir.is_symlink() or not receipts_dir.is_dir():
            raise LocalReleaseError("receipt directory is unsafe")
        actual = {entry.name.removesuffix(".json") for entry in receipts_dir.iterdir()}
    if actual - REQUIRED_GATES:
        raise LocalReleaseError("release bundle contains an unknown gate receipt")
    if not required_gates.issubset(REQUIRED_GATES):
        raise LocalReleaseError("receipt validation requested an unknown gate")
    if not required_gates.issubset(actual):
        missing = sorted(required_gates - actual)
        raise LocalReleaseError(f"required gate receipts are missing: {', '.join(missing)}")
    digest = artifact_inventory_digest(inventories)
    receipts = {}
    for gate in sorted(required_gates):
        path = receipts_dir / f"{gate}.json"
        receipt = _load_json(path, f"{gate} receipt")
        expected_keys = {
            "schema",
            "origin",
            "repository",
            "gate",
            "version",
            "sourceSha",
            "toolingSha",
            "buildId",
            "artifactInventorySha256",
            "executionPlatform",
            "issuedAt",
            "completedAt",
            "commands",
            "toolVersions",
            "exitStatus",
            "result",
            "reports",
        }
        if gate in ACCEPTANCE_SCENARIOS:
            expected_keys.add("acceptance")
        if not isinstance(receipt, dict) or set(receipt) != expected_keys:
            raise LocalReleaseError(f"invalid gate receipt schema: {gate}")
        expected_origin = "operator-attested" if gate in EXTERNAL_GATES else "local"
        if (
            receipt["schema"] != RECEIPT_SCHEMA
            or receipt["origin"] != expected_origin
            or receipt["repository"] != REPOSITORY
            or receipt["gate"] != gate
            or receipt["version"] != VERSION
            or receipt["sourceSha"] != context["sourceSha"]
            or receipt["toolingSha"] != context["toolingSha"]
            or receipt["buildId"] != context["buildId"]
            or receipt["artifactInventorySha256"] != digest
            or receipt["exitStatus"] != 0
            or receipt["result"] != "passed"
            or not _receipt_commands_valid(receipt["commands"])
            or not isinstance(receipt["toolVersions"], dict)
            or not receipt["toolVersions"]
            or any(
                not isinstance(name, str)
                or not name
                or not isinstance(version, str)
                or not version.strip()
                for name, version in receipt["toolVersions"].items()
            )
            or not isinstance(receipt["executionPlatform"], dict)
        ):
            raise LocalReleaseError(f"gate receipt does not bind to this build: {gate}")
        issued = _parse_time(receipt["issuedAt"], f"{gate} issuedAt")
        completed = _parse_time(receipt["completedAt"], f"{gate} completedAt")
        build_completed = _parse_time(context["completedAt"], "build completedAt")
        if (
            issued > completed
            or completed > now
            or issued < build_completed
            or now - issued > MAX_RECEIPT_AGE
        ):
            raise LocalReleaseError(f"gate receipt is stale or has invalid timestamps: {gate}")
        reports = receipt["reports"]
        if not isinstance(reports, list) or not reports:
            raise LocalReleaseError(f"gate receipt has no report evidence: {gate}")
        kinds = set()
        for report in reports:
            if not isinstance(report, dict) or set(report) != {"path", "size", "sha256", "kind"}:
                raise LocalReleaseError(f"gate report record is invalid: {gate}")
            relative = _safe_relative(report["path"])
            if (
                type(report["size"]) is not int
                or report["size"] < 0
                or report["kind"] not in ("log", "report")
                or not isinstance(report["sha256"], str)
                or not SHA256_PATTERN.fullmatch(report["sha256"])
            ):
                raise LocalReleaseError(f"gate report record is invalid: {gate}")
            size, checksum = _hash_file(bundle / "evidence" / relative)
            if size != report["size"] or checksum != report["sha256"]:
                raise LocalReleaseError(f"gate evidence has changed: {gate}")
            if report["kind"] == "log":
                if size == 0:
                    raise LocalReleaseError(f"gate log is empty: {gate}")
                kinds.add("log")
        if "log" not in kinds:
            raise LocalReleaseError(f"gate receipt is missing a nonempty log: {gate}")
        if gate in AUTOMATED_GATES:
            _validate_automated_receipt(bundle, receipt, gate)
        elif gate == "build":
            _validate_build_report(bundle, receipt, context)
        if gate in ACCEPTANCE_SCENARIOS:
            _validate_acceptance(receipt, gate)
            _validate_external_commands(receipt, gate)
            _validate_external_artifact_hashes(receipt, gate, inventories, bundle)
        receipts[gate] = receipt
    return receipts


def _receipt(
    gate: str,
    context: dict,
    inventories: dict[str, list[dict[str, object]]],
    commands: list[dict],
    reports: list[dict],
    execution_platform: dict | None = None,
    acceptance: dict | None = None,
    tool_versions: dict[str, str] | None = None,
) -> dict:
    receipt = {
        "schema": RECEIPT_SCHEMA,
        "origin": "operator-attested" if gate in EXTERNAL_GATES else "local",
        "repository": REPOSITORY,
        "gate": gate,
        "version": VERSION,
        "sourceSha": context["sourceSha"],
        "toolingSha": context["toolingSha"],
        "buildId": context["buildId"],
        "artifactInventorySha256": artifact_inventory_digest(inventories),
        "executionPlatform": execution_platform
        or {"system": platform.system(), "machine": platform.machine()},
        "issuedAt": _format_time(_now()),
        "completedAt": _format_time(_now()),
        "commands": commands,
        "toolVersions": {"python": sys.version.split()[0], **(tool_versions or {})},
        "exitStatus": 0,
        "result": "passed",
        "reports": reports,
    }
    if acceptance is not None:
        receipt["acceptance"] = acceptance
    return receipt


def _command_environment(overrides: dict[str, str] | None = None) -> dict[str, str]:
    environment = {
        key: value
        for key, value in os.environ.items()
        if not re.search(r"(TOKEN|PASSWORD|SECRET|CREDENTIAL|KUBECONFIG)", key, re.IGNORECASE)
    }
    environment["PYTHONDONTWRITEBYTECODE"] = "1"
    if overrides:
        environment.update(overrides)
    return environment


def _display_argv(argv: list[str], bundle: Path, repo_root: Path) -> list[str]:
    replacements = ((str(repo_root), "$REPO"), (str(bundle), "$BUNDLE"))
    output = []
    for argument in argv:
        value = argument
        for source, target in replacements:
            value = value.replace(source, target)
        output.append(value)
    return output


def _run_commands(
    gate: str,
    specs: list[dict],
    log_path: Path,
    bundle: Path,
    repo_root: Path = ROOT,
    environment_overrides: dict[str, str] | None = None,
    append: bool = False,
) -> list[dict]:
    commands = []
    environment = _command_environment(environment_overrides)
    _assert_no_symlink_ancestors(log_path.parent)
    if not log_path.parent.is_dir():
        raise LocalReleaseError("gate log directory is missing")
    if log_path.is_symlink() or log_path.exists() and not append:
        raise LocalReleaseError("gate evidence path already exists")
    if append and log_path.exists():
        descriptor, _ = _open_regular(log_path)
        os.close(descriptor)
    try:
        with log_path.open("ab" if append else "xb") as log:
            os.chmod(log_path, 0o600)
            for spec in specs:
                argv = list(spec["argv"])
                cwd = repo_root / spec.get("cwd", ".")
                record = {
                    "name": spec["name"],
                    "argv": _display_argv(argv, bundle, repo_root),
                    "cwd": spec.get("cwd", "."),
                    "exitStatus": -1,
                }
                log.write(("$ " + " ".join(record["argv"]) + "\n").encode())
                log.flush()
                try:
                    completed = subprocess.run(
                        argv,
                        cwd=cwd,
                        env=environment,
                        stdout=log,
                        stderr=subprocess.STDOUT,
                        check=False,
                    )
                except OSError as error:
                    log.write(f"command could not start: {type(error).__name__}\n".encode())
                    log.flush()
                    raise LocalReleaseError(f"{gate} command could not start; see {log_path.name}") from error
                record["exitStatus"] = completed.returncode
                commands.append(record)
                log.flush()
                if completed.returncode != 0:
                    raise LocalReleaseError(f"{gate} failed with exit {completed.returncode}; see {log_path.name}")
            log.flush()
            os.fsync(log.fileno())
    except LocalReleaseError:
        raise
    except OSError as error:
        raise LocalReleaseError(f"{gate} evidence log could not be written") from error
    if log_path.stat().st_size == 0:
        raise LocalReleaseError(f"{gate} produced an empty log")
    return commands


def _report_records(bundle: Path, report_paths: list[Path]) -> list[dict]:
    records = []
    for path in report_paths:
        try:
            relative = path.relative_to(bundle / "evidence").as_posix()
        except ValueError as error:
            raise LocalReleaseError("gate report must be inside the evidence directory") from error
        records.append(_evidence_record(bundle, relative, "report"))
    return records


def _write_build_receipt(
    bundle: Path,
    context: dict,
    commands: list[dict],
    report_paths: list[Path],
    tool_versions: dict[str, str],
) -> None:
    inventories = artifact_inventories(bundle)
    reports = [
        _evidence_record(bundle, "logs/build.log", "log"),
        *_report_records(bundle, report_paths),
    ]
    _write_receipt_file(
        bundle,
        "build",
        _receipt("build", context, inventories, commands, reports, tool_versions=tool_versions),
    )


def _check_full_matrix(source_sha: str, repo_root: Path = ROOT) -> list[dict]:
    directory = repo_root / "dist" / "release"
    manifest = release_artifacts.validate_staged(
        directory,
        expected_source_sha=source_sha,
        expected_version=VERSION,
    )
    if len(manifest["artifacts"]) != 15:
        raise LocalReleaseError("the full 15-artifact GoReleaser matrix is incomplete")
    entries = []
    for name in sorted(release_artifacts.expected_names(VERSION)):
        size, digest = _hash_file(directory / name)
        entries.append({"path": name, "size": size, "sha256": digest})
    return entries


def _build_context(bundle: Path, source_sha: str) -> dict:
    created = _format_time(_now())
    context = {
        "schema": "kubeatlas-local-build/v1",
        "repository": REPOSITORY,
        "version": VERSION,
        "candidateOnly": True,
        "sourceSha": source_sha,
        "toolingSha": source_sha,
        "buildId": str(uuid.uuid4()),
        "createdAt": created,
        "completedAt": created,
        "status": "building",
    }
    _exclusive_write(_context_path(bundle), _canonical_json(context))
    return context


def _finish_context(bundle: Path, context: dict) -> dict:
    context = dict(context)
    context["completedAt"] = _format_time(_now())
    context["status"] = "built"
    path = _context_path(bundle)
    _assert_no_symlink_ancestors(path)
    try:
        with path.open("r+b") as output:
            output.seek(0)
            output.write(_canonical_json(context))
            output.truncate()
            output.flush()
            os.fsync(output.fileno())
    except OSError as error:
        raise LocalReleaseError("build context could not be finalized") from error
    return context


def _ensure_evidence_dirs(bundle: Path) -> None:
    for relative in (
        "binaries",
        "ghcr/images/release-image-kubeatlas",
        "ghcr/images/release-image-kubeatlas-portal",
        "ghcr/charts",
        "evidence/logs",
        "evidence/receipts",
        "evidence/reports",
        "evidence/semgrep",
        "evidence/imports",
    ):
        path = bundle / relative
        _assert_no_symlink_ancestors(path.parent)
        path.mkdir(parents=True, exist_ok=False)


def _run_buildx_component(
    component: str,
    dockerfile: str,
    bundle: Path,
    source_sha: str,
    commands: list[dict],
    log_path: Path,
    repo_root: Path = ROOT,
) -> list[Path]:
    image_dir = bundle / "ghcr" / "images" / f"release-image-{component}"
    archive = image_dir / f"{component}.oci.tar"
    metadata_path = bundle / "evidence" / "reports" / f"{component}-build-metadata.json"
    raw_path = bundle / "evidence" / "reports" / f"{component}-raw-manifest.json"
    build_command = [
        "docker",
        "buildx",
        "build",
        "--platform",
        "linux/amd64,linux/arm64",
        "--target",
        "full",
        "--provenance=false",
        "--progress",
        "plain",
        "--output",
        f"type=oci,dest={archive}",
        "--metadata-file",
        str(metadata_path),
        "--build-arg",
        f"VERSION={VERSION}",
        "--build-arg",
        f"SOURCE_REVISION={source_sha}",
        "-f",
        dockerfile,
        ".",
    ]
    commands.extend(
        _run_commands(
            f"build-{component}",
            [{"name": f"Build {component} multi-platform OCI archive", "argv": build_command}],
            log_path,
            bundle,
            repo_root=repo_root,
            append=True,
        )
    )
    raw_command = ["skopeo", "inspect", "--raw", f"oci-archive:{archive}"]
    raw_record = {
        "name": f"Inspect {component} OCI archive",
        "argv": _display_argv(raw_command, bundle, repo_root),
        "cwd": ".",
        "exitStatus": -1,
    }
    with log_path.open("ab") as log:
        log.write(("$ " + " ".join(raw_record["argv"]) + "\n").encode())
        try:
            result = subprocess.run(
                raw_command,
                cwd=repo_root,
                env=_command_environment(),
                capture_output=True,
                check=False,
            )
        except OSError as error:
            log.write(f"command could not start: {type(error).__name__}\n".encode())
            raise LocalReleaseError(f"skopeo inspect could not start; see {log_path.name}") from error
        log.write(result.stdout)
        if result.stderr:
            log.write(result.stderr)
        log.flush()
        os.fsync(log.fileno())
    raw_record["exitStatus"] = result.returncode
    commands.append(raw_record)
    if result.returncode != 0:
        raise LocalReleaseError(f"skopeo inspect failed; see {log_path.name}")
    _exclusive_write(raw_path, result.stdout)
    platforms = validate_oci_archive.validate_archive(
        archive, raw_path, _expected_oci_labels(source_sha)
    )
    if set(platforms) != validate_oci_archive.EXPECTED_PLATFORMS:
        raise LocalReleaseError("OCI archive is missing a required platform")
    metadata = _load_json(metadata_path, f"{component} build metadata")
    build_digest = metadata.get("containerimage.digest")
    if not isinstance(build_digest, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", build_digest):
        raise LocalReleaseError(f"{component} build metadata has no image index digest")
    if build_digest != f"sha256:{hashlib.sha256(result.stdout).hexdigest()}":
        raise LocalReleaseError(f"{component} build digest does not match its raw OCI manifest")
    archive_size, archive_hash = _hash_file(archive)
    checksum_path = image_dir / f"{component}.oci.tar.sha256"
    _exclusive_write(checksum_path, f"{archive_hash}  {archive.name}\n".encode())
    metadata_out = {
        "component": component,
        "sourceSha": source_sha,
        "version": VERSION,
        "buildDigest": build_digest,
    }
    metadata_target = image_dir / f"{component}.metadata.json"
    _exclusive_write(metadata_target, _canonical_json(metadata_out))
    layout = bundle.parent / f".{bundle.name}-{component}-{uuid.uuid4().hex}-layout"
    if layout.exists() or layout.is_symlink():
        raise LocalReleaseError("temporary OCI validation path already exists")
    copy_command = [
        "skopeo",
        "copy",
        "--all",
        "--preserve-digests",
        f"oci-archive:{archive}",
        f"oci:{layout}:local-validation",
    ]
    commands.extend(
        _run_commands(
            f"oci-roundtrip-{component}",
            [{"name": f"Nonpublishing {component} OCI roundtrip", "argv": copy_command}],
            log_path,
            bundle,
            append=True,
        )
    )
    validate_command = [
        sys.executable,
        str(repo_root / "scripts" / "validate_oci_archive.py"),
        "verify-copy",
        "--layout",
        str(layout),
        "--raw-manifest",
        str(raw_path),
        "--tag",
        "local-validation",
    ]
    commands.extend(
        _run_commands(
            f"oci-roundtrip-verify-{component}",
            [{"name": f"Verify {component} preserved OCI digest", "argv": validate_command}],
            log_path,
            bundle,
            append=True,
        )
    )
    shutil.rmtree(layout)
    if archive_size == 0:
        raise LocalReleaseError(f"{component} OCI archive is empty")
    return [metadata_path, raw_path]


def build_bundle(source_sha: str, bundle_path: Path, repo_root: Path = ROOT) -> Path:
    source_sha = assert_authority(source_sha, repo_root)
    tool_versions = _validate_tools_for_gate("build")
    bundle = _bundle_path(bundle_path, repo_root)
    if bundle.exists() or bundle.is_symlink():
        raise LocalReleaseError("build refuses to reuse an existing release bundle")
    bundle.parent.mkdir(parents=True, exist_ok=True)
    _assert_no_symlink_ancestors(bundle.parent)
    try:
        bundle.mkdir(mode=0o700)
    except OSError as error:
        raise LocalReleaseError("release bundle directory could not be created exclusively") from error
    os.chmod(bundle, 0o700)
    _ensure_evidence_dirs(bundle)
    context = _build_context(bundle, source_sha)
    log_path = bundle / "evidence" / "logs" / "build.log"
    commands = []
    report_paths = []
    try:
        make_command = [
            "make",
            "release-binaries-dry",
            f"KUBEATLAS_RELEASE_VERSION={VERSION}",
            f"SOURCE_SHA={source_sha}",
        ]
        commands.extend(
            _run_commands(
                "build",
                [{"name": "Build and validate full GoReleaser matrix", "argv": make_command}],
                log_path,
                bundle,
                repo_root,
                {
                    "KUBEATLAS_RELEASE_VERSION": VERSION,
                    "SOURCE_SHA": source_sha,
                    "GOMAXPROCS": "2",
                },
            )
        )
        full_matrix = _check_full_matrix(source_sha, repo_root)
        full_report = bundle / "evidence" / "reports" / "full-matrix-inventory.json"
        _exclusive_write(
            full_report,
            _canonical_json(
                {
                    "schema": "kubeatlas-full-matrix/v1",
                    "repository": REPOSITORY,
                    "version": VERSION,
                    "sourceSha": source_sha,
                    "artifacts": full_matrix,
                }
            ),
        )
        report_paths.append(full_report)
        stage_command = [
            sys.executable,
            str(repo_root / "scripts" / "release_artifacts.py"),
            "stage-local",
            "--profile",
            PUBLICATION_PROFILE,
            "--dist",
            str(repo_root / "dist" / "release"),
            "--output",
            str(bundle / "binaries"),
            "--source-sha",
            source_sha,
            "--version",
            VERSION,
        ]
        commands.extend(
            _run_commands(
                "build",
                [{"name": "Stage strict local binary publication profile", "argv": stage_command}],
                log_path,
                bundle,
                repo_root,
                append=True,
            )
        )
        for component, dockerfile in (
            ("kubeatlas", "Dockerfile"),
            ("kubeatlas-portal", "Dockerfile.portal"),
        ):
            report_paths.extend(
                _run_buildx_component(
                    component, dockerfile, bundle, source_sha, commands, log_path, repo_root
                )
            )
        chart_command = [
            sys.executable,
            str(repo_root / "scripts" / "release_chart_package.py"),
            "--source-dir",
            str(repo_root),
            "--output-dir",
            str(bundle / "ghcr" / "charts"),
            "--version",
            VERSION,
            "--sha",
            source_sha,
        ]
        commands.extend(
            _run_commands(
                "build",
                [{"name": "Package version-aligned main and Portal charts", "argv": chart_command}],
                log_path,
                bundle,
                repo_root,
                append=True,
            )
        )
        _validate_built_artifacts(bundle, source_sha, repo_root)
        report_paths.extend(
            [
                bundle / "evidence" / "reports" / "kubeatlas-raw-manifest.json",
                bundle / "evidence" / "reports" / "kubeatlas-portal-raw-manifest.json",
            ]
        )
        context = _finish_context(bundle, context)
        _write_build_receipt(bundle, context, commands, report_paths, tool_versions)
        return bundle
    finally:
        _clean_head(repo_root, source_sha)


def _validate_chart_archive(path: Path, name: str, source_sha: str) -> None:
    expected_repository = f"ghcr.io/maomada/{name}"
    try:
        with tarfile.open(path, "r:gz") as archive:
            members = archive.getmembers()
            if len(members) > 1000:
                raise LocalReleaseError(f"chart archive contains too many members: {path.name}")
            total_size = 0
            for item in members:
                member_name = item.name.rstrip("/")
                if (
                    not member_name
                    or item.name.startswith("/")
                    or "\\" in item.name
                    or any(part in ("", ".", "..") for part in member_name.split("/"))
                    or member_name.split("/", 1)[0] != name
                    or not (item.isfile() or item.isdir())
                ):
                    raise LocalReleaseError(f"chart archive contains an unsafe member: {path.name}")
                total_size += item.size
                if item.size > 16 * 1024 * 1024 or total_size > 32 * 1024 * 1024:
                    raise LocalReleaseError(f"chart archive exceeds its content limit: {path.name}")
            chart_members = [item for item in members if item.name == f"{name}/Chart.yaml"]
            values_members = [item for item in members if item.name == f"{name}/values.yaml"]
            if len(chart_members) != 1 or len(values_members) != 1:
                raise LocalReleaseError(f"chart archive layout is invalid: {path.name}")
            chart_stream = archive.extractfile(chart_members[0])
            values_stream = archive.extractfile(values_members[0])
            if chart_stream is None or values_stream is None:
                raise LocalReleaseError(f"chart archive cannot be read: {path.name}")
            chart = chart_stream.read().decode("utf-8")
            values = values_stream.read().decode("utf-8")
    except (OSError, tarfile.TarError, UnicodeDecodeError) as error:
        raise LocalReleaseError(f"chart archive is invalid: {path.name}") from error
    expected = {
        "name": rf"(?m)^name:\s*{re.escape(name)}\s*$",
        "version": rf"(?m)^version:\s*(?:{re.escape(VERSION)}|\"{re.escape(VERSION)}\"|'{re.escape(VERSION)}')\s*$",
        "appVersion": rf"(?m)^appVersion:\s*(?:{re.escape(VERSION)}|\"{re.escape(VERSION)}\"|'{re.escape(VERSION)}')\s*$",
        "source": r'(?m)^\s+org\.opencontainers\.image\.source:\s*["\']https://github\.com/maomada/KubeAtlas["\']\s*$',
        "revision": rf'(?m)^\s+org\.opencontainers\.image\.revision:\s*["\']{re.escape(source_sha)}["\']\s*$',
    }
    for field, pattern in expected.items():
        if not re.search(pattern, chart):
            raise LocalReleaseError(f"packaged chart {field} does not match the release: {name}")
    if (
        not re.search(rf'(?m)^\s+repository:\s*["\']{re.escape(expected_repository)}["\']\s*$', values)
        or not re.search(rf'(?m)^\s+tag:\s*["\']{re.escape(VERSION)}["\']\s*$', values)
    ):
        raise LocalReleaseError(f"packaged chart image metadata mismatch: {name}")


def _expected_oci_labels(source_sha: str) -> dict[str, str]:
    return {
        "org.opencontainers.image.source": f"https://github.com/{REPOSITORY}",
        "org.opencontainers.image.revision": source_sha,
        "org.opencontainers.image.version": VERSION,
    }


def _validate_bundle_artifacts(bundle: Path, source_sha: str) -> dict:
    binaries = release_artifacts.validate_staged(
        bundle / "binaries",
        expected_source_sha=source_sha,
        expected_version=VERSION,
        profile=PUBLICATION_PROFILE,
    )
    ghcr_inventory = release_approval._inventory(bundle / "ghcr", "ghcr", VERSION)
    for component in ("kubeatlas", "kubeatlas-portal"):
        image_dir = bundle / "ghcr" / "images" / f"release-image-{component}"
        archive = image_dir / f"{component}.oci.tar"
        checksum = image_dir / f"{component}.oci.tar.sha256"
        metadata = _load_json(image_dir / f"{component}.metadata.json", "image metadata")
        if set(metadata) != {"component", "sourceSha", "version", "buildDigest"}:
            raise LocalReleaseError(f"image metadata schema mismatch: {component}")
        size, digest = _hash_file(archive)
        if (
            metadata["component"] != component
            or metadata["sourceSha"] != source_sha
            or metadata["version"] != VERSION
            or not isinstance(metadata["buildDigest"], str)
            or _read_regular(checksum, 256).decode("ascii") != f"{digest}  {archive.name}\n"
            or size == 0
        ):
            raise LocalReleaseError(f"image archive metadata or checksum mismatch: {component}")
        raw = bundle / "evidence" / "reports" / f"{component}-raw-manifest.json"
        platforms = validate_oci_archive.validate_archive(
            archive, raw, _expected_oci_labels(source_sha)
        )
        if set(platforms) != validate_oci_archive.EXPECTED_PLATFORMS:
            raise LocalReleaseError(f"image platform matrix mismatch: {component}")
        raw_digest = hashlib.sha256(_read_regular(raw, 1024 * 1024)).hexdigest()
        if metadata["buildDigest"] != f"sha256:{raw_digest}":
            raise LocalReleaseError(f"image index digest mismatch: {component}")
    charts = bundle / "ghcr" / "charts"
    checksums = {}
    for line in _read_regular(charts / "SHA256SUMS", 1024).decode("ascii").splitlines():
        fields = line.split()
        if len(fields) != 2 or fields[1] in checksums or not SHA256_PATTERN.fullmatch(fields[0]):
            raise LocalReleaseError("chart checksum file is malformed")
        checksums[fields[1]] = fields[0]
    expected_charts = {f"{name}-{VERSION}.tgz" for name in ("kubeatlas", "kubeatlas-portal")}
    if set(checksums) != expected_charts:
        raise LocalReleaseError("chart checksum set is not exact")
    for name in ("kubeatlas", "kubeatlas-portal"):
        filename = f"{name}-{VERSION}.tgz"
        path = charts / filename
        _, digest = _hash_file(path)
        if digest != checksums[filename]:
            raise LocalReleaseError(f"chart checksum mismatch: {filename}")
        _validate_chart_archive(path, name, source_sha)
    return {"binaries": binaries, "ghcr": ghcr_inventory}


def _validate_built_artifacts(bundle: Path, source_sha: str, repo_root: Path = ROOT) -> dict:
    full = release_artifacts.validate_staged(
        repo_root / "dist" / "release",
        expected_source_sha=source_sha,
        expected_version=VERSION,
    )
    if len(full["artifacts"]) != 15:
        raise LocalReleaseError("full 15-artifact matrix validation failed")
    selected = _validate_bundle_artifacts(bundle, source_sha)
    return {"full": full, **selected}


def _validate_bundle_content(bundle: Path, source_sha: str) -> dict:
    try:
        return _validate_bundle_artifacts(bundle, source_sha)
    except LocalReleaseError:
        raise
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError) as error:
        raise LocalReleaseError("release bundle content validation failed") from error


def validate_built_artifacts_command(bundle: Path, source_sha: str) -> int:
    _validate_built_artifacts(bundle, source_sha)
    print("Validated full binary matrix, local profile, OCI platforms, checksums, and charts.")
    return 0


def _timestamped_log(bundle: Path, gate: str) -> Path:
    return bundle / "evidence" / "logs" / f"{gate}-{uuid.uuid4().hex}.log"


def _gate_specs(bundle: Path, rules_source: Path, gate: str) -> tuple[list[dict], list[Path], dict[str, str]]:
    python = sys.executable
    root = ROOT
    reports = []
    env = {}
    if gate == "linux-ci":
        specs = [
            {"name": "Download root Go modules", "argv": ["go", "mod", "download"]},
            {"name": "Check Go formatting", "argv": ["bash", "-euo", "pipefail", "-c", "files=$(gofmt -l .); test -z \"$files\" || { printf '%s\\n' \"$files\"; exit 1; }"]},
            {"name": "Vet root Go module", "argv": ["go", "vet", "./..."]},
            {"name": "Run required root tests with PostgreSQL", "argv": ["bash", "-euo", "pipefail", "-c", ": \"${KUBEATLAS_TEST_POSTGRES_DSN:?KUBEATLAS_TEST_POSTGRES_DSN must name the configured test database}\"; test \"${KUBEATLAS_REQUIRE_POSTGRES_TESTS:-}\" = 1; pkgs=$(go list ./...); mapfile -t selected < <(printf '%s\\n' \"$pkgs\" | grep -vx 'github.com/maomada/KubeAtlas/internal/server'); go test -count=1 \"${selected[@]}\""]},
            {"name": "Evaluate PVC and CNPG PromQL in Prometheus", "argv": ["go", "test", "./internal/prometheus", "-run", "^(TestPVCScopePromQL|TestCNPGDiskGrowthPromQL)$", "-count=1"]},
            {"name": "Build explorer", "argv": ["go", "build", "./cmd/explorer"]},
            {"name": "Build standalone Portal", "argv": ["go", "build", "./cmd/kubeatlas-portal"]},
            {"name": "Download shared Go modules", "argv": ["go", "mod", "download"], "cwd": "pkg"},
            {"name": "Vet shared Go module", "argv": ["go", "vet", "./..."], "cwd": "pkg"},
            {"name": "Test shared Go module", "argv": ["go", "test", "-count=1", "./..."], "cwd": "pkg"},
            {"name": "Evaluate workload PromQL in Prometheus", "argv": ["go", "test", "./prom", "-run", "^TestWorkload(History)?PromQL$", "-count=1"], "cwd": "pkg"},
            {"name": "Build shared Go module", "argv": ["go", "build", "./..."], "cwd": "pkg"},
            {"name": "Test internal server", "argv": ["go", "test", "-count=1", "./internal/server"]},
        ]
        env = {
            "KUBEATLAS_REQUIRE_POSTGRES_TESTS": "1",
            "KUBEATLAS_TEST_PROMTOOL_IMAGE": "prom/prometheus:v3.5.0",
            "GOFLAGS": "-count=1",
        }
        return specs, reports, env
    if gate == "frontend":
        specs = [
            {"name": "Install pinned frontend dependencies", "argv": ["npm", "ci"]},
            {"name": "KubeAtlas UI typecheck", "argv": ["npm", "run", "tsc"], "cwd": "web"},
            {"name": "KubeAtlas UI tests", "argv": ["npm", "run", "test"], "cwd": "web"},
            {"name": "Workload metrics evidence capture", "argv": ["node", "--test", "scripts/workload-metrics-demo/capture.test.mjs"]},
            {"name": "KubeAtlas UI lint", "argv": ["npm", "run", "lint"], "cwd": "web"},
            {"name": "Build KubeAtlas UI", "argv": ["npm", "run", "build"], "cwd": "web"},
            {"name": "Build Portal UI", "argv": ["npx", "vite", "build", "--config", "vite.portal.config.ts"], "cwd": "web"},
            {"name": "k8s-ui typecheck", "argv": ["npm", "run", "tsc"], "cwd": "packages/k8s-ui"},
            {"name": "k8s-ui tests", "argv": ["npm", "test"], "cwd": "packages/k8s-ui"},
        ]
        return specs, reports, env
    if gate == "settings-e2e":
        return [
            {"name": "Install Chromium dependencies", "argv": ["npx", "playwright", "install", "--with-deps", "chromium"], "cwd": "web"},
            {"name": "Build embedded application", "argv": ["make", "build"]},
            {"name": "Settings browser journeys (fixture label)", "argv": ["npm", "run", "test:e2e:settings"], "cwd": "web"},
        ], reports, env
    if gate == "helm":
        script = r"""set -euo pipefail
helm lint deploy/helm/kubeatlas
helm lint deploy/helm/kubeatlas-portal --set image.repository=example.test/kubeatlas-portal --set existingSecret=portal-auth
helm template radar deploy/helm/kubeatlas >/dev/null
for mode in inline existing missing; do
  args=(--set cloud.enabled=true --set cloud.url=wss://cloud.example/agent --set cloud.clusterName=test)
  if [[ "$mode" == inline ]]; then args+=(--set cloud.token=rhc_deadbeef); fi
  if [[ "$mode" == existing ]]; then args+=(--set cloud.existingSecret=my-cloud-token); fi
  if output=$(helm template radar deploy/helm/kubeatlas "${args[@]}" 2>&1); then
    echo "cloud.enabled unexpectedly rendered"; exit 1
  fi
  grep -Fq "cloud.enabled is unavailable in KubeAtlas" <<<"$output"
done
helm template radar deploy/helm/kubeatlas --set argocd.token=argo_deadbeef --set argocd.url=https://argocd.example.com >/dev/null
helm template radar deploy/helm/kubeatlas --set argocd.existingSecret=my-argo-token >/dev/null
if helm template radar deploy/helm/kubeatlas --set argocd.token=argo_deadbeef --set argocd.existingSecret=my-argo-token 2>/dev/null; then exit 1; fi
helm template radar deploy/helm/kubeatlas --set timeline.storage=postgres --set timeline.postgres.existingSecret=radar-postgres --set timeline.retention=720h >/dev/null
if helm template radar deploy/helm/kubeatlas --set timeline.storage=postgres 2>/dev/null; then exit 1; fi
./scripts/test-chart.sh
HELM_BIN="${HELM3_BIN:-helm3}" helmfile -f ./deploy/helm/ unittest"""
        return [{"name": "Helm 4 lint/template/negative checks and Helm 3 helmfile tests", "argv": ["bash", "-euo", "pipefail", "-c", script]}], reports, env
    if gate == "release-security":
        scripts = [
            "scripts/release_approval.py",
            "scripts/release_artifacts.py",
            "scripts/release_chart_package.py",
            "scripts/release_github.py",
            "scripts/release_local.py",
            "scripts/release_local_publisher.py",
            "scripts/release_manifest.py",
            "scripts/release_policy.py",
            "scripts/release_registry.py",
            "scripts/release_windows_smoke.py",
            "scripts/security_dispositions.py",
            "scripts/security_scan.py",
            "scripts/validate_oci_archive.py",
            "scripts/test_release_local.py",
            "scripts/test_release_local_publisher.py",
        ]
        return [
            {"name": "Release policy regression suite", "argv": [python, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_release*.py"]},
            {"name": "Security regression suite", "argv": [python, "-m", "unittest", "discover", "-s", "scripts", "-p", "test_security*.py"]},
            {"name": "Compile release tooling", "argv": [python, "-m", "py_compile", *scripts]},
            {"name": "Lint workflows with actionlint 1.7.7", "argv": ["bash", "-euo", "pipefail", "-c", "go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/*.yml"]},
        ], reports, env
    if gate == "posix-installers":
        return [
            {"name": "POSIX installer syntax and ShellCheck", "argv": ["bash", "-euo", "pipefail", "-c", "sh -n install.sh && sh -n scripts/test-installers.sh && shellcheck --shell=sh install.sh scripts/test-installers.sh"]},
            {"name": "Reject GitHub API discovery in installers", "argv": ["bash", "-euo", "pipefail", "-c", "if grep -Fq 'api.github.com' install.sh install.ps1; then exit 1; fi"]},
            {"name": "POSIX installer fixtures", "argv": ["sh", "scripts/test-installers.sh"]},
        ], reports, env
    if gate in ("semgrep-self-test", "semgrep-full"):
        output = bundle / "evidence" / "semgrep" / f"{gate}-{uuid.uuid4().hex}"
        reports = [output / "canary-clean.json", output / "canary-unsafe.json"] if gate == "semgrep-self-test" else [
            output / "findings.json",
            output / "provenance.json",
            output / "dispositions.json",
        ]
        command = [
            python,
            "scripts/security_scan.py",
            "--rules-source",
            str(rules_source),
            "--output",
            str(output),
        ]
        if gate == "semgrep-self-test":
            command.append("--self-test")
        env = {"SEMGREP_SEND_METRICS": "off"}
        return [{"name": f"Run pinned Semgrep {gate}", "argv": command}], reports, env
    if gate == "package-oci":
        return [
            {"name": "Validate full and local binary manifests, OCI metadata, and chart packages", "argv": [
                python,
                str(root / "scripts" / "release_local.py"),
                "validate-artifacts",
                "--bundle",
                str(bundle),
                "--source-sha",
                _load_context(bundle)["sourceSha"],
            ]},
        ], reports, env
    raise LocalReleaseError(f"unsupported local gate: {gate}")


def _validate_build_report(bundle: Path, receipt: dict, context: dict) -> None:
    records = {
        item["path"]: item
        for item in receipt["reports"]
        if isinstance(item, dict) and isinstance(item.get("path"), str)
    }
    relative = "reports/full-matrix-inventory.json"
    if relative not in records or records[relative]["kind"] != "report":
        raise LocalReleaseError("build receipt must bind the complete matrix inventory report")
    report = _load_json(bundle / "evidence" / relative, "full matrix inventory")
    if set(report) != {"schema", "repository", "version", "sourceSha", "artifacts"}:
        raise LocalReleaseError("full matrix inventory schema is invalid")
    expected = set(release_artifacts.expected_names(VERSION))
    artifacts = report["artifacts"]
    if (
        report["schema"] != "kubeatlas-full-matrix/v1"
        or report["repository"] != REPOSITORY
        or report["version"] != VERSION
        or report["sourceSha"] != context["sourceSha"]
        or not isinstance(artifacts, list)
        or len(artifacts) != 15
        or any(
            not isinstance(item, dict)
            or set(item) != {"path", "size", "sha256"}
            or not isinstance(item["path"], str)
            or type(item["size"]) is not int
            or item["size"] <= 0
            or not isinstance(item["sha256"], str)
            or not SHA256_PATTERN.fullmatch(item["sha256"])
            for item in artifacts
        )
        or {item["path"] for item in artifacts} != expected
    ):
        raise LocalReleaseError("full matrix inventory does not bind all 15 expected artifacts")


def _validate_semgrep_receipt(bundle: Path, receipt: dict, gate: str, context: dict) -> None:
    reports = {
        Path(item["path"]).name: bundle / "evidence" / item["path"]
        for item in receipt["reports"]
        if item["kind"] == "report"
    }
    if gate == "semgrep-self-test":
        if set(reports) != {"canary-clean.json", "canary-unsafe.json"}:
            raise LocalReleaseError("Semgrep self-test receipt omits fixed canary reports")
        clean = _load_json(reports["canary-clean.json"], "Semgrep clean canary")
        unsafe = _load_json(reports["canary-unsafe.json"], "Semgrep unsafe canary")
        for report in (clean, unsafe):
            if report.get("errors") != [] or not isinstance(report.get("paths"), dict):
                raise LocalReleaseError("Semgrep canary has scanner errors or no path inventory")
        clean_targets = [Path(path) for path in clean["paths"].get("scanned", [])]
        unsafe_targets = [Path(path) for path in unsafe["paths"].get("scanned", [])]
        try:
            security_scan.require_complete(clean, clean_targets, 0)
            security_scan.require_complete(unsafe, unsafe_targets, 1)
        except (ValueError, KeyError, TypeError) as error:
            raise LocalReleaseError("Semgrep canary coverage or exit status is invalid") from error
        detected = {
            Path(item.get("path", "")).name
            for item in unsafe.get("results", [])
            if isinstance(item, dict)
        }
        if not {"unsafe.go", "unsafe.js", "unsafe.ts"}.issubset(detected):
            raise LocalReleaseError("Semgrep unsafe canary did not detect every required language")
        return

    if set(reports) != {"findings.json", "provenance.json", "dispositions.json"}:
        raise LocalReleaseError("Semgrep full receipt omits fixed findings, provenance, or disposition reports")
    findings = _load_json(reports["findings.json"], "Semgrep findings")
    provenance = _load_json(reports["provenance.json"], "Semgrep provenance")
    dispositions = _load_json(reports["dispositions.json"], "Semgrep dispositions")
    lock_path = ROOT / ".github" / "security-rules.lock.json"
    policy_path = ROOT / ".github" / "security-dispositions.json"
    lock = _load_json(lock_path, "Semgrep rules lock")
    lock_hash = hashlib.sha256(_read_regular(lock_path)).hexdigest()
    policy = _load_json(policy_path, "Semgrep disposition policy")
    targets = sorted(
        ROOT / name
        for name in subprocess.check_output(
            ["git", "-C", str(ROOT), "ls-files", "-z"], text=False
        ).decode().split("\0")
        if name and security_scan.production_source(name)
    )
    expected_names = [path.relative_to(ROOT).as_posix() for path in targets]
    if (
        provenance.get("sourceSha") != context["sourceSha"]
        or provenance.get("engine") != security_scan.ENGINE_VERSION
        or provenance.get("rulesCommit") != lock.get("commit")
        or provenance.get("lockSha256") != lock_hash
        or provenance.get("targets") != expected_names
    ):
        raise LocalReleaseError("Semgrep provenance is not bound to this source and locked ruleset")
    returncode = 1 if findings.get("results") else 0
    try:
        security_scan.require_complete(findings, targets, returncode)
        decision = security_dispositions.evaluate(
            findings, policy, ROOT, targets, security_scan.ENGINE_VERSION, lock_hash
        )
    except (ValueError, KeyError, TypeError) as error:
        raise LocalReleaseError("Semgrep scan or disposition policy validation failed") from error
    expected_dispositions = {
        **decision,
        "sourceSha": context["sourceSha"],
        "policySha256": hashlib.sha256(_read_regular(policy_path)).hexdigest(),
        "rawReportSha256": hashlib.sha256(_read_regular(reports["findings.json"])).hexdigest(),
    }
    if dispositions != expected_dispositions or not decision["passed"] or decision["unresolved"]:
        raise LocalReleaseError("Semgrep findings are unresolved or disposition provenance is invalid")


def _validate_automated_receipt(bundle: Path, receipt: dict, gate: str) -> None:
    required_tools = {
        "linux-ci": {"go": "go1.26."},
        "frontend": {"node": "v24."},
        "settings-e2e": {"node": "v24."},
        "helm": {"helm": "v4.2.3", "helm3": "v3.19.0", "helmfile": ""},
        "release-security": {"go": "go1.26."},
        "posix-installers": {"shellcheck": ""},
        "semgrep-self-test": {"semgrep": "1.180.0"},
        "semgrep-full": {"semgrep": "1.180.0"},
        "package-oci": {"python": ""},
    }[gate]
    versions = receipt["toolVersions"]
    if not set(required_tools).issubset(versions) or any(
        not isinstance(versions.get(tool), str)
        or not versions[tool].strip()
        or prefix and prefix not in versions[tool]
        for tool, prefix in required_tools.items()
    ):
        raise LocalReleaseError(f"{gate} receipt omits required tool versions")
    specs, report_paths, _ = _gate_specs(bundle, Path("/"), gate)
    commands = receipt["commands"]
    if gate in {"semgrep-self-test", "semgrep-full"}:
        argv = commands[0]["argv"]
        try:
            rules_source = Path(argv[argv.index("--rules-source") + 1])
            output_value = argv[argv.index("--output") + 1]
        except (ValueError, IndexError) as error:
            raise LocalReleaseError("Semgrep command is missing its fixed rules/output arguments") from error
        output_dir = Path(output_value.replace("$BUNDLE", str(bundle)))
        if not output_dir.resolve().is_relative_to((bundle / "evidence" / "semgrep").resolve()):
            raise LocalReleaseError("Semgrep output must remain in the signed evidence directory")
        if not re.fullmatch(
            rf"\$BUNDLE/evidence/semgrep/{re.escape(gate)}-[0-9a-f]{{32}}",
            output_value,
        ):
            raise LocalReleaseError("Semgrep output directory does not match the fixed gate")
        specs, report_paths, _ = _gate_specs(bundle, rules_source, gate)
        report_paths = [output_dir / path.name for path in report_paths]
        for spec in specs:
            expected_argv = _display_argv(spec["argv"], bundle, ROOT)
            actual = next((item for item in commands if item["name"] == spec["name"]), None)
            if actual is None:
                raise LocalReleaseError(f"{gate} receipt omits its fixed scanner command")
            expected_argv[expected_argv.index("--output") + 1] = output_value
            if actual["argv"] != expected_argv:
                raise LocalReleaseError(f"{gate} command does not match the fixed scanner contract")
    else:
        if len(commands) != len(specs):
            raise LocalReleaseError(f"{gate} receipt command count does not match the fixed gate")
        for actual, spec in zip(commands, specs):
            if (
                actual["name"] != spec["name"]
                or actual["argv"] != _display_argv(spec["argv"], bundle, ROOT)
                or actual["cwd"] != spec.get("cwd", ".")
            ):
                raise LocalReleaseError(f"{gate} receipt command does not match the fixed gate contract")
    expected_reports = {
        path.relative_to(bundle / "evidence").as_posix() for path in report_paths
    }
    actual_reports = {
        item["path"] for item in receipt["reports"] if item["kind"] == "report"
    }
    if actual_reports != expected_reports:
        raise LocalReleaseError(f"{gate} receipt report roles do not match the fixed gate")
    if gate.startswith("semgrep-"):
        context = _load_context(bundle)
        _validate_semgrep_receipt(bundle, receipt, gate, context)


def require_skopeo_copy_capabilities(runner=None) -> None:
    try:
        result = (runner or subprocess.run)(
            ["skopeo", "copy", "--help"],
            capture_output=True,
            text=True,
            check=False,
            timeout=30,
            env=_command_environment(),
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise LocalReleaseError("cannot inspect required Skopeo copy capabilities") from error
    help_text = "\n".join(
        text for text in (result.stdout, result.stderr) if isinstance(text, str)
    )
    if result.returncode != 0 or not {"--all", "--preserve-digests"} <= set(help_text.split()):
        raise LocalReleaseError("Skopeo copy must support --all and --preserve-digests")


def _validate_tools_for_gate(gate: str) -> dict[str, str]:
    required = {
        "build": {
            "go": "go1.26.",
            "goreleaser": "2.13.3",
            "node": "v24.",
            "npm": "",
            "make": "",
            "dpkg-deb": "",
            "rpm": "",
            "cpio": "",
            "docker": "",
            "skopeo": "",
            "helm": "v4.2.3",
        },
        "linux-ci": {"go": "go1.26."},
        "frontend": {"node": "v24."},
        "settings-e2e": {"node": "v24."},
        "helm": {"helm": "v4.2.3", "helm3": "v3.19.0", "helmfile": ""},
        "release-security": {"go": "go1.26."},
        "posix-installers": {"shellcheck": ""},
        "semgrep-self-test": {"semgrep": "1.180.0"},
        "semgrep-full": {"semgrep": "1.180.0"},
    }.get(gate, {})
    argument_map = {
        "go": ["version"],
        "goreleaser": ["--version"],
        "docker": ["--version"],
        "skopeo": ["--version"],
        "helm": ["version", "--short"],
        "helm3": ["version", "--short"],
        "helmfile": ["--version"],
        "node": ["--version"],
        "npm": ["--version"],
        "make": ["--version"],
        "dpkg-deb": ["--version"],
        "rpm": ["--version"],
        "cpio": ["--version"],
        "shellcheck": ["--version"],
        "semgrep": ["--version"],
    }
    versions = {}
    for tool, version_prefix in required.items():
        executable = os.environ.get("HELM3_BIN", "helm3") if tool == "helm3" else tool
        try:
            result = subprocess.run(
                [executable, *argument_map[tool]],
                capture_output=True,
                text=True,
                check=False,
                env=_command_environment(),
            )
        except OSError as error:
            raise LocalReleaseError(f"required tool is unavailable: {tool}") from error
        output = f"{result.stdout}\n{result.stderr}"
        if result.returncode != 0 or version_prefix and version_prefix not in output:
            raise LocalReleaseError(f"required tool version is not satisfied: {tool}")
        versions[tool] = output.strip()
    if gate == "build":
        for tool in ("rpm2cpio", "npx"):
            executable = shutil.which(tool)
            if executable is None:
                raise LocalReleaseError(f"required tool is unavailable: {tool}")
            versions[tool] = f"executable available: {executable}"
        require_skopeo_copy_capabilities()
        try:
            result = subprocess.run(
                ["docker", "buildx", "version"],
                capture_output=True,
                text=True,
                check=False,
                env=_command_environment(),
            )
        except OSError as error:
            raise LocalReleaseError("Docker Buildx is required for local OCI builds") from error
        if result.returncode != 0:
            raise LocalReleaseError("Docker Buildx is required for local OCI builds")
        try:
            result = subprocess.run(
                ["docker", "info"],
                capture_output=True,
                text=True,
                check=False,
                env=_command_environment(),
            )
        except OSError as error:
            raise LocalReleaseError("Docker daemon is unavailable for local OCI builds") from error
        if result.returncode != 0:
            raise LocalReleaseError("Docker daemon is unavailable for local OCI builds")
    if gate == "helm":
        helm3 = os.environ.get("HELM3_BIN", "helm3")
        try:
            result = subprocess.run(
                [helm3, "plugin", "list"],
                capture_output=True,
                text=True,
                check=False,
                env=_command_environment(),
            )
        except OSError as error:
            raise LocalReleaseError("Helm 3 helm-unittest 1.1.0 is required") from error
        if result.returncode != 0 or not re.search(r"(?m)^unittest\s+1\.1\.0(?:\s|$)", result.stdout):
            raise LocalReleaseError("Helm 3 helm-unittest plugin 1.1.0 is required")
    return versions


def run_checks(bundle_path: Path, source_sha: str, rules_source: Path, repo_root: Path = ROOT) -> None:
    source_sha = assert_authority(source_sha, repo_root)
    bundle = _bundle_path(bundle_path, repo_root)
    _ensure_bundle_layout(bundle)
    if (bundle / "approval.json").exists() or (bundle / "approval.sig").exists():
        raise LocalReleaseError("check cannot modify evidence after approval preparation")
    context = _load_context(bundle)
    if context["sourceSha"] != source_sha:
        raise LocalReleaseError("release bundle source SHA does not match")
    _validate_bundle_content(bundle, source_sha)
    rules_source = Path(os.path.abspath(rules_source))
    _assert_no_symlink_ancestors(rules_source)
    if rules_source.is_symlink() or not rules_source.is_dir():
        raise LocalReleaseError("Semgrep rules checkout is missing or unsafe")
    lock = _load_json(repo_root / ".github" / "security-rules.lock.json", "Semgrep rules lock")
    rules_commit = lock.get("commit")
    actual_rules_commit = _git_output(rules_source, "rev-parse", "HEAD")
    if actual_rules_commit != rules_commit:
        raise LocalReleaseError("Semgrep rules checkout does not match the committed rules lock")
    inventories = artifact_inventories(bundle)
    receipts_dir = bundle / "evidence" / "receipts"
    prior = {entry.name.removesuffix(".json") for entry in receipts_dir.iterdir()}
    if prior - REQUIRED_GATES:
        raise LocalReleaseError("release bundle contains an unknown gate receipt")
    if "build" not in prior:
        raise LocalReleaseError("build receipt is required before checks")
    validate_receipts(bundle, context, inventories, required_gates=frozenset({"build"}))
    for gate in prior - {"build"}:
        validate_receipts(bundle, context, inventories, required_gates=frozenset({gate}))
    failures = []
    try:
        for gate in AUTOMATED_GATES:
            if gate in prior:
                print(f"Reusing immutable local gate receipt: {gate}")
                continue
            try:
                tool_versions = _validate_tools_for_gate(gate)
                specs, report_paths, env = _gate_specs(bundle, rules_source, gate)
                if gate == "release-security":
                    with tempfile.TemporaryDirectory(prefix="kubeatlas-pycache-") as cache:
                        env["PYTHONPYCACHEPREFIX"] = cache
                        log_path = _timestamped_log(bundle, gate)
                        commands = _run_commands(gate, specs, log_path, bundle, repo_root, env)
                else:
                    log_path = _timestamped_log(bundle, gate)
                    commands = _run_commands(gate, specs, log_path, bundle, repo_root, env)
                for path in report_paths:
                    if not path.is_file() or path.is_symlink():
                        raise LocalReleaseError(f"{gate} did not produce required report evidence")
                reports = [
                    _evidence_record(bundle, log_path.relative_to(bundle / "evidence").as_posix(), "log"),
                    *_report_records(bundle, report_paths),
                ]
                receipt = _receipt(
                    gate,
                    context,
                    inventories,
                    commands,
                    reports,
                    tool_versions=tool_versions,
                )
                _write_receipt_file(bundle, gate, receipt)
                print(f"Passed local gate: {gate}")
            except (LocalReleaseError, OSError, ValueError, subprocess.SubprocessError) as error:
                failures.append(f"{gate}: {error}")
    except LocalReleaseError as error:
        failures.append(f"local checks: {error}")
    finally:
        try:
            _clean_head(repo_root, source_sha)
        except LocalReleaseError as error:
            failures.append(f"source authority after checks: {error}")
    if failures:
        raise LocalReleaseError("local checks incomplete: " + "; ".join(failures))


def _copy_imported_file(source: Path, destination: Path) -> tuple[int, str]:
    descriptor, file_stat = _open_regular(source)
    if file_stat.st_size > 64 * 1024 * 1024:
        os.close(descriptor)
        raise LocalReleaseError("imported acceptance evidence exceeds 64 MiB")
    with os.fdopen(descriptor, "rb") as source_stream:
        data = source_stream.read()
    if any(pattern.search(data) for pattern in SECRET_MARKERS):
        raise LocalReleaseError("imported evidence contains a credential or private-key marker")
    _exclusive_write(destination, data, 0o600)
    return len(data), hashlib.sha256(data).hexdigest()


def import_acceptance(bundle_path: Path, report_path: Path, evidence_root: Path, repo_root: Path = ROOT) -> None:
    bundle = _bundle_path(bundle_path, repo_root)
    _ensure_bundle_layout(bundle)
    if (bundle / "approval.json").exists() or (bundle / "approval.sig").exists():
        raise LocalReleaseError("acceptance evidence cannot change after approval preparation")
    context = _load_context(bundle)
    _clean_head(repo_root, context["toolingSha"])
    report_bytes = _read_regular(Path(report_path), release_approval.MAX_MANIFEST_BYTES)
    if any(pattern.search(report_bytes) for pattern in SECRET_MARKERS):
        raise LocalReleaseError("external acceptance report contains a credential or private-key marker")
    report = release_approval._load_json(report_bytes)
    if set(report) != {
        "schema",
        "origin",
        "repository",
        "gate",
        "version",
        "sourceSha",
        "toolingSha",
        "buildId",
        "artifactInventorySha256",
        "executionPlatform",
        "issuedAt",
        "completedAt",
        "commands",
        "exitStatus",
        "result",
        "context",
        "componentVersions",
        "limitations",
        "scenarios",
        "evidence",
        "artifactHashes",
        "deployedImageDigests",
        "executables",
        "toolVersions",
    }:
        raise LocalReleaseError("invalid external acceptance report schema")
    gate = report["gate"]
    if gate not in EXTERNAL_GATES:
        raise LocalReleaseError("acceptance report names an unapproved gate")
    if (bundle / "evidence" / "receipts" / f"{gate}.json").exists():
        raise LocalReleaseError("acceptance receipt already exists and cannot be overwritten")
    inventories = artifact_inventories(bundle)
    if (
        report["schema"] != ACCEPTANCE_SCHEMA
        or report["origin"] != "operator-attested"
        or report["repository"] != REPOSITORY
        or report["version"] != VERSION
        or report["sourceSha"] != context["sourceSha"]
        or report["toolingSha"] != context["toolingSha"]
        or report["buildId"] != context["buildId"]
        or report["artifactInventorySha256"] != artifact_inventory_digest(inventories)
        or type(report["exitStatus"]) is not int
        or not 0 <= report["exitStatus"] <= 255
        or report["result"] not in ("passed", "failed", "untested")
        or not isinstance(report["toolVersions"], dict)
        or not report["toolVersions"]
        or any(
            not isinstance(name, str)
            or not isinstance(version, str)
            or not version.strip()
            for name, version in report["toolVersions"].items()
        )
        or not _receipt_commands_valid(
            report["commands"], allow_failures=True, allow_empty=True
        )
    ):
        raise LocalReleaseError("external acceptance report does not bind to this release build")
    issued = _parse_time(report["issuedAt"], "acceptance issuedAt")
    completed = _parse_time(report["completedAt"], "acceptance completedAt")
    if issued > completed or completed > _now() or _now() - issued > MAX_RECEIPT_AGE:
        raise LocalReleaseError("external acceptance report is stale or has invalid timestamps")
    supplied = {
        "context": report["context"],
        "componentVersions": report["componentVersions"],
        "limitations": report["limitations"],
        "scenarios": report["scenarios"],
        "execution": report["executionPlatform"],
        "artifactHashes": report["artifactHashes"],
        "deployedImageDigests": report["deployedImageDigests"],
        "executables": report["executables"],
    }
    _validate_acceptance(
        {"acceptance": supplied, "commands": report["commands"]}, gate, require_passed=False
    )
    scenario_statuses = {item["status"] for item in supplied["scenarios"]}
    command_failed = any(item["exitStatus"] != 0 for item in report["commands"])
    expected_result = (
        "failed"
        if report["exitStatus"] != 0 or command_failed or "failed" in scenario_statuses
        else "untested"
        if "untested" in scenario_statuses
        else "passed"
    )
    if report["result"] != expected_result:
        raise LocalReleaseError("external acceptance result does not match its scenario and command statuses")
    if expected_result == "passed" and not report["commands"]:
        raise LocalReleaseError("passing external acceptance requires recorded commands")
    _validate_external_commands(report, gate)
    _validate_external_artifact_hashes(
        {"acceptance": supplied}, gate, inventories, bundle
    )
    evidence_items = report["evidence"]
    if not isinstance(evidence_items, list) or not evidence_items:
        raise LocalReleaseError("external acceptance report has no evidence files")
    source_root = Path(os.path.abspath(evidence_root))
    _assert_no_symlink_ancestors(source_root)
    if source_root.is_symlink() or not source_root.is_dir():
        raise LocalReleaseError("external evidence directory is missing or unsafe")
    target_dir = bundle / "evidence" / "imports" / gate
    if target_dir.exists() or target_dir.is_symlink():
        raise LocalReleaseError("external evidence destination already exists")
    prepared = []
    seen_paths = set()
    for item in evidence_items:
        if not isinstance(item, dict) or set(item) != {"path", "size", "sha256", "kind"}:
            raise LocalReleaseError("external evidence entry is invalid")
        relative = item["path"]
        if not isinstance(relative, str) or relative in seen_paths:
            raise LocalReleaseError("external evidence paths must be unique strings")
        seen_paths.add(relative)
        release_approval._safe_path(relative)
        if type(item["size"]) is not int or item["size"] < 0:
            raise LocalReleaseError("external evidence size is invalid")
        if not isinstance(item["sha256"], str) or not SHA256_PATTERN.fullmatch(item["sha256"]):
            raise LocalReleaseError("external evidence hash is invalid")
        if item["kind"] not in ("log", "report"):
            raise LocalReleaseError("external evidence type is invalid")
        source = source_root / relative
        size, checksum = _hash_file(source)
        if size != item["size"] or checksum != item["sha256"]:
            raise LocalReleaseError("external evidence hash or size mismatch")
        prepared.append((relative, source, size, checksum, item["kind"]))
    if not any(kind == "log" and size > 0 for _, _, size, _, kind in prepared):
        raise LocalReleaseError("external acceptance must include a nonempty log")
    temporary_dir = bundle / "evidence" / "imports" / f".{gate}-{uuid.uuid4().hex}.tmp"
    _assert_no_symlink_ancestors(temporary_dir.parent)
    temporary_dir.mkdir(mode=0o700, exist_ok=False)
    copied = []
    try:
        for index, (relative, source, size, checksum, kind) in enumerate(prepared):
            filename = f"{index:02d}-{Path(relative).name}"
            destination_relative = f"imports/{gate}/{filename}"
            copied_size, copied_hash = _copy_imported_file(
                source, temporary_dir / filename
            )
            if copied_size != size or copied_hash != checksum:
                raise LocalReleaseError("external evidence changed while importing")
            copied.append(
                {
                    "path": destination_relative,
                    "size": copied_size,
                    "sha256": copied_hash,
                    "kind": kind,
                }
            )
        report_hash = hashlib.sha256(report_bytes).hexdigest()
        _exclusive_write(temporary_dir / "acceptance-report.json", report_bytes, 0o600)
        copied.append(
            {
                "path": f"imports/{gate}/acceptance-report.json",
                "size": len(report_bytes),
                "sha256": report_hash,
                "kind": "report",
            }
        )
        temporary_dir.rename(target_dir)
    except Exception:
        shutil.rmtree(temporary_dir, ignore_errors=True)
        raise
    receipt = _receipt(
        gate,
        context,
        inventories,
        report["commands"],
        copied,
        execution_platform=report["executionPlatform"],
        acceptance=supplied,
        tool_versions=report["toolVersions"],
    )
    receipt["issuedAt"] = report["issuedAt"]
    receipt["completedAt"] = report["completedAt"]
    receipt["exitStatus"] = report["exitStatus"]
    receipt["result"] = report["result"]
    _write_receipt_file(bundle, gate, receipt)


def _expected_targets() -> list[str]:
    return [
        *release_approval.expected_targets("ghcr", VERSION),
        f"github-release:v{VERSION}",
        *expected_artifact_paths("binaries"),
    ]


def _validate_manifest(
    manifest: dict,
    context: dict,
    inventories: dict[str, list[dict[str, object]]],
    evidence: list[dict[str, object]],
    now: datetime,
    trusted_root: Path = ROOT,
) -> None:
    expected_keys = {
        "schema",
        "origin",
        "repository",
        "action",
        "version",
        "sourceSha",
        "toolingSha",
        "buildId",
        "keyFingerprint",
        "issuedAt",
        "expiresAt",
        "approvalId",
        "targets",
        "artifacts",
        "artifactInventorySha256",
        "evidence",
        "gates",
    }
    if set(manifest) != expected_keys:
        raise LocalReleaseError("invalid local approval manifest schema")
    if (
        manifest["schema"] != MANIFEST_SCHEMA
        or manifest["origin"] != "local"
        or manifest["repository"] != REPOSITORY
        or manifest["action"] != "publish"
        or manifest["version"] != VERSION
        or manifest["sourceSha"] != context["sourceSha"]
        or manifest["toolingSha"] != context["toolingSha"]
        or manifest["buildId"] != context["buildId"]
        or manifest["targets"] != _expected_targets()
        or manifest["artifacts"] != inventories
        or manifest["artifactInventorySha256"] != artifact_inventory_digest(inventories)
        or manifest["evidence"] != evidence
        or manifest["gates"] != sorted(REQUIRED_GATES)
    ):
        raise LocalReleaseError("local approval does not match the trusted bundle inventory")
    _, _, fingerprint = release_approval._trusted_key(trusted_root)
    if manifest["keyFingerprint"] != fingerprint:
        raise LocalReleaseError("local approval public-key fingerprint mismatch")
    approval_id = manifest["approvalId"]
    try:
        parsed = uuid.UUID(approval_id)
    except (ValueError, TypeError, AttributeError) as error:
        raise LocalReleaseError("approvalId must be a canonical UUID4") from error
    if parsed.version != 4 or str(parsed) != approval_id:
        raise LocalReleaseError("approvalId must be a canonical UUID4")
    issued = _parse_time(manifest["issuedAt"], "issuedAt")
    expires = _parse_time(manifest["expiresAt"], "expiresAt")
    now = now.astimezone(timezone.utc).replace(microsecond=0)
    if issued > now or expires <= now or expires <= issued or expires - issued > MAX_VALIDITY:
        raise LocalReleaseError("local approval timestamps are invalid or expired")


def prepare_approval(
    bundle_path: Path,
    source_sha: str,
    repo_root: Path = ROOT,
    main_sha_getter=None,
    now: datetime | None = None,
) -> Path:
    source_sha = assert_authority(source_sha, repo_root, require_main=True, main_sha_getter=main_sha_getter)
    bundle = _bundle_path(bundle_path, repo_root)
    _ensure_bundle_layout(bundle)
    if (bundle / "approval.json").exists() or (bundle / "approval.json").is_symlink():
        raise LocalReleaseError("approval manifest already exists; refusing to overwrite")
    if (bundle / "approval.sig").exists() or (bundle / "approval.sig").is_symlink():
        raise LocalReleaseError("detached signature already exists")
    context = _load_context(bundle)
    if context["sourceSha"] != source_sha or context["toolingSha"] != source_sha:
        raise LocalReleaseError("release bundle does not match the current main source and tooling")
    _validate_bundle_content(bundle, source_sha)
    inventories = artifact_inventories(bundle)
    receipts = validate_receipts(bundle, context, inventories, now)
    if set(receipts) != REQUIRED_GATES:
        raise LocalReleaseError("required release gates are incomplete")
    evidence = _validate_evidence_files(bundle)
    issued = (now or _now()).astimezone(timezone.utc).replace(microsecond=0)
    _, _, fingerprint = release_approval._trusted_key(repo_root)
    manifest = {
        "schema": MANIFEST_SCHEMA,
        "origin": "local",
        "repository": REPOSITORY,
        "action": "publish",
        "version": VERSION,
        "sourceSha": source_sha,
        "toolingSha": source_sha,
        "buildId": context["buildId"],
        "keyFingerprint": fingerprint,
        "issuedAt": _format_time(issued),
        "expiresAt": _format_time(issued + MAX_VALIDITY),
        "approvalId": str(uuid.uuid4()),
        "targets": _expected_targets(),
        "artifacts": inventories,
        "artifactInventorySha256": artifact_inventory_digest(inventories),
        "evidence": evidence,
        "gates": sorted(REQUIRED_GATES),
    }
    approval_bytes = _canonical_json(manifest)
    if len(approval_bytes) > release_approval.MAX_MANIFEST_BYTES:
        raise LocalReleaseError("approval manifest exceeds 64 KiB")
    _validate_manifest(manifest, context, inventories, evidence, issued, trusted_root=repo_root)
    output = bundle / "approval.json"
    _exclusive_write(output, approval_bytes)
    return output


def verify_approval(
    bundle_path: Path,
    source_sha: str,
    repo_root: Path = ROOT,
    main_sha_getter=None,
    now: datetime | None = None,
) -> dict:
    source_sha = assert_authority(source_sha, repo_root, require_main=True, main_sha_getter=main_sha_getter)
    bundle = _bundle_path(bundle_path, repo_root)
    _ensure_bundle_layout(bundle, signed=True)
    context = _load_context(bundle)
    if context["sourceSha"] != source_sha or context["toolingSha"] != source_sha:
        raise LocalReleaseError("release bundle source/tooling SHA does not match current main")
    _validate_bundle_content(bundle, source_sha)
    manifest_path = bundle / "approval.json"
    signature_path = bundle / "approval.sig"
    if manifest_path.is_symlink() or signature_path.is_symlink():
        raise LocalReleaseError("approval files must not be symlinks")
    try:
        descriptor, file_stat = _open_regular(manifest_path)
        if file_stat.st_size > release_approval.MAX_MANIFEST_BYTES:
            os.close(descriptor)
            raise LocalReleaseError("approval manifest exceeds 64 KiB")
        with os.fdopen(descriptor, "rb") as source:
            manifest_bytes = source.read(release_approval.MAX_MANIFEST_BYTES + 1)
        signature_descriptor, signature_stat = _open_regular(signature_path)
        if signature_stat.st_size != 88:
            os.close(signature_descriptor)
            raise LocalReleaseError("detached base64 Ed25519 signature must be 88 bytes")
        with os.fdopen(signature_descriptor, "rb") as source:
            signature_text = source.read(89).decode("ascii")
    except (OSError, UnicodeDecodeError, release_approval.ApprovalError) as error:
        raise LocalReleaseError("approval files cannot be read safely") from error
    manifest = release_approval._load_json(manifest_bytes)
    try:
        signature = release_approval._decode_signature(signature_text)
        key_path, _, _ = release_approval._trusted_key(repo_root)
        release_approval._verify_signature(key_path, manifest_bytes, signature)
    except release_approval.ApprovalError as error:
        raise LocalReleaseError("local approval signature verification failed") from error
    inventories = artifact_inventories(bundle)
    current_evidence = _validate_evidence_files(bundle)
    _validate_manifest(
        manifest,
        context,
        inventories,
        current_evidence,
        now or _now(),
        trusted_root=repo_root,
    )
    validate_receipts(bundle, context, inventories, now)
    return manifest


def _load_rules_commit(repo_root: Path = ROOT) -> str:
    lock = _load_json(repo_root / ".github" / "security-rules.lock.json", "Semgrep rules lock")
    commit = lock.get("commit")
    if not isinstance(commit, str) or not SHA_PATTERN.fullmatch(commit):
        raise LocalReleaseError("Semgrep rules lock commit is invalid")
    return commit


def _check_cli(args) -> int:
    run_checks(args.bundle, args.source_sha, args.rules_source)
    return 0


def _validate_artifacts_cli(args) -> int:
    _validate_built_artifacts(Path(args.bundle), args.source_sha)
    print("Artifact consistency checks passed.")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    build = subparsers.add_parser("build", help="build once; never publishes")
    build.add_argument("--source-sha", required=True)
    build.add_argument("--bundle", type=Path, required=True)
    check = subparsers.add_parser("check", help="run all fixed local release gates; never publishes")
    check.add_argument("--source-sha", required=True)
    check.add_argument("--bundle", type=Path, required=True)
    check.add_argument("--rules-source", type=Path, required=True)
    import_parser = subparsers.add_parser("import-acceptance", help="import operator-attested host evidence")
    import_parser.add_argument("--bundle", type=Path, required=True)
    import_parser.add_argument("--report", type=Path, required=True)
    import_parser.add_argument("--evidence-dir", type=Path, required=True)
    prepare = subparsers.add_parser("prepare", help="create unsigned local approval JSON")
    prepare.add_argument("--source-sha", required=True)
    prepare.add_argument("--bundle", type=Path, required=True)
    verify = subparsers.add_parser("verify", help="verify signature, current main, receipts, and inventories")
    verify.add_argument("--source-sha", required=True)
    verify.add_argument("--bundle", type=Path, required=True)
    publish = subparsers.add_parser("publish", help="publish only after local offline approval")
    publish.add_argument("--source-sha", required=True)
    publish.add_argument("--bundle", type=Path, required=True)
    publish.add_argument("--authfile", type=Path, required=True)
    validate = subparsers.add_parser("validate-artifacts", help=argparse.SUPPRESS)
    validate.add_argument("--bundle", type=Path, required=True)
    validate.add_argument("--source-sha", required=True)
    args = parser.parse_args()
    try:
        if args.command == "build":
            path = build_bundle(args.source_sha, args.bundle)
            print(f"Built release bundle: {path}")
        elif args.command == "check":
            _check_cli(args)
        elif args.command == "import-acceptance":
            import_acceptance(args.bundle, args.report, args.evidence_dir)
            print("Imported operator-attested acceptance evidence.")
        elif args.command == "prepare":
            path = prepare_approval(args.bundle, args.source_sha)
            print(f"Created unsigned local approval: {path}")
        elif args.command == "verify":
            verify_approval(args.bundle, args.source_sha)
            print("Verified local offline approval and immutable inventories.")
        elif args.command == "publish":
            from release_local_publisher import publish_release

            result = publish_release(
                args.bundle,
                args.source_sha,
                authfile_path=args.authfile,
            )
            print(f"Publication completed: {result}")
        else:
            return _validate_artifacts_cli(args)
    except (LocalReleaseError, OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print(f"Local release blocked: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
