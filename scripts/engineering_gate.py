#!/usr/bin/env python3
"""Fail-closed development gates; never a substitute for release acceptance."""

import argparse
import json
import os
from pathlib import Path
import re
import sys

REPOSITORY = "maomada/KubeAtlas"
SCHEMA = "kubeatlas-engineering-evidence/v1"
SELECTORS = ("pkg", "go", "web", "app", "helm", "containers")
CI_JOBS = {
    "installers": None, "changes": None, "release-policy": None,
    "workflow-lint": None, "engineering-tests": None,
    "pkg": "pkg", "backend": "go", "backend-server": "go",
    "repo-hygiene": "!go", "frontend": "web", "k8s-ui": "web",
    "settings-browser": "app", "helm": "helm", "container-build": "containers",
}


def boolean(value):
    if value not in ("true", "false"):
        raise ValueError(f"Invalid selector: {value!r}")
    return value == "true"


def sha(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
        raise ValueError("Expected a full lowercase commit SHA")
    return value


def evaluate_jobs(needs, required):
    if set(needs) != set(required):
        raise ValueError("Job inventory differs from the frozen gate plan")
    results = {}
    for job, selected in required.items():
        status = needs[job].get("result")
        if selected:
            results[job] = "passed" if status == "success" else "failed"
        elif status == "skipped":
            results[job] = "not_required_by_plan"
        else:
            # An unplanned successful job is evidence, not an exemption for failure.
            results[job] = "passed" if status == "success" else "failed"
    return results


def ci_results(needs):
    outputs = needs.get("changes", {}).get("outputs", {})
    selected = {key: boolean(outputs.get(key)) for key in SELECTORS}
    required = {}
    for job, selector in CI_JOBS.items():
        required[job] = (True if selector is None else
                         not selected[selector[1:]] if selector.startswith("!") else
                         selected[selector])
    return evaluate_jobs(needs, required)


def package_results(needs):
    selected = boolean(needs.get("changes", {}).get("outputs", {}).get("packages"))
    return evaluate_jobs(needs, {"changes": True, "validate": selected})


def receipt(gate, needs, env):
    if env.get("GITHUB_REPOSITORY") != REPOSITORY:
        raise ValueError("Unexpected repository")
    event = env.get("GITHUB_EVENT_NAME")
    if event not in ("pull_request", "push", "schedule"):
        raise ValueError("Unsupported evidence event")
    results = (ci_results(needs) if gate == "ci" else
               package_results(needs) if gate == "packages" else
               evaluate_jobs(needs, {"sast": True}))
    run_id, attempt = env.get("GITHUB_RUN_ID", ""), env.get("GITHUB_RUN_ATTEMPT", "")
    if not run_id.isdigit() or not attempt.isdigit() or int(attempt) < 1:
        raise ValueError("Missing workflow run identity")
    return {
        "schema": SCHEMA, "repository": REPOSITORY, "origin": "github-actions",
        "gate": gate, "head_sha": sha(env.get("HEAD_SHA")),
        "tested_sha": sha(env.get("GITHUB_SHA")), "event": event,
        "run_id": run_id, "run_attempt": attempt,
        "run_url": f"https://github.com/{REPOSITORY}/actions/runs/{run_id}/attempts/{attempt}",
        "results": results,
        "status": "passed" if all(v != "failed" for v in results.values()) else "failed",
    }


def aggregate(plan, receipts):
    if plan.get("schema") != "kubeatlas-engineering-plan/v1":
        raise ValueError("Invalid plan schema")
    if plan.get("repository") != REPOSITORY or plan.get("origin") != "github-actions":
        raise ValueError("This adapter accepts Actions evidence only")
    if plan.get("profile") not in ("pr-ready", "main"):
        raise ValueError("Only merge-ready profiles may pass")
    head, tested = sha(plan.get("head_sha")), sha(plan.get("tested_sha"))
    if plan["profile"] == "main" and head != tested:
        raise ValueError("Main evidence must test the source commit")
    expected_event = "push" if plan["profile"] == "main" else "pull_request"
    gates = plan.get("gates")
    if not isinstance(gates, dict) or set(gates) != {"ci", "security", "packages"}:
        raise ValueError("All three workflow gates are required")
    if len(receipts) != len(gates):
        raise ValueError("Missing or duplicate receipts")
    seen = set()
    for evidence in receipts:
        gate = evidence.get("gate")
        if gate not in gates or gate in seen:
            raise ValueError("Unknown or duplicate gate")
        seen.add(gate)
        for key, expected in {
            "schema": SCHEMA, "repository": REPOSITORY, "origin": "github-actions",
            "head_sha": head, "tested_sha": tested, "event": expected_event,
            "status": "passed",
        }.items():
            if evidence.get(key) != expected:
                raise ValueError(f"{gate}: incorrect {key}")
        binding = gates[gate]
        for key in ("run_id", "run_attempt"):
            expected = binding.get(key)
            if not isinstance(expected, str) or not expected.isdigit() or int(expected) < 1:
                raise ValueError(f"{gate}: invalid plan {key}")
            if evidence.get(key) != expected:
                raise ValueError(f"{gate}: stale {key}")
        required = binding.get("required_jobs")
        if not isinstance(required, dict) or any(type(v) is not bool for v in required.values()):
            raise ValueError(f"{gate}: invalid job plan")
        inventory = (set(CI_JOBS) if gate == "ci" else
                     {"sast"} if gate == "security" else {"changes", "validate"})
        if set(required) != inventory:
            raise ValueError(f"{gate}: incomplete job plan")
        unconditional = ([job for job, selector in CI_JOBS.items() if selector is None]
                         if gate == "ci" else ["sast"] if gate == "security" else ["changes"])
        if any(not required[job] for job in unconditional):
            raise ValueError(f"{gate}: mandatory job deselected")
        if plan["profile"] == "main":
            must_run = inventory - ({"repo-hygiene"} if gate == "ci" else set())
            if any(not required[job] for job in must_run):
                raise ValueError(f"{gate}: main cannot defer validation")
        results = evidence.get("results", {})
        if set(results) != inventory:
            raise ValueError(f"{gate}: incomplete job evidence")
        for job, selected in required.items():
            allowed = {"passed"} if selected else {"passed", "not_required_by_plan"}
            if results[job] not in allowed:
                raise ValueError(f"{gate}/{job}: required check did not pass")
    return {"status": "passed", "head_sha": head, "tested_sha": tested,
            "origin": "github-actions", "profile": plan["profile"],
            "gates": sorted(seen)}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    emit = sub.add_parser("receipt")
    emit.add_argument("--gate", choices=("ci", "packages", "security"), required=True)
    emit.add_argument("--output", required=True)
    check = sub.add_parser("aggregate")
    check.add_argument("--plan", required=True)
    check.add_argument("receipts", nargs="+")
    args = parser.parse_args()
    try:
        if args.command == "receipt":
            result = receipt(args.gate, json.loads(os.environ["NEEDS_JSON"]), os.environ)
            Path(args.output).write_text(json.dumps(result, indent=2) + "\n")
        else:
            result = aggregate(json.loads(Path(args.plan).read_text()),
                               [json.loads(Path(p).read_text()) for p in args.receipts])
        print(json.dumps(result, indent=2))
        return 0 if result["status"] == "passed" else 1
    except (ValueError, TypeError, KeyError, OSError) as error:
        print(f"Engineering gate blocked: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
