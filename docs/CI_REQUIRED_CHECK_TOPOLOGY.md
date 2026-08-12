# Required Check Topology

This document describes the required-check contract implemented by the workflow
YAML. The YAML and `scripts/ci_workflow_test.go` are authoritative; this is a
maintainer-facing map of ownership and merge-queue behavior.

## Always-triggered workflows

`.github/workflows/pr.yml` (`PR`) runs for pull requests targeting `main` and
for `merge_group`. It has no workflow-level `paths` or `paths-ignore` filters.
`.github/workflows/pr-risk.yml` follows the same trigger rule. Decisions about
expensive checks are made by detector jobs and job-level `if` conditions, so a
required aggregate is still created for every PR and merge-group commit.

`.github/workflows/main.yml` (`Main`) runs on pushes to `main`. Its post-merge,
integration, embedded, Windows, and Nix jobs remain separate surfaces.

## Single-owner verification

Each workflow's `build-artifacts` job is an artifact producer only: checkout,
Go setup, the canonical Linux binary build, executable-bit verification,
`SHA256SUMS`, the build manifest, and `ci-build-artifacts` upload. Artifact
consumers keep their `needs` edges and checksum verification.

`pr-policy-wrapper` is the only owner of policy checks (build tags, install
guidance, version consistency, docs checks, `testing.Short`, and the
`.beads/issues.jsonl` guard). In `PR`, both `BD_DOCS_DIFF_BASE` and
`CI_BEADS_DIFF_BASE` are
`${{ github.event.pull_request.base.sha || github.event.merge_group.base_sha }}`.
In `Main`, both are `${{ github.event.before }}`. The PR wrapper passes
`DOC_DRIFT_PATCH_OUT=${{ runner.temp }}/cli-docs-freshness.patch` and uploads
`cli-docs-freshness-patch` on failure.

`pr-lint-wrapper` is the only owner of `make ci-pr-lint`; it installs the pinned
`golangci-lint` `v2.9.0`. Standalone build-tag, version, docs, formatting, and
lint jobs are intentionally absent. Migration hygiene remains one job and owns
duplicate-version, nondeterministic-SQL, and frozen-migration checks; no
separate duplicate-migration authority exists.

## PR baseline aggregate

The baseline aggregate remains named `CI Gate / Required` and uses
`if: ${{ always() }}`. Its `needs`, `CI_GATE_REQUIRED` tokens, and
`${{ needs.<owner>.result }}` environment entries are the same ordered set:

1. `build-artifacts`
2. `check-cmd-bd-puregeo-tests`
3. `check-migration-hygiene`
4. `detect-package-gates`
5. `package-mcp`
6. `package-npm`
7. `package-website`
8. `pr-policy-wrapper`
9. `pr-core-wrapper`
10. `pr-lint-wrapper`
11. `test-domain-uow`

The mapping is intentionally one-to-one: deleted job IDs cannot remain in the
aggregate, and no owner can be omitted. The baseline gate has no
`CI_GATE_SKIPPED_OK` exception; package applicability is handled inside the
package jobs and those jobs remain aggregate owners.

`PR Risk / CI Gate / Required` is a separate aggregate for embedded/server
Dolt and Nix risk checks. Risk-tier skipped allowances belong only to
`pr-risk.yml`; they are not part of the baseline PR gate.

`test-embedded-storage` remains the sole risk-gate owner for its 5-way matrix.
Its legs are displayed as `Test (Embedded Dolt Storage N/5)`; branch protection
continues to require only the aggregate rather than individual legs.

Branch protection should require aggregate checks rather than individual
matrix or detector jobs, and required checks must be reported for
`merge_group`.

## Verification authority

`go test -count=1 ./scripts` validates the workflow ownership, exact 11-owner
mapping, base expressions, migration `BASE_SHA`, and diagnostics contract.
YAML changes must preserve the unfiltered triggers, artifact-consumer edges,
package applicability behavior, and the unique PR/Main test surfaces described
above.
