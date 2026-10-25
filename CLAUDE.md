# Stockbot

See [AGENTS.md](AGENTS.md) for agent guidelines, testing conventions, and local development setup.

## Model routing (pstack & Cloud Agents)

`grok-4.7` at `medium` reasoning (`grok-4.7-medium` in `.cursor/rules/pstack-models.mdc`; `grok-4.7-high` for hardest tasks) owns planning, investigation, architecture, synthesis, and judgment roles: explorers, explainers, investigators, synthesizers, reflect tooling, reflect judgment/divergent/synthesizer, arena cross-judge pool, architect runners, and interrogate reviewers.

`composer-2.5` with no `reasoning_effort` parameter owns all code-building roles: `feature, refactoring`, `bug-fix`, `perf-issue`, `hillclimb`, `swarm workers`, and Cloud Agent build/fix runs. Arena runners and architect/interrogate panels may pair Composer with Grok where the pstack rule lists both.

**Never use fast mode** (`-fast` model slugs or `fast: "true"`) at any time, ever — including urgent or verification work.

Per-role overrides live in `.cursor/rules/pstack-models.mdc`; delete a line there to fall back to the skill default for that role.
