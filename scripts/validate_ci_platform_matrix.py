#!/usr/bin/env python3
"""Parse ci.yml / release.yml and enforce Linux-default + non-publishing dispatch."""

from __future__ import annotations

import re
import sys
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parent.parent
CI_PATH = ROOT / ".github/workflows/ci.yml"
RELEASE_PATH = ROOT / ".github/workflows/release.yml"

REQUIRED_PUBLISH_NEEDS = frozenset(
    {"go", "frontend", "shell", "e2e", "denver", "watch-worker"}
)
FORBIDDEN_PUBLISH_NEEDS = frozenset(
    {"go-build-linux", "go-build-macos", "go-build-windows", "go-build-all-platforms"}
)

# Whole-expression contracts (post-fold). GitHub executes the literal string; partial
# mirrors/substrings/scenario evaluators cannot prove safety under OR/case/input broadening.
ALLOWED_CROSS_PLATFORM_IF = (
    "github.event_name != 'pull_request' && "
    "(inputs.go_cross_platform == true || inputs.go_cross_platform == 'true')"
)
ALLOWED_DOCKER_PUBLISH_IF = (
    "inputs.publish_image != 'skip' && github.event_name == 'push'"
)
ALLOWED_DOCKER_PR_IF = (
    "inputs.publish_image != 'skip' && github.event_name == 'pull_request' "
    "&& github.repository == 'Rhionin/stockbot'"
)


class WorkflowValidationError(Exception):
    pass


def _loader() -> type[yaml.SafeLoader]:
    """YAML 1.1 treats 'on' as bool; disable implicit bool resolution."""
    loader = yaml.SafeLoader
    loader.yaml_implicit_resolvers = {
        k: [(t, r) for t, r in v if t != "tag:yaml.org,2002:bool"]
        for k, v in loader.yaml_implicit_resolvers.items()
    }
    return loader


def load_workflow(path: Path) -> dict[str, Any]:
    text = path.read_text(encoding="utf-8")
    doc = yaml.load(text, Loader=_loader())
    if not isinstance(doc, dict):
        raise WorkflowValidationError(f"{path}: root must be a mapping")
    return doc


def _folded_if(value: Any) -> str:
    if value is None:
        return ""
    if not isinstance(value, str):
        raise WorkflowValidationError(f"job if must be string, got {type(value)}")
    return " ".join(value.split())


def require_exact_job_if(if_value: Any, allowed: str, *, job: str) -> None:
    folded = _folded_if(if_value)
    if folded != allowed:
        raise WorkflowValidationError(
            f"{job} if must match the pinned safety contract exactly; "
            f"expected {allowed!r}, got {folded!r}"
        )


def cross_platform_job_enabled(event_name: str, go_cross_platform: Any) -> bool:
    """GitHub Actions model for unit tests only (not used to validate committed if)."""
    if event_name == "pull_request":
        return False
    if go_cross_platform is True:
        return True
    if go_cross_platform == "true":
        return True
    return False


def docker_publish_enabled(event_name: str, publish_image: Any) -> bool:
    """GitHub Actions model for unit tests only (not used to validate committed if)."""
    if publish_image == "skip":
        return False
    if event_name != "push":
        return False
    return True


def docker_pr_verify_enabled(event_name: str, publish_image: Any) -> bool:
    if event_name != "pull_request":
        return False
    if publish_image == "skip":
        return False
    return True


def validate_workflow(
    ci: dict[str, Any],
    release: dict[str, Any],
    *,
    ci_text: str,
) -> None:
    jobs = ci.get("jobs")
    if not isinstance(jobs, dict):
        raise WorkflowValidationError("ci.yml: jobs missing")

    on = ci.get("on")
    if not isinstance(on, dict):
        raise WorkflowValidationError("ci.yml: on must be a mapping")

    for trigger in ("workflow_dispatch", "workflow_call"):
        block = on.get(trigger)
        if not isinstance(block, dict):
            raise WorkflowValidationError(f"ci.yml: missing on.{trigger}")
        inputs = block.get("inputs") or {}
        gcp = inputs.get("go_cross_platform")
        if not isinstance(gcp, dict):
            raise WorkflowValidationError(f"ci.yml: {trigger} missing go_cross_platform input")
        if gcp.get("type") != "boolean":
            raise WorkflowValidationError(f"ci.yml: {trigger}.go_cross_platform must be boolean")
        default = gcp.get("default")
        if default not in (False, "false"):
            raise WorkflowValidationError(
                f"ci.yml: {trigger}.go_cross_platform default must be false (got {default!r})"
            )

    dispatch_inputs = on["workflow_dispatch"].get("inputs") or {}
    pub = dispatch_inputs.get("publish_image")
    if not isinstance(pub, dict) or pub.get("default") != "skip":
        raise WorkflowValidationError(
            "ci.yml: workflow_dispatch.publish_image default must be 'skip'"
        )

    wcall = on["workflow_call"].get("inputs") or {}
    pub_call = wcall.get("publish_image")
    if not isinstance(pub_call, dict) or pub_call.get("default") != "publish":
        raise WorkflowValidationError(
            "ci.yml: workflow_call.publish_image default must be 'publish'"
        )

    linux = jobs.get("go-build-linux")
    if not isinstance(linux, dict):
        raise WorkflowValidationError("ci.yml: missing go-build-linux job")
    if "if" in linux:
        raise WorkflowValidationError("go-build-linux must not have job-level if:")

    for optional in ("go-build-macos", "go-build-windows"):
        job = jobs.get(optional)
        if not isinstance(job, dict):
            raise WorkflowValidationError(f"ci.yml: missing {optional} job")
        if "if" not in job:
            raise WorkflowValidationError(f"{optional} must have job-level if:")
        require_exact_job_if(job["if"], ALLOWED_CROSS_PLATFORM_IF, job=optional)

    if "go-build-all-platforms" in jobs:
        raise WorkflowValidationError("go-build-all-platforms must not exist")

    if re.search(
        r"os:\s*\[ubuntu-latest,\s*macos-latest,\s*windows-latest\]", ci_text
    ):
        raise WorkflowValidationError("forbidden three-OS matrix line present")

    publish = jobs.get("docker-publish")
    if not isinstance(publish, dict):
        raise WorkflowValidationError("ci.yml: missing docker-publish job")
    require_exact_job_if(
        publish.get("if"), ALLOWED_DOCKER_PUBLISH_IF, job="docker-publish"
    )

    needs = publish.get("needs")
    if not isinstance(needs, list):
        raise WorkflowValidationError("docker-publish needs must be a list")
    need_set = set(needs)
    if need_set != REQUIRED_PUBLISH_NEEDS:
        raise WorkflowValidationError(
            f"docker-publish needs must be {sorted(REQUIRED_PUBLISH_NEEDS)}, got {sorted(need_set)}"
        )
    if need_set & FORBIDDEN_PUBLISH_NEEDS:
        raise WorkflowValidationError("docker-publish must not need optional platform jobs")

    docker = jobs.get("docker")
    if isinstance(docker, dict):
        require_exact_job_if(
            docker.get("if"), ALLOWED_DOCKER_PR_IF, job="docker"
        )

    rel_jobs = release.get("jobs") or {}
    validate_job = rel_jobs.get("validate")
    if not isinstance(validate_job, dict):
        raise WorkflowValidationError("release.yml: missing validate job")
    if validate_job.get("uses") != "./.github/workflows/ci.yml":
        raise WorkflowValidationError("release.yml validate must use ci.yml")
    if validate_job.get("with"):
        raise WorkflowValidationError("release.yml validate must not pass with: (defaults)")

    def step_platforms(job_key: str, step_name: str) -> str:
        job = jobs[job_key]
        for step in job.get("steps") or []:
            if step.get("name") == step_name:
                with_ = step.get("with") or {}
                return str(with_.get("platforms", ""))
        return ""

    pr_plat = step_platforms("docker", "Build container image")
    if pr_plat != "linux/amd64,linux/arm64":
        raise WorkflowValidationError(
            f"PR docker platforms expected linux/amd64,linux/arm64 got {pr_plat!r}"
        )
    pub_plat = step_platforms("docker-publish", "Build and push container image")
    if pub_plat != "linux/arm64":
        raise WorkflowValidationError(f"publish platforms expected linux/arm64 got {pub_plat!r}")

    perms = ci.get("permissions") or {}
    if perms.get("contents") != "read" or perms.get("packages") != "write":
        raise WorkflowValidationError("permissions must be contents:read packages:write")


def validate_files(
    ci_path: Path = CI_PATH, release_path: Path = RELEASE_PATH
) -> None:
    ci_text = ci_path.read_text(encoding="utf-8")
    ci = load_workflow(ci_path)
    release = load_workflow(release_path)
    validate_workflow(ci, release, ci_text=ci_text)


def main() -> int:
    try:
        validate_files()
    except WorkflowValidationError as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    print("validate_ci_platform_matrix ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
