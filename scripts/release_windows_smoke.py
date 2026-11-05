#!/usr/bin/env python3
"""Verify and smoke-test the native Windows release ZIP."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import stat
import subprocess
import sys
import zipfile
from pathlib import Path, PurePosixPath, PureWindowsPath

from release_artifacts import _artifact_metadata, expected_names
from release_policy import validate_inputs


REPOSITORY = "maomada/KubeAtlas"
WINDOWS_ARCHIVE_MEMBERS = {
    "kubeatlas.exe",
    "LICENSE",
    "NOTICE",
    "README.md",
    "docs/packaging.md",
}
DOS_HEADER_SIZE = 64
PE_HEADER_SIZE = 4 + 20 + 2
MAX_PE_HEADER_OFFSET = 1024 * 1024


class WindowsSmokeError(ValueError):
    pass


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    try:
        with path.open("rb") as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(chunk)
    except OSError as error:
        raise WindowsSmokeError("Windows release ZIP cannot be read") from error
    return digest.hexdigest()


def _read_manifest(path: Path, version: str) -> tuple[dict, str]:
    if path.is_symlink() or not path.is_file():
        raise WindowsSmokeError("full binary build manifest is missing or unsafe")
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        raise WindowsSmokeError("full binary build manifest is malformed") from error
    if not isinstance(manifest, dict):
        raise WindowsSmokeError("full binary build manifest must be an object")
    if manifest.get("schemaVersion") != 1 or type(manifest.get("schemaVersion")) is not int:
        raise WindowsSmokeError("Windows smoke requires the full schema-1 build manifest")
    if manifest.get("repository") != REPOSITORY or manifest.get("version") != version:
        raise WindowsSmokeError("binary build manifest repository or version mismatch")
    source_sha = manifest.get("sourceSha")
    if not isinstance(source_sha, str):
        raise WindowsSmokeError("binary build manifest source SHA is invalid")
    try:
        validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    except (TypeError, ValueError) as error:
        raise WindowsSmokeError("binary build manifest source SHA or version is invalid") from error
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or any(not isinstance(item, dict) for item in artifacts):
        raise WindowsSmokeError("binary build manifest artifact list is invalid")
    filenames = [item.get("filename") for item in artifacts]
    if filenames != sorted(expected_names(version)) or len(filenames) != len(set(filenames)):
        raise WindowsSmokeError("Windows smoke requires the exact full 15-artifact build manifest")
    return manifest, source_sha


def _safe_members(archive: zipfile.ZipFile) -> list[zipfile.ZipInfo]:
    entries = archive.infolist()
    names = []
    for entry in entries:
        name = entry.filename
        posix = PurePosixPath(name)
        windows = PureWindowsPath(name)
        mode = (entry.external_attr >> 16) & 0o170000
        if (
            not name
            or "\\" in name
            or posix.is_absolute()
            or windows.is_absolute()
            or windows.drive
            or ".." in posix.parts
            or posix.as_posix() != name
            or mode not in (0, stat.S_IFREG)
        ):
            raise WindowsSmokeError("Windows ZIP contains an unsafe or non-regular entry")
        names.append(name)
    if len(names) != len(set(names)) or set(names) != WINDOWS_ARCHIVE_MEMBERS:
        raise WindowsSmokeError("Windows ZIP contents do not match the safe release layout")
    return entries


def _validate_amd64_pe(executable: Path) -> None:
    error_message = "kubeatlas.exe is not a valid AMD64 PE32+ executable"
    try:
        with executable.open("rb") as source:
            dos_header = source.read(DOS_HEADER_SIZE)
            if len(dos_header) != DOS_HEADER_SIZE or dos_header[:2] != b"MZ":
                raise WindowsSmokeError(error_message)
            pe_offset = int.from_bytes(dos_header[0x3C:0x40], "little")
            if pe_offset < DOS_HEADER_SIZE or pe_offset > MAX_PE_HEADER_OFFSET:
                raise WindowsSmokeError(error_message)
            source.seek(0, os.SEEK_END)
            if source.tell() < pe_offset + PE_HEADER_SIZE:
                raise WindowsSmokeError(error_message)
            source.seek(pe_offset)
            pe_header = source.read(PE_HEADER_SIZE)
    except OSError as error:
        raise WindowsSmokeError("kubeatlas.exe PE header cannot be read") from error
    if (
        len(pe_header) != PE_HEADER_SIZE
        or pe_header[:4] != b"PE\0\0"
        or int.from_bytes(pe_header[4:6], "little") != 0x8664
        or int.from_bytes(pe_header[20:22], "little") < 2
        or int.from_bytes(pe_header[24:26], "little") != 0x20B
    ):
        raise WindowsSmokeError(error_message)


def verify_and_extract(
    manifest_path: Path,
    archive_path: Path,
    extract_dir: Path,
    version: str,
) -> Path:
    manifest, source_sha = _read_manifest(manifest_path, version)
    expected_archive = f"kubeatlas_v{version}_windows_amd64.zip"
    matches = [item for item in manifest["artifacts"] if item.get("filename") == expected_archive]
    if len(matches) != 1:
        raise WindowsSmokeError("Windows ZIP is not uniquely listed in the build manifest")
    expected_metadata = _artifact_metadata(expected_archive, version, source_sha)
    expected_metadata.pop("checksum")
    artifact = matches[0]
    if any(artifact.get(key) != value for key, value in expected_metadata.items()):
        raise WindowsSmokeError("Windows ZIP metadata does not match the build profile")
    if archive_path.is_symlink() or not archive_path.is_file():
        raise WindowsSmokeError("Windows ZIP is missing or unsafe")
    if artifact.get("checksum") != _sha256(archive_path):
        raise WindowsSmokeError("Windows ZIP checksum does not match the full build manifest")
    extract_dir = Path(extract_dir)
    if extract_dir.is_symlink() or extract_dir.exists() and (not extract_dir.is_dir() or any(extract_dir.iterdir())):
        raise WindowsSmokeError("Windows smoke extraction directory must be empty and real")
    extract_dir.mkdir(parents=True, exist_ok=True)
    try:
        with zipfile.ZipFile(archive_path) as archive:
            entries = _safe_members(archive)
            for entry in entries:
                target = extract_dir.joinpath(*PurePosixPath(entry.filename).parts)
                if target.is_symlink():
                    raise WindowsSmokeError("Windows smoke extraction target is unsafe")
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.open(entry, "r") as source, target.open("xb") as destination:
                    shutil.copyfileobj(source, destination, length=1024 * 1024)
    except (OSError, zipfile.BadZipFile, RuntimeError) as error:
        if isinstance(error, WindowsSmokeError):
            raise
        raise WindowsSmokeError("Windows ZIP could not be safely extracted") from error
    executable = extract_dir / "kubeatlas.exe"
    if executable.is_symlink() or not executable.is_file():
        raise WindowsSmokeError("kubeatlas.exe is missing after extraction")
    _validate_amd64_pe(executable)
    return executable


def run_native_smoke(executable: Path, version: str) -> None:
    environment = os.environ.copy()
    environment.pop("KUBECONFIG", None)
    try:
        result = subprocess.run(
            [str(executable), "--version"],
            check=False,
            capture_output=True,
            text=True,
            timeout=30,
            env=environment,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise WindowsSmokeError("kubeatlas.exe --version did not complete") from error
    if result.returncode != 0 or result.stdout != f"kubeatlas {version}\n" or result.stderr:
        raise WindowsSmokeError("kubeatlas.exe --version did not return the exact expected output")
    try:
        help_result = subprocess.run(
            [str(executable), "--help"],
            check=False,
            capture_output=True,
            text=True,
            timeout=30,
            env=environment,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise WindowsSmokeError("kubeatlas.exe --help did not complete") from error
    if help_result.returncode != 0:
        raise WindowsSmokeError("kubeatlas.exe --help returned a nonzero exit code")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--extract-dir", type=Path, required=True)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    try:
        executable = verify_and_extract(args.manifest, args.archive, args.extract_dir, args.version)
        run_native_smoke(executable, args.version)
    except (WindowsSmokeError, OSError, ValueError) as error:
        print(f"Windows release smoke blocked: {error}", file=sys.stderr)
        return 1
    print(f"Native Windows smoke passed for kubeatlas {args.version}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
