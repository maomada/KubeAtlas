#!/usr/bin/env python3
"""Fail-closed GHCR collision checks for an owned KubeAtlas release."""

import argparse
import re
import subprocess
import sys

from release_approval import expected_targets
from release_policy import REPOSITORY, validate_inputs


class RegistryPreflightError(RuntimeError):
    pass


def release_targets(version, hub_only=False):
    validate_inputs(REPOSITORY, "refs/heads/main", "0" * 40, version)
    return tuple(expected_targets("ghcr-hub" if hub_only else "ghcr", version))


def classify_inspection(returncode, stdout="", stderr=""):
    if returncode == 0:
        return "present"

    message = f"{stdout}\n{stderr}".lower()
    unsafe_markers = (
        "unauthorized",
        "authentication required",
        "denied",
        "forbidden",
        "timed out",
        "timeout",
        "connection refused",
        "no such host",
        "temporary failure",
        "network is unreachable",
        "tls handshake",
    )
    if any(marker in message for marker in unsafe_markers):
        raise RegistryPreflightError("Registry inspection failed; absence was not established")

    absent_markers = (
        r"\bmanifest_unknown\b",
        r"\bname_unknown\b",
        r"\bmanifest\s+(?:is\s+)?unknown\b",
        r"\bmanifest\s+not\s+found\b",
        r"\bname\s+(?:is\s+)?unknown\b",
        r"\bname\s+not\s+found\b",
        r"\bno\s+such\s+manifest\b",
    )
    if any(re.search(marker, message) for marker in absent_markers):
        return "absent"
    raise RegistryPreflightError("Registry inspection failed; absence was not established")


def inspect_targets(version, authfile, runner=subprocess.run, hub_only=False):
    results = []
    collisions = []
    failures = []
    for ref in release_targets(version, hub_only=hub_only):
        try:
            result = runner(
                [
                    "skopeo",
                    "inspect",
                    "--raw",
                    "--authfile",
                    authfile,
                    f"docker://{ref}",
                ],
                check=False,
                capture_output=True,
                text=True,
                timeout=60,
            )
            status = classify_inspection(result.returncode, result.stdout, result.stderr)
        except (OSError, subprocess.TimeoutExpired, RegistryPreflightError):
            status = "inspection-failed"
            failures.append(ref)
        if status == "present":
            collisions.append(ref)
        results.append({"ref": ref, "status": status})

    if failures:
        raise RegistryPreflightError(
            "Could not safely inspect all release targets: " + ", ".join(failures)
        )
    if collisions:
        raise RegistryPreflightError(
            "Release blocked because target refs already exist: " + ", ".join(collisions)
        )
    return results


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--authfile", required=True)
    parser.add_argument("--hub-only", action="store_true")
    args = parser.parse_args()
    try:
        results = inspect_targets(args.version, args.authfile, hub_only=args.hub_only)
    except RegistryPreflightError as error:
        print(f"Release blocked: {error}", file=sys.stderr)
        return 1
    for item in results:
        print(f"Absent target confirmed: {item['ref']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
