#!/usr/bin/env python3

import http.client
import io
import json
import os
from pathlib import Path
import re
import socket
import sys
from typing import NamedTuple
import urllib.error
import urllib.parse
import urllib.request
import zipfile


ARTIFACT_NAME = "docker-build-release-candidate"
WORKFLOW_NAME = "docker-build.yml"
WORKFLOW_PATH = f".github/workflows/{WORKFLOW_NAME}"
IMMUTABLE_JOB_NAME = "Create multi-arch manifests"
IMMUTABLE_MARKER_STEP_NAME = "Publish immutable release candidate marker"
RUN_NAME_PREFIX = "Publish Docker image "
PAGE_SIZE = 100
MAX_ARTIFACTS = 1000
MAX_WORKFLOW_RUNS = 1000
MAX_JOBS = 1000
MAX_RESPONSE_BYTES = 1024 * 1024
MAX_TIMEOUT_SECONDS = 30
DIGEST_PATTERN = re.compile(r"sha256:[0-9a-f]{64}")
SHA_PATTERN = re.compile(r"[0-9a-f]{40}")
TAG_PATTERN = re.compile(r"[a-z0-9_][a-z0-9_.-]{0,121}")
SEMVER_PATTERN = re.compile(
    r"v?(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)\."
    r"(0|[1-9][0-9]*)"
    r"(?:-([0-9a-z-]+(?:\.[0-9a-z-]+)*))?"
)


class ReconcileError(Exception):
    pass


def unique_json_object(pairs):
    payload = {}
    for key, value in pairs:
        if key in payload:
            raise ReconcileError("JSON contains duplicate fields")
        payload[key] = value
    return payload


def reject_json_constant(_value):
    raise ReconcileError("JSON contains a non-standard number")


class Candidate(NamedTuple):
    schema: int
    repository: str
    workflow: str
    run_id: int
    run_number: int
    run_attempt: int
    head_sha: str
    tag: str
    amd64_digest: str
    arm64_digest: str
    manifest_digest: str
    certificate_identity: str

    def as_dict(self):
        return self._asdict()


class SafeRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file_pointer, code, message, headers, new_url):
        try:
            old_url = urllib.parse.urlsplit(request.full_url)
            redirected_url = urllib.parse.urlsplit(new_url)
            redirected_port = redirected_url.port
        except ValueError:
            raise ReconcileError("artifact redirect URL is invalid") from None
        cross_origin = old_url[:2] != redirected_url[:2]
        redirected_host = redirected_url.hostname
        trusted_cross_origin = (
            isinstance(redirected_host, str)
            and redirected_url.scheme == "https"
            and redirected_url.username is None
            and redirected_url.password is None
            and redirected_port in (None, 443)
            and any(
                redirected_host == suffix
                or redirected_host.endswith("." + suffix)
                for suffix in (
                    "actions.githubusercontent.com",
                    "blob.core.windows.net",
                )
            )
        )
        if redirected_url.scheme != "https" or (
            cross_origin and not trusted_cross_origin
        ):
            raise ReconcileError("artifact redirect host is not trusted")

        try:
            redirected = super().redirect_request(
                request, file_pointer, code, message, headers, new_url
            )
        except ValueError:
            raise ReconcileError("artifact redirect URL is invalid") from None
        if redirected is None:
            return None
        if cross_origin:
            redirected.remove_header("Authorization")
        return redirected


def elect_candidate(
    api_url,
    repository,
    token,
    timeout_seconds=10,
    open_url=None,
    allow_empty=False,
):
    if not isinstance(api_url, str) or not isinstance(token, str) or not token:
        raise ReconcileError("GitHub API URL, repository, and token are required")
    try:
        api = urllib.parse.urlsplit(api_url)
        api_port = api.port
    except ValueError:
        raise ReconcileError("GitHub API URL is invalid") from None
    if (
        api.scheme != "https"
        or not api.hostname
        or api.username is not None
        or api.password is not None
        or api.query
        or api.fragment
        or api_port not in (None, 443)
    ):
        raise ReconcileError("GitHub API URL must use a trusted HTTPS origin")
    if not isinstance(repository, str) or not re.fullmatch(
        r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository
    ):
        raise ReconcileError("GitHub repository name is invalid")
    if (
        not is_int(timeout_seconds)
        or timeout_seconds <= 0
        or timeout_seconds > MAX_TIMEOUT_SECONDS
    ):
        raise ReconcileError(
            f"API timeout must be between 1 and {MAX_TIMEOUT_SECONDS} seconds"
        )
    if not isinstance(allow_empty, bool):
        raise ReconcileError("allow_empty must be a boolean")
    if open_url is None:
        open_url = urllib.request.build_opener(SafeRedirectHandler()).open

    artifacts = list_candidate_artifacts(
        api_url, repository, token, timeout_seconds, open_url
    )
    candidates = []
    candidates_by_order = {}
    for artifact in artifacts:
        if artifact.get("expired") is True:
            continue
        artifact_run_id = artifact_workflow_run_id(artifact)
        current_run = get_workflow_run(
            api_url,
            repository,
            token,
            artifact_run_id,
            timeout_seconds,
            open_url,
        )
        if current_run.get("path") != WORKFLOW_PATH:
            continue
        validate_workflow_run(current_run)
        if current_run["id"] != artifact_run_id:
            raise ReconcileError(
                "candidate artifact run does not match authoritative run metadata"
            )
        candidate = download_candidate(
            api_url,
            repository,
            token,
            artifact,
            timeout_seconds,
            open_url,
        )
        authoritative_run = get_workflow_run_attempt(
            api_url,
            repository,
            token,
            candidate.run_id,
            candidate.run_attempt,
            timeout_seconds,
            open_url,
        )
        validate_candidate_run(candidate, authoritative_run)
        if not immutable_publication_succeeded(
            api_url,
            repository,
            token,
            candidate.run_id,
            candidate.run_attempt,
            timeout_seconds,
            open_url,
            exact_attempt=candidate.run_attempt,
        ):
            raise ReconcileError(
                "candidate marker step did not succeed in its authoritative attempt"
            )
        order = (candidate.run_number, candidate.run_attempt)
        previous = candidates_by_order.get(order)
        if previous is not None and previous != candidate:
            raise ReconcileError(
                "conflicting release candidates for the same run attempt"
            )
        candidates_by_order[order] = candidate
        if release_order_key(
            candidate.tag,
            candidate.run_number,
            candidate.run_attempt,
        ) is not None:
            candidates.append(candidate)

    if not candidates:
        if allow_empty:
            ensure_no_unrepresented_newer_release(
                api_url,
                repository,
                token,
                None,
                timeout_seconds,
                open_url,
            )
            return None
        raise ReconcileError("no latest-eligible immutable release candidate is available")
    selected = max(
        candidates,
        key=lambda candidate: release_order_key(
            candidate.tag,
            candidate.run_number,
            candidate.run_attempt,
        ),
    )
    ensure_no_unrepresented_newer_release(
        api_url,
        repository,
        token,
        selected,
        timeout_seconds,
        open_url,
    )
    return selected


def list_candidate_artifacts(api_url, repository, token, timeout_seconds, open_url):
    artifacts = []
    seen_artifact_ids = set()
    expected_total = None
    for page in range(1, MAX_ARTIFACTS // PAGE_SIZE + 1):
        query = urllib.parse.urlencode(
            {"name": ARTIFACT_NAME, "per_page": PAGE_SIZE, "page": page}
        )
        repository_path = urllib.parse.quote(repository, safe="/")
        url = (
            f"{api_url.rstrip('/')}/repos/{repository_path}/actions/artifacts?{query}"
        )
        payload = request_json(url, token, timeout_seconds, open_url)
        total_count = payload.get("total_count")
        page_artifacts = payload.get("artifacts")
        if not is_int(total_count) or total_count < 0 or not isinstance(page_artifacts, list):
            raise ReconcileError("GitHub artifact response has an invalid shape")
        if total_count > MAX_ARTIFACTS:
            raise ReconcileError(
                "GitHub artifact count exceeds the reconciliation limit"
            )
        if expected_total is None:
            expected_total = total_count
        elif expected_total != total_count:
            raise ReconcileError("GitHub artifact count changed during reconciliation")

        for artifact in page_artifacts:
            if (
                not isinstance(artifact, dict)
                or not is_int(artifact.get("id"))
                or artifact["id"] <= 0
                or not isinstance(artifact.get("expired"), bool)
            ):
                raise ReconcileError("GitHub returned invalid artifact metadata")
            artifact_id = artifact["id"]
            if artifact_id in seen_artifact_ids:
                continue
            if artifact.get("name") != ARTIFACT_NAME:
                raise ReconcileError("GitHub returned an unexpected artifact name")
            seen_artifact_ids.add(artifact_id)
            artifacts.append(artifact)

        if len(seen_artifact_ids) == expected_total:
            return artifacts
        if not page_artifacts:
            break

    raise ReconcileError("GitHub artifact listing was incomplete")


def get_workflow_run_attempt(
    api_url,
    repository,
    token,
    run_id,
    run_attempt,
    timeout_seconds,
    open_url,
):
    repository_path = urllib.parse.quote(repository, safe="/")
    url = (
        f"{api_url.rstrip('/')}/repos/{repository_path}/actions/runs/"
        f"{run_id}/attempts/{run_attempt}"
    )
    return request_json(url, token, timeout_seconds, open_url)


def get_workflow_run(
    api_url,
    repository,
    token,
    run_id,
    timeout_seconds,
    open_url,
):
    repository_path = urllib.parse.quote(repository, safe="/")
    url = (
        f"{api_url.rstrip('/')}/repos/{repository_path}/actions/runs/{run_id}"
    )
    return request_json(url, token, timeout_seconds, open_url)


def validate_candidate_run(candidate, workflow_run):
    validate_workflow_run(workflow_run)
    for candidate_field, run_field in (
        ("run_id", "id"),
        ("run_number", "run_number"),
        ("run_attempt", "run_attempt"),
        ("head_sha", "head_sha"),
    ):
        if getattr(candidate, candidate_field) != workflow_run[run_field]:
            raise ReconcileError(
                f"candidate {candidate_field} does not match authoritative run metadata"
            )
    if candidate.tag != workflow_run_tag(workflow_run):
        raise ReconcileError("candidate tag does not match authoritative run metadata")

    identity_prefix = (
        f"https://github.com/{candidate.repository}/.github/workflows/"
        f"{WORKFLOW_NAME}@"
    )
    if workflow_run["event"] == "push":
        expected_identities = {identity_prefix + f"refs/tags/{candidate.tag}"}
    else:
        head_branch = workflow_run["head_branch"]
        expected_identities = {
            identity_prefix + f"refs/heads/{head_branch}",
            identity_prefix + f"refs/tags/{head_branch}",
        }
    if candidate.certificate_identity not in expected_identities:
        raise ReconcileError(
            "candidate certificate_identity does not match authoritative run metadata"
        )


def ensure_no_unrepresented_newer_release(
    api_url,
    repository,
    token,
    selected,
    timeout_seconds,
    open_url,
):
    repository_path = urllib.parse.quote(repository, safe="/")
    workflow_path = urllib.parse.quote(WORKFLOW_NAME, safe="")
    selected_order = None
    if selected is not None:
        selected_order = release_order_key(
            selected.tag,
            selected.run_number,
            selected.run_attempt,
        )
        if selected_order is None:
            raise ReconcileError("selected release is not latest-eligible")
    previous_run_number = None
    inspected_runs = 0
    seen_run_ids = set()
    selected_found = False
    expected_total = None

    for page in range(1, MAX_WORKFLOW_RUNS // PAGE_SIZE + 1):
        query = urllib.parse.urlencode(
            {"per_page": PAGE_SIZE, "page": page, "exclude_pull_requests": "true"}
        )
        url = (
            f"{api_url.rstrip('/')}/repos/{repository_path}/actions/workflows/"
            f"{workflow_path}/runs?{query}"
        )
        payload = request_json(url, token, timeout_seconds, open_url)
        total_count = payload.get("total_count")
        workflow_runs = payload.get("workflow_runs")
        if not is_int(total_count) or total_count < 0 or not isinstance(workflow_runs, list):
            raise ReconcileError("GitHub workflow-run response has an invalid shape")
        if total_count > MAX_WORKFLOW_RUNS:
            raise ReconcileError(
                "GitHub workflow-run count exceeds the reconciliation limit"
            )
        if expected_total is None:
            expected_total = total_count
        elif expected_total != total_count:
            raise ReconcileError("GitHub workflow-run count changed during reconciliation")
        if not workflow_runs:
            break

        for workflow_run in workflow_runs:
            validate_workflow_run(workflow_run)
            run_id = workflow_run["id"]
            if run_id in seen_run_ids:
                raise ReconcileError("GitHub workflow-run listing contains a duplicate run")
            seen_run_ids.add(run_id)
            run_number = workflow_run["run_number"]
            if previous_run_number is not None and run_number > previous_run_number:
                raise ReconcileError("GitHub workflow runs were not returned newest first")
            previous_run_number = run_number
            inspected_runs += 1
            if inspected_runs > MAX_WORKFLOW_RUNS:
                raise ReconcileError("selected release is too old to reconcile safely")

            if selected is not None and run_id == selected.run_id:
                selected_found = True

            try:
                run_tag = workflow_run_tag(workflow_run)
            except ReconcileError:
                if immutable_publication_succeeded(
                    api_url,
                    repository,
                    token,
                    run_id,
                    1,
                    timeout_seconds,
                    open_url,
                ):
                    raise ReconcileError(
                        "completed immutable release marker has no authoritative tag"
                    ) from None
                continue

            run_order = release_order_key(
                run_tag,
                workflow_run["run_number"],
                workflow_run["run_attempt"],
            )
            is_unrepresented_release = run_order is not None and (
                selected_order is None or run_order > selected_order
            )
            minimum_attempt = 1
            if selected is not None and run_id == selected.run_id:
                minimum_attempt = selected.run_attempt + 1
            if is_unrepresented_release and immutable_publication_succeeded(
                api_url,
                repository,
                token,
                run_id,
                minimum_attempt,
                timeout_seconds,
                open_url,
            ):
                if selected is None:
                    raise ReconcileError(
                        "completed immutable release has no available candidate marker"
                    )
                if run_order[0] > selected_order[0]:
                    raise ReconcileError(
                        "newer completed immutable release with a higher semantic "
                        "version has no available candidate marker"
                    )
                raise ReconcileError(
                    "newer completed immutable release with an equal semantic version "
                    "has no available candidate marker"
                )

        if len(workflow_runs) < PAGE_SIZE:
            break

    if inspected_runs != expected_total:
        raise ReconcileError("GitHub workflow-run listing was incomplete")
    if selected is None:
        return
    if selected_found:
        return
    raise ReconcileError("selected candidate was not found in authoritative workflow runs")


def immutable_publication_succeeded(
    api_url,
    repository,
    token,
    run_id,
    minimum_attempt,
    timeout_seconds,
    open_url,
    exact_attempt=None,
):
    repository_path = urllib.parse.quote(repository, safe="/")
    seen_job_ids = set()
    expected_total = None
    marker_succeeded = False
    for page in range(1, MAX_JOBS // PAGE_SIZE + 1):
        query = urllib.parse.urlencode(
            {"filter": "all", "per_page": PAGE_SIZE, "page": page}
        )
        url = (
            f"{api_url.rstrip('/')}/repos/{repository_path}/actions/runs/"
            f"{run_id}/jobs?{query}"
        )
        payload = request_json(url, token, timeout_seconds, open_url)
        total_count = payload.get("total_count")
        jobs = payload.get("jobs")
        if not is_int(total_count) or total_count < 0 or not isinstance(jobs, list):
            raise ReconcileError("GitHub jobs response has an invalid shape")
        if total_count > MAX_JOBS:
            raise ReconcileError("workflow run job count exceeds the reconciliation limit")
        if expected_total is None:
            expected_total = total_count
        elif expected_total != total_count:
            raise ReconcileError("GitHub job count changed during reconciliation")

        for job in jobs:
            if (
                not isinstance(job, dict)
                or not is_int(job.get("id"))
                or job["id"] <= 0
                or not is_int(job.get("run_attempt"))
                or job["run_attempt"] <= 0
            ):
                raise ReconcileError("GitHub returned invalid job metadata")
            if job["id"] in seen_job_ids:
                raise ReconcileError("GitHub job listing contains a duplicate job")
            seen_job_ids.add(job["id"])
            steps = job.get("steps", [])
            if not isinstance(steps, list) or any(
                not isinstance(step, dict) for step in steps
            ):
                raise ReconcileError("GitHub returned invalid job steps")
            if (
                job.get("name") != IMMUTABLE_JOB_NAME
                or job["run_attempt"] < minimum_attempt
                or (
                    exact_attempt is not None
                    and job["run_attempt"] != exact_attempt
                )
            ):
                continue
            marker_succeeded = marker_succeeded or any(
                step.get("name") == IMMUTABLE_MARKER_STEP_NAME
                and step.get("conclusion") == "success"
                for step in steps
            )
        if len(seen_job_ids) == expected_total:
            return marker_succeeded
        if not jobs:
            break
    raise ReconcileError("GitHub job listing was incomplete")


def validate_workflow_run(workflow_run):
    if not isinstance(workflow_run, dict):
        raise ReconcileError("GitHub returned invalid workflow run metadata")
    for field in ("id", "run_number", "run_attempt"):
        if not is_int(workflow_run.get(field)) or workflow_run[field] <= 0:
            raise ReconcileError(f"authoritative workflow run has an invalid {field}")
    if not isinstance(workflow_run.get("head_sha"), str) or not SHA_PATTERN.fullmatch(
        workflow_run["head_sha"]
    ):
        raise ReconcileError("authoritative workflow run has an invalid head_sha")
    if not isinstance(workflow_run.get("head_branch"), str) or re.search(
        r"\s", workflow_run["head_branch"]
    ):
        raise ReconcileError("authoritative workflow run has an invalid head_branch")
    if workflow_run.get("path") != WORKFLOW_PATH:
        raise ReconcileError("candidate workflow path does not match docker-build.yml")


def workflow_run_tag(workflow_run):
    event = workflow_run.get("event")
    if event == "push":
        tag = workflow_run.get("head_branch")
    elif event == "workflow_dispatch":
        display_title = workflow_run.get("display_title")
        if not isinstance(display_title, str) or not display_title.startswith(
            RUN_NAME_PREFIX
        ):
            raise ReconcileError("workflow dispatch run title does not identify its tag")
        tag = display_title.removeprefix(RUN_NAME_PREFIX)
    else:
        raise ReconcileError("candidate workflow event is not a release trigger")
    if not isinstance(tag, str) or not TAG_PATTERN.fullmatch(tag):
        raise ReconcileError("authoritative workflow run has an invalid tag")
    return tag


def request_json(url, token, timeout_seconds, open_url):
    body = request_bytes(url, token, timeout_seconds, open_url)
    try:
        payload = json.loads(
            body,
            object_pairs_hook=unique_json_object,
            parse_constant=reject_json_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise ReconcileError("GitHub API returned invalid JSON") from None
    if not isinstance(payload, dict):
        raise ReconcileError("GitHub API returned a non-object response")
    return payload


def request_bytes(url, token, timeout_seconds, open_url):
    try:
        request = urllib.request.Request(
            url,
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": f"Bearer {token}",
                "User-Agent": "docker-release-latest-reconciler",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        with open_url(request, timeout=timeout_seconds) as response:
            status = getattr(response, "status", 200)
            if not is_int(status):
                raise ReconcileError("GitHub API returned an invalid HTTP status")
            if status < 200 or status >= 300:
                raise ReconcileError("GitHub API returned an HTTP error")
            body = response.read(MAX_RESPONSE_BYTES + 1)
    except urllib.error.HTTPError:
        raise ReconcileError("GitHub API returned an HTTP error") from None
    except (TimeoutError, socket.timeout):
        raise ReconcileError("GitHub API request timed out") from None
    except urllib.error.URLError as error:
        if isinstance(error.reason, (TimeoutError, socket.timeout)):
            raise ReconcileError("GitHub API request timed out") from None
        raise ReconcileError("GitHub API request failed") from None
    except ValueError:
        raise ReconcileError("GitHub API request is invalid") from None
    except http.client.HTTPException:
        raise ReconcileError("GitHub API request failed") from None
    except OSError:
        raise ReconcileError("GitHub API request failed") from None
    if len(body) > MAX_RESPONSE_BYTES:
        raise ReconcileError("GitHub API response exceeded the size limit")
    return body


def download_candidate(
    api_url,
    repository,
    token,
    artifact,
    timeout_seconds,
    open_url,
):
    archive_url = artifact.get("archive_download_url")
    if not isinstance(archive_url, str):
        raise ReconcileError("candidate artifact metadata is incomplete")
    if not same_origin(api_url, archive_url):
        raise ReconcileError("candidate artifact download URL has an unexpected origin")
    workflow_run_id = artifact_workflow_run_id(artifact)

    archive_body = request_bytes(
        archive_url, token, timeout_seconds, open_url
    )
    try:
        with zipfile.ZipFile(io.BytesIO(archive_body)) as archive:
            matching_files = [
                info
                for info in archive.infolist()
                if not info.is_dir() and info.filename == "candidate.json"
            ]
            if len(matching_files) != 1:
                raise ReconcileError(
                    "candidate artifact must contain exactly one candidate.json"
                )
            if matching_files[0].file_size > MAX_RESPONSE_BYTES:
                raise ReconcileError("candidate.json exceeded the size limit")
            candidate_data = archive.read(matching_files[0])
    except zipfile.BadZipFile:
        raise ReconcileError("candidate artifact is not a valid ZIP archive") from None

    try:
        payload = json.loads(
            candidate_data,
            object_pairs_hook=unique_json_object,
            parse_constant=reject_json_constant,
        )
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise ReconcileError("candidate.json contains invalid JSON") from None
    return validate_candidate(repository, workflow_run_id, payload)


def artifact_workflow_run_id(artifact):
    workflow_run = artifact.get("workflow_run")
    if not isinstance(workflow_run, dict):
        raise ReconcileError("candidate artifact metadata is incomplete")
    workflow_run_id = workflow_run.get("id")
    if not is_int(workflow_run_id) or workflow_run_id <= 0:
        raise ReconcileError("candidate artifact has an invalid workflow run ID")
    return workflow_run_id


def validate_candidate(repository, workflow_run_id, payload):
    expected_fields = set(Candidate._fields)
    if not isinstance(payload, dict) or set(payload) != expected_fields:
        raise ReconcileError("candidate.json has an invalid schema")
    for field in ("schema", "run_id", "run_number", "run_attempt"):
        if not is_int(payload[field]):
            raise ReconcileError(f"candidate field {field} must be an integer")
    if payload["schema"] != 1:
        raise ReconcileError("candidate schema is unsupported")
    if payload["run_id"] != workflow_run_id:
        raise ReconcileError("candidate run_id does not match its artifact")
    if payload["run_number"] <= 0 or payload["run_attempt"] <= 0:
        raise ReconcileError("candidate run ordering fields must be positive")
    if payload["repository"] != repository or payload["workflow"] != WORKFLOW_NAME:
        raise ReconcileError("candidate repository or workflow does not match")
    if not isinstance(payload["head_sha"], str) or not SHA_PATTERN.fullmatch(
        payload["head_sha"]
    ):
        raise ReconcileError("candidate head_sha is not a full commit SHA")
    if not isinstance(payload["tag"], str) or not TAG_PATTERN.fullmatch(payload["tag"]):
        raise ReconcileError("candidate tag is not a valid container tag")
    for field in ("amd64_digest", "arm64_digest", "manifest_digest"):
        value = payload[field]
        if not isinstance(value, str) or not DIGEST_PATTERN.fullmatch(value):
            raise ReconcileError(f"candidate {field} is not an immutable sha256 digest")

    identity_prefix = (
        f"https://github.com/{repository}/.github/workflows/{WORKFLOW_NAME}@refs/"
    )
    identity = payload["certificate_identity"]
    if (
        not isinstance(identity, str)
        or not re.fullmatch(
            re.escape(identity_prefix) + r"(?:heads|tags)/[^\s]+",
            identity,
        )
    ):
        raise ReconcileError("candidate certificate_identity is invalid")
    return Candidate(**payload)


def write_github_outputs(output_path, candidate):
    lines = [
        "latest_eligible=true",
        f"tag={candidate.tag}",
        f"amd64_digest={candidate.amd64_digest}",
        f"arm64_digest={candidate.arm64_digest}",
        f"manifest_digest={candidate.manifest_digest}",
        f"certificate_identity={candidate.certificate_identity}",
        f"run_number={candidate.run_number}",
        f"run_attempt={candidate.run_attempt}",
    ]
    with Path(output_path).open("a", encoding="utf-8") as output:
        output.write("\n".join(lines) + "\n")


def same_origin(left, right):
    try:
        left_url = urllib.parse.urlsplit(left)
        right_url = urllib.parse.urlsplit(right)
    except ValueError:
        return False
    return left_url.scheme == right_url.scheme and left_url.netloc == right_url.netloc


def is_int(value):
    return isinstance(value, int) and not isinstance(value, bool)


def semantic_version(tag):
    if not isinstance(tag, str):
        return None
    match = SEMVER_PATTERN.fullmatch(tag)
    if match is None:
        return None

    prerelease = match.group(4)
    prerelease_key = []
    if prerelease is not None:
        for identifier in prerelease.split("."):
            if identifier.isdigit():
                if len(identifier) > 1 and identifier.startswith("0"):
                    return None
                prerelease_key.append((0, int(identifier)))
            else:
                prerelease_key.append((1, identifier))

    return (
        int(match.group(1)),
        int(match.group(2)),
        int(match.group(3)),
        prerelease is None,
        tuple(prerelease_key),
    )


def release_order_key(tag, run_number, run_attempt):
    version = semantic_version(tag)
    if version is None:
        return None
    return version, run_number, run_attempt


def main():
    try:
        output_path = os.environ.get("GITHUB_OUTPUT")
        if not output_path:
            raise ReconcileError("GITHUB_OUTPUT is required")
        current_tag = os.environ.get("CURRENT_TAG", "")
        if not TAG_PATTERN.fullmatch(current_tag):
            raise ReconcileError("CURRENT_TAG is not a valid release container tag")

        timeout_seconds = int(os.environ.get("RELEASE_QUEUE_TIMEOUT_SECONDS", "10"))
        candidate = elect_candidate(
            os.environ.get("GITHUB_API_URL", ""),
            os.environ.get("GITHUB_REPOSITORY", ""),
            os.environ.get("GITHUB_TOKEN", ""),
            timeout_seconds=timeout_seconds,
            allow_empty=semantic_version(current_tag) is None,
        )
        if candidate is None:
            with Path(output_path).open("a", encoding="utf-8") as output:
                output.write("latest_eligible=false\n")
            print("No latest-eligible immutable release candidate is available")
            return 0
        write_github_outputs(output_path, candidate)
        print(
            "Elected immutable release "
            f"{candidate.tag} from run {candidate.run_number} "
            f"attempt {candidate.run_attempt}"
        )
        return 0
    except ReconcileError as error:
        print(f"::error::Latest reconciliation failed: {error}", file=sys.stderr)
        return 1
    except ValueError:
        print(
            "::error::Latest reconciliation failed: invalid reconciliation input",
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
