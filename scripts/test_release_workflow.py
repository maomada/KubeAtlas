import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


class ReleaseWorkflowTests(unittest.TestCase):
    def test_portal_image_injects_version_into_binary(self):
        dockerfile = (ROOT / "Dockerfile.portal").read_text()
        build_command = next(
            line for line in dockerfile.splitlines()
            if line.startswith("RUN ") and "go build" in line
        )
        self.assertIn("ARG VERSION=dev", dockerfile)
        self.assertIn('-ldflags "-s -w -X main.version=${VERSION}"', build_command)
        self.assertIn("./cmd/kubeatlas-portal", build_command)

    def test_codeql_is_manual_while_semgrep_remains_automatic(self):
        codeql = (ROOT / ".github/workflows/codeql.yml").read_text()
        self.assertIn("workflow_dispatch:", codeql)
        self.assertNotRegex(codeql, r"(?m)^  (push|pull_request|schedule):")
        self.assertIn("github/codeql-action/analyze@", codeql)
        self.assertIn("language: [go, javascript-typescript]", codeql)
        security = (ROOT / ".github/workflows/security.yml").read_text()
        for event in ("push", "pull_request", "schedule"):
            self.assertIn(f"  {event}:\n", security)
        self.assertNotIn("github.event.pull_request.draft", security)

    def test_heavy_builds_defer_drafts_but_run_when_ready_and_on_main(self):
        ci = (ROOT / ".github/workflows/ci.yml").read_text()
        package = (ROOT / ".github/workflows/package-validation.yml").read_text()
        guard = "github.event_name != 'pull_request' || github.event.pull_request.draft == false"
        for workflow in (ci, package):
            self.assertIn("types: [opened, synchronize, reopened, ready_for_review]", workflow)
            self.assertIn("  push:\n    branches: [main]", workflow)
            self.assertIn("github.event.pull_request.number || github.run_id", workflow)
            self.assertIn("cancel-in-progress: ${{ github.event_name == 'pull_request' }}", workflow)
        container = ci.split("  container-build:\n", 1)[1]
        self.assertIn(f"if: needs.changes.outputs.containers == 'true' && ({guard})", container)
        self.assertNotIn("github.event.pull_request.draft", ci.split("  container-build:\n", 1)[0])
        validate = package.split("  validate:\n", 1)[1].split("\n  required-gates:", 1)[0]
        self.assertIn("needs: changes", validate)
        self.assertIn("needs.changes.outputs.packages == 'true'", validate)
        self.assertIn(guard, validate)
        self.assertIn("timeout-minutes: 60", package)
        self.assertIn("make release-binaries-dry", package)
        self.assertIn("scripts/release_artifacts.py validate --directory dist/release", package)

    def test_ci_changes_select_all_jobs_without_broad_github_match(self):
        ci = (ROOT / ".github/workflows/ci.yml").read_text()
        orchestration = re.split(r"\n {12}[a-z][a-z0-9-]*:\n", ci.split("            ci:\n", 1)[1], maxsplit=1)[0]
        self.assertIn("'.github/workflows/ci.yml'", orchestration)
        self.assertIn("'.github/actions/**'", orchestration)
        self.assertNotIn("'.github/**'", orchestration)
        for name in ("pkg", "go", "web", "app", "helm", "containers", "installers"):
            self.assertIn(f"{name}: ${{{{ github.event_name != 'pull_request' || steps.filter.outputs.ci == 'true'", ci)

    def test_release_evidence_is_standalone_security_and_approval_is_retained(self):
        for name in ("release.yml", "release-binaries.yml"):
            with self.subTest(workflow=name):
                workflow = (ROOT / ".github/workflows" / name).read_text()
                self.assertIn("security_url", workflow)
                self.assertIn("SECURITY_URL", workflow)
                self.assertIn("security.yml", workflow)
                self.assertNotIn("codeql", workflow.lower())
                self.assertIn("name: ghcr-publish", workflow)
                self.assertEqual(workflow.count("trusted/scripts/release_policy.py"), 2)
        self.assertTrue((ROOT / ".github/workflows/codeql.yml").exists())

    def test_offline_approval_binds_prior_dry_run_and_gates_both_workflows(self):
        for name, first_write in (
            ("release.yml", "skopeo copy --all --preserve-digests"),
            ("release-binaries.yml", "Atomically create tag ref"),
        ):
            with self.subTest(workflow=name):
                workflow = (ROOT / ".github/workflows" / name).read_text()
                self.assertIn("build_run_id:", workflow)
                self.assertIn("approval_signature:", workflow)
                self.assertIn("release_approval.py create", workflow)
                self.assertEqual(workflow.count("release_approval.py verify"), 2)
                self.assertIn("name: release-approval", workflow)
                publish = workflow.split("\n  publish:\n", 1)[1]
                self.assertIn("needs: validate", publish)
                self.assertNotIn("needs: [validate, build", publish)
                self.assertIn("run-id: ${{ inputs.build_run_id }}", publish)
                self.assertIn("github-token: ${{ secrets.GITHUB_TOKEN }}", publish)
                self.assertIn("repository: maomada/KubeAtlas", publish)
                self.assertLess(publish.index("Verify signed approval"), publish.index(first_write))
                self.assertLess(
                    publish.index("Reverify offline approval immediately before publication"),
                    publish.index(first_write),
                )
                self.assertIn("if: always()", publish)
                self.assertIn("if-no-files-found: warn", publish)

    def test_binary_local_publication_profile_and_native_windows_smoke_gate_approval(self):
        workflow = (ROOT / ".github/workflows/release-binaries.yml").read_text()
        build = workflow.split("\n  build:\n", 1)[1].split("\n  windows-smoke:\n", 1)[0]
        smoke = workflow.split("\n  windows-smoke:\n", 1)[1].split("\n  dry-run:\n", 1)[0]
        dry_run = workflow.split("\n  dry-run:\n", 1)[1].split("\n  publish:\n", 1)[0]
        publish = workflow.split("\n  publish:\n", 1)[1]
        self.assertIn("if: ${{ inputs.publish != true }}", build)
        self.assertIn("release_artifacts.py stage-local", build)
        self.assertIn("name: kubeatlas-binary-release", build)
        self.assertIn("name: kubeatlas-binary-publication", build)
        self.assertIn("runs-on: windows-latest", smoke)
        self.assertIn("release_windows_smoke.py", smoke)
        self.assertIn("--version", (ROOT / "scripts/release_windows_smoke.py").read_text())
        self.assertIn("--help", (ROOT / "scripts/release_windows_smoke.py").read_text())
        self.assertIn("needs: [validate, build, windows-smoke]", dry_run)
        self.assertIn("validate --profile local", dry_run)
        self.assertIn("--channel binaries", dry_run)
        self.assertIn("kubeatlas-binary-publication", publish)
        self.assertIn("release-approval", publish)
        self.assertNotIn("name: kubeatlas-binary-release\n", publish)

    def test_release_is_manual_and_write_permission_is_publish_only(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertNotRegex(workflow, r"(?m)^  (push|pull_request|schedule|release):")
        self.assertEqual(workflow.count("packages: write"), 1)
        self.assertLess(workflow.index("publish:\n"), workflow.index("packages: write"))
        self.assertIn("cancel-in-progress: false", workflow)
        self.assertIn("inputs.publish == true", workflow)
        self.assertIn("inputs.publish != true", workflow)

    def test_release_targets_and_preflight_are_owned_and_versioned(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        registry = (ROOT / "scripts/release_registry.py").read_text()
        self.assertIn("ghcr.io/maomada/kubeatlas:${{ needs.validate.outputs.version }}", workflow)
        self.assertIn("ghcr.io/maomada/kubeatlas-portal:${{ needs.validate.outputs.version }}", workflow)
        self.assertIn("ghcr.io/maomada/charts/kubeatlas:${{ needs.validate.outputs.version }}", workflow)
        self.assertIn("ghcr.io/maomada/charts/kubeatlas-portal:${{ needs.validate.outputs.version }}", workflow)
        self.assertIn("Check all four target refs before any push", workflow)
        self.assertIn("manifest_unknown", registry)
        self.assertNotIn(":latest", workflow + registry)

    def test_legacy_publisher_workflows_are_removed(self):
        removed = (
            "dev-image.yml",
            "docker-build.yml",
            "release-image.yml",
            "release-desktop.yml",
            "publish-k8s-ui.yml",
            "publish-kubeatlas-app.yml",
        )
        for name in removed:
            self.assertFalse((ROOT / ".github/workflows" / name).exists())

    def test_ci_container_build_validates_and_copies_oci_archives_locally(self):
        workflow = (ROOT / ".github/workflows/ci.yml").read_text()
        container_job = workflow.split("  container-build:\n", 1)[1]
        self.assertIn("'.dockerignore'", workflow)
        self.assertIn("linux/amd64,linux/arm64", container_job)
        self.assertIn("push: false", container_job)
        self.assertIn("outputs: type=oci,dest=", container_job)
        for direction in ("cache-from", "cache-to"):
            cache = re.search(rf"{direction}: ([^\n]+)", container_job)
            self.assertIsNotNone(cache, f"Missing {direction} for repeated image builds")
            self.assertIn("type=gha", cache.group(1))
            self.assertIn("scope=", cache.group(1))
            self.assertIn("${{ matrix.component }}", cache.group(1))
        self.assertNotIn("type=cacheonly", container_job)
        self.assertIn("skopeo inspect --raw", container_job)
        self.assertIn("skopeo copy --all --preserve-digests", container_job)
        self.assertIn('"oci:$LAYOUT:$LAYOUT_TAG"', container_job)
        self.assertIn("python3 scripts/validate_oci_archive.py verify-copy", container_job)
        self.assertNotIn("docker://ghcr.io/", container_job)

    def test_release_build_validates_archive_with_trusted_tooling(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        build_job = workflow.split("  build-images:\n", 1)[1].split("\n  charts:\n", 1)[0]
        validator = "python3 trusted/scripts/validate_oci_archive.py validate"
        self.assertIn(validator, build_job)
        self.assertLess(build_job.index(validator), build_job.index("Record archive checksum"))
        self.assertLess(build_job.index(validator), build_job.index("Upload component release archive"))

    def test_binary_release_is_manual_and_builds_without_registry_access(self):
        workflow = (ROOT / ".github/workflows/release-binaries.yml").read_text()
        self.assertIn("workflow_dispatch:", workflow)
        self.assertNotRegex(workflow, r"(?m)^  (push|pull_request|schedule|release):")
        self.assertIn("contents: read", workflow)
        self.assertIn("contents: write", workflow)
        self.assertEqual(workflow.count("contents: write"), 1)
        self.assertNotIn("docker/login-action", workflow)
        self.assertIn("trusted/scripts/release_policy.py", workflow)
        self.assertIn("trusted/scripts/release_artifacts.py", workflow)
        self.assertIn("trusted/scripts/release_github.py", workflow)
        self.assertIn("gh release create", workflow)
        self.assertIn("gh release upload", workflow)
        self.assertNotIn("--clobber", workflow)
        self.assertNotIn(":latest", workflow)
        self.assertEqual(workflow.count("trusted/scripts/release_policy.py"), 2)
        self.assertIn("RELEASE_ARTIFACT_TOOL=../trusted/scripts/release_artifacts.py", workflow)
        self.assertIn("steps.upload.outcome", workflow)
        self.assertIn("--prerelease", workflow)
        self.assertIn("--latest=false", workflow)
        self.assertIn("--verify-tag", workflow)
        self.assertIn("--repo maomada/KubeAtlas", workflow)
        self.assertIn("SOURCE_SHA: ${{ inputs.source_sha }}", workflow)
        self.assertIn("VERSION: ${{ inputs.version }}", workflow)
        self.assertIn('--sha "$SOURCE_SHA" --version "$VERSION"', workflow)
        self.assertNotIn('"${{ needs.validate.outputs.', workflow)
        self.assertNotIn('--sha "${{ inputs.source_sha }}"', workflow)
        self.assertNotIn('--version "${{ inputs.version }}"', workflow)
        self.assertEqual(workflow.count("apt-get install --yes rpm dpkg-dev cpio"), 3)

    def test_binary_workflow_checks_both_collision_endpoints(self):
        workflow = (ROOT / ".github/workflows/release-binaries.yml").read_text()
        collision = (ROOT / "scripts/release_github.py").read_text()
        self.assertIn("git/ref/tags", collision)
        self.assertIn("releases/tags", collision)
        self.assertIn("HTTP 404", workflow + collision)
        self.assertIn("environment:", workflow)
        self.assertIn("/git/refs", collision)
        self.assertIn("Atomically create tag ref", workflow)
        self.assertIn("TAG_REF:", workflow)


if __name__ == "__main__":
    unittest.main()
