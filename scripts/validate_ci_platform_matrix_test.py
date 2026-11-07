#!/usr/bin/env python3
"""Independent oracle + mutation replay for ci platform / publish contracts."""

from __future__ import annotations

import copy
import tempfile
import unittest
from pathlib import Path

from validate_ci_platform_matrix import (
    CI_PATH,
    RELEASE_PATH,
    WorkflowValidationError,
    cross_platform_job_enabled,
    docker_publish_enabled,
    eval_ci_if,
    load_workflow,
    validate_docker_publish_if,
    validate_files,
    validate_workflow,
)


class ExpressionOracleTests(unittest.TestCase):
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


class DockerPublishIfRegressionTests(unittest.TestCase):
    def test_committed_expression_scenarios(self) -> None:
        pub_if = (
            "inputs.publish_image != 'skip' && github.event_name == 'push'"
        )
        validate_docker_publish_if(pub_if)

    def test_tester_dispatch_or_arm_rejected(self) -> None:
        bad = (
            "inputs.publish_image != 'skip' && github.event_name == 'push' "
            "|| github.event_name == 'workflow_dispatch'"
        )
        with self.assertRaises(WorkflowValidationError):
            validate_docker_publish_if(bad)

    def test_dispatch_or_enables_manual_even_with_skip(self) -> None:
        bad = (
            "inputs.publish_image != 'skip' && github.event_name == 'push' "
            "|| github.event_name == 'workflow_dispatch'"
        )
        self.assertTrue(
            eval_ci_if(
                bad,
                github_event_name="workflow_dispatch",
                inputs={"publish_image": "skip"},
            )
        )


class SuppliedCiTextRegressionTests(unittest.TestCase):
    def test_forbidden_matrix_regex_uses_supplied_file_not_repo_default(self) -> None:
        """Regression: forbidden matrix pattern in ci_text must fail when validating supplied copy."""
        release = load_workflow(RELEASE_PATH)
        clean_ci = load_workflow(CI_PATH)
        clean_text = CI_PATH.read_text(encoding="utf-8")
        poisoned_text = (
            clean_text
            + "\n# os: [ubuntu-latest, macos-latest, windows-latest]\n"
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
            base = d["jobs"]["docker-publish"]["if"]
            d["jobs"]["docker-publish"]["if"] = (
                f"{base} || github.event_name == 'workflow_dispatch'"
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


if __name__ == "__main__":
    unittest.main()
