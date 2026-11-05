import tempfile
import tarfile
import unittest
from pathlib import Path

import release_chart_package as package
import release_approval
import release_local


ROOT = Path(__file__).resolve().parents[1]
SHA = "a" * 40


class ReleaseChartPackageTests(unittest.TestCase):
    def test_staged_charts_stamp_version_repositories_and_source_revision(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "charts"
            staged = {}

            def package_fixture(command, **_kwargs):
                chart = Path(command[2])
                name = chart.name
                staged[name] = (
                    (chart / "Chart.yaml").read_text(),
                    (chart / "values.yaml").read_text(),
                )
                with tarfile.open(Path(command[4]) / f"{name}-0.1.0-test.1.tgz", "w:gz") as archive:
                    archive.add(chart, arcname=name)

            archives = package.package_charts(
                ROOT, output, "0.1.0-test.1", SHA, helm="helm", runner=package_fixture
            )
            self.assertEqual(
                [archive.name for archive in archives],
                ["kubeatlas-0.1.0-test.1.tgz", "kubeatlas-portal-0.1.0-test.1.tgz"],
            )
            self.assertEqual(set(staged), set(package.CHARTS))
            for name, repository in package.CHARTS.items():
                metadata, values = staged[name]
                self.assertIn('version: "0.1.0-test.1"', metadata)
                self.assertIn('appVersion: "0.1.0-test.1"', metadata)
                self.assertIn(
                    f'org.opencontainers.image.source: "https://github.com/maomada/KubeAtlas"',
                    metadata,
                )
                self.assertIn(f'org.opencontainers.image.revision: "{SHA}"', metadata)
                self.assertIn(f'repository: "{repository}"', values)
                self.assertIn('tag: "0.1.0-test.1"', values)
                self.assertIn("nodeSelector: {}", values)
            self.assertEqual(len((output / "SHA256SUMS").read_text().splitlines()), 2)
            self.assertEqual(
                {path.name for path in output.iterdir()},
                {"kubeatlas-0.1.0-test.1.tgz", "kubeatlas-portal-0.1.0-test.1.tgz", "SHA256SUMS"},
            )

    def test_packager_bytes_validate_as_exact_local_chart_inventory(self):
        with tempfile.TemporaryDirectory() as temporary:
            output = Path(temporary) / "ghcr" / "charts"
            version = release_local.VERSION

            def helm_package(command, **_kwargs):
                chart = Path(command[2])
                with tarfile.open(Path(command[4]) / f"{chart.name}-{version}.tgz", "w:gz") as archive:
                    archive.add(chart, arcname=chart.name)

            package.package_charts(ROOT, output, version, SHA, runner=helm_package, platform="linux/amd64")
            for name in package.CHARTS:
                release_local._validate_chart_archive(
                    output / f"{name}-{version}.tgz", name, SHA
                )
            expected = {
                path for path in release_approval.expected_artifact_paths("ghcr", version)
                if path.startswith("charts/")
            }
            actual = {
                path.relative_to(output.parent).as_posix()
                for path in output.iterdir()
            }
            self.assertEqual(actual, expected)
            self.assertFalse((output / ".staged").exists())

    def test_local_chart_requires_platform_selector_without_mutating_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            for name, repository in package.CHARTS.items():
                source = ROOT / "deploy" / "helm" / name
                original = (source / "values.yaml").read_bytes()
                staged = Path(temporary) / name
                package.prepare_chart(source, staged, release_local.VERSION, SHA, repository)
                archive_path = Path(temporary) / f"{name}.tgz"
                with tarfile.open(archive_path, "w:gz") as archive:
                    archive.add(staged, arcname=name)
                with self.assertRaisesRegex(release_local.LocalReleaseError, "node selector"):
                    release_local._validate_chart_archive(archive_path, name, SHA)
                self.assertEqual((source / "values.yaml").read_bytes(), original)

    def test_normalized_annotations_keep_exact_source_and_revision(self):
        with tempfile.TemporaryDirectory() as temporary:
            for name, repository in package.CHARTS.items():
                staged = Path(temporary) / name
                package.prepare_chart(
                    ROOT / "deploy" / "helm" / name, staged,
                    release_local.VERSION, SHA, repository, platform="linux/amd64",
                )
                metadata_path = staged / "Chart.yaml"
                original = metadata_path.read_text()
                source = "https://github.com/maomada/KubeAtlas"
                archive_path = Path(temporary) / f"{name}.tgz"
                for quote in ('"', "'", ""):
                    for field in (None, "source", "revision"):
                        with self.subTest(name=name, quote=quote, field=field):
                            actual_source = source + "/wrong" if field == "source" else source
                            actual_sha = "b" * 40 if field == "revision" else SHA
                            metadata_path.write_text(
                                original.replace(f'"{source}"', f"{quote}{actual_source}{quote}")
                                .replace(f'"{SHA}"', f"{quote}{actual_sha}{quote}")
                            )
                            with tarfile.open(archive_path, "w:gz") as archive:
                                archive.add(staged, arcname=name)
                            if field is None:
                                release_local._validate_chart_archive(archive_path, name, SHA)
                            else:
                                with self.assertRaisesRegex(
                                    release_local.LocalReleaseError, f"chart {field} does not match"
                                ):
                                    release_local._validate_chart_archive(archive_path, name, SHA)

    def test_invalid_source_sha_is_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            with self.assertRaises(ValueError):
                package.package_charts(ROOT, temporary, "1.2.3", "not-a-sha")


if __name__ == "__main__":
    unittest.main()
