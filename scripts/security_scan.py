#!/usr/bin/env python3
"""Run the pinned, standalone SAST gate without platform credentials."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
LOCK = ROOT / ".github/security-rules.lock.json"
ENGINE_VERSION = "1.180.0"
EXTENSIONS = {".go", ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs"}


def production_source(path):
    path = Path(path)
    if path.suffix not in EXTENSIONS:
        return False
    if any(part in {"testdata", "__tests__", "node_modules", "vendor", "dist", "wailsjs"} for part in path.parts):
        return False
    if path.name.endswith(("_test.go", ".test.ts", ".test.tsx", ".test.js", ".spec.ts", ".spec.tsx", ".d.ts")):
        return False
    name = path.as_posix()
    return (
        name.startswith(("cmd/", "internal/", "pkg/", "web/src/", "packages/"))
        and not name.startswith("cmd/testserver/")
    )


def prepare_rules(source, destination, lock):
    files = lock["files"]
    if not files:
        raise ValueError("Empty rule selection")
    for name, expected in files.items():
        relative = Path(name)
        if relative.is_absolute() or ".." in relative.parts:
            raise ValueError("Invalid rule path")
        original = source / relative
        if hashlib.sha256(original.read_bytes()).hexdigest() != expected:
            raise ValueError(f"Rule checksum mismatch: {name}")
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(original, target)
    shutil.copyfile(source / "LICENSE", destination / "LICENSE")


def require_complete(report, targets, returncode):
    if returncode not in (0, 1):
        raise ValueError(f"Scanner execution failed (exit {returncode})")
    if report.get("errors") != []:
        raise ValueError("Scanner reported errors; partial scans are not passing evidence")
    scanned = {Path(path).resolve() for path in report["paths"]["scanned"]}
    expected = {Path(path).resolve() for path in targets}
    if not expected or scanned != expected:
        raise ValueError(f"Incomplete target coverage: expected {len(expected)}, scanned {len(scanned)}")
    if not isinstance(report.get("results"), list):
        raise ValueError("Scanner results missing")
    if returncode != (1 if report["results"] else 0):
        raise ValueError("Scanner exit status and findings disagree")
    return not report["results"]


def scan(config, targets, output):
    env = {key: value for key, value in os.environ.items() if not key.startswith("SEMGREP_")}
    env["SEMGREP_SEND_METRICS"] = "off"
    command = [
        "semgrep", "scan", "--oss-only", "--metrics=off", "--disable-version-check",
        "--disable-nosem", "--strict", "--error", "--no-git-ignore",
        "--max-target-bytes=0", "--timeout=0", "--jobs=2",
        "--config", config.name, "--json", *map(str, targets),
    ]
    with output.open("w") as stream:
        result = subprocess.run(command, cwd=config.parent, env=env, stdout=stream, check=False)
    report = json.loads(output.read_text())
    passed = require_complete(report, targets, result.returncode)
    print(f"Scanned {len(targets)} files; findings: {len(report['results'])}; report: {output}")
    return passed, report


def self_test(config, output):
    with tempfile.TemporaryDirectory(prefix="kubeatlas-sast-canary-") as directory:
        root = Path(directory)
        clean = root / "clean.go"
        clean.write_text('package main\nfunc main() {}\n')
        passed, _ = scan(config, [clean], output / "canary-clean.json")
        if not passed:
            raise ValueError("Clean canary unexpectedly failed")
        samples = {
            "unsafe.go": 'package main\nimport "net/http/cgi"\nfunc main() { cgi.Serve(nil) }\n',
            "unsafe.js": 'function run(input) { return eval(input); }\n',
            "unsafe.ts": 'function run(input: string) { return eval(input); }\n',
        }
        targets = []
        for name, content in samples.items():
            path = root / name
            path.write_text(content)
            targets.append(path)
        passed, report = scan(config, targets, output / "canary-unsafe.json")
        detected = {Path(item["path"]).name for item in report["results"]}
        if passed or not set(samples).issubset(detected):
            raise ValueError("Unsafe Go/JS/TS canaries were not all blocked")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--rules-source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    try:
        version = subprocess.check_output(
            ["semgrep", "--disable-version-check", "--version"], text=True,
            env={**os.environ, "SEMGREP_SEND_METRICS": "off"},
        ).strip()
        if version != ENGINE_VERSION:
            raise ValueError(f"Expected Semgrep {ENGINE_VERSION}, got {version}")
        lock = json.loads(LOCK.read_text())
        with tempfile.TemporaryDirectory(prefix="kubeatlas-sast-rules-") as directory:
            config = Path(directory) / "rules"
            prepare_rules(args.rules_source.resolve(), config, lock)
            shutil.copyfile(ROOT / ".semgrepignore", config.parent / ".semgrepignore")
            if args.self_test:
                self_test(config, output)
                return 0
            tracked = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).decode().split("\0")
            targets = [ROOT / name for name in tracked if production_source(name)]
            subprocess.run(["git", "diff", "--exit-code", "HEAD", "--", *map(str, targets)],
                           cwd=ROOT, stdout=subprocess.DEVNULL, check=True)
            source_sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
            (output / "provenance.json").write_text(json.dumps({
                "sourceSha": source_sha, "engine": version,
                "event": os.environ.get("GITHUB_EVENT_NAME"),
                "runUrl": (
                    f"{os.environ['GITHUB_SERVER_URL']}/{os.environ['GITHUB_REPOSITORY']}"
                    f"/actions/runs/{os.environ['GITHUB_RUN_ID']}"
                    if os.environ.get("GITHUB_RUN_ID") else None
                ),
                "rulesCommit": lock["commit"], "ruleCount": lock["ruleCount"],
                "lockSha256": hashlib.sha256(LOCK.read_bytes()).hexdigest(),
                "targets": [str(path.relative_to(ROOT)) for path in targets],
            }, indent=2) + "\n")
            passed, _ = scan(config, targets, output / "findings.json")
            return 0 if passed else 1
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as error:
        print(f"Security scan blocked: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
