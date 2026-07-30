import io
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error
import urllib.parse
import zipfile


REPOSITORY = "genistarwynth/wynth-api"
API_URL = "https://api.github.test"
HELPER_PATH = Path(__file__).resolve().parents[1] / "releasequeue" / "reconcile_latest.py"

spec = importlib.util.spec_from_file_location("reconcile_latest", HELPER_PATH)
reconcile_latest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reconcile_latest)


class FakeResponse(io.BytesIO):
    def __init__(self, body):
        super().__init__(body)
        self.status = 200

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        self.close()


class ArtifactAPI:
    def __init__(
        self,
        candidates,
        workflow_runs=None,
        job_conclusions=None,
        marker_step_conclusions=None,
        historical_jobs=None,
        foreign_artifacts=None,
        attempt_overrides=None,
    ):
        self.artifacts = []
        self.archives = {}
        self.run_attempts = {}
        self.current_runs = {}
        default_runs = {}
        for artifact_id, candidate in enumerate(candidates, start=1):
            archive_url = f"{API_URL}/artifacts/{artifact_id}/download"
            self.artifacts.append(
                {
                    "id": artifact_id,
                    "name": reconcile_latest.ARTIFACT_NAME,
                    "expired": False,
                    "archive_download_url": archive_url,
                    "workflow_run": {"id": candidate["run_id"]},
                }
            )
            self.archives[archive_url] = candidate_archive(candidate)
            run = workflow_run(
                candidate["run_id"],
                candidate["run_number"],
                candidate["run_attempt"],
                tag=candidate["tag"],
            )
            self.run_attempts[(run["id"], run["run_attempt"])] = run
            self.current_runs[run["id"]] = run
            default_runs[run["id"]] = run
        for foreign in foreign_artifacts or []:
            artifact_id = len(self.artifacts) + 1
            archive_url = f"{API_URL}/artifacts/{artifact_id}/download"
            run = foreign["run"]
            self.artifacts.append(
                {
                    "id": artifact_id,
                    "name": reconcile_latest.ARTIFACT_NAME,
                    "expired": False,
                    "archive_download_url": archive_url,
                    "workflow_run": {"id": run["id"]},
                }
            )
            self.archives[archive_url] = foreign["archive"]
            self.current_runs[run["id"]] = run
            self.run_attempts[(run["id"], run["run_attempt"])] = run
        for key, run in (attempt_overrides or {}).items():
            self.run_attempts[key] = run
            self.current_runs[run["id"]] = run
            default_runs[run["id"]] = run
        for run in workflow_runs or []:
            default_runs[run["id"]] = run
            self.run_attempts.setdefault((run["id"], run["run_attempt"]), run)
        self.workflow_runs = sorted(
            default_runs.values(),
            key=lambda run: (run["run_number"], run["run_attempt"]),
            reverse=True,
        )
        self.job_conclusions = {
            candidate["run_id"]: "success" for candidate in candidates
        }
        self.job_conclusions.update(job_conclusions or {})
        self.marker_step_conclusions = {
            candidate["run_id"]: "success" for candidate in candidates
        }
        self.marker_step_conclusions.update(marker_step_conclusions or {})
        self.historical_jobs = historical_jobs or {}

    def open(self, request, *, timeout):
        self.timeout = timeout
        url = request.full_url
        parsed = urllib.parse.urlparse(url)
        if parsed.path == f"/repos/{REPOSITORY}/actions/artifacts":
            query = urllib.parse.parse_qs(parsed.query)
            if query.get("name") != [reconcile_latest.ARTIFACT_NAME]:
                raise AssertionError(f"unexpected artifact query: {url}")
            body = json.dumps(
                {"total_count": len(self.artifacts), "artifacts": self.artifacts}
            ).encode()
            return FakeResponse(body)
        workflow_runs_path = (
            f"/repos/{REPOSITORY}/actions/workflows/"
            f"{reconcile_latest.WORKFLOW_NAME}/runs"
        )
        if parsed.path == workflow_runs_path:
            body = json.dumps(
                {
                    "total_count": len(self.workflow_runs),
                    "workflow_runs": self.workflow_runs,
                }
            ).encode()
            return FakeResponse(body)
        run_path_prefix = f"/repos/{REPOSITORY}/actions/runs/"
        if parsed.path.startswith(run_path_prefix):
            suffix = parsed.path.removeprefix(run_path_prefix)
            if "/attempts/" in suffix:
                run_id, run_attempt = suffix.split("/attempts/", 1)
                run = self.run_attempts[(int(run_id), int(run_attempt))]
                return FakeResponse(json.dumps(run).encode())
            if suffix.endswith("/jobs"):
                run_id = int(suffix.removesuffix("/jobs"))
                conclusion = self.job_conclusions.get(run_id)
                jobs = []
                if conclusion is not None:
                    run = next(run for run in self.workflow_runs if run["id"] == run_id)
                    marker_conclusion = self.marker_step_conclusions.get(run_id)
                    steps = []
                    if marker_conclusion is not None:
                        steps.append(
                            {
                                "name": reconcile_latest.IMMUTABLE_MARKER_STEP_NAME,
                                "conclusion": marker_conclusion,
                            }
                        )
                    jobs.append(
                        {
                            "id": run_id * 10 + run["run_attempt"],
                            "name": reconcile_latest.IMMUTABLE_JOB_NAME,
                            "conclusion": conclusion,
                            "run_attempt": run["run_attempt"],
                            "steps": steps,
                        }
                    )
                jobs.extend(self.historical_jobs.get(run_id, []))
                return FakeResponse(
                    json.dumps({"total_count": len(jobs), "jobs": jobs}).encode()
                )
            if suffix.isdigit():
                return FakeResponse(json.dumps(self.current_runs[int(suffix)]).encode())
        if url in self.archives:
            return FakeResponse(self.archives[url])
        raise AssertionError(f"unexpected URL: {url}")


class ReconcileLatestTest(unittest.TestCase):
    def elect(
        self,
        candidates,
        workflow_runs=None,
        job_conclusions=None,
        marker_step_conclusions=None,
        historical_jobs=None,
        foreign_artifacts=None,
        attempt_overrides=None,
    ):
        api = ArtifactAPI(
            candidates,
            workflow_runs=workflow_runs,
            job_conclusions=job_conclusions,
            marker_step_conclusions=marker_step_conclusions,
            historical_jobs=historical_jobs,
            foreign_artifacts=foreign_artifacts,
            attempt_overrides=attempt_overrides,
        )
        selected = reconcile_latest.elect_candidate(
            API_URL,
            REPOSITORY,
            "fixture-token",
            timeout_seconds=3,
            open_url=api.open,
        )
        self.assertEqual(3, api.timeout)
        return selected

    def test_a_b_c_pending_replacement_converges_to_newest_completed_release(self):
        candidate_a = release_candidate(7001, 100, "v1.0.0")
        candidate_b = release_candidate(7002, 101, "v1.1.0")
        candidate_c = release_candidate(7003, 102, "v1.2.0")

        scenarios = [
            ("A running", [candidate_a], candidate_a),
            ("B pending", [candidate_b, candidate_a], candidate_b),
            (
                "C replaces pending B after all immutable versions published",
                [candidate_b, candidate_c, candidate_a],
                candidate_c,
            ),
        ]
        for name, candidates, expected in scenarios:
            with self.subTest(name=name):
                self.assertEqual(expected, self.elect(candidates).as_dict())

    def test_stale_job_elects_newer_release_instead_of_regressing_latest(self):
        candidate_a = release_candidate(8001, 200, "v2.0.0")
        candidate_b = release_candidate(8002, 201, "v2.1.0")
        candidate_c = release_candidate(8003, 202, "v2.2.0")

        selected = self.elect([candidate_c, candidate_a, candidate_b])

        self.assertEqual(candidate_c, selected.as_dict())

    def test_semver_precedence_prevents_later_older_release_regression(self):
        scenarios = [
            ("numeric prerelease", "v2.3.0-rc.10", "v2.3.0-rc.9"),
            ("stable over same-core prerelease", "v2.3.0", "v2.3.0-rc.99"),
            ("higher-core prerelease remains eligible", "v2.4.0-rc.1", "v2.3.9"),
        ]
        for offset, (name, newer_tag, older_tag) in enumerate(scenarios):
            with self.subTest(name=name):
                newer = release_candidate(8101 + offset * 2, 210, newer_tag)
                later_but_older = release_candidate(
                    8102 + offset * 2,
                    211,
                    older_tag,
                )

                selected = self.elect([later_but_older, newer])

                self.assertEqual(newer, selected.as_dict())

    def test_failed_newer_release_without_completion_marker_is_ineligible(self):
        candidate_a = release_candidate(9001, 300, "v3.0.0")
        failed_b = workflow_run(9002, 301, tag="v3.1.0")

        selected = self.elect(
            [candidate_a],
            workflow_runs=[failed_b],
            job_conclusions={failed_b["id"]: "failure"},
        )

        self.assertEqual(candidate_a, selected.as_dict())

    def test_legacy_dispatch_without_marker_does_not_block_reconciliation(self):
        candidate = release_candidate(9051, 310, "v3.0.1")
        legacy_dispatch = workflow_run(9050, 2, tag="main")
        legacy_dispatch.update(
            {
                "event": "workflow_dispatch",
                "display_title": "Publish Docker image (Multi-arch)",
            }
        )

        try:
            selected = self.elect(
                [candidate],
                workflow_runs=[legacy_dispatch],
                job_conclusions={legacy_dispatch["id"]: "success"},
            )
        except reconcile_latest.ReconcileError as error:
            self.fail(f"unmarked legacy dispatch blocked reconciliation: {error}")

        self.assertEqual(candidate, selected.as_dict())

    def test_expired_newer_marker_prevents_stale_rerun_regression(self):
        stale_b = release_candidate(9102, 301, "v3.1.0", run_attempt=2)
        completed_c = workflow_run(9103, 302, tag="v3.2.0")

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "newer completed immutable release",
        ):
            self.elect(
                [stale_b],
                workflow_runs=[completed_c],
                job_conclusions={completed_c["id"]: "success"},
                marker_step_conclusions={completed_c["id"]: "success"},
            )

    def test_expired_higher_semver_blocks_later_lower_version(self):
        later_but_older = release_candidate(9152, 302, "v3.1.0")
        expired_higher = workflow_run(9151, 301, tag="v3.2.0")

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "higher semantic version",
        ):
            self.elect(
                [later_but_older],
                workflow_runs=[expired_higher],
                job_conclusions={expired_higher["id"]: "success"},
                marker_step_conclusions={expired_higher["id"]: "success"},
            )

    def test_candidate_order_must_match_authoritative_run_metadata(self):
        forged = release_candidate(9201, 999, "v3.2.0")
        authoritative = workflow_run(9201, 303, tag="v3.2.0")

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "run_number"):
            self.elect(
                [forged],
                attempt_overrides={(9201, 1): authoritative},
            )

    def test_candidate_tag_must_match_authoritative_run_metadata(self):
        forged = release_candidate(9211, 304, "v3.3.0")
        authoritative = workflow_run(9211, 304, tag="v3.2.1")

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "candidate tag"):
            self.elect(
                [forged],
                attempt_overrides={(9211, 1): authoritative},
            )

    def test_candidate_sha_must_match_authoritative_run_metadata(self):
        forged = release_candidate(9221, 305, "v3.3.1")
        forged["head_sha"] = "a" * 40
        authoritative = workflow_run(9221, 305, tag="v3.3.1")
        authoritative["head_sha"] = "b" * 40

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "candidate head_sha"):
            self.elect(
                [forged],
                attempt_overrides={(9221, 1): authoritative},
            )

    def test_candidate_identity_must_match_authoritative_workflow_ref(self):
        forged = release_candidate(9222, 306, "v3.3.2")
        forged["certificate_identity"] = (
            f"https://github.com/{REPOSITORY}/.github/workflows/"
            "docker-build.yml@refs/heads/main"
        )

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "certificate_identity",
        ):
            self.elect([forged])

    def test_manual_dispatch_candidate_binds_tag_and_branch_identity(self):
        candidate = release_candidate(9223, 307, "v3.3.3")
        candidate["certificate_identity"] = (
            f"https://github.com/{REPOSITORY}/.github/workflows/"
            "docker-build.yml@refs/heads/main"
        )
        authoritative = workflow_run(9223, 307, tag="main")
        authoritative.update(
            {
                "event": "workflow_dispatch",
                "display_title": reconcile_latest.RUN_NAME_PREFIX + candidate["tag"],
            }
        )

        selected = self.elect(
            [candidate],
            attempt_overrides={(9223, 1): authoritative},
        )

        self.assertEqual(candidate, selected.as_dict())

    def test_candidate_requires_marker_success_in_its_exact_attempt(self):
        candidate = release_candidate(9231, 306, "v3.3.2")
        later_success = {
            "id": candidate["run_id"] * 10 + 2,
            "name": reconcile_latest.IMMUTABLE_JOB_NAME,
            "conclusion": "success",
            "run_attempt": 2,
            "steps": [
                {
                    "name": reconcile_latest.IMMUTABLE_MARKER_STEP_NAME,
                    "conclusion": "success",
                }
            ],
        }

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "marker step"):
            self.elect(
                [candidate],
                job_conclusions={candidate["run_id"]: "failure"},
                marker_step_conclusions={candidate["run_id"]: "failure"},
                historical_jobs={candidate["run_id"]: [later_success]},
            )

    def test_marker_success_survives_later_job_cancellation(self):
        stale_b = release_candidate(9252, 401, "v4.1.0", run_attempt=2)
        cancelled_c = workflow_run(9253, 402, tag="v4.2.0")

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "newer completed immutable release",
        ):
            self.elect(
                [stale_b],
                workflow_runs=[cancelled_c],
                job_conclusions={cancelled_c["id"]: "cancelled"},
                marker_step_conclusions={cancelled_c["id"]: "success"},
            )

    def test_failed_rerun_does_not_erase_earlier_successful_high_water(self):
        stale_b = release_candidate(9262, 411, "v4.1.1", run_attempt=2)
        rerun_c = workflow_run(9263, 412, run_attempt=2, tag="v4.1.2")
        successful_attempt_one = {
            "id": rerun_c["id"] * 10 + 1,
            "name": reconcile_latest.IMMUTABLE_JOB_NAME,
            "conclusion": "success",
            "run_attempt": 1,
            "steps": [
                {
                    "name": reconcile_latest.IMMUTABLE_MARKER_STEP_NAME,
                    "conclusion": "success",
                }
            ],
        }

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "newer completed immutable release",
        ):
            self.elect(
                [stale_b],
                workflow_runs=[rerun_c],
                job_conclusions={rerun_c["id"]: "failure"},
                historical_jobs={rerun_c["id"]: [successful_attempt_one]},
            )

    def test_unrelated_workflow_artifact_is_ignored_before_archive_parsing(self):
        candidate = release_candidate(9301, 304, "v3.3.0")
        wrong_workflow = workflow_run(
            9302,
            305,
            path=".github/workflows/unrelated.yml",
            tag="v3.4.0",
        )
        wrong_workflow["head_branch"] = None

        try:
            selected = self.elect(
                [candidate],
                foreign_artifacts=[{"run": wrong_workflow, "archive": b"not a zip"}],
            )
        except reconcile_latest.ReconcileError as error:
            self.fail(f"unrelated workflow artifact was not ignored: {error}")

        self.assertEqual(candidate, selected.as_dict())

    def test_api_timeout_fails_closed(self):
        def timeout_open(_request, *, timeout):
            self.assertEqual(1, timeout)
            raise TimeoutError("fixture timeout")

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "timed out"):
            reconcile_latest.elect_candidate(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=1,
                open_url=timeout_open,
            )

    def test_api_url_must_be_https_before_token_use(self):
        def empty_api_open(_request, *, timeout):
            self.assertEqual(3, timeout)
            return FakeResponse(json.dumps({"total_count": 0, "artifacts": []}).encode())

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "HTTPS"):
            reconcile_latest.elect_candidate(
                "http://api.github.test",
                REPOSITORY,
                "fixture-token",
                timeout_seconds=3,
                open_url=empty_api_open,
            )

    def test_rate_limit_api_error_fails_closed(self):
        def rate_limit_open(request, *, timeout):
            self.assertEqual(1, timeout)
            raise urllib.error.HTTPError(
                request.full_url,
                403,
                "rate limit",
                {"x-ratelimit-remaining": "0"},
                None,
            )

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "HTTP 403"):
            reconcile_latest.elect_candidate(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=1,
                open_url=rate_limit_open,
            )

    def test_workflow_history_over_limit_fails_closed(self):
        candidate_payload = release_candidate(12001, 600, "v6.0.0")
        selected = reconcile_latest.Candidate(**candidate_payload)
        run = workflow_run(12001, 600, tag="v6.0.0")

        def oversized_history_open(request, *, timeout):
            self.assertEqual(3, timeout)
            return FakeResponse(
                json.dumps(
                    {
                        "total_count": reconcile_latest.MAX_WORKFLOW_RUNS + 1,
                        "workflow_runs": [run],
                    }
                ).encode()
            )

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "limit"):
            reconcile_latest.ensure_no_unrepresented_newer_release(
                API_URL,
                REPOSITORY,
                "fixture-token",
                selected,
                3,
                oversized_history_open,
            )

    def test_workflow_history_rejects_duplicate_run_ids_across_pages(self):
        candidate_payload = release_candidate(13000, 200, "v999.0.0")
        selected = reconcile_latest.Candidate(**candidate_payload)
        first_page = [
            workflow_run(
                13000 + offset,
                200 - offset,
                tag="v999.0.0" if offset == 0 else f"v0.0.{200 - offset}",
            )
            for offset in range(reconcile_latest.PAGE_SIZE)
        ]

        def duplicate_page_open(request, *, timeout):
            self.assertEqual(3, timeout)
            query = urllib.parse.parse_qs(
                urllib.parse.urlparse(request.full_url).query
            )
            page = int(query["page"][0])
            runs = first_page if page == 1 else [first_page[-1]]
            return FakeResponse(
                json.dumps(
                    {
                        "total_count": reconcile_latest.PAGE_SIZE + 1,
                        "workflow_runs": runs,
                    }
                ).encode()
            )

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "duplicate"):
            reconcile_latest.ensure_no_unrepresented_newer_release(
                API_URL,
                REPOSITORY,
                "fixture-token",
                selected,
                3,
                duplicate_page_open,
            )

    def test_marker_authorization_rejects_duplicate_job_ids_across_pages(self):
        first_page = [
            {
                "id": 14000 + offset,
                "name": (
                    reconcile_latest.IMMUTABLE_JOB_NAME if offset == 0 else "Other job"
                ),
                "conclusion": "success",
                "run_attempt": 1,
                "steps": (
                    [
                        {
                            "name": reconcile_latest.IMMUTABLE_MARKER_STEP_NAME,
                            "conclusion": "success",
                        }
                    ]
                    if offset == 0
                    else []
                ),
            }
            for offset in range(reconcile_latest.PAGE_SIZE)
        ]

        def duplicate_job_page_open(request, *, timeout):
            self.assertEqual(3, timeout)
            query = urllib.parse.parse_qs(
                urllib.parse.urlparse(request.full_url).query
            )
            page = int(query["page"][0])
            jobs = first_page if page == 1 else [first_page[-1]]
            return FakeResponse(
                json.dumps(
                    {
                        "total_count": reconcile_latest.PAGE_SIZE + 1,
                        "jobs": jobs,
                    }
                ).encode()
            )

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "duplicate"):
            reconcile_latest.immutable_publication_succeeded(
                API_URL,
                REPOSITORY,
                "fixture-token",
                14000,
                1,
                3,
                duplicate_job_page_open,
                exact_attempt=1,
            )

    def test_artifact_redirects_reject_untrusted_hosts_and_strip_token(self):
        handler = reconcile_latest.SafeRedirectHandler()
        request = urllib.request.Request(
            f"{API_URL}/artifact",
            headers={"Authorization": "Bearer fixture-token"},
        )

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "redirect host"):
            handler.redirect_request(
                request,
                None,
                302,
                "Found",
                {},
                "https://127.0.0.1/private",
            )

        redirected = handler.redirect_request(
            request,
            None,
            302,
            "Found",
            {},
            "https://results.blob.core.windows.net/artifact?sig=fixture",
        )
        self.assertIsNotNone(redirected)
        self.assertIsNone(redirected.get_header("Authorization"))

    def test_malformed_candidate_fails_closed(self):
        malformed = release_candidate(10001, 400, "v4.0.0")
        malformed["manifest_digest"] = "latest"

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "manifest_digest"):
            self.elect([malformed])

    def test_candidate_json_rejects_duplicate_fields(self):
        candidate = release_candidate(10002, 401, "v4.0.1")
        encoded = json.dumps(candidate)
        duplicate_tag = encoded[:-1] + f', "tag": "{candidate["tag"]}"}}'
        api = ArtifactAPI([candidate])
        archive_url = api.artifacts[0]["archive_download_url"]
        api.archives[archive_url] = candidate_archive_json(duplicate_tag)

        with self.assertRaisesRegex(reconcile_latest.ReconcileError, "duplicate field"):
            reconcile_latest.elect_candidate(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=3,
                open_url=api.open,
            )

    def test_artifact_metadata_requires_boolean_expiration_state(self):
        candidate = release_candidate(10003, 402, "v4.0.2")
        api = ArtifactAPI([candidate])
        api.artifacts[0]["expired"] = "false"

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "artifact metadata",
        ):
            reconcile_latest.elect_candidate(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=3,
                open_url=api.open,
            )

    def test_outputs_contain_only_validated_candidate_fields(self):
        selected = self.elect([release_candidate(11001, 500, "v5.0.0")])
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "github-output"

            reconcile_latest.write_github_outputs(output_path, selected)

            self.assertEqual(
                [
                    "latest_eligible=true",
                    "tag=v5.0.0",
                    f"amd64_digest={selected.amd64_digest}",
                    f"arm64_digest={selected.arm64_digest}",
                    f"manifest_digest={selected.manifest_digest}",
                    f"certificate_identity={selected.certificate_identity}",
                    "run_number=500",
                    "run_attempt=1",
                ],
                output_path.read_text(encoding="utf-8").splitlines(),
            )

    def test_main_nonsemver_run_reconciles_pending_semver_candidate(self):
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "github-output"
            environment = {
                "CURRENT_TAG": "maintenance-snapshot",
                "GITHUB_API_URL": API_URL,
                "GITHUB_REPOSITORY": REPOSITORY,
                "GITHUB_TOKEN": "fixture-token",
                "GITHUB_OUTPUT": str(output_path),
            }
            pending = reconcile_latest.Candidate(
                **release_candidate(15001, 700, "v7.0.0")
            )
            with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(
                reconcile_latest,
                "elect_candidate",
                return_value=pending,
            ) as elect, mock.patch.object(
                reconcile_latest.sys,
                "stdout",
                io.StringIO(),
            ):
                result = reconcile_latest.main()

            self.assertEqual(0, result)
            elect.assert_called_once_with(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=10,
                allow_empty=True,
            )
            self.assertEqual(
                ["latest_eligible=true", "tag=v7.0.0"],
                output_path.read_text(encoding="utf-8").splitlines()[:2],
            )

    def test_main_nonsemver_run_noops_only_after_empty_election(self):
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "github-output"
            environment = {
                "CURRENT_TAG": "maintenance-snapshot",
                "GITHUB_API_URL": API_URL,
                "GITHUB_REPOSITORY": REPOSITORY,
                "GITHUB_TOKEN": "fixture-token",
                "GITHUB_OUTPUT": str(output_path),
            }
            with mock.patch.dict(os.environ, environment, clear=True), mock.patch.object(
                reconcile_latest,
                "elect_candidate",
                return_value=None,
            ) as elect, mock.patch.object(
                reconcile_latest.sys,
                "stdout",
                io.StringIO(),
            ):
                result = reconcile_latest.main()

            self.assertEqual(0, result)
            elect.assert_called_once()
            self.assertEqual(
                ["latest_eligible=false"],
                output_path.read_text(encoding="utf-8").splitlines(),
            )

    def test_allow_empty_rejects_unrepresented_semver_marker(self):
        ineligible_c = release_candidate(15103, 703, "maintenance-snapshot")
        missing_b = workflow_run(15102, 702, tag="v7.1.0")
        api = ArtifactAPI(
            [ineligible_c],
            workflow_runs=[missing_b],
            job_conclusions={missing_b["id"]: "success"},
            marker_step_conclusions={missing_b["id"]: "success"},
        )

        with self.assertRaisesRegex(
            reconcile_latest.ReconcileError,
            "completed immutable release",
        ):
            reconcile_latest.elect_candidate(
                API_URL,
                REPOSITORY,
                "fixture-token",
                timeout_seconds=3,
                open_url=api.open,
                allow_empty=True,
            )

    def test_main_fails_closed_when_current_tag_is_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "github-output"
            with mock.patch.dict(
                os.environ,
                {"GITHUB_OUTPUT": str(output_path)},
                clear=True,
            ), mock.patch.object(reconcile_latest, "elect_candidate") as elect:
                with mock.patch.object(
                    reconcile_latest.sys,
                    "stderr",
                    io.StringIO(),
                ):
                    result = reconcile_latest.main()

            self.assertEqual(1, result)
            elect.assert_not_called()
            self.assertFalse(output_path.exists())


def release_candidate(run_id, run_number, tag, run_attempt=1):
    return {
        "schema": 1,
        "repository": REPOSITORY,
        "workflow": "docker-build.yml",
        "run_id": run_id,
        "run_number": run_number,
        "run_attempt": run_attempt,
        "head_sha": commit_sha(run_id),
        "tag": tag,
        "amd64_digest": digest(run_number * 10 + 1),
        "arm64_digest": digest(run_number * 10 + 2),
        "manifest_digest": digest(run_number * 10 + 3),
        "certificate_identity": (
            f"https://github.com/{REPOSITORY}/.github/workflows/"
            f"docker-build.yml@refs/tags/{tag}"
        ),
    }


def workflow_run(
    run_id,
    run_number,
    run_attempt=1,
    path=".github/workflows/docker-build.yml",
    tag="v0.0.0",
):
    return {
        "id": run_id,
        "run_number": run_number,
        "run_attempt": run_attempt,
        "head_sha": commit_sha(run_id),
        "path": path,
        "event": "push",
        "head_branch": tag,
    }


def digest(value):
    return f"sha256:{value:064x}"


def commit_sha(value):
    return f"{value:040x}"


def candidate_archive(candidate):
    return candidate_archive_json(json.dumps(candidate))


def candidate_archive_json(candidate_json):
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("candidate.json", candidate_json)
    return buffer.getvalue()


if __name__ == "__main__":
    unittest.main()
