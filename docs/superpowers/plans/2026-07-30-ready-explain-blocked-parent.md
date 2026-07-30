# bd ready --explain: blocked 엔트리에 parent 노출

- Bead: beads-9mv
- Repo: `/Users/isy_macstudio/External/beads` (base `main`)
- Spec: `docs/superpowers/specs/2026-07-30-ready-explain-blocked-parent-design.md` @ `ed8f1d1555e246b6812dc006965b7aa8eefb12d8`
- spec_review: `codex@ed8f1d1555e246b6812dc006965b7aa8eefb12d8` (REVISE 종결, 발행 완료)

## Context

`bd ready --explain --json` 의 `blocked[]` 엔트리에 `parent` 가 없다. 같은 명령에
`--explain` 을 붙이지 않으면 나온다 — 같은 커맨드의 두 출력 모드 사이 비대칭이다.

소비자 실사고로 발견됐다. beads-ui 보드탭은 승인된 스펙(beads-ui
`docs/superpowers/specs/2026-07-17-board-worker-ux-v2-design.md` §3.3)대로 `parent`
평탄화 필드로 child 를 분류해 parent 카드로 접는다. Blocked 컬럼만 dependency 소스로
`bd ready --explain` 을 쓰기 때문에, 의존성 블로킹된 child 세 건
(`TRACE-ICI-9xp.6/.7/.8`)이 접히지 않고 Blocked 레인에 독립 카드로 노출됐고 부모
카드의 rollup 총계에서도 빠졌다.

`parent` 는 `issues` 테이블 컬럼이 아니라 `dependencies` 의 `parent-child` 엣지에서
유도되는 계산 필드다. 그래서 `types.Issue` 에는 `Parent` 가 없고, 내보내는 것은 각
wrapper 타입이 자기 필드로 선언하는 관행이다 — `IssueWithCounts`(`types.go:749`),
`IssueDetails`(`:760`), `ReadyItem`(`:1050`). blocked 계열 두 타입
(`BlockedIssue` `:1012`, `BlockedItem` `:1054`)만 그 필드가 없다. 그리고 값을 계산하는
지점도 없다 — `BlockedIssue` 를 조립하는 두 곳(`blocked.go:349-363`,
`external_blocked.go:158-165`)이 모두 `GetIssuesByIDsInTx`(유도 필드 미포함 SELECT)로
이슈를 가져와 값 복사만 한다.

의도한 결과: blocked 엔트리가 parent 를 노출해 소비자가 **코드 변경 0줄**로 정합된다.
그 주장은 Phase 6 의 브라우저 실측으로만 성립한다.

## 사전 확정 사실 (실측)

- 세 백엔드 전부 `issueops.GetBlockedIssuesInTx` 위임 — `domain/db/issue.go:778`,
  `embeddeddolt/store.go:475`, `dolt/queries.go:76`. storage 수정 1곳이 전부 커버한다.
- `cmd/bd/ready.go:353-358` 이 `outputJSON(blocked)` 로 `[]*types.BlockedIssue` 를
  **직접 직렬화**한다. 그래서 `BlockedIssue.Parent` 태그는 `json:"-"` 가 아니라
  `json:"parent,omitempty"` 여야 하고, `bd blocked --json` 소비자도 함께 정합된다.
- `mergeExternalBlocked`(`cmd/bd/ready_explain_merge.go:24`)는 candidate 를
  `entry := *ext` 로 값 복사한다 → Phase 4 에서 채운 `Parent` 가 그대로 따라간다.
  merge 쪽 수정은 없다.
- `blocked.go:309-316` 의 기존 parent 조회는 **best-effort** 다 — `if err == nil` 로
  오류를 무시하고 상속을 건너뛴다. Phase 3 이 이 의미를 보존해야 한다.
- 재사용할 기존 자산: `getParentedIDSetInTx`(`ready_work.go:625-662`, IN 배치 패턴),
  `buildSQLInClause`(`ready_work.go:723`), `queryBatchSize = 200`(`batching.go:5`),
  `DepTargetExpr`(`dependencies.go:38`), `optionalBlockedTable`(`blocked.go:18`).
- 정본 검증 묶음: `env TEST_TIMEOUT=10m make test` → `TEST_COVER=1 ./scripts/test.sh`,
  canonical flags `GOFLAGS=-tags=gms_pure_go, CGO_ENABLED=1`.

**테스트 가용성** — seam 을 어디에 둘지 결정한 근거:

| 층 | 하네스 | 기본 묶음에서 |
| --- | --- | --- |
| `internal/types/` | 순수 유닛 | **실행** |
| `internal/storage/issueops/` | `go-sqlmock` (`external_resolution_test.go` 패턴) | **실행** |
| `internal/storage/domain/db/` | Dolt Docker 컨테이너 | **skip** |
| `cmd/bd/*_proxied_integration_test.go` | proxied + dolt 바이너리 | **skip** |

skip 원인을 정확히 구분한다:

- `domain/db` — 기본 묶음의 원인은 **`BEADS_TEST_SKIP=dolt`** 다.
  `scripts/ci/lib/test-env.sh:52-53` 이 `BEADS_TEST_ENV_RUN_DOLT != 1` 일 때 그 skip 을
  추가하고, `checkDolt()`(`internal/testutil/testdoltserver.go:100`)는
  `hasTestSkip("dolt")`(`:103`)를 **가장 먼저** 보고 거기서 반환하므로
  `isDockerAvailable()`(`:107`)까지 가지 않는다. 이 머신의 docker 미동작(실측)은 bare
  package 로 직접 실행할 때 걸리는 **별도** 조건이다(`doltNoDocker`). 둘 다 skip 이지만
  원인이 다르다.
- proxied — `requireProxiedServerEnv`(`cmd/bd/proxied_integration_helpers_test.go:28`)가
  `BEADS_TEST_PROXIED_SERVER != "1"` 이면 skip. dolt 바이너리는 이 머신에 있다
  (`/opt/homebrew/bin/dolt`).

따라서 **모든 RED→GREEN seam 을 위 두 층에만 둔다.** `domain/db` 의 기존
`blockedInheritedThroughParent`(`ready_helpers_test.go:42`)는 상속 회귀를 이미 덮지만
기본 묶음에서 skip 되므로 seam 으로 계수하지 않는다.

## Phase 1: 타입 필드 추가와 explain 전달

1. `internal/types/types.go` — `BlockedIssue`(`:1012`)와 `BlockedItem`(`:1054`)에
   `Parent *string \`json:"parent,omitempty"\`` 추가. embed 된 `Issue` 에 `Parent` 가
   없으므로 필드 충돌은 없다.
2. `internal/types/types.go` — `BuildReadyExplanation` blocked 루프(`:1126-1143`)의
   `BlockedItem` 조립에 `Parent: bi.Parent` 한 줄 추가. ready 루프는 건드리지 않는다.
3. `internal/types/explain_test.go` — blocked parent 케이스 2건 추가. 이 파일에는 이미
   `TestBuildReadyExplanation_ReadyWithParent`(`:86`)가 ready 쪽을 고정하고
   `_BlockedIssues`(`:110`)가 blocked 조립을 다루되 parent 는 검증하지 않는다 — 결함의
   대조 쌍이므로 대칭이 되게 채운다.

검증: `GOFLAGS=-tags=gms_pure_go CGO_ENABLED=1 go test ./internal/types/` 통과.

## Phase 2: parent 조회 헬퍼를 IN 배치로

1. `internal/storage/issueops/blocked.go:56` `loadParentIDsForChildrenInTx` 를 IN 배치로
   재작성. 시그니처(`(ctx, tx, depTables, childIDs) (map[string]string, error)`)와 반환
   형태는 **바꾸지 않는다**. `getParentedIDSetInTx` 패턴을 따르되
   `optionalBlockedTable` 기반 tolerance 는 기존 코드 그대로 유지한다.
2. `internal/storage/issueops/blocked_test.go` (신규, sqlmock) — seam 은 **쿼리 형태**다:
   `queryBatchSize`(200)를 넘는 201건 입력이 **정확히 2개의 `IN` 쿼리와 기대 인자 분할**로
   처리되는지 검증한다. "전건 반환" 만 검사하면 기존 per-ID 구현도 통과해 RED 가 되지
   않는다 — N+1 제거라는 성능 요구를 테스트로 고정하는 것이 이 seam 의 목적이다.
   추가로 `wisp_dependencies` 부재 tolerance, `depTables` 순회 순서 우선순위 보존.

등가성 주의: 현재 구현은 `childParents[childID] = parentID` 로 마지막 행이 이긴다.
한 테이블 안에서 같은 child 에 `parent-child` 엣지가 2개 이상이면 행 순서가 이미
비결정적이다(`ORDER BY` 없음). 배치화가 그 비결정성을 **새로 만들지 않도록** depTable
순회 순서를 보존한다.

Phase 2 완료 조건에서 **caller-level 동작 불변 주장은 제외한다.** `blocked.go:309`
경로의 blocker 상속 회귀는 Phase 3 에서 검증한다 — 이 시점에는 그 주장을 확인할
수단이 없다.

검증: `go test ./internal/storage/issueops/` 통과 + 201건 입력이 2 쿼리로 분할됨을
sqlmock 기대로 확인.

## Phase 3: stored blocked 경로에 parent 채우기

1. `blocked.go` `GetBlockedIssuesInTx` — parent 맵 조회를 `blockedIDList` 전체 대상
   **1회**로 올린다. `displayIDs` 가 아닌 이유는 순서다: blocker 상속이 `blockerMap` 을
   보강하기 전에 맵이 필요하므로 그 시점에 `displayIDs` 가 확정되지 않았다.
2. **오류 의미를 보존한다.** 기존 `:309-316` 은 `if err == nil` best-effort 로, parent
   조회 실패 시 상속만 건너뛰고 direct-blocked 결과는 정상 반환한다. 맵을 공유로
   끌어올린 뒤에도 parent 조회 오류가 함수 전체를 실패시키지 않아야 한다.
3. 같은 맵을 두 용도로 나눠 쓴다 — ① blocker 상속(`inheritedIDs` 해당 항목만, 기존
   "이미 blockerMap 에 있으면 건너뜀" 조건 유지) ② `BlockedIssue.Parent` 채우기.
   두 용도의 의미는 분리해 유지한다: "parent 가 blocker 다" 와 "parent 가 parent 다" 를
   같은 판단으로 섞지 않는다.
4. `results` 조립부(`:349-363`) — `Issue: *issue` 복사와 함께 `Parent` 세팅. 원본
   `issueMap` 의 `*types.Issue` 는 변형하지 않는다.
5. `blocked_test.go` — parent 있는 stored blocked → `Parent` 채워짐, top-level → nil
   유지, blocker 상속 등가성(parent 가 유일 blocker 인 child 의 `BlockedBy` 불변),
   **parent 조회 오류 시에도 기존 blocker 가 있는 항목은 반환됨**(2번 회귀).

검증: `go test ./internal/storage/issueops/` 통과.

## Phase 4: external-blocked candidates 경로에 parent 채우기

1. `internal/storage/issueops/external_blocked.go` candidates 조립(`:158-165`) —
   `candidateIDs` 로 Phase 2 헬퍼를 호출해 맵을 얻고 `BlockedIssue.Parent` 세팅.
   오류 처분은 Phase 3 과 같은 best-effort 로 맞춘다.
2. `blocked_test.go` 또는 인접 테스트 — parent 있는 external candidate → `Parent`
   채워짐, 없으면 nil 유지.

external candidate 는 로컬 DB 의 이슈다. 미충족인 것은 blocker 쪽
(`external:<project>:<capability>`)이고 `parent-child` 엣지는 로컬에 있으므로 정상
계산된다.

검증: `go test ./internal/storage/issueops/` 통과.

## Phase 5: 통합 검증과 PR Delivery (머지 전)

1. `cmd/bd/ready_proxied_integration_test.go` — 스펙 §6 이 요구한 proxied 계약 케이스를
   추가한다. 이 파일의 진입점은 `TestProxiedServerReady`(`:70`)이므로 그 아래 명명된
   subtest 로 넣는다(예: `explain_blocked_parent`).
2. `env TEST_TIMEOUT=10m make test` 전체 통과. skip 된 스위트를 **통과로 계수하지
   않는다** — `domain/db` 와 proxied 는 skip 으로 기록하고 원인을 구분해 적는다.
3. proxied 계약 명시 실행:
   `BEADS_TEST_PROXIED_SERVER=1 GOFLAGS=-tags=gms_pure_go CGO_ENABLED=1 go test -v -timeout 10m -run '^TestProxiedServerReady$/^explain_blocked_parent$' ./cmd/bd`
   `-v` 로 통과와 skip 을 식별한다. skip 되면 그 사실을 결과로 기록한다.
4. `implementation` 게이트 — 통합된 최종 diff 1회 리뷰 (검증 green 이후).
5. PR Delivery — base `main` 으로 PR, URL·CI·게이트 적격성 보고 후 정지. 브랜치와
   워크트리를 보존한다.

**이 phase 는 `make install-force` 를 실행하지 않는다.** 구현은 워크트리에서 돌고
`make install-force` 는 산출물을 `~/.local/bin/bd` — 모든 워크스페이스가 공유하는 live
런타임 바이너리 — 에 설치한다. 워크트리에서 live 런타임 디렉터리에 설치하는 것은
금지이며, 머지 전 코드를 전역에 깔면 다른 워크스페이스의 `bd` 까지 바뀐다.

검증: `make test` 결과 + 단계 3 기록 + PR URL.

## Phase 6: 머지 후 배포와 실측 (별도 크로싱)

머지되어 base `main` 에 반영된 뒤, base 체크아웃에서 실행한다.

1. `make install-force` — 빌드 + `~/.local/bin/bd` 교체. 멱등. bdui 서버 재시작
   **불필요**(서버가 요청마다 `bd` 를 spawn).
2. CLI 실측 — TRACE-ICI 에서 `bd ready --explain --json` 의 `9xp.6/.7/.8` 에
   `parent: "TRACE-ICI-9xp"`. 같은 워크스페이스 `bd blocked --json` 에도 `parent`
   (Phase 1 의 부수 정합 확인).
3. 소비자 실측 — 브라우저 보드탭에서 세 건이 Blocked 레인에서 사라지고 `9xp` 카드
   rollup 총계가 5 → 8. **"beads-ui 0줄" 주장은 이 관측으로만 성립한다.**
4. 플릿 릴리스(태그 + `dependency.yaml` 핀 범프)는 별도 수동 레그이며 범위 밖.

검증: 단계 2·3 관측이 스펙 §8 과 일치.

## Test scope

RED→GREEN seam (전부 기본 묶음에서 무조건 도는 층):

| seam | phase | RED | GREEN |
| --- | --- | --- | --- |
| `BlockedItem.Parent` JSON 노출 | 1 | 두 필드 부재로 컴파일 실패 | 필드 추가 + `Parent: bi.Parent` |
| `omitempty` 생략 | 1 | (동일 컴파일 실패) | nil 이면 `parent` 키 없음 |
| 201건 → 정확히 2개 `IN` 쿼리 | 2 | per-ID 구현은 201 쿼리를 발행해 기대 불일치 | IN 배치 재작성 |
| `wisp_dependencies` 부재 tolerance·순회 순서 | 2 | — (회귀 고정용) | 재작성 후에도 불변 |
| stored blocked `Parent` 채워짐 | 3 | nil | 조립부 세팅 |
| blocker 상속 등가성 | 3 | — (회귀 고정용) | 조회 통합 후에도 불변 |
| parent 조회 오류 시 direct-blocked 반환 | 3 | 공유 map 승격이 오류를 전파하면 실패 | best-effort 의미 보존 |
| external candidate `Parent` 채워짐 | 4 | nil | 조립부 세팅 |

제외와 이유:

- `internal/storage/domain/db/ready_helpers_test.go` — 기존
  `blockedInheritedThroughParent`(`:42`)가 상속 회귀를 덮지만 기본 묶음이
  `BEADS_TEST_SKIP=dolt` 로 skip 한다. seam 이 아니라 보강으로만 다루고 추가하지 않는다.
- `cmd/bd/ready_proxied_integration_test.go` — Phase 5 단계 1 에서 계약 케이스를
  추가하되 `BEADS_TEST_PROXIED_SERVER` 게이트 때문에 기본 묶음에서 돌지 않는다. 단계 3
  에서 명시 실행하고 결과(통과/skip)를 기록하되 seam 으로 계수하지 않는다.
- `GetIssuesByIDsInTx` 자체에 parent 조회 추가 — 비목표(소비자 12곳에 비용 전가).
- parent 계산 경로 단일화 — 이 수정으로 경로가 셋이 되지만(SQL 조인 / `allDeps` 순회 /
  `parent-child` 엣지 배치) 통합은 별도 단위다. 스펙 §9 관측 잔여.
