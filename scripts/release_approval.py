#!/usr/bin/env python3
"""Create and verify signed offline release approvals."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import math
import os
import re
import stat
import subprocess
import sys
import tempfile
import uuid
from datetime import datetime, timedelta, timezone
from pathlib import Path, PurePosixPath

import release_artifacts
from release_policy import validate_inputs


REPOSITORY = "maomada/KubeAtlas"
TRUSTED_ROOT = Path(__file__).resolve().parents[1]
PUBLIC_KEY = ".github/release-signing-public.pem"
WORKFLOWS = {
    "ghcr": ".github/workflows/release.yml",
    "binaries": ".github/workflows/release-binaries.yml",
}
MAX_MANIFEST_BYTES = 64 * 1024
MAX_VALIDITY = timedelta(days=7)
ED25519_SPKI_PREFIX = bytes.fromhex("302a300506032b6570032100")
SHA256_PATTERN = re.compile(r"[0-9a-f]{64}")
SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
RUN_ID_PATTERN = re.compile(r"[1-9][0-9]{0,19}")
CHECK_URL_PATTERN = re.compile(
    r"https://github\.com/maomada/KubeAtlas/actions/runs/[1-9][0-9]*"
)
TIMESTAMP_PATTERN = re.compile(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z")


class ApprovalError(ValueError):
    pass


def _reject_constant(value: str):
    raise ApprovalError(f"non-finite JSON value is not permitted: {value}")


def _unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ApprovalError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _finite_float(value: str) -> float:
    parsed = float(value)
    if not math.isfinite(parsed):
        raise ApprovalError("non-finite JSON value is not permitted")
    return parsed


def _load_json(data: bytes) -> dict:
    if len(data) > MAX_MANIFEST_BYTES:
        raise ApprovalError("approval manifest exceeds 64 KiB")
    try:
        manifest = json.loads(
            data.decode("utf-8"),
            object_pairs_hook=_unique_object,
            parse_constant=_reject_constant,
            parse_float=_finite_float,
        )
    except (UnicodeDecodeError, json.JSONDecodeError, RecursionError) as error:
        raise ApprovalError("approval manifest is not valid UTF-8 JSON") from error
    if not isinstance(manifest, dict):
        raise ApprovalError("approval manifest must be a JSON object")
    return manifest


def _require_exact_keys(value: object, keys: set[str], name: str) -> dict:
    if not isinstance(value, dict) or set(value) != keys:
        raise ApprovalError(f"invalid {name} schema")
    return value


def _safe_path(value: str) -> None:
    path = PurePosixPath(value)
    if (
        not value
        or "\\" in value
        or path.is_absolute()
        or any(part in ("", ".", "..") for part in path.parts)
        or path.as_posix() != value
    ):
        raise ApprovalError(f"unsafe artifact path: {value!r}")


def expected_artifact_paths(channel: str, version: str) -> list[str]:
    if channel == "ghcr":
        paths = []
        for component in ("kubeatlas", "kubeatlas-portal"):
            directory = f"images/release-image-{component}"
            paths.extend(
                (
                    f"{directory}/{component}.oci.tar",
                    f"{directory}/{component}.oci.tar.sha256",
                    f"{directory}/{component}.metadata.json",
                )
            )
        paths.extend(
            (
                f"charts/kubeatlas-{version}.tgz",
                f"charts/kubeatlas-portal-{version}.tgz",
                "charts/SHA256SUMS",
            )
        )
        return sorted(paths)
    if channel == "binaries":
        return release_artifacts.local_publication_names(version, profile="local-linux")
    raise ApprovalError("unsupported release channel")


def expected_targets(channel: str, version: str) -> list[str]:
    if channel == "ghcr":
        return [
            f"ghcr.io/maomada/kubeatlas:{version}",
            f"ghcr.io/maomada/kubeatlas-portal:{version}",
            f"ghcr.io/maomada/charts/kubeatlas:{version}",
            f"ghcr.io/maomada/charts/kubeatlas-portal:{version}",
        ]
    if channel == "binaries":
        return [f"github-release:v{version}", *expected_artifact_paths(channel, version)]
    raise ApprovalError("unsupported release channel")


def _open_regular(path: Path):
    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise ApprovalError(f"cannot safely open artifact: {path.name}") from error
    file_stat = os.fstat(descriptor)
    if not stat.S_ISREG(file_stat.st_mode):
        os.close(descriptor)
        raise ApprovalError(f"artifact is not a regular file: {path.name}")
    return descriptor, file_stat.st_size


def _hash_file(path: Path) -> tuple[int, str]:
    descriptor, size = _open_regular(path)
    digest = hashlib.sha256()
    try:
        with os.fdopen(descriptor, "rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise ApprovalError(f"cannot read artifact: {path.name}") from error
    return size, digest.hexdigest()


def _inventory(root: Path, channel: str, version: str) -> list[dict[str, object]]:
    if root.is_symlink() or not root.is_dir():
        raise ApprovalError("artifact root must be a real directory")
    expected = expected_artifact_paths(channel, version)
    paths = []
    pending = [root]
    while pending:
        directory = pending.pop()
        if directory.is_symlink():
            raise ApprovalError("artifact directories must not be symlinks")
        try:
            entries = sorted(os.scandir(directory), key=lambda entry: entry.name)
        except OSError as error:
            raise ApprovalError("artifact directory cannot be read") from error
        for entry in entries:
            path = Path(entry.path)
            try:
                mode = entry.stat(follow_symlinks=False).st_mode
            except OSError as error:
                raise ApprovalError("artifact entry cannot be inspected") from error
            if stat.S_ISLNK(mode):
                raise ApprovalError(f"artifact symlink is not permitted: {entry.name}")
            if stat.S_ISDIR(mode):
                pending.append(path)
            elif stat.S_ISREG(mode):
                relative = path.relative_to(root).as_posix()
                _safe_path(relative)
                paths.append((relative, path))
            else:
                raise ApprovalError(f"artifact entry is not a regular file: {entry.name}")
    names = sorted(relative for relative, _ in paths)
    if names != expected:
        raise ApprovalError(
            f"artifact set mismatch: expected {expected!r}, got {names!r}"
        )
    inventory = []
    for relative, path in sorted(paths):
        size, digest = _hash_file(path)
        inventory.append({"path": relative, "size": size, "sha256": digest})
    return inventory


def _trusted_key(root: Path) -> tuple[Path, bytes, str]:
    trusted_root = root.resolve()
    key_path = root / PUBLIC_KEY
    if root.is_symlink() or key_path.is_symlink() or not key_path.is_file():
        raise ApprovalError("trusted release-signing public key is missing or unsafe")
    try:
        result = subprocess.run(
            ["openssl", "pkey", "-pubin", "-in", str(key_path), "-outform", "DER"],
            check=True,
            capture_output=True,
        )
    except FileNotFoundError as error:
        raise ApprovalError("OpenSSL is required for release approval") from error
    except subprocess.CalledProcessError as error:
        raise ApprovalError("trusted release-signing public key is invalid") from error
    der = result.stdout
    if len(der) != 44 or not der.startswith(ED25519_SPKI_PREFIX):
        raise ApprovalError("trusted release-signing key must be Ed25519")
    try:
        key_path.resolve(strict=True).relative_to(trusted_root)
    except (OSError, ValueError) as error:
        raise ApprovalError("trusted release-signing public key escapes trusted checkout") from error
    return key_path, der, hashlib.sha256(der).hexdigest()


def _workflow_binding(channel: str, environment: dict[str, str]) -> tuple[str, str]:
    workflow = WORKFLOWS.get(channel)
    if workflow is None:
        raise ApprovalError("unsupported release channel")
    expected_ref = f"{REPOSITORY}/{workflow}@refs/heads/main"
    if (
        environment.get("GITHUB_REPOSITORY") != REPOSITORY
        or environment.get("GITHUB_REF") != "refs/heads/main"
        or environment.get("WORKFLOW_REF") != expected_ref
    ):
        raise ApprovalError("release approval must be created or verified by the trusted main workflow")
    workflow_sha = environment.get("WORKFLOW_SHA", "")
    if not SHA_PATTERN.fullmatch(workflow_sha):
        raise ApprovalError("workflow SHA must be a full lowercase commit SHA")
    if environment.get("GITHUB_SHA") != workflow_sha:
        raise ApprovalError("workflow SHA does not match the current workflow run")
    return workflow, workflow_sha


def _validate_check_url(value: object, name: str) -> str:
    if not isinstance(value, str) or not CHECK_URL_PATTERN.fullmatch(value):
        raise ApprovalError(f"invalid {name} Actions evidence URL")
    return value


def _timestamp(value: object, name: str) -> datetime:
    if not isinstance(value, str) or not TIMESTAMP_PATTERN.fullmatch(value):
        raise ApprovalError(f"invalid approval {name} timestamp")
    try:
        parsed = datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
    except ValueError as error:
        raise ApprovalError(f"invalid approval {name} timestamp") from error
    if parsed.strftime("%Y-%m-%dT%H:%M:%SZ") != value:
        raise ApprovalError(f"invalid approval {name} timestamp")
    return parsed


def _validate_manifest(
    manifest: dict,
    channel: str,
    source_sha: str,
    version: str,
    build_run_id: str,
    key_fingerprint: str,
    now: datetime,
) -> None:
    _require_exact_keys(
        manifest,
        {
            "schemaVersion",
            "repository",
            "action",
            "channel",
            "sourceSha",
            "version",
            "workflow",
            "build",
            "approvalId",
            "issuedAt",
            "expiresAt",
            "keyFingerprint",
            "checks",
            "targets",
            "artifacts",
        },
        "approval manifest",
    )
    if type(manifest["schemaVersion"]) is not int or manifest["schemaVersion"] != 1:
        raise ApprovalError("unsupported approval schema version")
    if manifest["repository"] != REPOSITORY or manifest["action"] != "publish":
        raise ApprovalError("approval repository or action mismatch")
    if manifest["channel"] != channel or channel not in WORKFLOWS:
        raise ApprovalError("approval channel mismatch")
    if not isinstance(source_sha, str) or not SHA_PATTERN.fullmatch(source_sha):
        raise ApprovalError("selected source must be a full lowercase commit SHA")
    if manifest["sourceSha"] != source_sha:
        raise ApprovalError("approval source SHA mismatch")
    if not isinstance(version, str):
        raise ApprovalError("selected version must be a string")
    try:
        validate_inputs(REPOSITORY, "refs/heads/main", manifest["sourceSha"], manifest["version"])
    except (TypeError, ValueError) as error:
        raise ApprovalError("approval source or version is invalid") from error
    if manifest["version"] != version:
        raise ApprovalError("approval version mismatch")
    if manifest["workflow"] != WORKFLOWS[channel]:
        raise ApprovalError("approval workflow mismatch")
    build = _require_exact_keys(
        manifest["build"], {"runId", "runAttempt", "workflowSha"}, "approval build"
    )
    run_id = build["runId"]
    if not isinstance(run_id, str) or not RUN_ID_PATTERN.fullmatch(run_id):
        raise ApprovalError("approval build run ID must be a positive decimal string")
    if type(build["runAttempt"]) is not int or build["runAttempt"] <= 0:
        raise ApprovalError("approval build attempt must be a positive integer")
    workflow_sha = build["workflowSha"]
    if not isinstance(workflow_sha, str) or not SHA_PATTERN.fullmatch(workflow_sha):
        raise ApprovalError("approval workflow SHA must be a full lowercase commit SHA")
    if run_id != build_run_id:
        raise ApprovalError("approval build run ID mismatch")
    if manifest["keyFingerprint"] != key_fingerprint:
        raise ApprovalError("approval public-key fingerprint mismatch")
    approval_id = manifest["approvalId"]
    if not isinstance(approval_id, str):
        raise ApprovalError("approval ID must be a UUID4")
    try:
        parsed_id = uuid.UUID(approval_id)
    except (ValueError, AttributeError) as error:
        raise ApprovalError("approval ID must be a UUID4") from error
    if parsed_id.version != 4 or str(parsed_id) != approval_id:
        raise ApprovalError("approval ID must be a canonical UUID4")
    issued_at = _timestamp(manifest["issuedAt"], "issuedAt")
    expires_at = _timestamp(manifest["expiresAt"], "expiresAt")
    now = now.astimezone(timezone.utc).replace(microsecond=0)
    if issued_at > now:
        raise ApprovalError("approval was issued in the future")
    if expires_at <= now:
        raise ApprovalError("approval has expired")
    if expires_at <= issued_at or expires_at - issued_at > MAX_VALIDITY:
        raise ApprovalError("approval validity must be positive and no longer than seven days")
    checks = _require_exact_keys(manifest["checks"], {"ci", "security"}, "approval checks")
    for name in ("ci", "security"):
        _validate_check_url(checks[name], name)
    targets = manifest["targets"]
    if not isinstance(targets, list) or any(not isinstance(item, str) for item in targets):
        raise ApprovalError("approval targets must be a string list")
    if targets != expected_targets(channel, version):
        raise ApprovalError("approval targets do not match the trusted release profile")
    artifacts = manifest["artifacts"]
    if not isinstance(artifacts, list):
        raise ApprovalError("approval artifacts must be a list")
    artifact_paths = []
    for item in artifacts:
        artifact = _require_exact_keys(item, {"path", "size", "sha256"}, "approval artifact")
        if not isinstance(artifact["path"], str):
            raise ApprovalError("approval artifact path must be a string")
        _safe_path(artifact["path"])
        if type(artifact["size"]) is not int or artifact["size"] < 0:
            raise ApprovalError("approval artifact size must be a nonnegative integer")
        if not isinstance(artifact["sha256"], str) or not SHA256_PATTERN.fullmatch(artifact["sha256"]):
            raise ApprovalError("approval artifact SHA-256 is invalid")
        artifact_paths.append(artifact["path"])
    if artifact_paths != sorted(expected_artifact_paths(channel, version)):
        raise ApprovalError("approval artifact inventory does not match the trusted release profile")
    if len(artifact_paths) != len(set(artifact_paths)):
        raise ApprovalError("duplicate approval artifact path")


def create_approval(
    channel: str,
    artifact_root: Path,
    output: Path,
    source_sha: str,
    version: str,
    ci_url: str,
    security_url: str,
    environment: dict[str, str] | None = None,
    now: datetime | None = None,
    trusted_root: Path | None = None,
) -> dict:
    environment = dict(os.environ if environment is None else environment)
    workflow, workflow_sha = _workflow_binding(channel, environment)
    try:
        validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    except (TypeError, ValueError) as error:
        raise ApprovalError("selected source or version is invalid") from error
    run_id = environment.get("GITHUB_RUN_ID", "")
    run_attempt_text = environment.get("GITHUB_RUN_ATTEMPT", "")
    if not RUN_ID_PATTERN.fullmatch(run_id):
        raise ApprovalError("current workflow run ID must be a positive decimal string")
    if not re.fullmatch(r"[1-9][0-9]*", run_attempt_text):
        raise ApprovalError("current workflow run attempt must be a positive integer")
    try:
        run_attempt = int(run_attempt_text)
    except ValueError as error:
        raise ApprovalError("current workflow run attempt must be a positive integer") from error
    checks = {
        "ci": _validate_check_url(ci_url, "ci"),
        "security": _validate_check_url(security_url, "security"),
    }
    trusted_root = TRUSTED_ROOT if trusted_root is None else trusted_root
    _, _, fingerprint = _trusted_key(trusted_root)
    inventory = _inventory(artifact_root, channel, version)
    if _is_within(output, artifact_root):
        raise ApprovalError("approval manifest must be outside the signed artifact directory")
    issued_at = (now or datetime.now(timezone.utc)).astimezone(timezone.utc).replace(microsecond=0)
    expires_at = issued_at + MAX_VALIDITY
    manifest = {
        "schemaVersion": 1,
        "repository": REPOSITORY,
        "action": "publish",
        "channel": channel,
        "sourceSha": source_sha,
        "version": version,
        "workflow": workflow,
        "build": {
            "runId": run_id,
            "runAttempt": run_attempt,
            "workflowSha": workflow_sha,
        },
        "approvalId": str(uuid.uuid4()),
        "issuedAt": issued_at.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "expiresAt": expires_at.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "keyFingerprint": fingerprint,
        "checks": checks,
        "targets": expected_targets(channel, version),
        "artifacts": inventory,
    }
    data = json.dumps(manifest, indent=2, sort_keys=True, allow_nan=False).encode("utf-8") + b"\n"
    _load_json(data)
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    if output.exists() or output.is_symlink() or _is_within(output, artifact_root):
        raise ApprovalError("approval output already exists or is inside the signed artifact directory")
    try:
        descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(descriptor, "wb") as destination:
            destination.write(data)
    except OSError as error:
        raise ApprovalError("cannot write approval manifest") from error
    return manifest


def _is_within(path: Path, directory: Path) -> bool:
    try:
        Path(path).resolve().relative_to(Path(directory).resolve())
    except (OSError, ValueError):
        return False
    return True


def _decode_signature(value: str) -> bytes:
    try:
        signature = base64.b64decode(value, validate=True)
    except (ValueError, base64.binascii.Error) as error:
        raise ApprovalError("approval signature is not strict base64") from error
    if len(signature) != 64:
        raise ApprovalError("Ed25519 approval signature must be exactly 64 bytes")
    return signature


def _verify_signature(key_path: Path, manifest_bytes: bytes, signature: bytes) -> None:
    try:
        with tempfile.TemporaryDirectory(prefix="release-approval-") as directory:
            manifest_path = Path(directory) / "approval.json"
            signature_path = Path(directory) / "approval.sig"
            manifest_path.write_bytes(manifest_bytes)
            signature_path.write_bytes(signature)
            subprocess.run(
                [
                    "openssl",
                    "pkeyutl",
                    "-verify",
                    "-rawin",
                    "-pubin",
                    "-inkey",
                    str(key_path),
                    "-in",
                    str(manifest_path),
                    "-sigfile",
                    str(signature_path),
                ],
                check=True,
                capture_output=True,
                text=True,
            )
    except FileNotFoundError as error:
        raise ApprovalError("OpenSSL is required for release approval") from error
    except subprocess.CalledProcessError as error:
        raise ApprovalError("approval signature verification failed") from error


def _validate_current_context(channel: str, workflow_sha: str, environment: dict[str, str]) -> None:
    workflow, current_workflow_sha = _workflow_binding(channel, environment)
    if current_workflow_sha != workflow_sha:
        raise ApprovalError("approval was created by a different trusted workflow SHA")
    attempt = environment.get("GITHUB_RUN_ATTEMPT", "")
    if attempt != "1":
        raise ApprovalError("publication workflow reruns are not permitted")
    if workflow not in WORKFLOWS.values():
        raise ApprovalError("current release workflow is not trusted")


def query_build_run(run_id: str, environment: dict[str, str] | None = None) -> dict:
    environment = os.environ if environment is None else environment
    if not (environment.get("GH_TOKEN") or environment.get("GITHUB_TOKEN")):
        raise ApprovalError("GH_TOKEN is required to verify the signed build run")
    try:
        result = subprocess.run(
            ["gh", "api", f"repos/{REPOSITORY}/actions/runs/{run_id}"],
            check=True,
            capture_output=True,
            text=True,
            env=dict(environment),
        )
    except (FileNotFoundError, subprocess.CalledProcessError) as error:
        raise ApprovalError("read-only build-run API query failed") from error
    try:
        response = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise ApprovalError("build-run API returned malformed JSON") from error
    if not isinstance(response, dict):
        raise ApprovalError("build-run API response must be an object")
    return response


def _validate_build_run(run: dict, manifest: dict) -> None:
    build = manifest["build"]
    expected = {
        "id": int(build["runId"]),
        "status": "completed",
        "conclusion": "success",
        "event": "workflow_dispatch",
        "head_branch": "main",
        "run_attempt": build["runAttempt"],
        "head_sha": build["workflowSha"],
    }
    for key, value in expected.items():
        actual = run.get(key)
        if type(value) is int and type(actual) is not int:
            raise ApprovalError(f"build-run API evidence has invalid {key}")
        if actual != value:
            raise ApprovalError(f"build-run API evidence does not match signed {key}")
    if run.get("path") not in {
        manifest["workflow"],
        f"{manifest['workflow']}@refs/heads/main",
    }:
        raise ApprovalError("build-run API evidence does not match signed workflow path")
    for key in ("repository", "head_repository"):
        repository = run.get(key)
        if not isinstance(repository, dict) or repository.get("full_name") != REPOSITORY:
            raise ApprovalError(f"build-run API evidence has invalid {key}")


def verify_approval(
    channel: str,
    manifest_path: Path,
    artifact_root: Path,
    source_sha: str,
    version: str,
    build_run_id: str,
    signature_base64: str,
    signature_output: Path | None = None,
    environment: dict[str, str] | None = None,
    now: datetime | None = None,
    run_evidence: dict | None = None,
    trusted_root: Path | None = None,
) -> dict:
    environment = dict(os.environ if environment is None else environment)
    manifest_path = Path(manifest_path)
    artifact_root = Path(artifact_root)
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise ApprovalError("signed approval manifest is missing or unsafe")
    if _is_within(manifest_path, artifact_root):
        raise ApprovalError("signed approval manifest must be outside the artifact directory")
    try:
        descriptor, _ = _open_regular(manifest_path)
        with os.fdopen(descriptor, "rb") as source:
            manifest_bytes = source.read(MAX_MANIFEST_BYTES + 1)
    except ApprovalError:
        raise
    except OSError as error:
        raise ApprovalError("signed approval manifest cannot be read") from error
    if len(manifest_bytes) > MAX_MANIFEST_BYTES:
        raise ApprovalError("approval manifest exceeds 64 KiB")
    manifest = _load_json(manifest_bytes)
    signature = _decode_signature(signature_base64)
    if signature_output is not None:
        signature_output = Path(signature_output)
        if _is_within(signature_output, artifact_root):
            raise ApprovalError("decoded signature evidence must be outside the artifact directory")
        if signature_output.exists() or signature_output.is_symlink():
            raise ApprovalError("decoded signature evidence already exists or is unsafe")
        signature_output.parent.mkdir(parents=True, exist_ok=True)
        try:
            descriptor = os.open(
                signature_output,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                0o600,
            )
            with os.fdopen(descriptor, "wb") as destination:
                destination.write(signature)
        except OSError as error:
            raise ApprovalError("decoded signature evidence cannot be written") from error
    trusted_root = TRUSTED_ROOT if trusted_root is None else trusted_root
    key_path, _, fingerprint = _trusted_key(trusted_root)
    _verify_signature(key_path, manifest_bytes, signature)
    _validate_manifest(
        manifest,
        channel,
        source_sha,
        version,
        build_run_id,
        fingerprint,
        now or datetime.now(timezone.utc),
    )
    _validate_current_context(channel, manifest["build"]["workflowSha"], environment)
    if run_evidence is None:
        run_evidence = query_build_run(manifest["build"]["runId"], environment)
    elif callable(run_evidence):
        run_evidence = run_evidence(manifest["build"]["runId"])
    if not isinstance(run_evidence, dict):
        raise ApprovalError("build-run API evidence must be an object")
    _validate_build_run(run_evidence, manifest)
    current_inventory = _inventory(artifact_root, channel, version)
    if current_inventory != manifest["artifacts"]:
        raise ApprovalError("artifact inventory does not match the signed approval")
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    create = subparsers.add_parser("create")
    create.add_argument("--channel", choices=tuple(WORKFLOWS), required=True)
    create.add_argument("--artifact-root", type=Path, required=True)
    create.add_argument("--output", type=Path, required=True)
    create.add_argument("--source-sha", required=True)
    create.add_argument("--version", required=True)
    create.add_argument("--ci-url", required=True)
    create.add_argument("--security-url", required=True)
    verify = subparsers.add_parser("verify")
    verify.add_argument("--channel", choices=tuple(WORKFLOWS), required=True)
    verify.add_argument("--manifest", type=Path, required=True)
    verify.add_argument("--artifact-root", type=Path, required=True)
    verify.add_argument("--source-sha", required=True)
    verify.add_argument("--version", required=True)
    verify.add_argument("--signature-output", type=Path)
    verify.add_argument("--build-run-id", default=os.environ.get("BUILD_RUN_ID"))
    args = parser.parse_args()
    try:
        if args.command == "create":
            create_approval(
                args.channel,
                args.artifact_root,
                args.output,
                args.source_sha,
                args.version,
                args.ci_url,
                args.security_url,
            )
            print(f"Created {args.output.name} for {args.channel} dry run")
        else:
            signature = os.environ.get("APPROVAL_SIGNATURE", "")
            if not args.build_run_id:
                raise ApprovalError("BUILD_RUN_ID is required")
            verify_approval(
                args.channel,
                args.manifest,
                args.artifact_root,
                args.source_sha,
                args.version,
                args.build_run_id,
                signature,
                signature_output=args.signature_output,
            )
            print(f"Verified offline approval for {args.channel} publication")
    except (ApprovalError, OSError, ValueError, KeyError, TypeError) as error:
        print(f"Release approval blocked: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
