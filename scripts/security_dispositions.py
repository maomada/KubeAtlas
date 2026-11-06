"""Apply reviewed, source-bound dispositions without changing scanner evidence."""

import hashlib
import json
from pathlib import Path


def source_digest(root, targets):
    files = sorted((Path(path).resolve().relative_to(root.resolve()).as_posix(),
                    hashlib.sha256(Path(path).read_bytes()).hexdigest()) for path in targets)
    return hashlib.sha256(json.dumps(files, separators=(",", ":")).encode()).hexdigest()


def finding_key(item, root):
    path = Path(item["path"])
    if not path.is_absolute():
        path = root / path
    name = path.resolve().relative_to(root.resolve()).as_posix()
    return (item["check_id"], name, item["start"]["line"], item["start"]["col"],
            item["end"]["line"], item["end"]["col"])


def evaluate(report, policy, root, targets, engine, lock_hash):
    if policy["schemaVersion"] != 1:
        raise ValueError("Unsupported security disposition schema")
    if policy["engine"] != engine or policy["rulesLockSha256"] != lock_hash:
        raise ValueError("Security dispositions require review after scanner/rule changes")
    if policy["sourceTreeSha256"] != source_digest(root, targets):
        raise ValueError("Security dispositions require review after production source changes")
    entries = policy["entries"]
    if not isinstance(entries, list):
        raise ValueError("Disposition entries must be a list")
    by_key = {}
    ids = set()
    for entry in entries:
        if entry["id"] in ids:
            raise ValueError("Duplicate disposition ID")
        ids.add(entry["id"])
        if entry["disposition"] not in ("false-positive", "mitigated", "hold"):
            raise ValueError("Unknown disposition")
        if not entry["evidence"].strip() or not entry["reviewedSourceSha"].strip():
            raise ValueError("Disposition evidence missing")
        approval = entry["approval"]
        if approval["approved"] is not True or not approval["actor"] or not approval["reference"]:
            raise ValueError("Disposition approval missing")
        key = finding_key(entry["finding"], root)
        if key in by_key:
            raise ValueError("Duplicate disposition finding")
        by_key[key] = entry
    approved, unresolved, seen = [], [], set()
    for item in report["results"]:
        key = finding_key(item, root)
        if key in seen:
            raise ValueError("Duplicate scanner finding identity")
        seen.add(key)
        entry = by_key.get(key)
        record = {"rule": key[0], "path": key[1], "start": item["start"], "end": item["end"]}
        if entry:
            record["id"] = entry["id"]
            record["disposition"] = entry["disposition"]
        if entry and entry["disposition"] in ("false-positive", "mitigated"):
            approved.append(record)
        else:
            unresolved.append(record)
    if set(by_key) - seen:
        raise ValueError("Stale dispositions: a reviewed finding is no longer present")
    return {"rawFindingCount": len(report["results"]), "approved": approved,
            "unresolved": unresolved, "passed": not unresolved}
