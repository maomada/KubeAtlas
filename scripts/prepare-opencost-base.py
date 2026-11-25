#!/usr/bin/env python3
"""Prepare a pinned OpenCost Helm chart for Hub-hosted Agent registration."""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile

import yaml


UPSTREAM_CHART_VERSION = "2.5.34"
UPSTREAM_CHART_SHA256 = "9ba42bb2ae4df6854d51e1d08fa45f14aed49781b8e597e00c0d789c271aacee"
CHART_LICENSE_SHA256 = "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"
EXPORTER_VERSION = "1.121.4"
EXPORTER_DIGEST = "sha256:daa04ae48f17b830de941c3602563c5a01fabb13522ede1ba057f09e96613aaa"
EXPORTER_NOTICE_SHA256 = "84b7e435b157b4b811125c23728bf0558123afb94b13bae60d10f2c9c484c770"
BUNDLE_VERSION = "2.5.34-kubeatlas.2"
MAX_ARCHIVE_BYTES = 512 * 1024
MAX_EXPANDED_BYTES = 16 * 1024 * 1024
MAX_FILES = 1024
CHART_PATH = "opencost/Chart.yaml"
VALUES_PATH = "opencost/values.yaml"


def read_upstream_chart(path):
    with path.open("rb") as source:
        data = source.read(MAX_ARCHIVE_BYTES + 1)
    if len(data) > MAX_ARCHIVE_BYTES:
        raise ValueError("upstream OpenCost chart exceeds the archive size limit")
    if hashlib.sha256(data).hexdigest() != UPSTREAM_CHART_SHA256:
        raise ValueError(f"upstream OpenCost chart {UPSTREAM_CHART_VERSION} SHA-256 mismatch")

    files = {}
    expanded_size = 0
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for entry in archive:
            parts = entry.name.split("/")
            if (parts[0] != "opencost" or len(parts) < 2 or "\\" in entry.name or
                    any(part in ("", ".", "..", "crds") for part in parts) or
                    entry.name.endswith(".tgz") or entry.name in files or not entry.isfile()):
                raise ValueError(f"unexpected chart archive entry {entry.name!r}")
            expanded_size += entry.size
            if len(files) >= MAX_FILES or expanded_size > MAX_EXPANDED_BYTES:
                raise ValueError("expanded OpenCost chart exceeds the archive limits")
            with archive.extractfile(entry) as member:
                files[entry.name] = member.read()
    return files


def prepare_chart(files):
    metadata = yaml.safe_load(files[CHART_PATH])
    if (metadata.get("name") != "opencost" or metadata.get("apiVersion") != "v2" or
            metadata.get("type") != "application" or metadata.get("version") != UPSTREAM_CHART_VERSION or
            metadata.get("appVersion") != EXPORTER_VERSION or metadata.get("dependencies")):
        raise ValueError("upstream OpenCost chart metadata does not match the pinned release")
    metadata.update(version=BUNDLE_VERSION, description="OpenCost exporter for KubeAtlas offline installation")
    files[CHART_PATH] = yaml.safe_dump(metadata, sort_keys=False).encode()

    values = yaml.safe_load(files[VALUES_PATH])
    cost = values["opencost"]
    image = cost["exporter"]["image"]
    if (image.get("registry"), image.get("repository"), image.get("tag")) != (
        "ghcr.io", "opencost/opencost", f"{EXPORTER_VERSION}@{EXPORTER_DIGEST}",
    ):
        raise ValueError("upstream OpenCost chart does not pin the expected exporter image")

    # Registration replaces the placeholders after discovering the target cluster's Prometheus.
    image["fullImageName"] = f"kubeatlas.invalid/kubeatlas/opencost@{EXPORTER_DIGEST}"
    cost["prometheus"]["internal"]["enabled"] = False
    cost["prometheus"]["external"].update(enabled=True, url="http://prometheus.invalid:9090")
    cost["metrics"]["serviceMonitor"].update(enabled=False, namespace="", additionalLabels={})
    cost["metrics"]["kubeStateMetrics"]["emitKsmV1Metrics"] = False
    cost["ui"]["enabled"] = False
    cost["cloudCost"]["enabled"] = False
    values["plugins"]["enabled"] = False
    values["plugins"]["install"]["enabled"] = False
    values["imagePullSecrets"] = []
    files[VALUES_PATH] = (json.dumps(values, sort_keys=True, allow_nan=False) + "\n").encode()

    output = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
            for path, contents in sorted(files.items()):
                entry = tarfile.TarInfo(path)
                entry.size = len(contents)
                archive.addfile(entry, io.BytesIO(contents))

    data = output.getvalue()
    if len(data) > MAX_ARCHIVE_BYTES:
        raise ValueError("prepared OpenCost chart exceeds the archive size limit")
    return data


def write_chart(files, destination):
    data = prepare_chart(files)
    output = destination.open("xb")
    try:
        with output:
            output.write(data)
    except OSError:
        destination.unlink(missing_ok=True)
        raise
    return hashlib.sha256(data).hexdigest()


def download_release_file(url, path, expected, max_bytes):
    subprocess.run([
        "curl", "--fail", "--silent", "--show-error", "--location",
        "--proto", "=https", "--proto-redir", "=https", "--retry", "3",
        "--connect-timeout", "15", "--max-time", "120",
        "--max-filesize", str(max_bytes), "--output", str(path), url,
    ], check=True)
    with path.open("rb") as source:
        data = source.read(max_bytes + 1)
    if len(data) > max_bytes or hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f"{path.name} does not match the pinned OpenCost release")
    return data


def prepare_release(directory):
    directory.mkdir()
    source = directory / "upstream-opencost.tgz"
    chart_tag = f"opencost-{UPSTREAM_CHART_VERSION}"
    download_release_file(
        f"https://github.com/opencost/opencost-helm-chart/releases/download/{chart_tag}/{chart_tag}.tgz",
        source, UPSTREAM_CHART_SHA256, MAX_ARCHIVE_BYTES,
    )
    files = read_upstream_chart(source)
    files["opencost/LICENSE"] = download_release_file(
        f"https://raw.githubusercontent.com/opencost/opencost-helm-chart/{chart_tag}/LICENSE",
        directory / "chart-LICENSE", CHART_LICENSE_SHA256, 32 * 1024,
    )
    exporter_notice = download_release_file(
        f"https://raw.githubusercontent.com/opencost/opencost/v{EXPORTER_VERSION}/NOTICE",
        directory / "exporter-NOTICE", EXPORTER_NOTICE_SHA256, 32 * 1024,
    )
    files["opencost/NOTICE"] = (
        b"KubeAtlas modified Chart.yaml and values.yaml for offline OpenCost exporter installation.\n\n"
        + exporter_notice
    )
    write_chart(files, directory / "opencost.tgz")

    layout = directory / "opencost-oci"
    subprocess.run([
        "skopeo", "copy", "--all", "--format", "oci", "--retry-times", "3",
        f"docker://ghcr.io/opencost/opencost@{EXPORTER_DIGEST}", f"oci:{layout}:exporter",
    ], check=True, stdout=sys.stderr)
    index = json.loads((layout / "index.json").read_bytes())
    manifests = index["manifests"]
    if (len(manifests) != 1
            or manifests[0].get("annotations", {}).get("org.opencontainers.image.ref.name") != "exporter"):
        raise ValueError("expected a single OpenCost exporter descriptor in the OCI layout")
    digest = manifests[0]["digest"]
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError("invalid OpenCost exporter descriptor digest")
    return digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    inputs = parser.add_mutually_exclusive_group(required=True)
    inputs.add_argument("--input-chart", type=Path, help="official OpenCost Helm chart archive")
    inputs.add_argument("--release-dir", type=Path, help="new directory for the pinned release chart and exporter layout")
    parser.add_argument("--output-chart", type=Path, help="new offline OpenCost chart archive; required with --input-chart")
    args = parser.parse_args()
    if bool(args.input_chart) != bool(args.output_chart):
        parser.error("--input-chart and --output-chart must be used together")
    try:
        if args.release_dir:
            digest = prepare_release(args.release_dir)
        else:
            digest = write_chart(read_upstream_chart(args.input_chart), args.output_chart)
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError, tarfile.TarError, yaml.YAMLError) as error:
        parser.exit(1, f"prepare-opencost-base: {error}\n")

    print(digest)


if __name__ == "__main__":
    main()
