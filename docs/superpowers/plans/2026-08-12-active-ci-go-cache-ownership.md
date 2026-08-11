# 활성 CI Go cache 소유권 구현 계획

## Context

- 실행 권한은 Bead `beads-ou6`, 승인된 spec `docs/superpowers/specs/2026-08-12-active-ci-go-cache-ownership-design.md`, spec commit `402678d85bb11cf0967fb8d350bfb051374a6243`에 있다. route는 `full_plan`, workflow mode는 `standard`, plan path는 `docs/superpowers/plans/2026-08-12-active-ci-go-cache-ownership.md`로 고정되어 있다.
- 현재 active PR/PR Risk/Nightly workflow의 `setup-go` cache ownership은 암묵적이고, trusted writer와 restore-only consumer, exact key/profile 경계가 repository contract로 고정되어 있지 않다. 이번 구현은 trusted main-ref `Go Cache` workflow를 유일 writer로 만들고 active untrusted workflows는 explicit restore-only consumer로 전환한다.
- 변경 범위는 새 `.github/workflows/go-cache.yml`, `.github/workflows/pr.yml`, `.github/workflows/pr-risk.yml`, `.github/workflows/nightly.yml`, `.github/workflows/ci-measurements.yml`, cache contract tests, 관련 CI 문서와 upstream attribution에 한정한다. 비활성 `main.yml`, Regression baseline cache, release/migration/cross-version/Nix/package cache, test command 의미, aggregate owner와 storage sharding은 바꾸지 않는다.
- 실행은 `beads-am7` 구현이 target base에 landed된 뒤 시작한다. 실행 진입 시 `bd show beads-am7 --json`과 fetched target-base history로 확인하고, 아직 landed되지 않았거나 parent가 소유한 artifact/wrapper topology가 불명확하면 source-of-truth hard stop으로 종료한다. `beads-yvf`가 먼저 landed되었으면 `test-embedded-storage` 5-way matrix와 gate mapping을 그대로 보존하고 cache step은 `build-embedded`/명시된 consumer에만 추가한다. `beads-gkw`의 Regression workflow와 baseline cache는 건드리지 않는다.
- 첫 편집 전에 `AGENT_INSTRUCTIONS.md`, `docs/PROJECT_CHARTER.md`, `CONTRIBUTING.md`, `PR_MAINTAINER_GUIDELINES.md`, `docs/agents/repo-ops.toml`을 다시 읽는다. `scripts/pr-preflight.sh --search "Go cache setup-go actions/cache 5278" --repo gastownhall/beads`와 upstream PR #5278 readback을 실행한다. 재사용한 key schema/test/code의 contributor와 commit metadata를 보존하고, material adaptation의 `Co-authored-by` 또는 PR body design attribution을 실제 upstream metadata에서 가져오며 추측해 만들지 않는다.
- action time에 target base를 다시 해석한다. 현재 승인 기준은 `main`이지만 선언·configured upstream을 검증하고, `git fetch --no-tags <base-remote> main` 뒤 remote tip을 40-hex로 고정한다. writable delivery remote와 GitHub repo는 `git remote get-url --push origin`에서 별도로 고정하고 base verification remote로 대체하지 않는다.
- fetched base tip에서 `.worktrees/beads-ou6` worktree와 `beads-ou6` branch를 만들고 basename과 branch가 일치하는지 확인한 뒤 parent를 `in_progress`로 claim한다. 저장된 plan의 각 `## Phase N` section SHA-256 앞 12자와 `plan_task_anchor`를 사용해 정확히 한 phase child를 만들고 Phase 2→1, Phase 3→2, Phase 4→3 순서의 `blocks` dependency를 기록·readback한다.
- implementation selector는 `controller=codex`, `requested=inherit`, `resolved=codex`, `model=auto`, `effort=auto`를 유지한다. Phase 1은 bounded static contract unit이므로 fresh `gpt-5.6-luna`/`max`, Phase 2와 Phase 3은 trust boundary·workflow state와 다중 consumer wiring이 결합된 complex unit이므로 각 phase마다 fresh `gpt-5.6-terra`/`high` leaf에 맡긴다. Phase 4는 root가 전체 diff, review gate, attribution, Beads lifecycle과 publish safety를 소유한다.
- leaf는 해당 phase의 candidate diff와 verification만 만들고 Beads write, commit, push, PR 생성, nested dispatch를 하지 않는다. root는 phase마다 전체 `git status`, full diff와 focused verification을 직접 확인하고, owned paths만 stage해 한국어 commit과 `Agent-Signature` trailer를 만든 뒤 execution receipt를 child notes에 기록하고 `resolved` readback을 완료한다.
- `worker-ineligible`은 merge 뒤 cold writer와 같은 generation의 warm risky PR 최소 3회 evidence가 모두 모일 때까지 유지한다. 표본을 만들기 위한 의미 없는 commit/PR, manual feature-ref save 또는 untrusted save는 금지한다.

## Phase 1: Go cache ownership RED 계약 고정

1. parent의 `scripts/ci_workflow_test.go` YAML parser/helper를 확장해 `go-cache.yml` trusted trigger, proven-safe `paths-ignore`, permissions/concurrency, full-SHA cache actions, module/non-race/race exact key·restore prefix·path, save cardinality와 main-ref/miss/success condition을 assertion으로 고정한다.
2. PR/PR Risk/Nightly consumer inventory를 parsed job/step structure로 고정한다. 모든 in-scope `setup-go`의 `cache:false`, restore-before-first-Go-command ordering, matching `GOCACHE`, PR/PR Risk save 0, measurement workflow의 explicit uncached 상태를 검증한다. step name 문자열 검색만으로 통과하지 않게 `uses`, `with`, `if`, env, event/job 구조를 함께 검사한다.
3. repository의 current `//go:embed` patterns를 tracked targets로 전개해 writer `paths-ignore`와 교집합이 없음을 검사한다. broad recursive Markdown ignore를 금지하고 `beads-gkw` classifier가 target base에 있으면 docs/metadata safe inventory byte-level 의미가 같되 Regression 전용 scoped `_test.go` exemption은 writer ignore에 들어가지 않음을 고정한다.
4. parent aggregate owner/job IDs, `beads-yvf` storage matrix/conformance/server/cmd surface, existing Regression binary cache와 inactive `main.yml`이 변경되지 않는 negative contract를 추가한다. root는 현재 base에서 RED가 active writer·explicit cache boundary 부재 때문에 발생하는지 확인하고 parser/fixture 오류는 먼저 고친다.

검증: `go test ./scripts -run 'GoCache|CIWorkflow'`가 컴파일과 fixture discovery는 성공한 채 unique writer, explicit cache:false, exact keys와 consumer wiring assertions에서만 예상대로 실패해야 한다. root가 assertion별 원인을 확인한 뒤 RED commit과 Phase 1 execution receipt를 남긴다.

## Phase 2: trusted writer workflow와 immutable cache family 구현

1. `.github/workflows/go-cache.yml`을 추가한다. `push` to `main`은 spec의 proven-safe docs/metadata `paths-ignore`만 사용하고 `workflow_dispatch`를 유지한다. workflow permissions는 `contents: read`, concurrency는 main ref별 cancel-in-progress이며 feature-ref manual run은 restore/warm까지만 허용하고 save condition에서 제외한다.
2. pinned `setup-go`에 `cache:false`와 `id: setup-go`를 지정하고, pinned `actions/cache/restore`로 module/non-race/race v2 family를 복원한다. module key는 `go.mod`/`go.sum` hash, build key는 source SHA와 `base-gms_pure_go`·race/non-race profile을 포함하며 spec의 segment order, path와 trailing-dash restore prefix를 byte-for-byte 유지한다. 구현 시 repository allowed action major와 upstream security state를 확인해 full commit SHA를 사용한다.
3. `source ./.buildflags`, `go mod download`, non-race `cmd/bd` build와 `BEADS_TEST_SKIP=dolt` race all-package dry compile을 explicit profile `GOCACHE`로 실행한다. timing/summary에는 restore match, download/build/compile duration, cache sizes와 save attempted/skipped를 기록하고 raw environment나 token을 출력하지 않는다. race warm command가 external service나 runnable test body를 시작하지 않았음을 log와 첫 runtime run에서 검증한다.
4. warm commands와 required summary가 성공하고 main ref이며 exact restore miss일 때만 module/non-race/race save를 각각 한 번 실행한다. partial/failed warm, cache hit와 feature-ref manual run은 save하지 않는다. Phase 1 writer contract를 GREEN으로 만들고 save action이 writer job 밖에 없는지 root가 full workflow diff로 확인한다.

검증: `go test ./scripts -run 'GoCache.*Writer|CIWorkflow'`와 설치되어 있으면 `actionlint .github/workflows/go-cache.yml`이 성공해야 한다. root는 exact key/prefix/path, action SHA, permission, concurrency와 세 save condition을 직접 대조한 뒤 Phase 2 commit과 execution receipt를 남긴다.

## Phase 3: restore-only consumers·measurement 경계·문서 완성

1. `.github/workflows/pr.yml`의 `build-artifacts`에는 module/non-race, `pr-core-wrapper`에는 module/race restore를 추가하고 해당 `setup-go`에 `cache:false`와 matching `GOCACHE`를 명시한다. untrusted `pull_request`/`merge_group` jobs에는 save action을 추가하지 않고 parent의 artifact/wrapper owner와 aggregate mapping을 유지한다.
2. `.github/workflows/pr-risk.yml`의 `build-embedded`에 module/non-race/race restore와 matching profiles를 연결한다. storage/conformance/server/cmd jobs, tier detector, gate needs/result mapping과 landed된 yvf matrix를 수정하지 않는다. `.github/workflows/nightly.yml`의 `full-test`와 `embedded-storage`에는 spec inventory대로 restore/GOCACHE를 연결하고 나머지 in-scope setup-go에도 explicit `cache:false`를 둔다.
3. `.github/workflows/ci-measurements.yml`의 모든 setup-go를 `cache:false`로 만들고 `beads-go-*` restore/save가 없음을 유지한다. restore/timing probe는 key/action failure를 job failure로 남기되 size observability가 unavailable이면 test/build result를 덮지 않게 한다. measured evidence 없이 consumer inventory를 확장하지 않는다.
4. cache topology 문서와 PR body source notes를 갱신한다. trusted single writer, restore-only trust boundary, exact v2 families, inactive Main/Regression/yvf non-goals, cold/warm evidence contract와 upstream PR #5278 attribution을 기록하되 keys를 여러 문서의 독립 source-of-truth로 복제하지 않는다. Phase 1 전체 contract를 GREEN으로 만든다.

검증: `go test ./scripts -run 'GoCache|CIWorkflow'`, `go test ./scripts`와 설치되어 있으면 `actionlint`로 `go-cache.yml`, `pr.yml`, `pr-risk.yml`, `nightly.yml`, `ci-measurements.yml`을 검사한다. static parse 성공만으로 runtime cache hit를 주장하지 않으며 actionlint 미설치는 next-best evidence로 기록한다.

## Phase 4: command 검증, implementation review, PR Delivery

1. root가 fetched target base 대비 전체 commit range, `git status`, owned-path inventory와 sibling topology 불변을 검토한다. `git diff --check`, targeted/full `go test ./scripts`, available `actionlint`를 exact branch HEAD에서 다시 실행한다. 명시적으로 만든 temp `GOCACHE`와 output path를 사용해 `.buildflags` 기반 `go mod download`와 non-race `cmd/bd` warm build를 실행하고, race all-package dry compile은 로컬 budget이 허용하면 실행하며 그렇지 않으면 actual writer run을 required evidence로 남긴다.
2. required local verification이 green인 pinned HEAD에 대해 standard implementation review gate를 연다. reviewer packet은 승인 spec, 저장 plan, base..HEAD 전체 diff, key/cardinality/consumer contract와 command evidence를 포함한다. `REVISE`면 모든 finding을 한 batch로 처리하고 root가 exact delta 또는 broad trust-boundary change면 full diff를 follow-up self-review한 뒤 관련 검증과 `impl_review` receipt를 새 HEAD에 맞춘다.
3. commit/push 직전에 worktree basename과 branch가 `beads-ou6`인지, target-base ancestry와 origin writable repo가 변하지 않았는지 다시 확인한다. `scripts/pr-preflight.sh --search`와 upstream #5278 attribution을 재검증하고 verified commits만 `git push origin beads-ou6`로 게시한다. known CI failure, head drift, source-of-truth/attribution conflict 또는 stale review는 push 전에 hard stop한다.
4. GitHub body file에 writer/consumer trust boundary, exact profiles, local verification, expected cold/warm evidence와 #5278 contributor attribution을 기록하고 `scripts/gh-body-lint`를 통과시킨다. `gh pr create --repo <origin-owner/repo> --base <resolved-target-base> --head beads-ou6` 뒤 PR 번호로 preflight를 readback한다. Phase 4 child를 resolved로 만들고 parent에 `pr_url`, completion report와 `resolved`를 기록하되 `worker-ineligible`은 유지한 채 PR URL, head OID, CI status와 다음 merge action을 보고하고 멈춘다.

검증: pushed branch HEAD와 PR head OID가 같고 PR base가 action-time target base와 같아야 한다. parent와 네 phase child의 lifecycle, current `impl_review`, `pr_url`, completion report와 유지된 `worker-ineligible` label을 `bd show --json`으로 readback해야 한다.

## PR Delivery 이후 merge·runtime closure

- 후속 사용자 요청은 `pr-finish --review`로 시작한다. 같은 pinned head의 PR/PR Risk/Nightly-relevant static checks와 cache contract가 green이고 feedback/AI-review/head/base invariants가 닫힌 뒤에만 merge한다.
- merge commit의 `go-cache.yml` change가 만든 trusted main writer run에서 exact/fallback restore state, module/non-race/race sizes, download/build/compile durations, three save outcomes와 published keys를 기록한다. post-merge sync와 `docs/agents/repo-ops.toml` managed deploy/readback도 수행한다.
- cold writer 성공 이후 같은 Go/toolchain generation의 자연 발생 risky PR 최소 3회에서 module/non-race/race exact 또는 prefix matched key, restored size, setup/download/build/compile duration, PR/PR Risk critical span과 runner-minutes를 수집한다. build key의 source SHA 때문에 prefix fallback이 정상일 수 있으므로 `cache-hit=false`만으로 미사용을 판단하지 않는다.
- cold writer와 세 warm consumer evidence가 모두 readback되고 writer uniqueness, untrusted save 0와 key/profile contract가 유지될 때 `worker-ineligible`을 제거한다. target-base containment과 deploy verification을 확인하고 phase children을 leaves-first로 close한 다음 parent를 마지막에 close한다. warm 표본이 아직 없거나 효과가 불명확하면 Bead와 label을 유지하고 fixed 성능 향상을 주장하지 않는다.

## Test scope

- Phase 1 RED: active trusted writer, explicit setup-go cache boundary, exact v2 keys, restore-only consumers와 measurement uncached contract가 없는 현재 topology에서 승인된 assertions만 실패해야 한다. YAML parser, embed expansion과 fixture discovery 오류는 RED acceptance가 아니다.
- Phase 2 GREEN: trusted writer trigger/permissions/concurrency, proven-safe paths-ignore, pinned actions, three exact family/profile keys와 save cardinality/conditions가 green이 된다. consumer assertions는 Phase 3까지 의도적으로 RED다.
- Phase 3 GREEN: PR/PR Risk/Nightly restore-only inventory, explicit cache:false/GOCACHE, measurement uncached, embed safety와 parent/yvf/gkw negative contracts를 포함한 전체 `go test ./scripts`가 green이 된다.
- Phase 4 integration: exact branch에서 static contracts, actionlint availability, non-race warm command와 가능한 race dry compile을 확인한다. 로컬 filesystem cache 생성은 GitHub cache backend hit/save나 runtime 성능을 증명하지 않는다.
- runtime acceptance: merge 뒤 cold trusted writer와 이후 같은 generation의 warm risky PR 3회가 matched key, sizes와 durations를 제공한다. queue/code variance 때문에 fixed percentage를 hard gate로 삼지 않는다.
- 제외: inactive `main.yml`, Regression baseline cache, Embedded Dolt matrix, release/migration/cross-version/Nix/package cache, aggregate owner와 Go dependency/version은 이 계획의 RED-GREEN seam이 아니다.
