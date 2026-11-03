#!/usr/bin/env python3
"""Fail-closed GitHub Release collision preflight and evidence helpers."""

from __future__ import annotations

import argparse
import json
import os
import urllib.error
import urllib.request
from pathlib import Path

from release_policy import validate_inputs

REPOSITORY = "maomada/KubeAtlas"


class ReleasePreflightError(RuntimeError):
    pass


def classify_response(status: int) -> str:
    if status == 404:
        return "absent"
    if status == 200:
        return "present"
    raise ReleasePreflightError(
        f"GitHub Release collision check returned HTTP {status}; absence was not established"
    )


def collision_paths(version: str) -> tuple[str, str]:
    validate_inputs(REPOSITORY, "refs/heads/main", "0" * 40, version)
    tag = f"v{version}"
    return (f"repos/{REPOSITORY}/git/ref/tags/{tag}", f"repos/{REPOSITORY}/releases/tags/{tag}")


def preflight(version: str, getter) -> list[dict[str, object]]:
    repository_path = f"repos/{REPOSITORY}"
    try:
        repository_status = getter(repository_path)
    except Exception as error:
        raise ReleasePreflightError(
            "authenticated canonical repository check failed"
        ) from error
    if repository_status != 200:
        raise ReleasePreflightError(
            f"authenticated canonical repository check returned HTTP {repository_status}; cannot establish absence"
        )
    results = [{"path": repository_path, "status": "authenticated"}]
    collisions = []
    for path in collision_paths(version):
        try:
            status = classify_response(getter(path))
        except ReleasePreflightError:
            raise
        except Exception as error:
            raise ReleasePreflightError(f"GitHub Release collision check failed: {path}") from error
        results.append({"path": path, "status": status})
        if status == "present":
            collisions.append(path)
    if collisions:
        raise ReleasePreflightError("release tag or release already exists: " + ", ".join(collisions))
    return results


def create_tag_ref(version: str, source_sha: str, poster) -> dict[str, str]:
    validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    path = f"repos/{REPOSITORY}/git/refs"
    tag = f"refs/tags/v{version}"
    try:
        status = poster(path, {"ref": tag, "sha": source_sha})
    except Exception as error:
        raise ReleasePreflightError("atomic tag-ref creation failed; no retry or rollback was attempted") from error
    if status != 201:
        raise ReleasePreflightError(
            f"atomic tag-ref creation returned HTTP {status}; the tag was not replaced"
        )
    return {"ref": tag, "sha": source_sha, "status": "created"}


def github_get(path: str, token: str) -> int:
    request = urllib.request.Request(
        f"https://api.github.com/{path}",
        headers={"Accept": "application/vnd.github+json", "Authorization": f"Bearer {token}"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code
    except urllib.error.URLError as error:
        raise ReleasePreflightError("GitHub repository check failed") from error


def github_post(path: str, token: str, body: dict[str, str]) -> int:
    request = urllib.request.Request(
        f"https://api.github.com/{path}",
        data=json.dumps(body).encode("utf-8"),
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "X-GitHub-Api-Version": "2022-11-28",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return response.status
    except urllib.error.HTTPError as error:
        return error.code
    except urllib.error.URLError as error:
        raise ReleasePreflightError("atomic GitHub tag-ref request failed") from error


def write_evidence(path: Path, status: str, version: str, source_sha: str, details: str = "") -> None:
    path.write_text(
        json.dumps(
            {
                "repository": REPOSITORY,
                "version": version,
                "sourceSha": source_sha,
                "status": status,
                "details": details,
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--source-sha")
    parser.add_argument("--create-tag", action="store_true")
    parser.add_argument("--token", default=os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN"))
    parser.add_argument("--evidence", type=Path)
    args = parser.parse_args()
    try:
        if not args.token:
            raise ReleasePreflightError("GH_TOKEN is required")
        if args.create_tag:
            if not args.source_sha:
                raise ReleasePreflightError("--source-sha is required with --create-tag")
            result = create_tag_ref(
                args.version,
                args.source_sha,
                lambda path, body: github_post(path, args.token, body),
            )
            details = json.dumps(result)
        else:
            result = preflight(args.version, lambda path: github_get(path, args.token))
            details = json.dumps(result)
        if args.evidence:
            write_evidence(args.evidence, "passed", args.version, args.source_sha or "", details)
        if args.create_tag:
            print(f"Created {result['ref']} at {result['sha']}")
        else:
            for item in result:
                if item["path"] != f"repos/{REPOSITORY}":
                    print(f"Verified HTTP 404: {item['path']}")
    except (ReleasePreflightError, ValueError) as error:
        if args.evidence:
            write_evidence(args.evidence, "blocked", args.version, os.environ.get("SOURCE_SHA", ""), str(error))
        print(f"Release blocked: {error}", file=__import__("sys").stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
