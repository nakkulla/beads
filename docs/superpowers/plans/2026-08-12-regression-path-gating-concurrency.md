# Regression Tests 경로 gating과 stale run 취소 구현 계획

## Context

- 실행 권한은 Bead `beads-gkw`, 승인된 spec `docs/superpowers/specs/2026-08-12-regression-path-gating-concurrency-design.md`, spec commit `94f1660209324ab373b719e57cad2a3958764024`에 있다. route는 `full_plan`, workflow mode는 `standard`, plan path는 `docs/superpowers/plans/2026-08-12-regression-path-gating-concurrency.md`로 고정되어 있다.
- 현재 `Regression Tests` workflow는 docs-only main push도 heavy differential suite를 실행하고, detector 오류와 unknown path에서 coverage를 보존하지 못하며, PR/main stale work를 안전하게 정리하지 못한다. 이번 구현은 proven-safe docs/metadata-only diff만 skip하고 모든 불확실성을 full run으로 돌리며, PR concurrency와 trusted push-main monotonic arbitration을 추가한다.
- 변경 범위는 `.github/workflows/regression.yml`, 새 `.github/scripts/ci-regression-tier.sh`, 새 `.github/scripts/ci-regression-supersede.sh`, regression behavioral/static contract tests, `docs/CI_REQUIRED_CHECK_TOPOLOGY.md`, `docs/CI_TEST_SURFACE_AUDIT.md`, 필요 시 historical wording만 바로잡는 `docs/CI_CLEANUP_PLAN.md`에 한정한다. Regression test body/baseline cache, required-check/merge-group 정책, parent aggregate, Go cache, Embedded Dolt sharding은 바꾸지 않는다.
- 실행은 `beads-am7` 구현이 target base에 landed된 뒤 시작한다. 실행 진입 시 `bd show beads-am7 --json`과 fetched target-base history로 이를 확인하며, 아직 landed되지 않았거나 canonical workflow contract와 문서 owner가 불명확하면 source-of-truth hard stop으로 종료한다. `beads-yvf`/`beads-ou6`가 먼저 landed되었으면 그 surface를 보존하고 Regression 소유 파일 밖 변경을 만들지 않는다.
- 첫 편집 전에 `AGENT_INSTRUCTIONS.md`, `docs/PROJECT_CHARTER.md`, `CONTRIBUTING.md`, `PR_MAINTAINER_GUIDELINES.md`와 `docs/agents/repo-ops.toml`을 다시 읽는다. `scripts/pr-preflight.sh --search "regression path gating concurrency stale run" --repo gastownhall/beads`를 실행하고 관련 외부 contributor PR이 있으면 재구현보다 기여 보존 경로를 우선하며 ownership 충돌은 hard stop으로 올린다.
- action time에 target base를 다시 해석한다. 현재 승인 기준은 `main`이지만 선언·configured upstream을 검증하고, `git fetch --no-tags <base-remote> main` 뒤 remote tip을 40-hex로 고정한다. writable delivery remote와 GitHub repo는 `git remote get-url --push origin`에서 별도로 고정하고 base verification remote로 대체하지 않는다.
- fetched base tip에서 `.worktrees/beads-gkw` worktree와 `beads-gkw` branch를 만들고 basename과 branch가 일치하는지 확인한 뒤 parent를 `in_progress`로 claim한다. 저장된 plan의 각 `## Phase N` section SHA-256 앞 12자와 `plan_task_anchor`를 사용해 정확히 한 phase child를 만들고 Phase 2→1, Phase 3→2, Phase 4→3 순서의 `blocks` dependency를 기록·readback한다.
- implementation selector는 `controller=codex`, `requested=inherit`, `resolved=codex`, `model=auto`, `effort=auto`를 유지한다. Phase 1은 bounded test unit이므로 fresh `gpt-5.6-luna`/`max`, Phase 2와 Phase 3은 fail-safe state·permission·scheduler semantics가 결합된 complex unit이므로 각 phase마다 fresh `gpt-5.6-terra`/`high` leaf에 맡긴다. Phase 4는 root가 전체 diff, review gate, Beads lifecycle과 publish safety를 소유한다.
- leaf는 해당 phase의 candidate diff와 verification만 만들고 Beads write, commit, push, PR 생성, nested dispatch를 하지 않는다. root는 phase마다 전체 `git status`, full diff와 focused verification을 직접 확인하고, owned paths만 stage해 한국어 commit과 `Agent-Signature` trailer를 만든 뒤 execution receipt를 child notes에 기록하고 `resolved` readback을 완료한다.
- `worker-ineligible`은 구현 PR 이후 최초의 자연스러운 docs/metadata-only main evidence까지 유지한다. 구현을 위한 빈 commit, 의미 없는 production change 또는 고의 실패 run은 만들지 않으며, managed deploy 자체를 docs-only Git event evidence로 간주하지 않는다.

## Phase 1: Regression detector·arbitration RED 계약 고정

1. parent가 제공한 YAML parser/helper를 재사용해 `scripts/ci_workflow_test.go`의 Regression static contract를 확장한다. 세 trigger, no workflow path filter/no `merge_group`, PR-only shared concurrency, push/manual run-ID 분리, detector/arbitration job outputs와 permissions, heavy command·timeout·baseline cache 불변, aggregate gate 비소유를 assertion으로 고정한다.
2. 새 `scripts/ci_regression_test.go`에 `t.TempDir()` isolated git repo와 event JSON fixture를 만든다. PR/main bounds, docs·`.beads`·scoped `_test.go` safe cases, risky source/regression/build cases, embedded Markdown/YAML/JSON/SQL target, unknown path, initial/zero/missing/empty diff, manual override와 label precedence를 표 기반으로 실행한다. global git config와 production Beads/event state는 사용하지 않는다.
3. 같은 test file에 candidate-output wrapper failure와 fake paginated GitHub run/jobs/cancel API를 추가한다. detector/supersession script nonzero·missing·duplicate·invalid output은 각각 full/proceed fallback이어야 하고, lower ancestral cancel, reversed completion self-abort, higher docs-only skipped, non-ancestor/missing object, API error와 409 race를 모두 고정한다. 실제 GitHub API나 token은 호출하지 않는다.
4. root는 현재 base에서 RED가 새 script 부재, inline detector, concurrency/arbitration 부재 때문에 발생하는지 확인한다. fixture bootstrap, YAML parse, fake API 또는 temp git 구성 오류는 유효한 RED로 인정하지 않고 먼저 고친다.

검증: `go test ./scripts -run 'Regression|CIWorkflow'`가 컴파일·fixture bootstrap은 통과한 채 승인된 seam에서만 예상대로 실패해야 한다. root가 assertion별 실패 원인을 확인한 뒤 RED commit과 Phase 1 execution receipt를 남긴다.

## Phase 2: fail-safe detector와 proven-safe classifier 구현

1. `.github/scripts/ci-regression-tier.sh`를 추가해 `EVENT_NAME`, PR/push bounds, event path와 candidate `GITHUB_OUTPUT`만 입력받게 한다. 모든 정상 분기는 `run_regression`과 single-line `reason`을 정확히 한 번 쓰고, invalid/missing/all-zero/same bounds, diff failure, empty diff, event/label parse failure와 unknown event/path는 exit 0의 full-run 결과로 만든다.
2. spec의 explicit docs/metadata safe regex와 scoped `_test.go` exemption을 구현한다. `tests/regression/**`, production Go/storage/types/build inputs와 current `go:embed` target은 full run으로 남기고, `workflow_dispatch`와 `run-regression`을 force-run 최우선으로 둔다. `skip-regression`은 safe diff reason만 보강하며 risky/unknown bypass가 되지 않고 두 label이 함께 있으면 `run-regression`이 이긴다.
3. `.github/workflows/regression.yml`의 inline detector를 repository script 호출로 바꾸고 PR/push exact SHA env를 전달한다. step은 `mktemp` candidate output을 검증한 뒤에만 실제 `GITHUB_OUTPUT`에 append하고, script nonzero 또는 누락·중복·invalid boolean·multiline reason이면 `run_regression=true` fallback을 쓴다. 실제 output write failure는 workflow failure로 남긴다.
4. Phase 1 detector matrix와 static detector assertions를 GREEN으로 만들되 supersession RED는 Phase 3까지 명시적으로 남긴다. raw path logging과 fallback reason이 secret 없이 진단 가능하고 temp candidate file이 trap으로 정리되는지 root가 확인한다.

검증: `bash -n .github/scripts/ci-regression-tier.sh`와 detector에 한정한 `go test ./scripts -run 'Regression.*(Tier|Detector|Workflow)'`가 성공해야 하며, supersession tests의 남은 RED가 예상된 구현 공백과 정확히 대응해야 한다.

## Phase 3: monotonic main supersession·workflow·문서 완성

1. `.github/scripts/ci-regression-supersede.sh`를 추가한다. current run ID/SHA를 검증하고 `regression.yml`·`main`·`push` active pages만 조회하며, lower run ID의 ancestral active run만 cancel한다. higher risk-bearing arbitration job이 확인되고 current SHA가 ancestor면 current를 `proceed=false`로 self-abort한다. unknown ancestry, invalid input, list/jobs/cancel/output failure는 취소하지 않고 `proceed=true`로 heavy coverage를 유지한다.
2. API transport를 fixture에서 교체 가능한 command/function seam으로 두고 candidate output을 검증한다. cancel 202와 completion race 409를 구분하고, cancellation requested IDs와 single-line reason을 기록하되 repository/ref 범위를 외부 입력으로 넓히지 않는다.
3. Regression workflow에 PR-only shared concurrency와 push/manual run-ID group을 넣고, risky trusted push에만 `supersede-stale-main` job을 연결한다. workflow default는 `contents: read`, 해당 job만 `actions: write`/`contents: read`를 가지며 checkout은 `persist-credentials:false`, `fetch-depth:0`이다. heavy job은 `always()`, detector success+true와 arbitration `proceed != false`를 함께 검사해 PR/manual skip dependency와 arbitration degradation이 coverage를 막지 않게 한다.
4. `docs/CI_REQUIRED_CHECK_TOPOLOGY.md`, `docs/CI_TEST_SURFACE_AUDIT.md`와 필요한 historical cleanup wording을 실제 정책에 맞춘다. separate/non-required/no merge-group 경계, PR/main classifier, manual force-run, error/unknown full-run과 stale arbitration을 설명하고 path regex를 중복 source-of-truth로 복사하지 않는다.

검증: `bash -n .github/scripts/ci-regression-tier.sh .github/scripts/ci-regression-supersede.sh`, `go test ./scripts -run 'Regression|CIWorkflow'`, `go test ./scripts`가 성공해야 한다. 설치되어 있으면 `shellcheck` 두 script와 `actionlint .github/workflows/regression.yml`도 성공해야 하며, 미설치 도구는 명시적 next-best evidence로 기록한다.

## Phase 4: 통합 검증, implementation review, PR Delivery

1. root가 fetched target base 대비 전체 commit range, `git status`, owned-path inventory와 sibling surface 불변을 검토한다. `git diff --check`, 두 script의 `bash -n`, targeted/full `go test ./scripts`, 설치된 경우 `shellcheck`/`actionlint`를 exact branch HEAD에서 다시 실행한다. differential regression test body를 바꾸지 않았으므로 로컬 heavy regression suite는 필수 gate가 아니며 실제 risky PR job이 runtime coverage를 제공한다.
2. required local verification이 green인 pinned HEAD에 대해 standard implementation review gate를 연다. reviewer packet은 승인 spec, 저장 plan, base..HEAD 전체 diff, detector/arbitration matrices와 command evidence를 포함한다. `REVISE`면 모든 finding을 한 batch로 처리하고 root가 exact delta 또는 broad semantic change면 full diff를 follow-up self-review한 뒤 관련 검증과 `impl_review` receipt를 새 HEAD에 맞춘다.
3. commit/push 직전에 worktree basename과 branch가 `beads-gkw`인지, target-base ancestry와 origin writable repo가 변하지 않았는지 다시 확인한다. `scripts/pr-preflight.sh --search`를 재실행하고 verified commits만 `git push origin beads-gkw`로 게시한다. known CI failure, head drift, source-of-truth conflict 또는 stale review는 push 전에 hard stop한다.
4. GitHub body file에 proven-safe skip, fail-safe fallback, `actions:write` job boundary, separate/non-required 정책, tests와 runtime evidence pending을 기록하고 `scripts/gh-body-lint`를 통과시킨다. `gh pr create --repo <origin-owner/repo> --base <resolved-target-base> --head beads-gkw` 뒤 PR 번호로 preflight를 readback한다. Phase 4 child를 resolved로 만들고 parent에 `pr_url`, completion report와 `resolved`를 기록하되 `worker-ineligible`은 유지한 채 PR URL, head OID, CI status와 다음 merge action을 보고하고 멈춘다.

검증: pushed branch HEAD와 PR head OID가 같고 PR base가 action-time target base와 같아야 한다. parent와 네 phase child의 lifecycle, current `impl_review`, `pr_url`, completion report와 유지된 `worker-ineligible` label을 `bd show --json`으로 readback해야 한다.

## PR Delivery 이후 merge·runtime closure

- 후속 사용자 요청은 `pr-finish --review`로 시작한다. 같은 pinned head의 PR aggregate와 risky `Regression Tests`가 green이고 feedback/AI-review/head/base invariants가 닫힌 뒤에만 merge한다.
- merge가 만든 risky main run에서 detector reason, arbitration outcome, heavy regression success와 URL/SHA를 기록한다. post-merge sync와 `docs/agents/repo-ops.toml`의 managed deploy/readback을 수행하되 이 deploy는 docs-only event evidence를 대신하지 않는다.
- 이후 최초로 자연 발생한 docs/metadata-only main push에서 detector success, safe reason과 heavy job skip을 기록한다. 의미 없는 commit이나 고의 production change로 표본을 만들지 않는다. 자연스러운 같은-PR synchronize 또는 연속 risky main push가 있으면 cancellation을 opportunistic evidence로 추가하되 closure hard gate로 만들지 않는다.
- risky implementation PR, risky main, docs-only main의 세 required run URL/SHA/reason/result가 모두 readback된 뒤 `worker-ineligible`을 제거한다. target-base containment과 deploy verification을 다시 확인하고 phase children을 leaves-first로 close한 다음 parent를 마지막에 close한다. evidence가 아직 없으면 Bead와 label을 유지하고 runtime 성공을 주장하지 않는다.

## Test scope

- Phase 1 RED: external detector/supersession scripts, proven-safe push classifier, candidate fallback와 monotonic arbitration이 없는 현재 구현에서 승인된 assertions만 실패해야 한다. fixture bootstrap, YAML parser, fake API와 temp git 오류는 RED acceptance가 아니다.
- Phase 2 GREEN: PR/main detector matrix, label precedence, event/diff/unknown fail-safe와 candidate-output full fallback이 green이 된다. supersession and final topology assertions는 Phase 3까지 의도적으로 RED다.
- Phase 3 GREEN: reversed completion, lower ancestral cancellation, docs-only/non-ancestor/API degradation preservation, permissions/concurrency/heavy-job topology와 전체 `go test ./scripts`가 green이 된다.
- Phase 4 integration: exact branch에서 shell syntax, Go behavioral/static contracts, docs/workflow diff hygiene와 implementation review를 확인한다. 로컬에서 실제 GitHub run을 취소하거나 production regression을 고의 실패시키지 않는다.
- runtime acceptance: risky PR은 기존 differential command가 성공해야 하고, merge 뒤 risky main과 최초 자연 docs-only main evidence가 각각 full-run과 safe-skip contract를 증명한다. PR/main cancellation sequence는 자연 발생할 때만 보강 evidence로 수집한다.
- 제외: `tests/regression` body/baseline cache, required-check/merge-group/branch protection, parent aggregate, Go cache, Embedded Dolt sharding, release/migration/cross-version workflow는 이 계획의 RED-GREEN seam이 아니다.
