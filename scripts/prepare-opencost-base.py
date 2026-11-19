#!/usr/bin/env python3
"""Prepare a pinned OpenCost Helm chart for Hub-hosted Agent registration."""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile

import yaml


UPSTREAM_CHART_VERSION = "2.5.34"
UPSTREAM_CHART_SHA256 = "9ba42bb2ae4df6854d51e1d08fa45f14aed49781b8e597e00c0d789c271aacee"
EXPORTER_VERSION = "1.121.4"
EXPORTER_DIGEST = "sha256:daa04ae48f17b830de941c3602563c5a01fabb13522ede1ba057f09e96613aaa"
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input-chart", required=True, type=Path, help="official OpenCost Helm chart archive")
    parser.add_argument("--output-chart", required=True, type=Path, help="new offline OpenCost chart archive")
    args = parser.parse_args()

    try:
        data = prepare_chart(read_upstream_chart(args.input_chart))
        destination = args.output_chart.open("xb")
        try:
            with destination:
                destination.write(data)
        except OSError:
            args.output_chart.unlink(missing_ok=True)
            raise
    except (OSError, ValueError, tarfile.TarError, yaml.YAMLError) as error:
        parser.exit(1, f"prepare-opencost-base: {error}\n")

    print(hashlib.sha256(data).hexdigest())


if __name__ == "__main__":
    main()
