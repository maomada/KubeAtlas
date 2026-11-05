#!/usr/bin/env python3
"""Fail-closed publisher for a locally signed KubeAtlas release bundle."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import release_approval
import release_github
import release_registry
import release_local

REPOSITORY = release_local.REPOSITORY
VERSION = release_local.VERSION


class PublisherError(release_local.LocalReleaseError):
    pass


def _runner_result(runner, argv: list[str], timeout: int = 300):
    try:
        result = runner(
            argv,
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise PublisherError("publisher command could not be completed; publication state may be unknown") from error
    if result.returncode != 0:
        raise PublisherError("publisher command failed; publication state may be unknown")
    return result


def _gh(runner, args: list[str], timeout: int = 120) -> str:
    result = _runner_result(runner, ["gh", *args], timeout)
    return result.stdout


def _canonical(value: dict) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False) + "\n").encode()


def _append_journal(path: Path, event: str, target: str, source_sha: str) -> None:
    record = _canonical(
        {
            "time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "event": event,
            "target": target,
            "sourceSha": source_sha,
        }
    )
    descriptor = os.open(path, os.O_WRONLY | os.O_APPEND | getattr(os, "O_NOFOLLOW", 0))
    try:
        offset = 0
        while offset < len(record):
            offset += os.write(descriptor, record[offset:])
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _create_attempt(journal_dir: Path, bundle: Path, source_sha: str) -> tuple[Path, Path]:
    release_local._assert_no_symlink_ancestors(journal_dir)
    journal_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    if journal_dir.is_symlink() or not journal_dir.is_dir():
        raise PublisherError("publisher journal directory is unsafe")
    try:
        journal_dir.resolve(strict=True).relative_to(bundle.resolve(strict=True))
    except ValueError:
        pass
    else:
        raise PublisherError("publisher journal must be outside the release bundle")
    journal = journal_dir / f"publish-v{VERSION}.jsonl"
    lock = journal_dir / f"publish-v{VERSION}.lock"
    try:
        lock_descriptor = os.open(lock, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError as error:
        raise PublisherError("publisher lock already exists; inspect publication state manually") from error
    try:
        os.fsync(lock_descriptor)
    finally:
        os.close(lock_descriptor)
    try:
        descriptor = os.open(journal, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError as error:
        lock.unlink()
        raise PublisherError("this version already has a publication attempt; retries are forbidden") from error
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)
    directory_descriptor = os.open(journal_dir, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
    try:
        os.fsync(directory_descriptor)
    finally:
        os.close(directory_descriptor)
    _append_journal(journal, "attempt-started", "local-publisher", source_sha)
    return journal, lock


def _authfile(path: Path) -> Path:
    release_local._assert_no_symlink_ancestors(path)
    try:
        descriptor, file_stat = release_local._open_regular(path)
    except release_local.LocalReleaseError as error:
        raise PublisherError("GHCR authfile is missing or unsafe") from error
    finally:
        if "descriptor" in locals():
            os.close(descriptor)
    if file_stat.st_mode & 0o077:
        raise PublisherError("GHCR authfile must not be readable by group or other users")
    return path.resolve(strict=True)


def _artifact_map(manifest: dict, bundle: Path, channel: str) -> dict[str, tuple[Path, str]]:
    entries = manifest["artifacts"][channel]
    result = {}
    for item in entries:
        relative = item["path"]
        try:
            release_approval._safe_path(relative)
        except release_approval.ApprovalError as error:
            raise PublisherError("signed artifact inventory contains an unsafe path") from error
        if relative in result or not isinstance(item["sha256"], str):
            raise PublisherError("signed artifact inventory is malformed")
        result[relative] = (bundle / channel / relative, item["sha256"])
    return result


def _checked_bytes(path: Path, expected_sha: str) -> bytes:
    data = release_local._read_regular(path)
    if not data or hashlib.sha256(data).hexdigest() != expected_sha:
        raise PublisherError("release artifact changed after signature verification")
    return data


def _run_and_journal(
    runner,
    verify,
    journal: Path,
    source_sha: str,
    gate: str,
    target: str,
    action,
):
    verify()
    _append_journal(journal, f"intent:{gate}", target, source_sha)
    try:
        result = action()
    except Exception as error:
        _append_journal(journal, f"unknown:{gate}", target, source_sha)
        if isinstance(error, PublisherError):
            raise
        raise PublisherError("remote write failed or has an unknown result; do not retry") from error
    _append_journal(journal, f"complete:{gate}", target, source_sha)
    return result


def _verify_image(runner, authfile: Path, archive: Path, ref: str, expected_digest: str) -> None:
    local = _runner_result(
        runner,
        ["skopeo", "inspect", "--raw", "--authfile", str(authfile), f"oci-archive:{archive}"],
    ).stdout.encode()
    if hashlib.sha256(local).hexdigest() != expected_digest.removeprefix("sha256:"):
        raise PublisherError("local OCI manifest digest differs from approved build metadata")
    _runner_result(
        runner,
        [
            "skopeo",
            "copy",
            "--all",
            "--preserve-digests",
            "--authfile",
            str(authfile),
            f"oci-archive:{archive}",
            f"docker://{ref}",
        ],
        timeout=1800,
    )
    remote = _runner_result(
        runner,
        ["skopeo", "inspect", "--raw", "--authfile", str(authfile), f"docker://{ref}"],
    ).stdout.encode()
    if hashlib.sha256(local).digest() != hashlib.sha256(remote).digest():
        raise PublisherError("published image manifest digest differs from the approved OCI archive")


def _verify_chart(runner, authfile: Path, archive: Path, chart: str) -> None:
    _runner_result(
        runner,
        [
            "helm",
            "push",
            str(archive),
            "oci://ghcr.io/maomada/charts",
            "--registry-config",
            str(authfile),
        ],
        timeout=1800,
    )
    with tempfile.TemporaryDirectory(prefix="kubeatlas-chart-verify-") as destination:
        _runner_result(
            runner,
            [
                "helm",
                "pull",
                f"oci://ghcr.io/maomada/charts/{chart}",
                "--version",
                VERSION,
                "--destination",
                destination,
                "--registry-config",
                str(authfile),
            ],
            timeout=300,
        )
        downloaded = Path(destination) / f"{chart}-{VERSION}.tgz"
        expected_size, expected_hash = release_local._hash_file(archive)
        actual_size, actual_hash = release_local._hash_file(downloaded)
        if expected_size != actual_size or expected_hash != actual_hash:
            raise PublisherError("published chart archive differs from the approved package")


def _draft_release(runner, source_sha: str) -> int:
    response = _gh(
        runner,
        [
            "api",
            f"repos/{REPOSITORY}/releases",
            "--method",
            "POST",
            "-f",
            f"tag_name=v{VERSION}",
            "-f",
            f"target_commitish={source_sha}",
            "-f",
            f"name=KubeAtlas v{VERSION}",
            "-F",
            "draft=true",
            "-f",
            "body=Locally built release approved by a detached Ed25519 signature.",
        ],
    )
    try:
        release = release_approval._load_json(response.encode())
        release_id = release["id"]
    except (release_approval.ApprovalError, KeyError, TypeError) as error:
        raise PublisherError("GitHub did not return a valid draft-release record") from error
    if (
        type(release_id) is not int
        or release.get("draft") is not True
        or release.get("tag_name") != f"v{VERSION}"
        or release.get("target_commitish") != source_sha
    ):
        raise PublisherError("GitHub draft release does not bind to the approved source and tag")
    return release_id


def _upload_asset(runner, source: Path, tag: str) -> None:
    _gh(runner, ["release", "upload", tag, str(source), "--repo", REPOSITORY])


def _expose_release(runner, release_id: int, tag: str, source_sha: str) -> None:
    response = _gh(
        runner,
        [
            "api",
            f"repos/{REPOSITORY}/releases/{release_id}",
            "--method",
            "PATCH",
            "-F",
            "draft=false",
        ],
    )
    try:
        release = release_approval._load_json(response.encode())
    except release_approval.ApprovalError as error:
        raise PublisherError("GitHub release exposure response was invalid") from error
    if (
        release.get("draft") is not False
        or release.get("tag_name") != tag
        or release.get("target_commitish") != source_sha
    ):
        raise PublisherError("GitHub release did not become public at the approved tag")


def _verify_remote_asset(runner, tag: str, name: str, expected_sha: str) -> None:
    with tempfile.TemporaryDirectory(prefix="kubeatlas-release-asset-") as destination:
        _gh(
            runner,
            [
                "release",
                "download",
                tag,
                "--repo",
                REPOSITORY,
                "--pattern",
                name,
                "--dir",
                destination,
            ],
        )
        downloaded = Path(destination) / name
        _, actual_sha = release_local._hash_file(downloaded)
        if actual_sha != expected_sha:
            raise PublisherError(f"uploaded GitHub asset hash mismatch: {name}")


def publish_release(
    bundle_path: Path,
    source_sha: str,
    repo_root: Path = release_local.ROOT,
    main_sha_getter=None,
    runner=subprocess.run,
    journal_dir: Path | None = None,
    authfile_path: Path | None = None,
) -> Path:
    bundle = release_local._bundle_path(bundle_path, repo_root)
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if not token:
        raise PublisherError("GH_TOKEN or GITHUB_TOKEN is required for GitHub preflight and publication")
    authfile_value = authfile_path or (
        Path(os.environ["GHCR_AUTHFILE"]) if os.environ.get("GHCR_AUTHFILE") else None
    )
    if authfile_value is None:
        raise PublisherError("--authfile or GHCR_AUTHFILE must name a preauthenticated GHCR authfile")
    authfile = _authfile(Path(authfile_value))
    manifest = release_local.verify_approval(
        bundle, source_sha, repo_root, main_sha_getter=main_sha_getter
    )
    _runner_result(runner, ["gh", "--version"])
    _runner_result(runner, ["skopeo", "--version"])
    _runner_result(runner, ["helm", "version", "--short"])
    try:
        registry_status = release_registry.inspect_targets(VERSION, str(authfile), runner)
        github_status = release_github.preflight(
            VERSION, lambda path: release_github.github_get(path, token)
        )
    except (release_registry.RegistryPreflightError, release_github.ReleasePreflightError) as error:
        raise PublisherError("all-target GitHub/GHCR preflight failed; no remote writes were made") from error
    if len(registry_status) != 4 or any(item["status"] != "absent" for item in registry_status):
        raise PublisherError("GHCR preflight did not prove all four targets absent")
    if len(github_status) != 3 or any(
        item.get("status") not in ("authenticated", "absent") for item in github_status
    ):
        raise PublisherError("GitHub preflight did not prove tag and release absent")
    if main_sha_getter is None:
        main_sha_getter = lambda: release_local.query_main_sha()
    binaries = _artifact_map(manifest, bundle, "binaries")
    ghcr = _artifact_map(manifest, bundle, "ghcr")
    expected_binary_paths = set(release_approval.expected_artifact_paths("binaries", VERSION))
    expected_ghcr_paths = set(release_approval.expected_artifact_paths("ghcr", VERSION))
    if set(binaries) != expected_binary_paths or set(ghcr) != expected_ghcr_paths:
        raise PublisherError("signed inventory does not match the exact supported publication profile")
    journal_dir = journal_dir or (
        Path.home() / ".local" / "state" / "kubeatlas-release"
    )
    journal, lock = _create_attempt(journal_dir, bundle, source_sha)

    def verify():
        release_local.verify_approval(
            bundle,
            source_sha,
            repo_root,
            main_sha_getter=main_sha_getter,
        )

    try:
        for component in ("kubeatlas", "kubeatlas-portal"):
            archive_relative = f"images/release-image-{component}/{component}.oci.tar"
            archive = ghcr[archive_relative][0]
            metadata_relative = f"images/release-image-{component}/{component}.metadata.json"
            metadata = release_local._load_json(ghcr[metadata_relative][0], "image metadata")
            expected_digest = metadata.get("buildDigest", "")
            if (
                metadata.get("component") != component
                or metadata.get("sourceSha") != source_sha
                or metadata.get("version") != VERSION
                or not isinstance(expected_digest, str)
                or not expected_digest.startswith("sha256:")
                or len(expected_digest) != 71
            ):
                raise PublisherError("approved image metadata does not match the release")
            ref = f"ghcr.io/maomada/{component}:{VERSION}"
            _run_and_journal(
                runner,
                verify,
                journal,
                source_sha,
                "ghcr-image",
                ref,
                lambda archive=archive, ref=ref, digest=expected_digest: _verify_image(
                    runner, authfile, archive, ref, digest
                ),
            )
        for chart in ("kubeatlas", "kubeatlas-portal"):
            archive_relative = f"charts/{chart}-{VERSION}.tgz"
            archive = ghcr[archive_relative][0]
            ref = f"ghcr.io/maomada/charts/{chart}:{VERSION}"
            _run_and_journal(
                runner,
                verify,
                journal,
                source_sha,
                "ghcr-chart",
                ref,
                lambda archive=archive, chart=chart: _verify_chart(runner, authfile, archive, chart),
            )
        _run_and_journal(
            runner,
            verify,
            journal,
            source_sha,
            "github-tag",
            f"refs/tags/v{VERSION}",
            lambda: release_github.create_tag_ref(
                VERSION,
                source_sha,
                lambda path, body: release_github.github_post(path, token, body),
            ),
        )
        release_id = _run_and_journal(
            runner,
            verify,
            journal,
            source_sha,
            "github-draft",
            f"v{VERSION}",
            lambda: _draft_release(runner, source_sha),
        )
        for name in sorted(binaries):
            path, digest = binaries[name]
            _checked_bytes(path, digest)

            def upload(path=path, name=name, digest=digest):
                _upload_asset(runner, path, f"v{VERSION}")
                _verify_remote_asset(runner, f"v{VERSION}", name, digest)

            _run_and_journal(
                runner,
                verify,
                journal,
                source_sha,
                "github-asset",
                name,
                upload,
            )
        _run_and_journal(
            runner,
            verify,
            journal,
            source_sha,
            "github-expose",
            f"v{VERSION}",
            lambda: _expose_release(runner, release_id, f"v{VERSION}", source_sha),
        )
        _append_journal(journal, "publication-complete", f"v{VERSION}", source_sha)
    finally:
        try:
            lock.unlink()
        except FileNotFoundError:
            pass
    return journal


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--bundle", type=Path, required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--authfile", type=Path, required=True)
    parser.add_argument("--journal-dir", type=Path)
    args = parser.parse_args()
    try:
        journal = publish_release(
            args.bundle,
            args.source_sha,
            journal_dir=args.journal_dir,
            authfile_path=args.authfile,
        )
        print(f"Publication completed; durable journal: {journal}")
    except (PublisherError, release_local.LocalReleaseError, ValueError) as error:
        print(f"Local publication blocked: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
