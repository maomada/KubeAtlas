#!/usr/bin/env python3
"""Install or upgrade a native Linux KubeAtlas Hub from local release assets."""

from __future__ import annotations

import argparse
from dataclasses import asdict, dataclass
import hashlib
from http.cookies import SimpleCookie
import ipaddress
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import ssl
import stat
import subprocess
import sys
import tempfile
import time
from urllib.error import URLError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, HTTPSHandler, ProxyHandler, Request, build_opener


COMPONENT = "kubeatlas-portal"
SERVICE = COMPONENT + ".service"
BINARY = Path("/usr/bin") / COMPONENT
UNIT = Path("/etc/systemd/system") / SERVICE
MANAGEMENT = Path("/var/lib/kubeatlas-portal-install")
STATE_DIR = Path("/var/lib/kubeatlas-portal")
STATE = STATE_DIR / "state.json"
CATALOGS = Path("/var/lib/kubeatlas-portal-artifacts")
ENV_FILE = MANAGEMENT / "portal.env"
DEPLOYMENT = MANAGEMENT / "deployment.json"
VERSION = r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?"
PACKAGE = re.compile(rf"{COMPONENT}_({VERSION})_linux_(amd64|arm64)\.(deb|rpm)")
SHA256 = re.compile(r"[0-9a-f]{64}")
AUTHORITY = re.compile(r"(?:[A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\])(?::[0-9]+)?")
MAX_HTTP_BODY = 16 * 1024 * 1024
MANAGED_PACKAGE_ENV = ("env", "KUBEATLAS_NATIVE_INSTALL_MANAGED=1")


class InstallError(RuntimeError):
    pass


def require(condition, message):
    if not condition:
        raise InstallError(message)


def run(*args, accepted=(0,), timeout=180):
    command = [str(arg) for arg in args]
    result = subprocess.run(command, capture_output=True, timeout=timeout, text=True, errors="replace")
    require(result.returncode in accepted,
            f"{command[0]} {command[1]} failed: {result.stderr.strip()[-1000:] or result.returncode}")
    return result.stdout.strip()


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for data in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(data)
    return value.hexdigest()


def checksum(checksums, asset):
    entries = []
    for line in checksums.read_text(encoding="utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64}) [ *]([^/\\]+)", line)
        if match and match[2] == asset.name:
            entries.append(match[1])
    require(len(entries) == 1, f"Expected one checksum for {asset.name}")
    require(digest(asset) == entries[0], f"SHA-256 mismatch for {asset.name}")
    return entries[0]


def normalize_origin(value):
    url = urlsplit(value)
    require(value.startswith("https://") and AUTHORITY.fullmatch(url.netloc) and url.path in ("", "/")
            and not url.query and not url.fragment and "?" not in value and "#" not in value
            and not any(char.isspace() for char in value), "--origin must be an HTTPS root origin")
    require(url.port is None or 0 < url.port < 65536, "Invalid HTTPS origin port")
    host = (url.hostname or "").lower().removesuffix(".")
    if ":" in host:
        ipaddress.IPv6Address(host)
        host = f"[{host}]"
    else:
        label = r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?"
        require(len(host) <= 253 and all(re.fullmatch(label, piece) for piece in host.split(".")),
                "Invalid HTTPS origin host")
    port = "" if url.port in (None, 443) else f":{url.port}"
    return f"https://{host}{port}"


def normalize_listen(value):
    require(AUTHORITY.fullmatch(value), "--listen must be an IP address and port")
    url = urlsplit("http://" + value)
    address = ipaddress.ip_address(url.hostname or "")
    require(not address.is_unspecified and not address.is_multicast and url.port is not None and url.port > 0,
            "--listen must bind a specific local address and port")
    return f"{address}:{url.port}" if address.version == 4 else f"[{address}]:{url.port}"


def credentials(values):
    require(isinstance(values, dict) and set(values) == {"username", "password"},
            "Credentials must contain only username and password")
    user, password = values["username"], values["password"]
    require(isinstance(user, str) and isinstance(password, str) and 1 <= len(user.encode()) <= 256
            and 16 <= len(password.encode()) <= 4096 and ":" not in user
            and all(char.isprintable() for char in user + password), "Invalid Hub owner credential format")
    return values


def read_private(path):
    with open_private(path) as file:
        return file.read(65537).decode("utf-8")


def open_private(path):
    descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    try:
        info = os.fstat(descriptor)
        require(stat.S_ISREG(info.st_mode) and info.st_uid == 0 and stat.S_IMODE(info.st_mode) & 0o177 == 0
                and info.st_size <= 65536, f"Use a root-owned 0600 regular file for {path}")
        return os.fdopen(descriptor, "rb")
    except BaseException:
        os.close(descriptor)
        raise


def encode_env(values):
    return "".join(f"KUBEATLAS_PORTAL_{key.upper()}={json.dumps(value, ensure_ascii=False)}\n"
                   for key, value in values.items())


def read_env(path):
    values = {}
    for line in read_private(path).splitlines():
        key, separator, value = line.partition("=")
        require(separator and key.startswith("KUBEATLAS_PORTAL_"), "Managed credentials are invalid")
        key = key.removeprefix("KUBEATLAS_PORTAL_").lower()
        require(key not in values, "Managed credentials contain duplicate values")
        values[key] = json.loads(value)
    return credentials(values)


def atomic_bytes(path, data, mode=0o600, owner=None):
    descriptor, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as file:
            os.fchmod(file.fileno(), mode)
            if owner is not None:
                os.fchown(file.fileno(), *owner)
            file.write(data)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, path)
    finally:
        Path(temporary).unlink(missing_ok=True)


@dataclass(frozen=True)
class Deployment:
    version: str
    package: str
    package_sha256: str
    listen: str
    origin: str
    previous_version: str | None
    unit_format: int = 1

    def unit(self):
        allowed_hosts = urlsplit(self.origin).netloc
        address = ipaddress.ip_address(urlsplit("http://" + self.listen).hostname)
        if not address.is_loopback and self.listen != allowed_hosts:
            allowed_hosts += "," + self.listen
        command = (f"{BINARY} --listen={self.listen} --hub-state-file={STATE} "
                   f"--hub-artifacts-dir={CATALOGS / self.version} "
                   f"--mcp-allowed-hosts={allowed_hosts}")
        if self.previous_version:
            command += (f" --hub-previous-artifacts-dir={CATALOGS / self.previous_version}"
                        f" --hub-previous-artifacts-version={self.previous_version}")
        env_file = str(ENV_FILE)
        if self.unit_format == 2:
            command += f" --hub-origin={self.origin}"
            env_file = f"-{env_file}"
        return ("[Unit]\nDescription=KubeAtlas Hub\nAfter=network-online.target\nWants=network-online.target\n\n"
                f"[Service]\nUser={COMPONENT}\nGroup={COMPONENT}\nEnvironmentFile={env_file}\n"
                f"UMask=0077\nExecStart={command}\nRestart=on-failure\n\n"
                "[Install]\nWantedBy=multi-user.target\n").encode()


def package_identity(path):
    match = PACKAGE.fullmatch(path.name)
    require(match, "Select an official kubeatlas-portal Linux DEB or RPM")
    version, arch, kind = match.groups()
    host_arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
    require(arch == host_arch, f"Package architecture {arch} does not match this host")
    expected_version = version.replace("-", "~", 1)
    if kind == "deb":
        fields = [run("dpkg-deb", "--field", path, key) for key in ("Package", "Version", "Architecture")]
        expected = [COMPONENT, expected_version, arch]
    else:
        fields = run("rpm", "-qp", "--queryformat", "%{NAME}\n%{VERSION}\n%{RELEASE}\n%{ARCH}\n",
                     path).splitlines()
        expected = [COMPONENT, expected_version, "1", {"amd64": "x86_64", "arm64": "aarch64"}[arch]]
    require(fields == expected, f"Package metadata does not match {path.name}")
    return version, kind


def extract_binary(package, kind, stage, version):
    output = stage / "payload"
    output.mkdir()
    if kind == "deb":
        run("dpkg-deb", "--extract", package, output)
    else:
        repacked = subprocess.run(["rpm2cpio", str(package)], check=True, capture_output=True, timeout=120)
        subprocess.run(["cpio", "-id", "--quiet", "--no-absolute-filenames"], cwd=output,
                       input=repacked.stdout, check=True, capture_output=True, timeout=120)
    candidate = output / BINARY.relative_to("/")
    require(candidate.is_file() and not candidate.is_symlink(), "Portal executable missing from package")
    require(run(candidate, "--version") == f"{COMPONENT} {version}", "Portal executable version mismatch")
    return candidate


def install_package(package, kind, rollback=False):
    if kind == "deb":
        run(*MANAGED_PACKAGE_ENV, "dpkg", "--install", package)
    else:
        arguments = ["--oldpackage", "--replacepkgs"] if rollback else []
        run(*MANAGED_PACKAGE_ENV, "rpm", "--upgrade", *arguments, package)


def installed_package(kind):
    if kind == "deb":
        return run("dpkg-query", "--show", "--showformat=${Status}", COMPONENT,
                   accepted=(0, 1)) == "install ok installed"
    return bool(run("rpm", "--query", "--queryformat", "%{NAME}", COMPONENT,
                    accepted=(0, 1)) == COMPONENT)


def copy_catalog(source, target):
    require(source.is_dir() and not source.is_symlink(), "Catalog must be a regular directory")
    shutil.copytree(source, target, symlinks=True)
    for path in (target, *target.rglob("*")):
        mode = path.lstat().st_mode
        require(stat.S_ISREG(mode) or stat.S_ISDIR(mode), "Catalog contains a link or special file")
        path.chmod(0o755 if stat.S_ISDIR(mode) else 0o444)


def stage_catalog(args, stage, candidate, version):
    catalog = stage / "catalog"
    if args.catalog_archive:
        require(args.catalog_archive.name == f"kubeatlas-hub-agent-bundle_{version}.tar.gz",
                "Select the Agent bundle archive for the package version")
        archive = stage / args.catalog_archive.name
        shutil.copyfile(args.catalog_archive, archive)
        archive_hash = checksum(args.checksums, archive)
        run(candidate, "--import-hub-artifacts", archive, "--hub-artifacts-sha256", archive_hash,
            "--hub-artifacts-dir", catalog)
    else:
        copy_catalog(args.catalog, catalog)
        run(candidate, "--check-hub-artifacts", "--hub-artifacts-dir", catalog)
    manifest = json.loads((catalog / "manifest.json").read_text(encoding="utf-8"))
    require(manifest["version"] == version and manifest.get("bootstrap"),
            "Agent catalog must match the package version and include a bootstrap script")
    return catalog, manifest


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def request(opener, origin, path, data=None, method="GET", cookie=None, expected_status=200):
    headers = {"Origin": origin, "Cache-Control": "no-cache"}
    if cookie:
        headers["Cookie"] = cookie
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = Request(origin + path, method=method, headers=headers,
                      data=json.dumps(data).encode() if data is not None else None)
    with opener.open(request, timeout=5) as reply:
        body = reply.read(MAX_HTTP_BODY + 1)
        require(reply.status == expected_status and len(body) <= MAX_HTTP_BODY,
                f"Unexpected Hub response at {path} (HTTP {reply.status})")
        return body, reply.headers


def wait_payload(opener, origin, path, expected, timeout):
    until = time.monotonic() + timeout
    reason = "artifact bytes did not match the release"
    while True:
        try:
            body, _ = request(opener, origin, path)
            if hashlib.sha256(body).hexdigest() == expected:
                return
        except (OSError, URLError, InstallError) as error:
            reason = str(error)
        if time.monotonic() >= until:
            raise InstallError(f"Hub readiness failed at {origin}{path}: {reason}")
        time.sleep(1)


def wait_setup(opener, origin, timeout):
    path = "/api/setup"
    until = time.monotonic() + timeout
    while True:
        try:
            body, _ = request(opener, origin, path)
            required = json.loads(body).get("required")
            require(isinstance(required, bool), "Invalid Hub setup status")
            return required
        except (OSError, URLError, InstallError, ValueError, AttributeError) as error:
            if time.monotonic() >= until:
                raise InstallError(f"Hub readiness failed at {origin}{path}: {error}") from error
        time.sleep(1)


def configure_origin(opener, local, origin, owner):
    _, headers = request(opener, local, "/api/auth/login", owner, method="POST", expected_status=204)
    cookies = SimpleCookie()
    for value in headers.get_all("Set-Cookie", []):
        cookies.load(value)
    session = cookies.get("__Host-kubeatlas_portal_session") or cookies.get("kubeatlas_portal_session")
    require(session, "Hub owner sign-in did not return a session")
    cookie = f"{session.key}={session.coded_value}"
    path = "/api/hub/connections"
    try:
        body, _ = request(opener, local, path, cookie=cookie)
        current = json.loads(body)
        if current["publicURL"] != origin:
            require(not current["publicURL"], "Existing Hub public origin differs from the configured origin")
            body, _ = request(opener, local, path, {"lanURL": current["lanURL"], "publicURL": origin},
                              method="PUT", cookie=cookie)
            require(json.loads(body)["publicURL"] == origin, "Hub did not save the supplied HTTPS origin")
    finally:
        try:
            request(opener, local, "/api/auth/logout", {}, method="POST", cookie=cookie, expected_status=204)
        except (OSError, URLError, InstallError):
            pass


def verify_running(options, deployment, catalog, manifest, owner):
    trust = ssl.create_default_context()
    if options.ca_file:
        trust.load_verify_locations(cafile=options.ca_file)
    transport = build_opener(ProxyHandler({}), NoRedirect(), HTTPSHandler(context=trust))
    local = "http://" + deployment.listen
    path = "/downloads/agent/" + deployment.version + "/manifest.json"
    expected = digest(catalog / "manifest.json")
    if deployment.unit_format == 2:
        local_requires_setup = wait_setup(transport, local, options.timeout)
        external_requires_setup = wait_setup(transport, deployment.origin, options.timeout)
        if local_requires_setup and external_requires_setup:
            _, headers = request(transport, deployment.origin, "/setup")
            require(headers.get_content_type() == "text/html", "HTTPS Hub setup page is unavailable")
            run("systemctl", "is-active", "--quiet", SERVICE)
            return
    wait_payload(transport, local, path, expected, options.timeout)
    run("systemctl", "is-active", "--quiet", SERVICE)
    if deployment.unit_format == 1:
        configure_origin(transport, local, deployment.origin, owner)
    wait_payload(transport, deployment.origin, path, expected, options.timeout)
    _, headers = request(transport, deployment.origin, "/login")
    require(headers.get_content_type() == "text/html", "HTTPS Hub login page is unavailable")
    providers, _ = request(transport, deployment.origin, "/api/auth/providers")
    require(json.loads(providers).get("local") is True, "HTTPS Hub login API is unavailable")
    for artifact in (manifest, *previous_manifest(deployment)):
        image = artifact["image"]["digest"]
        body, _ = request(transport, deployment.origin, f"/v2/kubeatlas/agent/manifests/{image}")
        require(hashlib.sha256(body).hexdigest() == image.removeprefix("sha256:"), "Agent registry digest mismatch")
        if artifact.get("opencost"):
            image = artifact["opencost"]["imageDigest"]
            body, _ = request(transport, deployment.origin, f"/v2/kubeatlas/opencost/manifests/{image}")
            require(hashlib.sha256(body).hexdigest() == image.removeprefix("sha256:"), "OpenCost registry digest mismatch")


def previous_manifest(deployment):
    if not deployment.previous_version:
        return ()
    manifest = CATALOGS / deployment.previous_version / "manifest.json"
    return (json.loads(manifest.read_text(encoding="utf-8")),)


def ensure_directory(path, mode, owner=(0, 0)):
    if not path.exists():
        path.mkdir(mode=mode)
        os.chown(path, *owner)
        path.chmod(mode)
    info = path.lstat()
    require(stat.S_ISDIR(info.st_mode) and (info.st_uid, info.st_gid) == owner
            and stat.S_IMODE(info.st_mode) == mode, f"Unexpected permissions or owner for {path}")


def service_account():
    import grp
    import pwd

    try:
        account = pwd.getpwnam(COMPONENT)
    except KeyError:
        nologin = next((path for path in ("/usr/sbin/nologin", "/sbin/nologin") if Path(path).is_file()), None)
        require(nologin, "nologin is required to create the Hub service account")
        run("useradd", "--system", "--user-group", "--no-create-home", "--home-dir", STATE_DIR,
            "--shell", nologin, COMPONENT)
        account = pwd.getpwnam(COMPONENT)
    require(account.pw_uid != 0 and account.pw_gid != 0 and Path(account.pw_shell).name == "nologin"
            and Path(account.pw_dir) == STATE_DIR and grp.getgrnam(COMPONENT).gr_gid == account.pw_gid,
            "The dedicated Hub service account conflicts with an existing user")
    return account.pw_uid, account.pw_gid


def service_property(name):
    return run("systemctl", "show", "--property=" + name, "--value", SERVICE, accepted=(0, 1))


def load_deployment():
    data = json.loads(read_private(DEPLOYMENT))
    deployment = Deployment(**data)
    package = PACKAGE.fullmatch(deployment.package)
    require(deployment.unit_format in (1, 2)
            and package and package[1] == deployment.version and SHA256.fullmatch(deployment.package_sha256)
            and (not deployment.previous_version or re.fullmatch(VERSION, deployment.previous_version))
            and deployment.origin == normalize_origin(deployment.origin)
            and deployment.listen == normalize_listen(deployment.listen), "Invalid managed Hub deployment metadata")
    require(UNIT.is_file() and not UNIT.is_symlink() and UNIT.read_bytes() == deployment.unit()
            and service_property("FragmentPath") == str(UNIT) and not service_property("DropInPaths"),
            "The live Hub service differs from the managed unit. Preserve the current service before migrating it.")
    return deployment


def deployment_inputs(args, stage):
    package = stage / args.package.name
    shutil.copyfile(args.package, package)
    package_hash = checksum(args.checksums, package)
    version, kind = package_identity(package)
    candidate = extract_binary(package, kind, stage, version)
    catalog, manifest = stage_catalog(args, stage, candidate, version)
    return package, package_hash, version, kind, catalog, manifest


def cache_package(source):
    descriptor, temporary = tempfile.mkstemp(prefix=".package-", dir=MANAGEMENT)
    try:
        with os.fdopen(descriptor, "wb") as destination, source.open("rb") as package:
            shutil.copyfileobj(package, destination)
            destination.flush()
            os.fsync(destination.fileno())
        os.replace(temporary, MANAGEMENT / source.name)
    finally:
        Path(temporary).unlink(missing_ok=True)


def publish_catalog(staged, destination):
    require(not os.path.lexists(destination), f"Versioned catalog already exists: {destination}")
    with tempfile.TemporaryDirectory(dir=CATALOGS, prefix=".import-") as temporary:
        incoming = Path(temporary) / "catalog"
        copy_catalog(staged, incoming)
        incoming.rename(destination)


def stop_service():
    run("systemctl", "stop", SERVICE)
    require(service_property("MainPID") in ("", "0"), "The Hub service still has a running process")


def state_files():
    return (STATE, STATE.with_name("setup-code"))


def state_backup(source, destination):
    if os.path.lexists(source):
        require(stat.S_ISREG(source.lstat().st_mode), f"{source} is not a regular file")
        atomic_bytes(destination, source.read_bytes())


def rollback(kind, old, backup, owner, active):
    if old or service_property("LoadState") != "not-found":
        stop_service()
    for path in state_files():
        state_backup(path, backup / f"failed-{path.name}")
        previous = backup / path.name
        if previous.exists():
            atomic_bytes(path, previous.read_bytes(), owner=owner)
        else:
            path.unlink(missing_ok=True)
    if old:
        install_package(MANAGEMENT / old.package, kind, rollback=True)
        atomic_bytes(UNIT, (backup / SERVICE).read_bytes(), mode=0o644)
    else:
        if UNIT.exists():
            run("systemctl", "disable", SERVICE)
            UNIT.unlink()
        ENV_FILE.unlink(missing_ok=True)
        if installed_package(kind):
            if kind == "deb":
                run(*MANAGED_PACKAGE_ENV, "dpkg", "--remove", COMPONENT)
            else:
                run(*MANAGED_PACKAGE_ENV, "rpm", "--erase", COMPONENT)
    run("systemctl", "daemon-reload")
    if old and active:
        run("systemctl", "start", SERVICE)
        catalog = CATALOGS / old.version / "manifest.json"
        local = "http://" + old.listen
        transport = build_opener(ProxyHandler({}), NoRedirect())
        if old.unit_format == 1 or not wait_setup(transport, local, 60):
            wait_payload(transport, local, f"/downloads/agent/{old.version}/manifest.json", digest(catalog), 60)


def install_or_upgrade(args, stage):
    package, package_hash, version, kind, staged_catalog, manifest = deployment_inputs(args, stage)
    managed = args.action == "upgrade"
    old = load_deployment() if managed else None
    if old:
        require(args.credentials is None, "Upgrade uses the existing managed credentials")
        require((not args.origin or args.origin == old.origin) and (not args.listen or args.listen == old.listen),
                "Upgrade preserves the configured Hub origin and listener")
        require(package.suffix == Path(old.package).suffix, "Upgrade uses the installed package format")
        owner = read_env(ENV_FILE) if old.unit_format == 1 else None
        previous_package = MANAGEMENT / old.package
        require(previous_package.is_file() and digest(previous_package) == old.package_sha256,
                "Cached rollback package is missing or has changed")
        require(run(BINARY, "--version") == f"{COMPONENT} {old.version}",
                "The installed executable differs from deployment metadata")
        if version == old.version:
            catalog = CATALOGS / version
            require(package_hash == old.package_sha256 and digest(catalog / "manifest.json") ==
                    digest(staged_catalog / "manifest.json"), "Artifacts for an installed version must be identical")
            verify_running(args, old, catalog, manifest, owner)
            return old
        require(args.retire_previous or not old.previous_version,
                f"The Hub currently serves catalog {old.previous_version}. Confirm Agents no longer use it, "
                "then pass --retire-previous-catalog to replace it")
        run(BINARY, "--check-hub-artifacts", "--hub-artifacts-dir", CATALOGS / old.version)
        if kind == "deb":
            run("dpkg", "--compare-versions", version.replace("-", "~", 1), "gt", old.version.replace("-", "~", 1))
    else:
        require(args.origin, "Fresh installation requires --origin")
        require(not any(os.path.lexists(path) for path in (UNIT, *state_files(), ENV_FILE, DEPLOYMENT, BINARY))
                and service_property("LoadState") == "not-found" and not installed_package(kind),
                "A KubeAtlas Hub already exists. Use upgrade for a managed installation")
        owner = credentials(json.loads(read_private(args.credentials))) if args.credentials else None

    if kind == "rpm":
        run("rpm", "--upgrade", "--test", package)

    deployment = Deployment(version, package.name, package_hash, old.listen if old else args.listen or "127.0.0.1:1333",
                            old.origin if old else args.origin, old.version if old else None, unit_format=2)
    identity = service_account()
    ensure_directory(MANAGEMENT, 0o700)
    ensure_directory(STATE_DIR, 0o700, identity)
    ensure_directory(CATALOGS, 0o755)
    cache_package(package)
    catalog = CATALOGS / version
    publish_catalog(staged_catalog, catalog)
    backup = Path(tempfile.mkdtemp(prefix="backup-", dir=MANAGEMENT))
    active = bool(old and service_property("ActiveState") == "active")
    if old:
        atomic_bytes(backup / SERVICE, UNIT.read_bytes())
    updated = False
    try:
        if old:
            stop_service()
            for path in state_files():
                state_backup(path, backup / path.name)
        updated = True
        if not old and owner is not None:
            atomic_bytes(ENV_FILE, encode_env(owner).encode())
        install_package(package, kind)
        atomic_bytes(UNIT, deployment.unit(), mode=0o644)
        run("systemctl", "daemon-reload")
        run("systemctl", "start", SERVICE)
        verify_running(args, deployment, catalog, manifest, owner)
        if not old:
            run("systemctl", "enable", SERVICE)
        atomic_bytes(DEPLOYMENT, json.dumps(asdict(deployment), indent=2).encode() + b"\n")
    except BaseException as error:
        try:
            if updated:
                rollback(kind, old, backup, identity, active)
            elif active:
                run("systemctl", "start", SERVICE)
            shutil.rmtree(catalog)
        except Exception as recovery:
            raise InstallError(f"{error}. Hub recovery needs attention: {recovery}. Backup: {backup}") from error
        restored = "Restored the previous deployment" if old else "Rolled back the new installation"
        raise InstallError(f"{error}. {restored}. Backup: {backup}") from error
    if old:
        print(f"Previous package and state backup retained in {MANAGEMENT} and {backup}")
    return deployment


def options(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("install", "upgrade"))
    parser.add_argument("--package", required=True, type=Path, help="Official Linux kubeatlas-portal DEB or RPM")
    parser.add_argument("--checksums", required=True, type=Path, help="Trusted release SHA-256 checksum file")
    catalog = parser.add_mutually_exclusive_group(required=True)
    catalog.add_argument("--catalog-archive", type=Path, help="Release Agent bundle listed in the checksum file")
    catalog.add_argument("--catalog", type=Path, help="Prepared Agent catalog directory")
    parser.add_argument("--origin", type=normalize_origin, help="HTTPS origin of the configured reverse proxy")
    parser.add_argument("--listen", type=normalize_listen, help="Hub listener IP and port (default 127.0.0.1:1333)")
    parser.add_argument("--port", type=int, help="Override the Hub listener port for a new installation")
    parser.add_argument("--credentials", type=Path, help="Optional root-owned 0600 JSON file for unattended setup")
    parser.add_argument("--ca-file", type=Path, help="Additional CA file for verifying the HTTPS probe")
    parser.add_argument("--timeout", type=int, default=60, help="Readiness wait per origin in seconds (default 60)")
    parser.add_argument("--retire-previous-catalog", dest="retire_previous", action="store_true",
                        help="Replace an older served catalog after migrating its remaining Agents")
    result = parser.parse_args(argv)
    require(result.timeout > 0, "The readiness timeout must be positive")
    if result.port is not None:
        require(0 < result.port < 65536, "--port must be between 1 and 65535")
        address = result.listen.rsplit(":", 1)[0] if result.listen else "127.0.0.1"
        result.listen = normalize_listen(f"{address}:{result.port}")
    return result


def interrupted(signum, frame):
    raise InterruptedError(f"Installer received signal {signum}")


def main():
    try:
        args = options()
        require(platform.system() == "Linux" and Path("/run/systemd/system").is_dir(), "Linux systemd is required")
        require(os.geteuid() == 0, "Run this installer with sudo on a Linux systemd host")
        import fcntl

        os.umask(0o077)
        signal.signal(signal.SIGTERM, interrupted)
        lock = os.open("/run/lock/kubeatlas-portal-install.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        with os.fdopen(lock, "wb") as lock_file:
            fcntl.flock(lock_file, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with tempfile.TemporaryDirectory(prefix="kubeatlas-hub-", dir="/var/tmp") as temporary:
                deployment = install_or_upgrade(args, Path(temporary))
        print(f"KubeAtlas Hub {deployment.version} is ready at {deployment.origin}")
        if deployment.unit_format == 2 and not STATE.exists() and STATE.with_name("setup-code").is_file():
            print(f"Complete administrator setup at {deployment.origin}/setup")
            command = (BINARY, f"--hub-state-file={STATE}", "--show-setup-code")
            if sys.stdout.isatty():
                try:
                    print(f"One-time setup code: {run(*command)}")
                except (InstallError, OSError, subprocess.SubprocessError):
                    print("To read the setup code, run:", *command)
            else:
                print("To read the setup code, run:", *command)
        return 0
    except (InstallError, OSError, ValueError, TypeError, KeyError, KeyboardInterrupt, subprocess.SubprocessError) as error:
        print(f"KubeAtlas Hub installer: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
