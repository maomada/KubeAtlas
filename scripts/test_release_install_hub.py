import hashlib
from importlib.util import module_from_spec, spec_from_file_location
from io import BytesIO
import json
from pathlib import Path
import shutil
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch


SPEC = spec_from_file_location("install_hub", Path(__file__).with_name("install-hub.py"))
installer = module_from_spec(SPEC)
sys.modules[SPEC.name] = installer
SPEC.loader.exec_module(installer)


class HubDeploymentTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        paths = {
            "UNIT": root / "kubeatlas-portal.service",
            "MANAGEMENT": root / "install", "STATE_DIR": root / "state",
            "STATE": root / "state" / "state.json", "CATALOGS": root / "catalogs",
            "DEPLOYMENT": root / "install" / "deployment.json", "ENV_FILE": root / "install" / "portal.env",
            "BINARY": root / "kubeatlas-portal",
        }
        path_patch = patch.multiple(installer, **paths)
        path_patch.start()
        self.addCleanup(path_patch.stop)
        self.stage = root / "stage"
        self.stage.mkdir()
        self.owner = {"username": "test-owner", "password": "a-test-only-owner-password"}
        self.credentials = root / "credentials.json"
        self.credentials.write_text(json.dumps(self.owner), encoding="utf-8")
        self.origin = "https://hub.example.net"
        self.package = self.stage / "kubeatlas-portal_1.2.4_linux_amd64.deb"
        self.package.write_bytes(b"verified package")
        self.catalog = self.stage / "catalog"
        self.catalog.mkdir()
        self.manifest = {"version": "1.2.4", "bootstrap": {"sha256": "1" * 64},
                         "image": {"digest": "sha256:" + "2" * 64}}
        (self.catalog / "manifest.json").write_text(json.dumps(self.manifest), encoding="utf-8")
        self.installed = None
        self.calls = []
        self.active = False

        self.mock("deployment_inputs", return_value=(self.package, installer.digest(self.package), "1.2.4", "deb",
                                                      self.catalog, self.manifest))
        self.mock("service_property", side_effect=self.service_property)
        self.mock("service_account", return_value=(0, 0))
        self.mock("ensure_directory", side_effect=lambda path, *args: path.mkdir(exist_ok=True))
        self.mock("read_private", side_effect=lambda path: path.read_text(encoding="utf-8"))
        self.mock("publish_catalog", side_effect=shutil.copytree)
        self.mock("atomic_bytes", side_effect=lambda path, data, **kwargs: path.write_bytes(data))
        self.mock("install_package", side_effect=self.install_package)
        self.mock("installed_package", side_effect=lambda kind: self.installed is not None)
        self.mock("run", side_effect=self.command)
        self.mock("wait_payload")

    def mock(self, name, **kwargs):
        patcher = patch.object(installer, name, **kwargs)
        self.addCleanup(patcher.stop)
        return patcher.start()

    def options(self, mode):
        install = mode == "install"
        return SimpleNamespace(action=mode, credentials=self.credentials if install else None,
                               origin=self.origin if install else None, listen=None, retire_previous=False)

    def service_property(self, field):
        return {
            "LoadState": "loaded" if installer.UNIT.exists() else "not-found",
            "MainPID": "123" if self.active else "0",
            "ActiveState": "active" if self.active else "inactive",
            "FragmentPath": str(installer.UNIT),
            "DropInPaths": "",
        }[field]

    def command(self, *args, **kwargs):
        self.calls.append(args)
        if args[:2] == ("systemctl", "stop"):
            self.active = False
        elif args[:2] == ("systemctl", "start"):
            self.active = True
        elif args[:2] == ("dpkg", "--remove"):
            self.installed = None
        elif args == (installer.BINARY, "--version"):
            return "kubeatlas-portal " + self.installed
        return ""

    def install_package(self, package, kind, rollback=False):
        self.calls.append(("package", package, rollback))
        self.installed = installer.PACKAGE.fullmatch(package.name)[1]

    def provision_previous_install(self, previous_version=None):
        installer.MANAGEMENT.mkdir()
        installer.STATE_DIR.mkdir()
        installer.CATALOGS.mkdir()
        catalog = installer.CATALOGS / "1.2.3"
        catalog.mkdir()
        (catalog / "manifest.json").write_text('{"version":"1.2.3"}', encoding="utf-8")
        old_package = installer.MANAGEMENT / "kubeatlas-portal_1.2.3_linux_amd64.deb"
        old_package.write_bytes(b"old package")
        old = installer.Deployment("1.2.3", old_package.name, installer.digest(old_package),
                                   "127.0.0.1:9290", self.origin, previous_version)
        installer.UNIT.write_bytes(old.unit())
        installer.DEPLOYMENT.write_text(json.dumps(installer.asdict(old)), encoding="utf-8")
        installer.ENV_FILE.write_text(installer.encode_env(self.owner), encoding="utf-8")
        installer.STATE.write_bytes(b"old cluster state")
        self.installed = "1.2.3"
        self.active = True
        return old

    def test_fresh_install_writes_one_managed_unit(self):
        self.mock("verify_running")
        deployed = installer.install_or_upgrade(self.options("install"), self.stage)
        self.assertTrue(self.active)
        self.assertEqual(self.installed, "1.2.4")
        self.assertEqual(deployed.previous_version, None)
        self.assertEqual(installer.UNIT.read_bytes(), deployed.unit())
        self.assertEqual(installer.load_deployment(), deployed)
        self.assertEqual(installer.read_env(installer.ENV_FILE), self.owner)
        self.assertEqual((installer.MANAGEMENT / self.package.name).read_bytes(), self.package.read_bytes())

    def test_fresh_failure_before_unit_creation_can_retry(self):
        with patch.object(installer, "install_package", side_effect=installer.InstallError("package rejected")):
            with self.assertRaisesRegex(installer.InstallError, "Rolled back the new installation"):
                installer.install_or_upgrade(self.options("install"), self.stage)
        self.assertFalse(installer.UNIT.exists())
        self.assertFalse(installer.ENV_FILE.exists())
        self.assertFalse((installer.CATALOGS / "1.2.4").exists())
        self.assertNotIn(("systemctl", "stop", installer.SERVICE), self.calls)

    def test_upgrade_retains_agent_catalog_and_state(self):
        old = self.provision_previous_install()
        self.mock("verify_running")
        deployed = installer.install_or_upgrade(self.options("upgrade"), self.stage)
        self.assertEqual(deployed.previous_version, old.version)
        self.assertIn(b"--hub-previous-artifacts-version=1.2.3", installer.UNIT.read_bytes())
        self.assertEqual(installer.STATE.read_bytes(), b"old cluster state")
        self.assertTrue((installer.CATALOGS / "1.2.3" / "manifest.json").is_file())
        self.assertTrue(self.active)

    def test_failed_upgrade_restores_unit_package_and_state(self):
        old = self.provision_previous_install()
        def fail_readiness(*args):
            installer.STATE.write_bytes(b"state written by candidate")
            raise installer.InstallError("HTTPS not ready")
        self.mock("verify_running", side_effect=fail_readiness)
        with self.assertRaisesRegex(installer.InstallError, "Restored the previous deployment"):
            installer.install_or_upgrade(self.options("upgrade"), self.stage)
        self.assertEqual(self.installed, old.version)
        self.assertTrue(self.active)
        self.assertEqual(installer.UNIT.read_bytes(), old.unit())
        self.assertEqual(installer.load_deployment(), old)
        self.assertEqual(installer.STATE.read_bytes(), b"old cluster state")
        self.assertEqual(next(installer.MANAGEMENT.glob("backup-*/failed-state.json")).read_bytes(),
                         b"state written by candidate")
        self.assertFalse((installer.CATALOGS / "1.2.4").exists())

    def test_upgrade_does_not_silently_retire_an_older_catalog(self):
        self.provision_previous_install(previous_version="1.2.2")
        with self.assertRaisesRegex(installer.InstallError, "--retire-previous-catalog"):
            installer.install_or_upgrade(self.options("upgrade"), self.stage)
        self.assertEqual(self.installed, "1.2.3")
        self.assertTrue(self.active)

    def test_checksums_must_match_selected_asset_exactly_once(self):
        checksums = self.stage / "hub-checksums.txt"
        expected = hashlib.sha256(self.package.read_bytes()).hexdigest()
        line = f"{expected}  {self.package.name}\n"
        checksums.write_text(line, encoding="utf-8")
        self.assertEqual(installer.checksum(checksums, self.package), expected)
        checksums.write_text(line + line, encoding="utf-8")
        with self.assertRaisesRegex(installer.InstallError, "one checksum"):
            installer.checksum(checksums, self.package)
        checksums.write_text(line, encoding="utf-8")
        self.package.write_bytes(b"changed package")
        with self.assertRaisesRegex(installer.InstallError, "SHA-256 mismatch"):
            installer.checksum(checksums, self.package)

    def test_https_origin_has_no_path_or_userinfo(self):
        self.assertEqual(installer.normalize_origin("https://Hub.Example.net:8443/"),
                         "https://hub.example.net:8443")
        self.assertEqual(installer.normalize_origin("https://Hub.Example.net.:443"), self.origin)
        for invalid in ("http://hub.example.net", "https://hub.example.net/path", "https://me@hub.example.net",
                        "https://hub.example.net?#", "https://hub.example.net%25", "https://-"):
            with self.subTest(origin=invalid), self.assertRaises(installer.InstallError):
                installer.normalize_origin(invalid)

    def test_non_loopback_bind_is_allowed_for_local_readiness(self):
        deployment = installer.Deployment("1.2.4", self.package.name, installer.digest(self.package),
                                          "192.0.2.100:9290", self.origin, None)
        self.assertIn(b"--mcp-allowed-hosts=hub.example.net,192.0.2.100:9290", deployment.unit())

    def test_login_uses_the_portal_no_content_response(self):
        def open_request(request, timeout):
            reply = BytesIO()
            reply.status = 204
            reply.headers = {}
            return reply

        opener = SimpleNamespace(open=open_request)
        body, _ = installer.request(opener, self.origin, "/api/auth/login", self.owner,
                                    method="POST", expected_status=204)
        self.assertEqual(body, b"")
        with self.assertRaisesRegex(installer.InstallError, "HTTP 204"):
            installer.request(opener, self.origin, "/downloads/agent/1.2.4/manifest.json")


if __name__ == "__main__":
    unittest.main()
