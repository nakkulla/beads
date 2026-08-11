# Regression Tests 경로 gating과 stale run 취소 설계

## 문서 상태

- Bead: `beads-gkw`
- Route: `spec_backed`
- Workflow mode: `standard`
- 설계 승인: 2026-08-12 사용자 전체 설계 승인
- 기준 브랜치/SHA: `main` / `a33863a82c2853eb9f6f595b8f9b68adaf2a0819`
- 구현 단위: `Regression Tests` detector와 concurrency를 고치는 독립 PR
- 선행 기준선: `beads-am7`의 승인된 spec/plan이 정의한 CI single-owner DAG

## 배경과 문제

`.github/workflows/regression.yml`은 `pull_request`, `push` to `main`,
`workflow_dispatch`에서 실행된다. PR에서는 changed path를 검사하지만 `push`와 manual
event는 모두 즉시 `run_regression=true`를 출력한다. 그 결과 Markdown이나 Beads metadata만
바뀐 main push도 20분 budget의 differential regression suite를 실행한다.

현재 detector에는 다음 안전성·효율 문제가 함께 있다.

1. `push`에는 `github.event.before`/`github.sha` diff 판정이 없다.
2. inline `git diff` 또는 event JSON parse가 실패하면 `set -e`로 detector job이 실패하고
   heavy regression job은 실행되지 않는다. workflow는 red지만 실제 risk coverage는 없다.
3. risky allowlist에 없는 unknown path는 skip된다. 새 production path가 생기면 detector를
   갱신하기 전까지 false negative가 될 수 있다.
4. `skip-regression` label은 risky diff도 무조건 우회할 수 있다.
5. workflow에 `concurrency`가 없어 같은 PR synchronize run과 연속 main push가 함께 실행된다.
6. detector logic이 YAML inline shell이라 temp git repository를 사용한 behavioral test가 없다.

`Regression Tests`는 현재 branch protection의 직접 required check가 아니며 `merge_group`에도
참여하지 않는다. 이번 Bead는 그 정책을 바꾸지 않고, 기존 workflow 안에서 proven-safe skip과
stale cancellation만 추가한다.

## 목표

1. PR과 main push 모두 exact diff bounds로 같은 classifier를 사용한다.
2. 오직 증명된 docs/metadata-only change만 heavy regression을 skip한다.
3. missing/invalid bounds, diff 실패, event parse 실패와 unknown path는 full run으로 fail safe한다.
4. `run-regression`과 `workflow_dispatch` force-run을 유지한다.
5. `skip-regression`이 risky/unknown change를 우회하지 못하게 한다.
6. 같은 PR의 stale workflow와 main의 stale risk-bearing work를 SHA ancestry와 `run_id`로
   monotonic하게 supersede한다.
7. detector, monotonic arbitration과 workflow topology를 behavioral/static contract test로 고정한다.

## 비목표

- `tests/regression` test body, baseline version 또는 binary cache 변경
- Regression workflow를 required check로 승격
- `merge_group` trigger 또는 informational aggregate gate 추가
- branch protection/ruleset 변경
- `PR`, `PR Risk`, `Main` aggregate owner 집합 변경
- Embedded Dolt tier/sharding 변경
- Go module/build cache key, writer 또는 restore 정책 변경
- package, website, release, migration, cross-version workflow gating 변경
- `beads-am7` policy/lint/artifact DAG 변경

## 소유권과 sibling 정합성

- `beads-gkw`는 `.github/workflows/regression.yml`, 새 regression detector/supersession scripts,
  regression-specific workflow contract와 관련 설명 문서만 소유한다.
- `beads-am7`은 `PR`/`Main` single-owner DAG와 baseline aggregate를 소유한다.
- `beads-yvf`는 `PR Risk` Embedded Dolt storage matrix를 소유한다.
- `beads-ou6`는 active PR/PR Risk/Nightly cache family를 소유한다. 기존
  `~/.cache/beads-regression` baseline binary cache는 별도 기존 surface이며 이 문서가
  변경하지 않는다.

`beads-gkw`는 sibling과 semantic dependency가 없지만 구현은 `beads-am7`의 최종 target
base를 먼저 반영해 canonical workflow contract test와 문서 변경을 덮어쓰지 않는다.
Regression-specific fixture/helper는 같은 `scripts` Go package 안에서 parent test helper를
재사용한다.

## 실행 단위 disposition과 Worker eligibility

detector, arbitration, workflow, tests와 docs는 현재 Bead의 한 PR이 운반한다. required runtime
evidence 중 risky PR run과 implementation merge가 만드는 risky main run은 해당 PR/merge
lifecycle에서 얻을 수 있다. 그러나 “의미 없는 commit을 만들지 않는다”는 제약 아래 최초의
자연스러운 docs/metadata-only main push evidence는 merge 뒤 별도 외부 event를 기다려야 하며 현재
`docs/agents/repo-ops.toml`의 managed deploy command가 그 event를 생성하지 않는다.

따라서 docs-only main 표본은 현재 Bead의 required no-PR interactive residue다. formal spec gate를
닫을 때 `spec_review`와 같은 logical write로 `worker-ineligible` label을 추가한다. required risky
PR, risky main, docs-only main run의 URL/SHA/reason과 결과를 모두 read back한 뒤에만 label을
제거하고 Bead를 완료할 수 있다. PR synchronize와 두 risky main push의 cancellation sequence는
의미 있는 후속 commit이 자연스럽게 있을 때 추가로 기록하는 opportunistic evidence이며 closure
requirement가 아니다. 이 구분을 바꾸거나 dependency-backed Bead+PR로 옮기려면 spec delta review가
필요하다.

## 선택한 설계

### 1. detector를 repository-owned script로 분리

inline shell을 `.github/scripts/ci-regression-tier.sh`로 이동한다. script는 GitHub context를
직접 참조하지 않고 env 입력과 `GITHUB_OUTPUT`만 사용한다.

```text
EVENT_NAME
PR_BASE_SHA
PR_HEAD_SHA
PUSH_BEFORE_SHA
PUSH_HEAD_SHA
GITHUB_EVENT_PATH
GITHUB_OUTPUT
```

출력은 기존 이름을 유지한다.

```text
run_regression=true|false
reason=<single-line reason>
```

모든 정상 분기는 두 output을 정확히 한 번 쓴다. workflow step은 script에 실제
`GITHUB_OUTPUT` 대신 `mktemp`로 만든 candidate output을 전달한다. script exit 0과 output schema
검증이 모두 성공했을 때만 candidate bytes를 실제 `GITHUB_OUTPUT`에 append한다. script nonzero,
output 누락·중복·invalid boolean·multiline reason은 모두 candidate를 버리고 다음 fallback을 실제
output에 쓴다.

```text
run_regression=true
reason=detector error; defaulting to full regression
```

따라서 예상한 diff/event 오류뿐 아니라 detector 자체의 예기치 않은 오류도 heavy run으로
fail safe한다. 실제 `GITHUB_OUTPUT` 자체에 쓸 수 없는 infrastructure failure만 detector job
failure가 되며, 이를 success로 위장하지 않는다. candidate temp file은 step 종료 시 trap으로
제거한다.

### 2. event별 diff bounds

event policy는 다음과 같다.

| Event | Bounds | 기본 동작 |
|---|---|---|
| `pull_request` | `PR_BASE_SHA..PR_HEAD_SHA` | diff 분류 |
| `push` to `main` | `PUSH_BEFORE_SHA..PUSH_HEAD_SHA` | diff 분류 |
| `workflow_dispatch` | 없음 | 항상 full run |
| 그 외 event | 없음 | unknown event, full run |

PR/push bounds가 비어 있거나 40-hex가 아니거나 동일하거나 all-zero before SHA면 full run이다.
initial/force push와 shallow/missing object도 임의 base를 만들지 않고 full run한다.
`git diff --name-only --no-renames <base> <head>` 실패는 full run이며 reason에 event와 실패
class를 기록한다. rename은 old/new path 해석 차이를 피하기 위해 no-renames의 delete+add로
분류한다.

empty diff는 unknown 상태로 full run한다. GitHub event가 실제 change를 가리키는데 empty인
경우를 docs-only로 추정하지 않는다.

### 3. explicit safe classifier

classifier는 “risky allowlist에 없으면 safe”가 아니라 “모든 path가 explicit safe class면
safe”로 바꾼다.

증명된 safe class는 다음 semantic regex로 한정한다.

```text
^docs/
^\.beads/
^\.github/ISSUE_TEMPLATE/
^\.github/PULL_REQUEST_TEMPLATE\.md$
^(AGENTS|AGENT_INSTRUCTIONS|ARTICLES|BENCHMARKS|CHANGELOG|CLAUDE|CONTRIBUTING|FEDERATION-SETUP|NEWSLETTER|PROPOSAL-pull-config-wedge|PR_MAINTAINER_GUIDELINES|README|RELEASING|SECURITY|build-docs)\.md$
^(LICENSE|NOTICE)$
```

전역 `*.md` suffix는 safe 규칙이 아니다. 이 repository는
`internal/templates/skills/beads/SKILL.md`, `internal/templates/agents/defaults/*.md`처럼
Markdown을 `go:embed`로 binary에 넣는다. embedded YAML/JSON/SQL도 같은 이유로 unsafe다. 현재
`//go:embed` directive가 가리키는 모든 tracked target을 contract test에서 전개해 safe class와
교집합이 없음을 검증한다. 새 embedded target이 safe class와 충돌하면 classifier 확장보다 해당
path를 unsafe로 유지하는 것이 기본이다.

기존 detector가 제외하던 다음 test-only change도 candidate binary와 regression harness를
바꾸지 않으므로 safe class로 유지한다.

```text
^(cmd/bd|internal/storage|internal/types)/.*_test\.go$
```

단, `tests/regression/**`는 test-only여도 regression harness 자체이므로 항상 full run이다.
`.github/workflows/regression.yml`, `.github/scripts/ci-regression-tier.sh`,
`.github/scripts/ci-regression-supersede.sh`, `Makefile`,
`.buildflags`, `go.mod`, `go.sum`, `cmd/bd/**`, `internal/storage/**`, `internal/types/**`의
non-test path는 full run이다.

위 safe class에 포함되지 않은 path는 모두 `unknown path`로 full run한다. 따라서 새 Go
package, 새 build script, package/runtime source가 detector 갱신 전까지 잘못 skip되지 않는다.
safe class 확장은 별도 contract fixture와 rationale 없이 하지 않는다.

### 4. manual override와 label precedence

우선순위는 다음과 같다.

1. `workflow_dispatch`는 항상 full run한다.
2. PR의 `run-regression` label은 diff보다 먼저 force-run한다.
3. event JSON/label parse 실패는 full run한다.
4. diff가 risky 또는 unknown이면 full run한다.
5. 모든 path가 safe일 때만 skip한다.

`skip-regression`은 risky/unknown을 이기는 bypass가 아니다. backward-compatible 표시를 위해
label은 읽되, 모든 path가 safe일 때 reason을 `safe diff; skip-regression label present`로
구체화하는 용도로만 사용한다. risky/unknown diff에서 label이 있으면 full run하고 reason에
`skip-regression ignored for unsafe diff`를 남긴다.

두 label이 함께 있으면 `run-regression`이 이긴다. 이 precedence는 fixture로 고정한다.

### 5. coverage-preserving monotonic supersession

[GitHub concurrency contract](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)는
같은 group의 running run뿐 아니라 기존 pending run도 최신 항목으로 대체한다. 따라서 모든 main
push를 workflow-level 한 group에 넣으면 risky push A의 regression을 뒤이은 docs-only push B가
취소한 뒤, B는 `A..B`만 보고 skip하는 coverage hole이 생긴다.

workflow-level concurrency는 PR만 공유하고 push/manual은 run별 unique group을 사용한다.

```yaml
concurrency:
  group: ${{ format('{0}-{1}-{2}', github.workflow, github.event_name, github.event_name == 'pull_request' && github.event.pull_request.number || github.run_id) }}
  cancel-in-progress: true
```

main push는 GitHub의 last-entrant group ordering에 의존하지 않는다. detector가
`run_regression=true`를 낸 trusted push에만 `supersede-stale-main` job을 실행하고, 새
`.github/scripts/ci-regression-supersede.sh`가 GitHub Actions REST API로 monotonic arbitration을
수행한다. [workflow-run list/cancel API](https://docs.github.com/en/rest/actions/workflow-runs)는
workflow file, branch와 event로 run을 좁힐 수 있고 cancel에는 `Actions: write`가 필요하다.

```yaml
permissions:
  contents: read

jobs:
  supersede-stale-main:
    name: Supersede stale main regression
    needs: detect-regression-need
    if: github.event_name == 'push' && needs.detect-regression-need.outputs.run_regression == 'true'
    permissions:
      actions: write
      contents: read

  regression:
    needs: [detect-regression-need, supersede-stale-main]
    if: always() && needs.detect-regression-need.result == 'success' && needs.detect-regression-need.outputs.run_regression == 'true' && needs.supersede-stale-main.outputs.proceed != 'false'
```

job output은 `proceed=true|false`, single-line `reason`과 취소를 요청한 run ID 목록이다. 실제
`GITHUB_OUTPUT`에는 validated final wrapper만 쓰며 script failure, missing/duplicate output 또는
invalid boolean은 `proceed=true`, `reason=arbitration degraded; keeping full regression`으로
대체한다.

script 입력은 `GITHUB_REPOSITORY`, `GITHUB_RUN_ID`, `GITHUB_SHA`, `GH_TOKEN`과 candidate output
path다. workflow filename과 main branch는 regression contract 상수이며 다른 repository/ref를
외부 입력으로 받아 취소 범위를 넓히지 않는다.

arbitration algorithm은 다음으로 고정한다.

1. current `GITHUB_RUN_ID`가 decimal이고 current SHA가 40-hex인지 검증한다. invalid input/API
   failure는 cancellation을 포기하고 `proceed=true`로 full run한다.
2. `regression.yml`, `branch=main`, `event=push`의 모든 active page를 조회한다.
3. lower `run_id`의 non-completed run은 그 head SHA가 current SHA의 ancestor일 때만 cancel한다.
   non-ancestor, missing object와 ancestry check failure는 취소하지 않아 두 run을 모두 보존한다.
4. higher `run_id`의 active run job 목록에서 `Supersede stale main regression`이 이미
   `in_progress`이거나
   `completed` non-skipped이면 risk-bearing run으로 인정한다. current SHA가 higher head SHA의
   ancestor일 때 current job은 `proceed=false`로 self-abort한다. 아직 detector를 기다리는 queued
   job이나 completed-skipped job은 docs-only일 수 있으므로 supersession 근거로 쓰지 않는다.
5. cancel `202`는 accepted, completion race의 `409`는 benign으로 기록한다. 다른 list/jobs/cancel
   오류는 warning과 `arbitration=degraded`를 남기고 current heavy run을 계속한다.

script는 API transport를 주입 가능한 함수/command로 분리해 fixture에서 실제 run을 취소하지 않는다.
`supersede-stale-main`은 push-main commit만 checkout하고 `persist-credentials:false`,
`fetch-depth:0`을 사용한다. `actions:write` token은 이 job에만 있고 PR/manual job이나 heavy test
process에 전달되지 않는다. script/output schema 실패도 final wrapper가 `proceed=true`로 바꾼다.

- 같은 PR number의 synchronize run은 workflow-level에서 이전 run 전체를 취소한다.
- docs-only main push는 arbitration job 자체가 skipped라 앞선 risky run을 취소하지 않는다.
- newer risk-bearing push는 lower run ID이면서 ancestor인 work만 cancel한다.
- older detector가 나중에 끝나도 higher risk-bearing arbitration job을 취소할 수 없고 자신이
  `proceed=false`가 된다.
- API/ancestry uncertainty는 중복 실행으로 비용만 늘릴 뿐 coverage를 제거하지 않는다.
- PR, push와 manual은 event/run ID로 분리되어 서로 취소하지 않는다.

취소는 stale compute를 줄이는 정책이며 이전 실패를 success로 바꾸지 않는다. PR은 최신 run,
main은 ancestry가 확인된 최신 completed risk-bearing heavy job을 coverage evidence로 사용한다.

### 6. workflow topology 보존

trigger는 계속 다음 세 개만 가진다.

- `push` branches `[main]`
- `pull_request` branches `[main]`
- `workflow_dispatch`

workflow-level `paths`/`paths-ignore`는 추가하지 않는다. `detect-regression-need`는 항상
실행한다. `supersede-stale-main`은 risky main push에만 실행하며, `regression`은 detector output이
true이고 monotonic arbitration이 current run을 superseded로 판정하지 않았을 때 실행한다.
arbitration job의 skipped/failure dependency가 PR/manual full run을 막지 않도록 `always()`와
detector result/output을 함께 검사한다.

job display name, timeout, Dolt setup, `~/.cache/beads-regression` key/path와 다음 command는
유지한다.

```bash
go test -tags=regression,gms_pure_go -timeout=20m -v ./tests/regression/...
```

`merge_group`, final aggregate 또는 required-check semantics를 추가하지 않는다.

## 실패 처리

- bounds missing/invalid/all-zero: detector success + full run
- `git diff` object/range failure: detector success + full run
- event JSON 없음/invalid 또는 `jq` failure: detector success + full run
- empty diff: detector success + full run
- unknown event/path: detector success + full run
- detector script nonzero 또는 candidate output invalid: wrapper가 `true` fallback, heavy run
- 실제 `GITHUB_OUTPUT`에 쓸 수 없음: detector job failure, workflow red
- safe diff: detector success + heavy job skipped
- stale PR run: workflow-level concurrency가 cancel
- stale risky main work: newer run이 lower-ID ancestor를 cancel하고, reversed detector order의 older
  run은 higher risk-bearing job을 확인해 self-abort
- docs-only main run: 앞선 risky heavy job을 cancel하지 않고 자신은 skip
- arbitration API/ancestry/output failure: cancel하지 않고 current heavy run 계속, warning 기록

script는 fallback reason을 stderr와 `GITHUB_OUTPUT`에 모두 남긴다. raw changed paths는 log에
한 줄씩 출력하되 output value에는 newline을 넣지 않는다.

rollback은 detector/supersession scripts, workflow wiring, concurrency/arbitration과 contract/docs를
한 PR로 revert하는 것이다. failure fallback만 제거하거나 `skip-regression` bypass를 임시 복원하는
부분 rollback은 금지한다.

## Test scope

RED-GREEN seam은 event bounds, safe/unknown path classification, label precedence,
fail-safe fallback과 monotonic supersession topology다.

### RED

현재 base에서 다음 test는 실패해야 한다.

1. docs-only main push가 `run_regression=false`를 출력한다.
2. risky main push가 `true`를 출력한다.
3. missing/zero SHA, invalid diff와 unknown path가 `true`를 출력한다.
4. risky PR의 `skip-regression`이 bypass되지 않는다.
5. reversed detector completion에서 newer run만 lower ancestor를 취소하고 older run은 self-abort한다.
6. docs-only higher run, non-ancestor와 API failure가 current risky run을 취소/skip하지 않는다.
7. detector와 supersession logic이 external script로 behavioral fixture에서 실행된다.

### GREEN behavioral fixtures

Go test가 `t.TempDir()` 아래 isolated git repository와 event JSON을 만들고 실제 script를
실행한다. 최소 matrix는 다음이다.

| Case | Expected |
|---|---|
| PR Markdown/docs only | false |
| PR `.beads/**` only | false |
| PR existing scoped `_test.go` only | false |
| PR `cmd/bd` production Go | true |
| PR `internal/storage` production Go | true |
| PR `tests/regression/**` | true |
| PR embedded Markdown asset | true |
| PR embedded YAML/JSON/SQL asset | true |
| PR unknown path | true |
| main docs-only push | false |
| main risky push | true |
| main initial/all-zero before | true |
| invalid/missing object | true |
| empty diff | true |
| `workflow_dispatch` | true |
| `run-regression` label | true |
| risky + `skip-regression` | true |
| safe + `skip-regression` | false |
| both labels | true |
| invalid event JSON | true |
| detector script nonzero | wrapper fallback true |
| candidate output missing/duplicate/invalid | wrapper fallback true |

fixture는 global git config를 차단하고 repo-local `core.hooksPath`를 사용한다. production
Beads database나 실제 GitHub event file을 변경하지 않는다.

supersession fixture는 fake paginated run/jobs API와 temp git ancestry를 사용해 다음을 검증한다.

| Arbitration case | Expected |
|---|---|
| current 102, active ancestral 101 | cancel 101, proceed true |
| current 101 finishes detector after active risk-bearing 102 | cancel none, proceed false |
| higher docs-only job is skipped | proceed true |
| lower/higher SHA is non-ancestor or missing | no cancel/self-abort, proceed true |
| list/jobs/cancel error or invalid output | warning, proceed true |
| cancel completion race `409` | benign, proceed true |

reversed-order fixture는 102 arbitration을 먼저, 101을 나중에 호출해 101이 절대 102를 취소하지
않고 heavy suite도 시작하지 않는지 고정한다.

### GREEN static workflow contract

`scripts/ci_workflow_test.go`는 YAML을 parse해 다음을 고정한다.

- 세 trigger와 branch target이 유지된다.
- workflow-level path filter와 `merge_group`이 없다.
- workflow-level concurrency는 같은 PR만 공유하고 push/manual은 `run_id`로 분리한다.
- heavy job-level concurrency가 없고 `supersede-stale-main`만 risky push-main에서 실행된다.
- workflow 기본 권한은 `contents:read`이고 supersession job만 `actions:write`를 override한다.
- supersession job만 `actions:write`, checkout `persist-credentials:false`, `fetch-depth:0`을 가지며
  PR/manual/heavy job은 write token을 받지 않는다.
- `regression.needs`/`if`가 detector success+true, arbitration `proceed`와 fail-safe `always()`를
  함께 고정한다.
- checkout `fetch-depth: 0`, detector output names와 job-level `if`가 유지된다.
- push/PR SHA env가 script에 전달된다.
- detector wrapper가 candidate output schema를 검증하고 nonzero/invalid output을 `true`
  fallback으로 바꾼다.
- supersession wrapper가 candidate output schema를 검증하고 nonzero/invalid output을
  `proceed=true` fallback으로 바꾼다.
- repository의 현재 `//go:embed` target이 safe classifier와 교차하지 않는다.
- Regression command, timeout과 baseline binary cache가 바뀌지 않는다.
- PR/Main/PR Risk aggregate gate에는 Regression job이 추가되지 않는다.

## 문서 갱신

`docs/CI_REQUIRED_CHECK_TOPOLOGY.md`는 Regression이 separate/non-required이고 job-level
detector를 사용한다는 사실을 유지하면서 다음을 갱신한다.

- PR과 main push 모두 path classification을 사용한다.
- `workflow_dispatch`는 force-run이다.
- docs/metadata-only는 proven-safe skip이고 unknown/error는 full run이다.
- `merge_group`/required 승격은 여전히 별도 선택이다.

`docs/CI_TEST_SURFACE_AUDIT.md`는 snapshot 성격을 보존하되 현재 trigger/detector 설명과 stale
run cancellation을 실제 YAML에 맞춘다. `docs/CI_CLEANUP_PLAN.md`의 “main은 항상
regression” 문장이 active policy처럼 읽히면 historical measurement임을 표시하고 현재 contract로
링크한다. 긴 YAML/path list를 문서에 복제하지 않고 script와 contract test를 source로 가리킨다.

## 검증 bundle

```bash
bash -n .github/scripts/ci-regression-tier.sh
bash -n .github/scripts/ci-regression-supersede.sh
go test ./scripts -run 'Regression|CIWorkflow'
go test ./scripts
git diff --check
```

가능하면 `shellcheck`와 `actionlint .github/workflows/regression.yml`을 추가한다. differential
regression suite의 test body/baseline을 바꾸지 않으므로 full local regression run은 static/
detector 구현의 필수 RED-GREEN gate가 아니다. 실제 risky PR에서는 기존 job이 정상 실행·성공해야
한다.

## runtime acceptance

closure에 필요한 runtime evidence는 다음 세 가지다.

1. risky implementation PR의 detector reason과 differential regression success
2. implementation merge가 만드는 risky main push의 arbitration/heavy success
3. 최초의 자연스러운 docs/metadata-only main push의 detector success와 heavy skip

같은 PR에 의미 있는 후속 commit이 있으면 synchronize cancellation을, 자연스럽게 연속된 두
risk-bearing main push가 있으면 monotonic cancel/self-abort를 추가로 기록한다. 두 sequence는 static/
behavioral contract를 보강하는 opportunistic evidence이며, 이를 만들기 위한 빈 commit이나 의미
없는 production change는 금지한다.

required docs-only/risky 실제 main evidence가 아직 발생하지 않았으면 contract test를 runtime
success로 과장하지 않는다. Bead 완료 전에 세 required run의 URL/SHA/reason을 notes 또는
completion report에 추가한다. production PR을 고의로 실패시키거나 의미 없는 commit을 만들지
않는다.

## Acceptance criteria

1. docs/metadata-only PR과 main push만 heavy regression을 skip한다.
2. risky Go/storage/types/regression/build input은 계속 실행한다.
3. `workflow_dispatch`와 `run-regression` force-run이 유지된다.
4. `skip-regression`은 risky/unknown change를 우회하지 못한다.
5. missing/invalid SHA, diff/event parse 실패, empty diff와 unknown path는 full run한다.
6. 같은 PR의 stale workflow는 기존 event group으로 취소되고, main은 newer run이 lower-ID
   ancestor만 cancel하며 reversed detector order의 older run은 self-abort한다. docs-only main과
   manual run은 risky coverage를 취소하지 않는다.
7. workflow는 separate/non-required로 남고 `merge_group`/branch protection을 바꾸지 않는다.
8. reversed-order/API-failure를 포함한 behavioral/static contract tests와 required risky PR,
   risky main, docs-only main run evidence가 있다.
9. Regression test body/baseline cache, sibling cache/sharding과 parent artifact DAG가 diff에 섞이지
   않는다.
