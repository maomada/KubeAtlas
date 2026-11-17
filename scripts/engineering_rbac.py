#!/usr/bin/env python3
"""Create and clean up only UID-ledgered RBAC test fixtures."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import uuid
from urllib.parse import quote

OWNER_LABEL = "kubeatlas.dev/engineering-test-run"
RESOURCES = {
    ("v1", "Namespace"): (False, "namespaces", "/api/v1/namespaces"),
    ("v1", "Pod"): (True, "pods", "/api/v1/namespaces/{namespace}/pods"),
    ("v1", "Secret"): (True, "secrets", "/api/v1/namespaces/{namespace}/secrets"),
    ("rbac.authorization.k8s.io/v1", "Role"): (
        True, "roles.rbac.authorization.k8s.io",
        "/apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/roles"
    ),
    ("rbac.authorization.k8s.io/v1", "RoleBinding"): (
        True, "rolebindings.rbac.authorization.k8s.io",
        "/apis/rbac.authorization.k8s.io/v1/namespaces/{namespace}/rolebindings"
    ),
    ("rbac.authorization.k8s.io/v1", "ClusterRole"): (
        False, "clusterroles.rbac.authorization.k8s.io",
        "/apis/rbac.authorization.k8s.io/v1/clusterroles"
    ),
    ("rbac.authorization.k8s.io/v1", "ClusterRoleBinding"): (
        False, "clusterrolebindings.rbac.authorization.k8s.io",
        "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings"
    ),
    ("apiextensions.k8s.io/v1", "CustomResourceDefinition"): (
        False, "customresourcedefinitions.apiextensions.k8s.io",
        "/apis/apiextensions.k8s.io/v1/customresourcedefinitions"
    ),
    ("test.skyhook.io/v1", "KubeAtlasTest"): (
        False, "kubeatlastests.test.skyhook.io",
        "/apis/test.skyhook.io/v1/kubeatlastests"
    ),
}


def kubectl(kubeconfig, context, args, input_text=None, request_timeout="15s"):
    return subprocess.run(
        [
            "kubectl",
            f"--request-timeout={request_timeout}",
            f"--kubeconfig={kubeconfig}",
            f"--context={context}",
            *args,
        ],
        input=input_text,
        capture_output=True,
        text=True,
        check=False,
    )


def append_record(ledger, record):
    path = Path(ledger)
    data = (json.dumps(record, sort_keys=True) + "\n").encode()
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "ab", closefd=False) as stream:
            stream.write(data)
            stream.flush()
            os.fsync(fd)
    finally:
        os.close(fd)


def object_identity(obj):
    metadata = obj.get("metadata")
    if not isinstance(metadata, dict):
        raise ValueError("manifest has no metadata")
    identity = {
        "apiVersion": obj.get("apiVersion"),
        "kind": obj.get("kind"),
        "namespace": metadata.get("namespace", ""),
        "name": metadata.get("name"),
    }
    if not all(isinstance(identity[key], str) and identity[key] for key in ("apiVersion", "kind", "name")):
        raise ValueError("manifest is missing apiVersion, kind, or name")
    if identity["kind"] == "List" or (identity["apiVersion"], identity["kind"]) not in RESOURCES:
        raise ValueError("manifest must be one supported fixture object")
    namespaced, _, _ = RESOURCES[(identity["apiVersion"], identity["kind"])]
    if namespaced != bool(identity["namespace"]):
        raise ValueError("fixture namespace does not match its resource scope")
    return identity


def create(args):
    manifest = sys.stdin.read()
    dry_run = kubectl(
        args.kubeconfig,
        args.context,
        ["create", "--dry-run=client", "-f", "-", "-o", "json"],
        manifest,
    )
    if dry_run.returncode:
        print(f"Manifest validation failed (kubectl exit {dry_run.returncode}).", file=sys.stderr)
        return 1
    try:
        obj = json.loads(dry_run.stdout)
        identity = object_identity(obj)
    except (json.JSONDecodeError, ValueError) as error:
        print(f"Manifest validation failed: {error}", file=sys.stderr)
        return 1

    metadata = obj["metadata"]
    labels = metadata.setdefault("labels", {})
    if not isinstance(labels, dict):
        print("Manifest labels are invalid.", file=sys.stderr)
        return 1
    if OWNER_LABEL in labels and labels[OWNER_LABEL] != args.owner:
        print("Manifest already has a different owner label.", file=sys.stderr)
        return 1
    labels[OWNER_LABEL] = args.owner
    attempt_id = uuid.uuid4().hex
    common = {
        **identity,
        "ownerLabel": {"key": OWNER_LABEL, "value": args.owner},
        "attemptId": attempt_id,
    }
    try:
        append_record(args.ledger, {"status": "attempted", **common})
    except OSError as error:
        print(f"Could not durably record create intent: {error}", file=sys.stderr)
        return 1

    created = kubectl(
        args.kubeconfig,
        args.context,
        ["create", "-f", "-", "-o", "json"],
        json.dumps(obj),
    )
    if created.returncode:
        try:
            append_record(args.ledger, {"status": "uncertain", **common})
        except OSError:
            pass
        print(
            f"Create outcome is uncertain for {identity['kind']} "
            f"{identity['namespace']}/{identity['name']}; no cleanup ownership was inferred.",
            file=sys.stderr,
        )
        return 1
    try:
        created_obj = json.loads(created.stdout)
        created_identity = object_identity(created_obj)
        created_metadata = created_obj["metadata"]
        if created_identity != identity:
            raise ValueError("API response identity differs from the manifest")
        if created_metadata.get("labels", {}).get(OWNER_LABEL) != args.owner:
            raise ValueError("API response owner label differs from the run")
        uid = created_metadata.get("uid")
        if not isinstance(uid, str) or not uid:
            raise ValueError("API response did not include a UID")
        append_record(
            args.ledger,
            {
                "status": "created",
                **common,
                "uid": uid,
            },
        )
    except (json.JSONDecodeError, KeyError, OSError, ValueError, AttributeError) as error:
        try:
            append_record(args.ledger, {"status": "uncertain", **common})
        except OSError:
            pass
        print(
            f"Create succeeded without a durable ownership record for "
            f"{identity['kind']} {identity['namespace']}/{identity['name']}: {error}. "
            "Residue is uncertain and will not be deleted.",
            file=sys.stderr,
        )
        return 1

    print(
        f"Created {identity['kind']} {identity['namespace']}/{identity['name']} "
        f"uid={uid}."
    )
    return 0


def load_ledger(path):
    records = []
    with Path(path).open(encoding="utf-8") as stream:
        for line_number, line in enumerate(stream, 1):
            try:
                record = json.loads(line)
            except json.JSONDecodeError as error:
                raise ValueError(f"invalid ledger line {line_number}") from error
            if not isinstance(record, dict):
                raise ValueError(f"invalid ledger record on line {line_number}")
            records.append(record)
    return records


def resource_url(record):
    kind = record.get("kind")
    api_version = record.get("apiVersion")
    if (api_version, kind) not in RESOURCES:
        raise ValueError("ledger contains an unsupported resource")
    namespaced, _, template = RESOURCES[(api_version, kind)]
    name = record.get("name")
    namespace = record.get("namespace", "")
    if not isinstance(name, str) or not name or namespaced != bool(namespace):
        raise ValueError("ledger resource identity is incomplete")
    values = {"name": quote(name, safe="-._~")}
    if namespaced:
        values["namespace"] = quote(namespace, safe="-._~")
    return template.format(**values) + "/" + values["name"]


def resource_reference(record):
    key = (record.get("apiVersion"), record.get("kind"))
    if key not in RESOURCES:
        raise ValueError("ledger contains an unsupported resource")
    namespaced, resource, _ = RESOURCES[key]
    name = record.get("name")
    namespace = record.get("namespace", "")
    if (
        not isinstance(name, str)
        or not name
        or not isinstance(namespace, str)
        or namespaced != bool(namespace)
    ):
        raise ValueError("ledger resource identity is incomplete")
    return namespaced, f"{resource}/{name}", namespace


def get_object(args, record):
    namespaced, reference, namespace = resource_reference(record)
    command = ["get", reference, "--ignore-not-found", "-o", "json"]
    if namespaced:
        command.extend(["--namespace", namespace])
    result = kubectl(args.kubeconfig, args.context, command)
    if result.returncode:
        raise RuntimeError(f"kubectl get failed (exit {result.returncode})")
    if not result.stdout.strip():
        return None
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError("kubectl get returned invalid JSON") from error


def wait_for_deletion(args, record):
    namespaced, reference, namespace = resource_reference(record)
    command = ["wait", "--for=delete", reference, "--timeout=30s"]
    if namespaced:
        command.extend(["--namespace", namespace])
    result = kubectl(
        args.kubeconfig,
        args.context,
        command,
        request_timeout="35s",
    )
    if result.returncode:
        raise RuntimeError(f"kubectl wait for deletion failed (exit {result.returncode})")


def delete_owned(args, record):
    label = record.get("ownerLabel")
    if not isinstance(label, dict) or label.get("key") != OWNER_LABEL or label.get("value") != args.owner:
        raise ValueError("ledger owner label does not match this run")
    url = resource_url(record)
    obj = get_object(args, record)
    if obj is None:
        print(f"Already absent: {record['kind']} {record.get('namespace', '')}/{record['name']}.")
        return
    if object_identity(obj) != {
        key: record.get(key, "") for key in ("apiVersion", "kind", "namespace", "name")
    }:
        raise ValueError(f"live object identity mismatch; refused to delete {record['kind']} {record['name']}")
    metadata = obj.get("metadata", {})
    if metadata.get("uid") != record.get("uid"):
        raise ValueError(f"UID mismatch; refused to delete {record['kind']} {record['name']}")
    if metadata.get("labels", {}).get(OWNER_LABEL) != args.owner:
        raise ValueError(f"owner label mismatch; refused to delete {record['kind']} {record['name']}")
    resource_version = metadata.get("resourceVersion")
    if not isinstance(resource_version, str) or not resource_version:
        raise ValueError(f"missing resourceVersion; refused to delete {record['kind']} {record['name']}")
    options = {
        "apiVersion": "v1",
        "kind": "DeleteOptions",
        "preconditions": {
            "uid": record["uid"],
            "resourceVersion": resource_version,
        },
    }
    result = kubectl(
        args.kubeconfig,
        args.context,
        ["delete", f"--raw={url}", "-f", "-"],
        json.dumps(options),
    )
    if result.returncode:
        raise RuntimeError(f"kubectl delete failed (exit {result.returncode})")
    description = f"{record['kind']} {record.get('namespace', '')}/{record['name']}"
    print(f"Deletion requested for owned {description}.")
    wait_for_deletion(args, record)
    print(f"Deletion confirmed absent: {description}.")


def cleanup(args):
    try:
        records = load_ledger(args.ledger)
    except (OSError, ValueError) as error:
        print(f"Cannot safely read cleanup ledger: {error}", file=sys.stderr)
        return 1

    completed = {record.get("attemptId") for record in records if record.get("status") in ("created", "uncertain")}
    uncertain = [
        record for record in records
        if record.get("status") == "uncertain"
        or (record.get("status") == "attempted" and record.get("attemptId") not in completed)
    ]
    failures = 0
    for record in reversed(records):
        if record.get("status") != "created":
            continue
        try:
            delete_owned(args, record)
        except (RuntimeError, ValueError, KeyError, TypeError) as error:
            failures += 1
            print(
                f"Cleanup refused/failed for {record.get('kind', '?')} "
                f"{record.get('namespace', '')}/{record.get('name', '?')}: {error}",
                file=sys.stderr,
            )
    for record in uncertain:
        failures += 1
        print(
            f"Uncertain residue retained: {record.get('kind', '?')} "
            f"{record.get('namespace', '')}/{record.get('name', '?')}; reconcile manually.",
            file=sys.stderr,
        )
    return 1 if failures else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("create", "cleanup"):
        subparser = commands.add_parser(name)
        subparser.add_argument("--ledger", required=True)
        subparser.add_argument("--kubeconfig", required=True)
        subparser.add_argument("--context", required=True)
        subparser.add_argument("--owner", required=True)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", args.owner):
        parser.error("owner must be a DNS label")
    try:
        return create(args) if args.command == "create" else cleanup(args)
    except OSError as error:
        print(f"Engineering RBAC helper failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
