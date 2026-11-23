import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import release_linux_smoke as smoke


class ReleaseLinuxSmokeTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.directory = Path(temp.name)
        self.source_sha = "a" * 40
        self.version = "1.2.3-rc.1"
        self.evidence = ["CI evidence", "Security evidence"]
        self.artifacts = []
        for component in ("kubeatlas", "kubeatlas-portal"):
            for architecture in ("amd64", "arm64"):
                for extension in ("deb", "rpm"):
                    filename = f"{component}_{self.version}_linux_{architecture}.{extension}"
                    (self.directory / filename).write_bytes(b"fixture")
                    self.artifacts.append({"architecture": architecture, "component": component, "filename": filename})

    def extract(self, package, destination):
        binary_dir = destination / "usr/bin"
        binary_dir.mkdir(parents=True)
        (binary_dir / package.name.split("_")[0]).write_bytes(b"fixture")

    def run_binary(self, command, **kwargs):
        component = Path(command[0]).name
        if command[1] == "--version":
            return subprocess.CompletedProcess(command, 0, f"{component} {self.version}\n".encode(), b"")
        return subprocess.CompletedProcess(command, 0, b"", b"Usage of fixture")

    @patch("release_linux_smoke.platform.machine", return_value="aarch64")
    @patch("release_linux_smoke.platform.system", return_value="Linux")
    def test_smokes_both_components_in_both_native_package_formats(self, _system, _machine):
        with (
            patch.object(smoke, "validate_local_staged", return_value={"artifacts": self.artifacts}) as validate,
            patch.object(smoke, "extract_package", side_effect=self.extract) as extract,
            patch.object(smoke.subprocess, "run", side_effect=self.run_binary) as run,
        ):
            smoke.smoke_packages(self.directory, self.source_sha, self.version, "arm64", self.evidence)

        validate.assert_called_once_with(
            self.directory, expected_source_sha=self.source_sha, expected_version=self.version,
            evidence_urls=self.evidence, profile="local-linux",
        )
        self.assertEqual(
            [call.args[0].name for call in extract.call_args_list],
            [item["filename"] for item in self.artifacts if item["architecture"] == "arm64"],
        )
        self.assertEqual([call.args[0][1] for call in run.call_args_list], ["--version", "--help"] * 4)
        self.assertTrue(all(call.kwargs["check"] and call.kwargs["timeout"] for call in run.call_args_list))

    @patch("release_linux_smoke.platform.machine", return_value="aarch64")
    @patch("release_linux_smoke.platform.system", return_value="Linux")
    def test_smokes_hub_packages_with_hub_publication_profile(self, _system, _machine):
        packages = [item for item in self.artifacts if item["component"] == "kubeatlas-portal"]
        with (
            patch.object(smoke, "validate_local_staged", return_value={"artifacts": packages}) as validate,
            patch.object(smoke, "extract_package", side_effect=self.extract) as extract,
            patch.object(smoke.subprocess, "run", side_effect=self.run_binary) as run,
        ):
            smoke.smoke_packages(self.directory, self.source_sha, self.version, "arm64", self.evidence, "hub-linux")

        validate.assert_called_once_with(
            self.directory, expected_source_sha=self.source_sha, expected_version=self.version,
            evidence_urls=self.evidence, profile="hub-linux",
        )
        self.assertEqual(
            [call.args[0].name for call in extract.call_args_list],
            [f"kubeatlas-portal_{self.version}_linux_arm64.{suffix}" for suffix in ("deb", "rpm")],
        )
        self.assertEqual(
            [call.args[0][1] for call in run.call_args_list],
            ["--version", "--help", "--check-hub-artifacts"] * 2,
        )

    @patch("release_linux_smoke.platform.machine", return_value="x86_64")
    @patch("release_linux_smoke.platform.system", return_value="Linux")
    def test_requires_native_architecture(self, _system, _machine):
        with patch.object(smoke, "validate_local_staged") as validate:
            with self.assertRaisesRegex(ValueError, "native Linux arm64"):
                smoke.smoke_packages(self.directory, self.source_sha, self.version, "arm64", self.evidence)
            validate.assert_not_called()

    @patch("release_linux_smoke.platform.machine", return_value="x86_64")
    @patch("release_linux_smoke.platform.system", return_value="Linux")
    def test_rejects_wrong_packaged_binary_version(self, _system, _machine):
        wrong = subprocess.CompletedProcess([], 0, b"kubeatlas 1.0.0\n", b"")
        with (
            patch.object(smoke, "validate_local_staged", return_value={"artifacts": self.artifacts}),
            patch.object(smoke, "extract_package", side_effect=self.extract),
            patch.object(smoke.subprocess, "run", return_value=wrong) as run,
        ):
            with self.assertRaisesRegex(ValueError, "binary version mismatch"):
                smoke.smoke_packages(self.directory, self.source_sha, self.version, "amd64", self.evidence)
        self.assertEqual(run.call_count, 1)


if __name__ == "__main__":
    unittest.main()
