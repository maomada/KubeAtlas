#!/usr/bin/env python3
"""Verify fresh admin setup and run a Hub-issued enrollment on disposable kind."""

import argparse
import hashlib
from http.cookiejar import CookieJar
import ipaddress
import json
import os
from pathlib import Path
import re
import secrets
import signal
import ssl
import subprocess
import sys
import time
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import HTTPCookieProcessor, HTTPSHandler, ProxyHandler, Request, build_opener


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def kube(args, *command):
    return subprocess.check_output(
        ["kubectl", "--kubeconfig", str(args.kubeconfig), "--context", args.context, *command],
        timeout=30, text=True,
    ).strip()


class Hub:
    def __init__(self, origin, ca):
        self.origin = origin
        self.jar = CookieJar()
        self.opener = build_opener(
            ProxyHandler({}), HTTPSHandler(context=ssl.create_default_context(cafile=str(ca))),
            HTTPCookieProcessor(self.jar),
        )

    def request(self, path, data=None, status=200, document=False):
        body = json.dumps(data).encode() if data is not None else None
        request = Request(
            self.origin + path, data=body,
            headers={"Content-Type": "application/json", "Origin": self.origin},
        )
        with self.opener.open(request, timeout=20) as response:
            require(response.status == status, f"Unexpected Hub status for {path}")
            raw = response.read(8 * 1024 * 1024 + 1)
            require(len(raw) <= 8 * 1024 * 1024, "Hub response exceeds acceptance limit")
            if document:
                return response.geturl(), raw
            return json.loads(raw) if raw else None


def initialize(args, hub):
    require(hub.request("/api/setup") == {"required": True}, "Fresh package did not require setup")
    url, page = hub.request("/", document=True)
    require(url == args.origin + "/setup" and b'id="portal"' in page, "Fresh Hub did not serve the setup page")
    code = subprocess.check_output(
        ["docker", "exec", args.container, "/usr/bin/kubeatlas-portal",
         "--hub-state-file=/var/lib/kubeatlas-portal/state.json", "--show-setup-code"],
        text=True, timeout=30,
    ).strip()
    require(re.fullmatch(r"[A-Za-z0-9_-]{43}", code), "Invalid one-time setup code")
    credentials = {"username": "acceptance-owner", "password": secrets.token_urlsafe(32)}
    hub.request("/api/setup", {**credentials, "code": code}, status=204)
    require(hub.request("/api/setup") == {"required": False}, "Hub setup did not complete")
    subprocess.run(
        ["docker", "exec", args.container, "sh", "-ceu",
         'state=/var/lib/kubeatlas-portal; test "$(stat -c %a "$state"/state.json)" = 600; '
         'test "$(stat -c %a "$state")" = 700; test ! -e "$state"/setup-code'],
        check=True, timeout=30,
    )
    subprocess.run(
        ["docker", "exec", args.container, "systemctl", "restart", "kubeatlas-portal"],
        check=True, timeout=30,
    )
    for _ in range(30):
        try:
            setup = hub.request("/api/setup")
            break
        except HTTPError as error:
            if error.code not in (502, 503):
                raise
        time.sleep(1)
    else:
        raise RuntimeError("Hub did not respond after restart")
    require(setup == {"required": False}, "Setup reopened after restart")
    hub.request("/api/auth/login", credentials, status=204)
    require(any(cookie.name == "__Host-kubeatlas_portal_session" and cookie.secure for cookie in hub.jar),
            "Hub did not issue a secure login session")


def install_agent(args, hub, command):
    environment = {key: os.environ[key] for key in ("PATH", "HOME", "LANG") if key in os.environ}
    environment.update(KUBECONFIG=str(args.kubeconfig), CURL_CA_BUNDLE=str(args.ca),
                       KUBEATLAS_AGENT_HUB_CA_FILE=str(args.ca))
    path = args.kubeconfig.with_name(f"{args.method}-pairing.log")
    prompt = re.compile(r"Enrollment ([0-9a-f]{32}); approval code ([A-Z2-9]{4}-[A-Z2-9]{4})\.")
    process = None
    try:
        with path.open("xb") as log:
            process = subprocess.Popen(
                ["bash", "-c", command], stdin=subprocess.PIPE, stdout=log, stderr=log,
                env=environment, start_new_session=True,
            )
            for _ in range(120):
                match = prompt.search(path.read_text(errors="replace"))
                if match:
                    enrollment_id, code = match.groups()
                    break
                require(process.poll() is None, "Hub command exited before the pairing prompt")
                time.sleep(1)
            else:
                raise RuntimeError("Hub command did not present a pairing prompt")
            pending = hub.request("/api/hub/enrollments/lookup", {"userCode": code})
            require(pending.get("id") == enrollment_id and pending.get("contextName") == args.context
                    and pending.get("status") == "pending"
                    and pending.get("clusterFingerprint") == hashlib.sha256(args.uid.encode()).hexdigest(),
                    "Pairing request did not refer to the isolated Kubernetes context")
            approval = hub.request(f"/api/hub/enrollments/{enrollment_id}/approve", {
                "userCode": code, "organizationId": "self-hosted", "name": "Acceptance " + args.method,
            })
            require(approval.get("status") == "approved", "Hub pairing approval did not succeed")
            process.communicate(b"y\ny\n", timeout=330)
            require(process.returncode == 0, "Hub-issued command failed during Agent installation")
            return enrollment_id
    except BaseException:
        if process is not None and process.poll() is None:
            os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=10)
        if path.is_file():
            for line in path.read_text(errors="replace").splitlines()[-10:]:
                line = re.sub(r"\bka_(?:device|agent)_[A-Za-z0-9_-]+\b", "[redacted]", line)
                line = re.sub(r"\b[A-Z2-9]{4}-[A-Z2-9]{4}\b", "[redacted]", line)
                print(line, file=sys.stderr)
        raise
    finally:
        path.unlink(missing_ok=True)


def accept(args):
    origin = urlsplit(args.origin)
    require(origin.scheme == "https" and origin.hostname and origin.port in (22431, 22432)
            and ipaddress.ip_address(origin.hostname).is_private and not origin.path,
            "Hub origin is not on the isolated HTTPS bridge")
    require(args.context.startswith("kind-ka-native-") and args.container.startswith("ka-native-"),
            "A disposable kind fixture is required")
    require(kube(args, "config", "current-context") == args.context, "Unexpected active kubeconfig context")
    api = kube(args, "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
    require(api.startswith("https://127.0.0.1:"), "Kind API is not bound to loopback")
    require(kube(args, "get", "ns", "kube-system", "-o", "jsonpath={.metadata.uid}") == args.uid,
            "Kubernetes fixture identity changed")
    nodes = json.loads(kube(args, "get", "nodes", "-o", "json"))["items"]
    require(len(nodes) == 1 and nodes[0]["status"]["nodeInfo"]["architecture"] == args.architecture
            and nodes[0]["metadata"]["name"] == args.context.removeprefix("kind-") + "-control-plane",
            "Expected a native kind node for the requested architecture")
    namespace = "accept-native-" + args.method
    kube(args, "create", "namespace", namespace)

    hub = Hub(args.origin, args.ca)
    initialize(args, hub)
    info = hub.request("/api/hub/connections")
    catalog = info["catalog"]
    require(info.get("publicURL") == args.origin and catalog.get("available")
            and catalog.get("bootstrapAvailable") and catalog.get("version") == args.version
            and catalog.get("imageDigest") == args.image_digest,
            "Hub is not serving the approved Agent catalog for its HTTPS origin")
    require(not hub.request("/api/hub/clusters")["clusters"], "Expected no clusters in the fresh Hub")
    issued = hub.request("/api/hub/enrollments/command", {"profile": "public"})
    command = issued["command"]
    require(issued["origin"] == args.origin and args.origin in command
            and f"KUBEATLAS_VERSION='{args.version}'" in command and "/bootstrap.sh" in command,
            "Hub command did not pin its own HTTPS origin and candidate version")
    enrollment = install_agent(args, hub, command)
    cluster_id = "enrollment-" + enrollment[:12]
    agent_ns = "kubeatlas-agent-" + enrollment[:10]
    for _ in range(60):
        row = next((item for item in hub.request("/api/hub/clusters")["clusters"]
                    if item["id"] == cluster_id), None)
        if row and row.get("state") == "connected":
            break
        time.sleep(1)
    else:
        raise RuntimeError("Fresh Hub did not observe a connected Agent")
    require(row["agent"]["version"] == args.version, "Connected Agent version differs from package")
    deployment = json.loads(kube(args, "-n", agent_ns, "get", "deployment", agent_ns, "-o", "json"))
    require(deployment["spec"]["template"]["spec"]["containers"][0]["image"]
            == origin.netloc + "/kubeatlas/agent@" + args.image_digest
            and deployment["status"].get("readyReplicas", 0) >= 1,
            "Agent Deployment did not become Ready with the pinned Hub image")
    for _ in range(45):
        try:
            namespaces = hub.request(f"/c/{cluster_id}/api/namespaces")
            if any(item.get("name") == namespace for item in namespaces):
                break
        except HTTPError as error:
            if error.code not in (502, 503):
                raise
        time.sleep(1)
    else:
        raise RuntimeError("Hub could not read the prepared namespace through the Agent")

    with args.package.open("rb") as package:
        checksum = hashlib.file_digest(package, "sha256").hexdigest()
    print(json.dumps({
        "package": args.package.name, "packageSha256": checksum, "sourceSha": args.source_sha,
        "version": args.version, "architecture": args.architecture,
        "packageManager": {"deb": "apt", "rpm": "dnf"}[args.method], "systemdActive": True,
        "administratorSetup": True, "agentState": row["state"], "agentImageDigest": args.image_digest,
        "permittedNamespaceRead": namespace,
    }))


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("method", choices=("deb", "rpm"))
    parser.add_argument("--origin", required=True)
    parser.add_argument("--container", required=True)
    parser.add_argument("--context", required=True)
    parser.add_argument("--uid", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--architecture", choices=("amd64", "arm64"), required=True)
    parser.add_argument("--source-sha", required=True)
    parser.add_argument("--image-digest", required=True)
    parser.add_argument("--kubeconfig", type=Path, required=True)
    parser.add_argument("--ca", type=Path, required=True)
    parser.add_argument("--package", type=Path, required=True)
    accept(parser.parse_args())


if __name__ == "__main__":
    main()
