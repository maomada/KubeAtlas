import unittest

import release_github


class ReleaseGithubTests(unittest.TestCase):
    def test_only_verified_404_is_absence(self):
        self.assertEqual(release_github.classify_response(404), "absent")
        for status in (403, 401, 500, 503):
            with self.subTest(status=status), self.assertRaises(release_github.ReleasePreflightError):
                release_github.classify_response(status)

    def test_network_errors_block_preflight(self):
        def fail(_path):
            raise OSError("network unavailable")

        with self.assertRaises(release_github.ReleasePreflightError):
            release_github.preflight("1.2.3", fail)

    def test_repository_access_must_be_authenticated_before_collision_checks(self):
        for status in (404, 403):
            calls = []

            def getter(path):
                calls.append(path)
                return status

            with self.subTest(status=status), self.assertRaisesRegex(
                release_github.ReleasePreflightError, "canonical repository check"
            ):
                release_github.preflight("1.2.3", getter)
            self.assertEqual(calls, ["repos/maomada/KubeAtlas"])

    def test_existing_tag_or_release_blocks_preflight(self):
        for existing_index in (0, 1):
            responses = [200, 404, 404]
            responses[existing_index + 1] = 200
            with self.subTest(existing_index=existing_index), self.assertRaises(
                release_github.ReleasePreflightError
            ):
                release_github.preflight("1.2.3", lambda _path: responses.pop(0))

    def test_both_absent_endpoints_are_checked(self):
        checked = []
        result = release_github.preflight(
            "1.2.3-rc.1",
            lambda path: checked.append(path) or (200 if path == "repos/maomada/KubeAtlas" else 404),
        )
        self.assertEqual(len(result), 3)
        self.assertEqual(len(checked), 3)
        self.assertTrue(all("v1.2.3-rc.1" in path for path in checked[1:]))
        self.assertEqual(result[0]["status"], "authenticated")

    def test_atomic_ref_creation_never_accepts_a_race_or_replaces_a_tag(self):
        sha = "a" * 40
        request = []
        result = release_github.create_tag_ref(
            "1.2.3", sha, lambda path, body: request.append((path, body)) or 201
        )
        self.assertEqual(request, [(
            "repos/maomada/KubeAtlas/git/refs",
            {"ref": "refs/tags/v1.2.3", "sha": sha},
        )])
        self.assertEqual(result["sha"], sha)
        for status in (200, 409, 422, 403, 500):
            with self.subTest(status=status), self.assertRaises(
                release_github.ReleasePreflightError
            ):
                release_github.create_tag_ref("1.2.3", sha, lambda *_: status)


if __name__ == "__main__":
    unittest.main()
