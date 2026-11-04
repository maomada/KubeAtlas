#!/usr/bin/env python3
"""Stage and validate the immutable binary release artifact set."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import tarfile
import tempfile
import zipfile
from pathlib import Path, PurePosixPath

from release_policy import validate_inputs


REPOSITORY = "maomada/KubeAtlas"
COMPONENTS = ("kubeatlas", "kubeatlas-portal")
ARCHIVES = (
    ("kubeatlas", "linux", "amd64", ".tar.gz"),
    ("kubeatlas", "linux", "arm64", ".tar.gz"),
    ("kubeatlas", "darwin", "amd64", ".tar.gz"),
    ("kubeatlas", "darwin", "arm64", ".tar.gz"),
    ("kubeatlas", "windows", "amd64", ".zip"),
    ("kubeatlas-portal", "linux", "amd64", ".tar.gz"),
    ("kubeatlas-portal", "linux", "arm64", ".tar.gz"),
)
PACKAGES = tuple(
    (component, "linux", arch, suffix)
    for component in COMPONENTS
    for arch in ("amd64", "arm64")
    for suffix in (".deb", ".rpm")
)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def expected_names(version: str) -> set[str]:
    names = {
        f"{component}_v{version}_{os_name}_{arch}{suffix}"
        for component, os_name, arch, suffix in ARCHIVES
    }
    names.update(
        f"{component}_{version}_{os_name}_{arch}{suffix}"
        for component, os_name, arch, suffix in PACKAGES
    )
    return names


def local_asset_names(version: str) -> list[str]:
    names = {
        f"{component}_{version}_linux_{architecture}{suffix}"
        for component, _, architecture, suffix in PACKAGES
    }
    names.add(f"kubeatlas_v{version}_windows_amd64.zip")
    return sorted(names)


def local_publication_names(version: str) -> list[str]:
    return sorted({*local_asset_names(version), "binary-manifest.json", "checksums.txt"})


def _package_version(version: str) -> str:
    return version.replace("-", "~", 1)


def _safe_name(name: str) -> None:
    path = PurePosixPath(name)
    if (
        not name
        or path.is_absolute()
        or ".." in path.parts
        or "\\" in name
    ):
        raise ValueError(f"unsafe artifact path: {name!r}")


def _archive_members(path: Path) -> list[str]:
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            entries = archive.infolist()
            if any(not entry.is_dir() and ((entry.external_attr >> 16) & 0o170000) not in (0, 0o100000) for entry in entries):
                raise ValueError(f"non-regular archive entry: {path.name}")
            names = [entry.filename for entry in entries]
    else:
        with tarfile.open(path, "r:gz") as archive:
            entries = archive.getmembers()
            if any(not (entry.isfile() or entry.isdir()) for entry in entries):
                raise ValueError(f"non-regular archive entry: {path.name}")
            names = [entry.name for entry in entries]
    for name in names:
        _safe_name(name)
    if len(names) != len(set(names)):
        raise ValueError(f"duplicate archive entries: {path.name}")
    return names


def _validate_archive(path: Path, component: str, suffix: str) -> None:
    names = set(_archive_members(path))
    binary = component + (".exe" if suffix == ".zip" else "")
    expected = {binary, "LICENSE", "NOTICE", "README.md", "docs/packaging.md"}
    if names != expected:
        raise ValueError(
            f"{path.name} contains {sorted(names)!r}; expected {sorted(expected)!r}"
        )


def _validate_elf_stream(stream, architecture: str, artifact: str) -> None:
    header = stream.read(20)
    if len(header) < 20 or header[:4] != b"\x7fELF" or header[5] not in (1, 2):
        raise ValueError(f"invalid ELF binary: {artifact}")
    byteorder = "little" if header[5] == 1 else "big"
    machine = int.from_bytes(header[18:20], byteorder)
    expected = {"amd64": 62, "arm64": 183}[architecture]
    if machine != expected:
        raise ValueError(f"ELF architecture mismatch: {artifact}")


def _validate_elf_file(path: Path, architecture: str, artifact: str) -> None:
    with path.open("rb") as stream:
        _validate_elf_stream(stream, architecture, artifact)


def _validate_linux_archive_arch(path: Path, component: str, architecture: str) -> None:
    with tarfile.open(path, "r:gz") as archive:
        binary = archive.extractfile(component)
        if binary is None:
            raise ValueError(f"Linux archive binary is missing: {path.name}")
        _validate_elf_stream(binary, architecture, path.name)


def _run(command: list[str]) -> str:
    try:
        result = subprocess.run(command, check=True, capture_output=True, text=True)
    except (FileNotFoundError, subprocess.CalledProcessError) as error:
        raise ValueError(f"package inspection failed: {' '.join(command)}") from error
    return result.stdout


def _validate_deb(path: Path, component: str, architecture: str, version: str) -> None:
    fields = [
        _run(["dpkg-deb", "--field", str(path), key]).strip()
        for key in ("Package", "Version", "Architecture")
    ]
    if fields != [component, _package_version(version), architecture]:
        raise ValueError(f"DEB metadata mismatch: {path.name}")
    paths = {}
    for line in _run(["dpkg-deb", "--contents", str(path)]).splitlines():
        fields = line.split()
        if not fields:
            continue
        mode, name = fields[0], fields[-1].removeprefix("./")
        if mode.startswith("d"):
            continue
        if not mode.startswith("-"):
            raise ValueError(f"unsupported DEB file type: {path.name}")
        paths[name] = mode
    expected_paths = {
        f"usr/bin/{component}",
        f"usr/share/doc/{component}/LICENSE",
        f"usr/share/doc/{component}/NOTICE",
        f"usr/share/doc/{component}/README.md",
        f"usr/share/doc/{component}/packaging.md",
    }
    if set(paths) != expected_paths:
        raise ValueError(f"DEB contents mismatch: {path.name}")
    with tempfile.TemporaryDirectory() as tmp:
        _run(["dpkg-deb", "--control", str(path), tmp])
        if {entry.name for entry in Path(tmp).iterdir()} - {"control", "md5sums"}:
            raise ValueError(f"DEB maintainer scripts are not permitted: {path.name}")
        _run(["dpkg-deb", "--extract", str(path), tmp])
        _validate_elf_file(Path(tmp) / "usr/bin" / component, architecture, path.name)


def _validate_rpm(path: Path, component: str, architecture: str, version: str) -> None:
    fields = _run(
        ["rpm", "-qp", "--queryformat", "%{NAME}\\n%{VERSION}\\n%{RELEASE}\\n%{ARCH}\\n", str(path)]
    ).splitlines()
    expected_arch = {"amd64": "x86_64", "arm64": "aarch64"}[architecture]
    if fields != [component, _package_version(version), "1", expected_arch]:
        raise ValueError(f"RPM metadata mismatch: {path.name}")
    paths = set(_run(["rpm", "-qp", "--list", str(path)]).splitlines())
    expected_paths = {
        f"/usr/bin/{component}",
        f"/usr/share/doc/{component}/LICENSE",
        f"/usr/share/doc/{component}/NOTICE",
        f"/usr/share/doc/{component}/README.md",
        f"/usr/share/doc/{component}/packaging.md",
    }
    if paths != expected_paths:
        raise ValueError(f"RPM contents mismatch: {path.name}")
    scripts = _run(["rpm", "-qp", "--scripts", str(path)]).strip()
    if scripts and scripts != "(none)":
        raise ValueError(f"RPM maintainer scripts are not permitted: {path.name}")
    with tempfile.TemporaryDirectory() as tmp:
        rpm2cpio = shutil.which("rpm2cpio")
        if rpm2cpio:
            extract_command = [rpm2cpio, str(path)]
        else:
            script = Path(os.environ.get("RPM_CONFIGDIR", "")) / "rpm2cpio.sh"
            if not script.is_file():
                raise ValueError("rpm2cpio is required to inspect RPM payloads")
            extract_command = ["sh", str(script), str(path)]
        payload = subprocess.run(
            extract_command, check=True, capture_output=True
        ).stdout
        subprocess.run(
            ["cpio", "-idmu", "--quiet", "--no-absolute-filenames"],
            input=payload,
            cwd=tmp,
            check=True,
            capture_output=True,
        )
        _validate_elf_file(Path(tmp) / "usr/bin" / component, architecture, path.name)


def _artifact_metadata(name: str, version: str, source_sha: str) -> dict[str, object]:
    archive_pattern = re.fullmatch(
        rf"(kubeatlas(?:-portal)?)_v{re.escape(version)}_(linux|darwin|windows)_(amd64|arm64)(\.tar\.gz|\.zip)",
        name,
    )
    package_pattern = re.fullmatch(
        rf"(kubeatlas(?:-portal)?)_{re.escape(version)}_(linux)_(amd64|arm64)(\.deb|\.rpm)",
        name,
    )
    match = archive_pattern or package_pattern
    if not match:
        raise ValueError(f"unexpected artifact name: {name}")
    component, os_name, architecture, _ = match.groups()
    if component == "kubeatlas-portal" and os_name != "linux":
        raise ValueError(f"Portal artifact has unsupported platform: {name}")
    return {
        "filename": name,
        "checksum": "",
        "component": component,
        "platform": os_name,
        "architecture": architecture,
        "version": version,
        "sourceSha": source_sha,
    }


def _evidence_urls(source_sha: str, evidence_urls: list[str] | None = None) -> list[str]:
    return list(dict.fromkeys([f"https://github.com/{REPOSITORY}/commit/{source_sha}", *(evidence_urls or [])]))


def _checksum_lines(directory: Path, names: list[str]) -> str:
    return "".join(f"{sha256(directory / name)}  {name}\n" for name in names)


def validate_staged(
    directory: Path,
    manifest_path: Path | None = None,
    expected_source_sha: str | None = None,
    expected_version: str | None = None,
    evidence_urls: list[str] | None = None,
    profile: str = "full",
) -> dict:
    if profile == "local":
        return validate_local_staged(
            directory,
            manifest_path=manifest_path,
            expected_source_sha=expected_source_sha,
            expected_version=expected_version,
            evidence_urls=evidence_urls,
        )
    if profile != "full":
        raise ValueError(f"unsupported binary publication profile: {profile}")
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("artifact directory must be a real directory")
    manifest_path = manifest_path or directory / "binary-manifest.json"
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise ValueError("binary-manifest.json is missing")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(manifest, dict):
        raise ValueError("invalid binary manifest schema")
    if set(manifest) != {"schemaVersion", "repository", "version", "sourceSha", "artifacts"}:
        raise ValueError("invalid binary manifest schema")
    version = manifest.get("version")
    source_sha = manifest.get("sourceSha")
    if not isinstance(version, str) or not isinstance(source_sha, str):
        raise ValueError("manifest version and sourceSha are required")
    if manifest.get("schemaVersion") != 1:
        raise ValueError("unsupported binary manifest schema")
    validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    if manifest.get("repository") != REPOSITORY:
        raise ValueError("manifest repository mismatch")
    if expected_source_sha is not None and source_sha != expected_source_sha:
        raise ValueError("manifest source SHA does not match the selected source")
    if expected_version is not None and version != expected_version:
        raise ValueError("manifest version does not match the selected version")
    expected = expected_names(version)
    entries = list(directory.iterdir())
    if any(path.is_symlink() or not path.is_file() for path in entries):
        raise ValueError("artifact directory must contain files only")
    actual = {path.name for path in entries}
    allowed = expected | {"checksums.txt", "binary-manifest.json"}
    if actual != allowed:
        raise ValueError(f"artifact set mismatch: expected {sorted(allowed)!r}, got {sorted(actual)!r}")
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or any(not isinstance(item, dict) for item in artifacts):
        raise ValueError("manifest artifact set does not match the expected matrix")
    filenames = [item.get("filename") for item in artifacts]
    if any(not isinstance(name, str) for name in filenames):
        raise ValueError("manifest artifact filenames must be strings")
    if len(filenames) != len(set(filenames)):
        raise ValueError("duplicate manifest artifact entry")
    if set(filenames) != expected:
        raise ValueError("manifest artifact set does not match the expected matrix")
    if (directory / "checksums.txt").is_symlink() or not (directory / "checksums.txt").is_file():
        raise ValueError("checksums.txt is missing")
    checksums = {}
    for line in (directory / "checksums.txt").read_text(encoding="utf-8").splitlines():
        fields = line.split()
        if len(fields) != 2 or not re.fullmatch(r"[0-9a-f]{64}", fields[0]):
            raise ValueError("invalid checksums.txt entry")
        _safe_name(fields[1])
        if fields[1] in checksums:
            raise ValueError(f"duplicate checksum entry: {fields[1]}")
        checksums[fields[1]] = fields[0]
    if set(checksums) != expected | {"binary-manifest.json"}:
        raise ValueError("checksums.txt does not cover the exact staged set")
    for item in artifacts:
        name = item.get("filename")
        _safe_name(name)
        if Path(name).name != name:
            raise ValueError(f"unsafe artifact filename: {name!r}")
        expected_metadata = _artifact_metadata(name, version, source_sha)
        expected_metadata["checksum"] = sha256(directory / name)
        expected_metadata["evidenceUrls"] = _evidence_urls(source_sha, evidence_urls)
        if item != expected_metadata:
            raise ValueError(f"artifact metadata mismatch: {name}")
        if (
            item.get("version") != version
            or item.get("sourceSha") != source_sha
            or item.get("checksum") != sha256(directory / name)
            or checksums.get(name) != item.get("checksum")
        ):
            raise ValueError(f"artifact metadata or checksum mismatch: {name}")
        if name.endswith((".tar.gz", ".zip")):
            _validate_archive(directory / name, item["component"], Path(name).suffix)
            if item["platform"] == "linux":
                _validate_linux_archive_arch(directory / name, item["component"], item["architecture"])
        elif name.endswith(".deb"):
            _validate_deb(directory / name, item["component"], item["architecture"], version)
        elif name.endswith(".rpm"):
            _validate_rpm(directory / name, item["component"], item["architecture"], version)
    if checksums["binary-manifest.json"] != sha256(manifest_path):
        raise ValueError("binary-manifest.json checksum mismatch")
    return manifest


def validate_local_staged(
    directory: Path,
    manifest_path: Path | None = None,
    expected_source_sha: str | None = None,
    expected_version: str | None = None,
    evidence_urls: list[str] | None = None,
) -> dict:
    if directory.is_symlink() or not directory.is_dir():
        raise ValueError("local publication directory must be a real directory")
    manifest_path = manifest_path or directory / "binary-manifest.json"
    if manifest_path.is_symlink() or not manifest_path.is_file():
        raise ValueError("binary-manifest.json is missing")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(manifest, dict):
        raise ValueError("invalid local binary manifest schema")
    if set(manifest) != {
        "schemaVersion",
        "profile",
        "repository",
        "version",
        "sourceSha",
        "artifacts",
    }:
        raise ValueError("invalid local binary manifest schema")
    if type(manifest.get("schemaVersion")) is not int or manifest["schemaVersion"] != 2:
        raise ValueError("unsupported local binary manifest schema")
    if manifest.get("profile") != "local":
        raise ValueError("unsupported local binary publication profile")
    version = manifest.get("version")
    source_sha = manifest.get("sourceSha")
    if not isinstance(version, str) or not isinstance(source_sha, str):
        raise ValueError("manifest version and sourceSha are required")
    validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    if manifest.get("repository") != REPOSITORY:
        raise ValueError("manifest repository mismatch")
    if expected_source_sha is not None and source_sha != expected_source_sha:
        raise ValueError("manifest source SHA does not match the selected source")
    if expected_version is not None and version != expected_version:
        raise ValueError("manifest version does not match the selected version")
    expected = set(local_asset_names(version))
    entries = list(directory.iterdir())
    if any(path.is_symlink() or not path.is_file() for path in entries):
        raise ValueError("local publication directory must contain files only")
    actual = {path.name for path in entries}
    allowed = expected | {"checksums.txt", "binary-manifest.json"}
    if actual != allowed:
        raise ValueError(
            f"local artifact set mismatch: expected {sorted(allowed)!r}, got {sorted(actual)!r}"
        )
    artifacts = manifest.get("artifacts")
    if not isinstance(artifacts, list) or any(not isinstance(item, dict) for item in artifacts):
        raise ValueError("local manifest artifact set does not match the publication profile")
    filenames = [item.get("filename") for item in artifacts]
    if any(not isinstance(name, str) for name in filenames):
        raise ValueError("manifest artifact filenames must be strings")
    if len(filenames) != len(set(filenames)):
        raise ValueError("duplicate manifest artifact entry")
    if filenames != sorted(expected):
        raise ValueError("local manifest artifact set does not match the publication profile")
    checksums_path = directory / "checksums.txt"
    if checksums_path.is_symlink() or not checksums_path.is_file():
        raise ValueError("checksums.txt is missing")
    checksums = {}
    for line in checksums_path.read_text(encoding="utf-8").splitlines():
        fields = line.split()
        if len(fields) != 2 or not re.fullmatch(r"[0-9a-f]{64}", fields[0]):
            raise ValueError("invalid checksums.txt entry")
        _safe_name(fields[1])
        if fields[1] in checksums:
            raise ValueError(f"duplicate checksum entry: {fields[1]}")
        checksums[fields[1]] = fields[0]
    if set(checksums) != expected | {"binary-manifest.json"}:
        raise ValueError("checksums.txt does not cover the exact local staged set")
    for item in artifacts:
        name = item.get("filename")
        _safe_name(name)
        if Path(name).name != name:
            raise ValueError(f"unsafe artifact filename: {name!r}")
        expected_metadata = _artifact_metadata(name, version, source_sha)
        expected_metadata["checksum"] = sha256(directory / name)
        expected_metadata["evidenceUrls"] = _evidence_urls(source_sha, evidence_urls)
        if item != expected_metadata:
            raise ValueError(f"artifact metadata mismatch: {name}")
        if checksums.get(name) != item.get("checksum"):
            raise ValueError(f"artifact metadata or checksum mismatch: {name}")
        if name.endswith(".zip"):
            _validate_archive(directory / name, item["component"], ".zip")
        elif name.endswith(".deb"):
            _validate_deb(directory / name, item["component"], item["architecture"], version)
        elif name.endswith(".rpm"):
            _validate_rpm(directory / name, item["component"], item["architecture"], version)
        else:
            raise ValueError(f"unsupported local publication artifact: {name}")
    if checksums["binary-manifest.json"] != sha256(manifest_path):
        raise ValueError("binary-manifest.json checksum mismatch")
    return manifest


def create_staged(
    dist: Path,
    output: Path,
    source_sha: str,
    version: str,
    evidence_urls: list[str] | None = None,
) -> dict:
    validate_inputs(REPOSITORY, "refs/heads/main", source_sha, version)
    names = sorted(expected_names(version))
    output.mkdir(parents=True, exist_ok=True)
    for path in output.iterdir():
        if path.is_file() or path.is_symlink():
            path.unlink()
        elif path.is_dir():
            shutil.rmtree(path)
    for name in names:
        source = dist / name
        if source.is_symlink() or not source.is_file():
            raise ValueError(f"missing expected artifact: {name}")
        shutil.copy2(source, output / name)
    artifacts = [_artifact_metadata(name, version, source_sha) for name in names]
    for item in artifacts:
        item["checksum"] = sha256(output / item["filename"])
        item["evidenceUrls"] = _evidence_urls(source_sha, evidence_urls)
    manifest = {
        "schemaVersion": 1,
        "repository": REPOSITORY,
        "version": version,
        "sourceSha": source_sha,
        "artifacts": artifacts,
    }
    manifest_path = output / "binary-manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    (output / "checksums.txt").write_text(
        _checksum_lines(output, names + ["binary-manifest.json"]), encoding="utf-8"
    )
    validate_staged(output, manifest_path, evidence_urls=evidence_urls)
    return manifest


def create_local_staged(
    dist: Path,
    output: Path,
    source_sha: str,
    version: str,
    evidence_urls: list[str] | None = None,
) -> dict:
    validate_staged(
        dist,
        expected_source_sha=source_sha,
        expected_version=version,
        evidence_urls=evidence_urls,
        profile="full",
    )
    dist_resolved = dist.resolve()
    output_resolved = output.resolve()
    if output.is_symlink():
        raise ValueError("local publication output directory must not be a symlink")
    if output_resolved == dist_resolved or output_resolved.is_relative_to(dist_resolved) or dist_resolved.is_relative_to(output_resolved):
        raise ValueError("local publication staging must use a separate directory")
    output.mkdir(parents=True, exist_ok=True)
    if any(output.iterdir()):
        raise ValueError("local publication output directory must be empty")
    names = local_asset_names(version)
    for name in names:
        source = dist / name
        if source.is_symlink() or not source.is_file():
            raise ValueError(f"missing expected artifact: {name}")
        shutil.copy2(source, output / name)
    artifacts = [_artifact_metadata(name, version, source_sha) for name in names]
    for item in artifacts:
        item["checksum"] = sha256(output / item["filename"])
        item["evidenceUrls"] = _evidence_urls(source_sha, evidence_urls)
    manifest = {
        "schemaVersion": 2,
        "profile": "local",
        "repository": REPOSITORY,
        "version": version,
        "sourceSha": source_sha,
        "artifacts": artifacts,
    }
    manifest_path = output / "binary-manifest.json"
    manifest_path.write_text(
        json.dumps(manifest, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )
    (output / "checksums.txt").write_text(
        _checksum_lines(output, names + ["binary-manifest.json"]),
        encoding="utf-8",
    )
    validate_staged(
        output,
        manifest_path,
        expected_source_sha=source_sha,
        expected_version=version,
        evidence_urls=evidence_urls,
        profile="local",
    )
    return manifest


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    create = subparsers.add_parser("create")
    create.add_argument("--dist", type=Path, required=True)
    create.add_argument("--output", type=Path, required=True)
    create.add_argument("--source-sha", required=True)
    create.add_argument("--version", required=True)
    create.add_argument("--evidence-url", action="append", default=[])
    stage_local = subparsers.add_parser("stage-local")
    stage_local.add_argument("--dist", type=Path, required=True)
    stage_local.add_argument("--output", type=Path, required=True)
    stage_local.add_argument("--source-sha", required=True)
    stage_local.add_argument("--version", required=True)
    stage_local.add_argument("--evidence-url", action="append", default=[])
    validate = subparsers.add_parser("validate")
    validate.add_argument("--directory", type=Path, required=True)
    validate.add_argument("--source-sha")
    validate.add_argument("--version")
    validate.add_argument("--evidence-url", action="append")
    validate.add_argument("--profile", choices=("full", "local"), default="full")
    args = parser.parse_args()
    try:
        if args.command == "create":
            create_staged(args.dist, args.output, args.source_sha, args.version, args.evidence_url)
        elif args.command == "stage-local":
            create_local_staged(args.dist, args.output, args.source_sha, args.version, args.evidence_url)
        else:
            validate_staged(
                args.directory,
                expected_source_sha=args.source_sha,
                expected_version=args.version,
                evidence_urls=args.evidence_url,
                profile=args.profile,
            )
    except (OSError, ValueError, KeyError, json.JSONDecodeError) as error:
        parser.error(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
