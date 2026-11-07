#!/usr/bin/env python3
"""Regression tests on parsed ci.yml / release.yml (committed workflow contracts)."""

from __future__ import annotations

import copy
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from validate_ci_platform_matrix import (
    ALLOWED_CROSS_PLATFORM_IF,
    ALLOWED_DOCKER_PUBLISH_IF,
    CI_PATH,
    RELEASE_PATH,
    WorkflowValidationError,
    _folded_if,
    cross_platform_job_enabled,
    docker_publish_enabled,
    load_workflow,
    require_exact_job_if,
    validate_files,
    validate_workflow,
)


class ExpressionOracleTests(unittest.TestCase):
    """GitHub behavior reference — not used to validate committed if strings."""

    def test_cross_platform_pull_request_never_runs(self) -> None:
        for val in (True, False, "true", "false", "", None):
            self.assertFalse(cross_platform_job_enabled("pull_request", val))

    def test_cross_platform_string_and_boolean_opt_in(self) -> None:
        self.assertTrue(cross_platform_job_enabled("workflow_dispatch", True))
        self.assertTrue(cross_platform_job_enabled("workflow_dispatch", "true"))
        self.assertFalse(cross_platform_job_enabled("workflow_dispatch", "TRUE"))
        self.assertFalse(cross_platform_job_enabled("workflow_dispatch", False))
        self.assertFalse(cross_platform_job_enabled("workflow_dispatch", ""))
        self.assertFalse(cross_platform_job_enabled("push", False))
        self.assertTrue(cross_platform_job_enabled("push", True))

    def test_publish_never_on_dispatch_or_pr(self) -> None:
        for pub in (None, "", "publish", "skip"):
            val = "" if pub is None else pub
            self.assertFalse(docker_publish_enabled("workflow_dispatch", val))
        self.assertFalse(docker_publish_enabled("pull_request", ""))
        self.assertFalse(docker_publish_enabled("pull_request", "publish"))

    def test_publish_on_release_push_only(self) -> None:
        self.assertTrue(docker_publish_enabled("push", "publish"))
        self.assertFalse(docker_publish_enabled("push", "skip"))


class PinnedContractTests(unittest.TestCase):
    def test_committed_docker_publish_if_exact(self) -> None:
        ci = load_workflow(CI_PATH)
        require_exact_job_if(
            ci["jobs"]["docker-publish"]["if"],
            ALLOWED_DOCKER_PUBLISH_IF,
            job="docker-publish",
        )

    def test_committed_cross_platform_if_exact(self) -> None:
        ci = load_workflow(CI_PATH)
        for job in ("go-build-macos", "go-build-windows"):
            require_exact_job_if(
                ci["jobs"][job]["if"], ALLOWED_CROSS_PLATFORM_IF, job=job
            )


class SuppliedCiTextRegressionTests(unittest.TestCase):
    def test_forbidden_matrix_regex_uses_supplied_file_not_repo_default(self) -> None:
        release = load_workflow(RELEASE_PATH)
        clean_ci = load_workflow(CI_PATH)
        clean_text = CI_PATH.read_text(encoding="utf-8")
        poisoned_text = (
            clean_text + "\n# os: [ubuntu-latest, macos-latest, windows-latest]\n"
        )
        with self.assertRaises(WorkflowValidationError) as ctx:
            validate_workflow(clean_ci, release, ci_text=poisoned_text)
        self.assertIn("forbidden three-OS matrix", str(ctx.exception))


class LiveWorkflowTests(unittest.TestCase):
    def test_committed_files_pass(self) -> None:
        validate_files()

    def test_main_exit_ok(self) -> None:
        import validate_ci_platform_matrix as m

        self.assertEqual(m.main(), 0)


class MutationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.ci_text = CI_PATH.read_text(encoding="utf-8")
        self.release_text = RELEASE_PATH.read_text(encoding="utf-8")
        self.ci_doc = load_workflow(CI_PATH)
        self.release_doc = load_workflow(RELEASE_PATH)

    def _assert_mutation_fails(self, mutate_ci) -> None:
        doc = copy.deepcopy(self.ci_doc)
        mutate_ci(doc)
        with tempfile.TemporaryDirectory() as tmp:
            ci_p = Path(tmp) / "ci.yml"
            rel_p = Path(tmp) / "release.yml"
            ci_p.write_text(
                __import__("yaml").dump(doc, sort_keys=False), encoding="utf-8"
            )
            rel_p.write_text(self.release_text, encoding="utf-8")
            with self.assertRaises(WorkflowValidationError):
                validate_files(ci_p, rel_p)

    def _publish_base(self, d: dict) -> str:
        return _folded_if(d["jobs"]["docker-publish"]["if"])

    def test_survivor_remove_string_true_arm(self) -> None:
        def mutate(d):
            expr = (
                "github.event_name != 'pull_request' && "
                "(inputs.go_cross_platform == true)"
            )
            d["jobs"]["go-build-macos"]["if"] = expr
            d["jobs"]["go-build-windows"]["if"] = expr

        self._assert_mutation_fails(mutate)

    def test_survivor_defaults_false_to_true(self) -> None:
        def mutate(d):
            d["on"]["workflow_dispatch"]["inputs"]["go_cross_platform"]["default"] = True
            d["on"]["workflow_call"]["inputs"]["go_cross_platform"]["default"] = True

        self._assert_mutation_fails(mutate)

    def test_survivor_remove_windows_if_only(self) -> None:
        def mutate(d):
            del d["jobs"]["go-build-windows"]["if"]

        self._assert_mutation_fails(mutate)

    def test_survivor_remove_macos_if_only(self) -> None:
        def mutate(d):
            del d["jobs"]["go-build-macos"]["if"]

        self._assert_mutation_fails(mutate)

    def test_survivor_gates_moved_to_comment_yaml(self) -> None:
        def mutate(d):
            del d["jobs"]["go-build-macos"]["if"]
            del d["jobs"]["go-build-windows"]["if"]

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_allows_pull_request(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                "inputs.publish_image != 'skip' && github.event_name != 'pull_request'"
            )

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_dispatch_reachable(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                "inputs.publish_image != 'skip' && github.event_name != 'pull_request'"
            )
            del d["on"]["workflow_dispatch"]["inputs"]["publish_image"]

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_dispatch_or_broadening(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._publish_base(d)} || github.event_name == 'workflow_dispatch'"
            )

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_or_go_cross_platform(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._publish_base(d)} || inputs.go_cross_platform == true"
            )

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_or_force_publish(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._publish_base(d)} || inputs.publish_image == 'force'"
            )

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_if_uppercase_workflow_dispatch(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._publish_base(d)} || github.event_name == 'WORKFLOW_DISPATCH'"
            )

        self._assert_mutation_fails(mutate)

    def test_survivor_drop_go_from_publish_needs(self) -> None:
        def mutate(d):
            needs = list(d["jobs"]["docker-publish"]["needs"])
            needs.remove("go")
            d["jobs"]["docker-publish"]["needs"] = needs

        self._assert_mutation_fails(mutate)

    def test_survivor_publish_needs_includes_macos(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["needs"] = list(
                d["jobs"]["docker-publish"]["needs"]
            ) + ["go-build-macos"]

        self._assert_mutation_fails(mutate)

    def test_survivor_linux_job_if_false(self) -> None:
        def mutate(d):
            d["jobs"]["go-build-linux"]["if"] = "false"

        self._assert_mutation_fails(mutate)

    def test_survivor_forbidden_matrix_job(self) -> None:
        def mutate(d):
            d["jobs"]["go-build-all-platforms"] = {
                "runs-on": "ubuntu-latest",
                "steps": [{"run": "true"}],
            }

        self._assert_mutation_fails(mutate)

    def test_caught_identity_passes(self) -> None:
        validate_workflow(self.ci_doc, self.release_doc, ci_text=self.ci_text)


class TesterMutantCliReplayTests(unittest.TestCase):
    """Validator main() exit on Tester mutants via validate_files on temp ci.yml copies."""

    def _mutant_exit_code(self, mutate_ci) -> int:
        ci_doc = load_workflow(CI_PATH)
        mutate_ci(ci_doc)
        release_text = RELEASE_PATH.read_text(encoding="utf-8")
        script = Path(__file__).resolve().parent / "validate_ci_platform_matrix.py"
        with tempfile.TemporaryDirectory() as tmp:
            ci_p = Path(tmp) / "ci.yml"
            rel_p = Path(tmp) / "release.yml"
            ci_p.write_text(
                __import__("yaml").dump(ci_doc, sort_keys=False), encoding="utf-8"
            )
            rel_p.write_text(release_text, encoding="utf-8")
            proc = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    "import sys; from pathlib import Path; "
                    "sys.path.insert(0, %r); "
                    "from validate_ci_platform_matrix import validate_files, main; "
                    "validate_files(Path(%r), Path(%r)); "
                    "raise SystemExit(main())"
                    % (str(script.parent), str(ci_p), str(rel_p)),
                ],
                capture_output=True,
                text=True,
                cwd=str(script.parent),
            )
            if proc.returncode != 0:
                return proc.returncode
            try:
                validate_files(ci_p, rel_p)
            except WorkflowValidationError:
                return 1
            return 0

    def _base_publish(self, d: dict) -> str:
        return _folded_if(d["jobs"]["docker-publish"]["if"])

    def test_cli_reject_or_go_cross_platform(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._base_publish(d)} || inputs.go_cross_platform == true"
            )

        self.assertEqual(self._mutant_exit_code(mutate), 1)

    def test_cli_reject_or_force(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._base_publish(d)} || inputs.publish_image == 'force'"
            )

        self.assertEqual(self._mutant_exit_code(mutate), 1)

    def test_cli_reject_uppercase_dispatch(self) -> None:
        def mutate(d):
            d["jobs"]["docker-publish"]["if"] = (
                f"{self._base_publish(d)} || github.event_name == 'WORKFLOW_DISPATCH'"
            )

        self.assertEqual(self._mutant_exit_code(mutate), 1)


if __name__ == "__main__":
    unittest.main()
