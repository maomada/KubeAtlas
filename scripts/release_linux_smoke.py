#!/usr/bin/env python3
"""Verify the staged Linux packages and run their binaries on a native runner."""

import argparse
import platform
import subprocess
import tempfile
from pathlib import Path

from release_artifacts import HUB_CATALOG, HUB_LINUX_PROFILE, validate_local_staged


def extract_package(package: Path, destination: Path) -> None:
    if package.suffix == ".deb":
        subprocess.run(
            ["dpkg-deb", "--extract", str(package), str(destination)],
            check=True, capture_output=True, timeout=60,
        )
        return
    if package.suffix != ".rpm":
        raise ValueError(f"unsupported Linux package: {package.name}")
    payload = subprocess.run(
        ["rpm2cpio", str(package)], check=True, capture_output=True, timeout=60,
    ).stdout
    subprocess.run(
        ["cpio", "-idmu", "--quiet", "--no-absolute-filenames"],
        input=payload, cwd=destination, check=True, capture_output=True, timeout=60,
    )


def smoke_packages(
    directory: Path,
    source_sha: str,
    version: str,
    architecture: str,
    evidence_urls: list[str],
    profile: str = "local-linux",
) -> None:
    native = {"x86_64": "amd64", "aarch64": "arm64"}.get(platform.machine())
    if platform.system() != "Linux" or native != architecture:
        raise ValueError(f"a native Linux {architecture} runner is required")
    manifest = validate_local_staged(
        directory, expected_source_sha=source_sha, expected_version=version,
        evidence_urls=evidence_urls, profile=profile,
    )
    for artifact in manifest["artifacts"]:
        if artifact["architecture"] != architecture:
            continue
        component = artifact["component"]
        package = (directory / artifact["filename"]).resolve(strict=True)
        with tempfile.TemporaryDirectory(prefix="kubeatlas-linux-smoke-") as temp:
            destination = Path(temp)
            extract_package(package, destination)
            binary = destination / "usr/bin" / component
            if binary.is_symlink() or not binary.is_file():
                raise ValueError(f"missing regular binary in {package.name}")
            result = subprocess.run(
                [str(binary), "--version"], cwd=destination,
                check=True, capture_output=True, timeout=30,
            )
            if result.stdout.strip() != f"{component} {version}".encode():
                raise ValueError(f"binary version mismatch: {package.name}")
            help_result = subprocess.run(
                [str(binary), "--help"], cwd=destination,
                check=True, capture_output=True, timeout=30,
            )
            if not (help_result.stdout.strip() or help_result.stderr.strip()):
                raise ValueError(f"empty help output: {package.name}")
            if profile == HUB_LINUX_PROFILE:
                subprocess.run(
                    [str(binary), "--check-hub-artifacts", "--hub-artifacts-dir", str(destination / HUB_CATALOG)],
                    cwd=destination, check=True, capture_output=True, timeout=60,
                )
        print(f"Verified native package binary: {package.name}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--architecture", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--evidence-url", action="append", required=True)
    parser.add_argument("--profile", choices=("local-linux", HUB_LINUX_PROFILE), default="local-linux")
    args = parser.parse_args()
    try:
        smoke_packages(args.directory, args.source_sha, args.version, args.architecture, args.evidence_url, args.profile)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
