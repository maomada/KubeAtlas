#!/usr/bin/env python3
"""Fail-closed source selection for the owned release workflows."""

import argparse
import json
import os
import re
import subprocess
import sys

REPOSITORY = "maomada/KubeAtlas"
WORKFLOWS = ("ci.yml", "security.yml")
NATIVE_HUB_PACKAGING_FILES = (
    "deploy/linux/kubeatlas-portal.nfpm.yaml",
    "scripts/release_hub_packages.sh",
)
VERSION = re.compile(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?")


def validate_inputs(repository, ref, sha, version):
    if repository != REPOSITORY or ref != "refs/heads/main":
        raise ValueError("Release must be dispatched from maomada/KubeAtlas main")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Source must be a full lowercase 40-character commit SHA")
    match = VERSION.fullmatch(version)
    if not match or len(version) > 128:
        raise ValueError("Version must be SemVer without v prefix or build metadata")
    if match.group(4):
        for identifier in match.group(4).split("."):
            if identifier.isdigit() and len(identifier) > 1 and identifier.startswith("0"):
                raise ValueError("Numeric prerelease identifiers cannot have leading zeroes")


def require_success(pages, sha, workflow):
    runs = [
        run
        for page in pages
        for run in page["workflow_runs"]
        if run.get("head_sha") == sha
        and run.get("event") == "push"
        and run.get("head_branch") == "main"
        and run.get("head_repository", {}).get("full_name") == REPOSITORY
        and run.get("path") == f".github/workflows/{workflow}"
    ]
    if not runs:
        raise ValueError(f"No main-push {workflow} run for {sha}")
    latest = max(runs, key=lambda run: (run["id"], run.get("run_attempt", 1)))
    if latest.get("status") != "completed" or latest.get("conclusion") != "success":
        raise ValueError(f"Latest {workflow} run for {sha} has not passed: {latest.get('html_url', '')}")
    return latest["html_url"]


def require_native_hub_packaging(source_dir, sha):
    tree = subprocess.run(
        ["git", "-C", source_dir, "ls-tree", "-r", "--name-only", sha, "--", *NATIVE_HUB_PACKAGING_FILES],
        check=True, capture_output=True, text=True,
    )
    missing = sorted(set(NATIVE_HUB_PACKAGING_FILES) - set(tree.stdout.splitlines()))
    if missing:
        raise ValueError(
            f"Source {sha} lacks the native Hub packaging contract ({', '.join(missing)}). "
            "Select a main commit that builds complete Hub DEB/RPM packages and the offline Agent bundle."
        )


def validate_source(source_dir, sha, version, *, require_native_hub=False):
    validate_inputs(os.environ.get("GITHUB_REPOSITORY"), os.environ.get("GITHUB_REF"), sha, version)
    subprocess.run(
        ["git", "-C", source_dir, "merge-base", "--is-ancestor", sha, "origin/main"],
        check=True,
    )
    if require_native_hub:
        require_native_hub_packaging(source_dir, sha)
    evidence = {}
    for workflow in WORKFLOWS:
        endpoint = (
            f"repos/{REPOSITORY}/actions/workflows/{workflow}/runs"
            f"?head_sha={sha}&event=push&branch=main&per_page=100"
        )
        result = subprocess.run(
            ["gh", "api", "--paginate", "--slurp", endpoint],
            check=True, capture_output=True, text=True,
        )
        evidence[workflow] = require_success(json.loads(result.stdout), sha, workflow)
    return {"repository": REPOSITORY, "sourceSha": sha, "version": version, "checks": evidence}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-dir", default=".")
    parser.add_argument("--sha", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--require-native-hub-packaging", action="store_true")
    args = parser.parse_args()
    try:
        evidence = validate_source(
            args.source_dir, args.sha, args.version,
            require_native_hub=args.require_native_hub_packaging,
        )
    except (ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"Release blocked: {error}", file=sys.stderr)
        return 1
    print(json.dumps(evidence, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
