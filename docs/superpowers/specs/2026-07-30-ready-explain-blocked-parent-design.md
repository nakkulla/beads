# bd ready --explain: blocked 엔트리에 parent 노출

- Bead: beads-9mv
- 날짜: 2026-07-30
- 선행 스펙: `2026-07-28-ready-explain-external-blocked-design.md` (external-unsatisfied 이슈 blocked 배열 포함)

## 1. 문제

`bd ready --explain --json` 의 `blocked[]` 엔트리에 `parent` 가 없다. 같은 명령에
`--explain` 을 붙이지 않으면 `parent` 가 나온다 — 같은 커맨드의 두 출력 모드 사이
비대칭이다.

실측 (TRACE-ICI, 2026-07-30):

| 명령 | `parent` |
| --- | --- |
| `bd ready --explain --json` → `blocked[]` | 없음 |
| `bd ready --explain --json` → `ready[]` | 있음 |
| `bd ready --limit 1000 --json` | 있음 |
| `bd list --json --tree=false` | 있음 |
| `bd show <id> --json` | 있음 |

blocked 엔트리 키는 13개였다 — `blocked_by, blocked_by_count, created_at,
created_by, description, id, issue_type, metadata, owner, priority, status,
title, updated_at`.

소비자 실사고: beads-ui 보드탭은 승인된 스펙(beads-ui
`docs/superpowers/specs/2026-07-17-board-worker-ux-v2-design.md` §3.3)에 따라
`parent` 평탄화 필드로 child 를 분류하고, parent 카드가 렌더되는 한 child 를 모든
컬럼에서 제외해 parent 카드 안 compact 행으로만 보여준다. Blocked 컬럼만
dependency 소스로 `bd ready --explain` 을 쓰기 때문에(`server/list-adapters.js`
`DEPENDENCY_BLOCKED_ARGS`), 의존성 블로킹된 child 가 접히지 않고 Blocked 레인에
독립 카드로 노출됐다 — `TRACE-ICI-9xp.6/.7/.8`, parent `TRACE-ICI-9xp` 는
in_progress 로 렌더 중. 같은 부모의 rollup child 목록·총계에서도 빠졌다.

`9xp.3/.4` 도 같은 blocked 배열에 있었으나 `status=in_progress` 라 소비자 측
필터에서 걸러져 증상이 open 인 세 건에만 보였다.

## 2. 근본 원인

`parent` 는 `issues` 테이블 컬럼이 아니라 `dependencies` 의 `parent-child` 엣지에서
유도되는 계산 필드다(`types.Issue.Parent`, `internal/types/types.go:749`, 주석
"Computed parent from parent-child dep (bd-ym8c)").

`BlockedIssue` 를 조립하는 **두 지점**이 그 계산을 하지 않는다. 둘 다
`GetIssuesByIDsInTx`(`internal/storage/issueops/dependencies.go:755`)로 이슈 본문을
가져오는데, 그 쿼리는 `SELECT <컬럼들> FROM <table> WHERE id IN (...)` 이라 유도
필드인 parent 를 담지 않는다. 그리고 조립부는 `Issue: *issue` 로 값 복사만 한다.

| 지점 | 이슈 조회 | 조립 |
| --- | --- | --- |
| stored blocked (`is_blocked=1`) | `blocked.go:323` | `blocked.go:349-363` |
| external-blocked candidates | `external_blocked.go:146` | `external_blocked.go:158-165` |

`BuildReadyExplanation`(`types.go:1077`)의 blocked 루프(`:1126-1143`)는
`BlockedItem{Issue: bi.Issue, ...}` 로 그 빈 `Parent` 를 그대로 전달한다.

**ready 배열이 parent 를 갖는 이유는 별개 경로다.** ready 루프(`:1106-1121`)는
`allDeps` 를 순회해 `DepParentChild` 를 찾아 `ReadyItem.Parent` 에 넣는다. 그리고
`bd ready`(--explain 없이)는 또 다른 경로 — `ready_work_counts.go:116,158-160` 이
SQL 조인으로 `parentID` 를 함께 SELECT 해 `IssueWithCounts.Parent` 를 채운다. 즉
저장소에 parent 소스가 이미 두 개 있고, blocked 경로만 어느 쪽도 쓰지 않는다.

## 3. 목표 / 비목표

목표:

- `bd ready --explain --json` 의 `blocked[]` 엔트리가 parent 를 가진 이슈에 대해
  `parent` 를 노출한다. top-level 이슈는 지금처럼 키가 생략된다.
- stored blocked 와 external-blocked candidates 두 경로 모두 정합된다.
- 수정이 `bd ready --explain` 의 dependency 조회 성능 표면을 넓히지 않는다.

비목표:

- `ReadyItem.Parent`(`types.go:1050`) 가 embed 된 `Issue.Parent` 를 shadow 하는
  중복의 정리. 관측만 기록하고 이 단위에서 건드리지 않는다.
- `GetIssuesByIDsInTx` 자체에 parent 조회를 추가하는 것. 소비자가 12곳이라 전
  경로에 비용이 붙는다(§5 대안 검토).
- beads-ui 측 변경. §7 참조.

## 4. 설계

### 4.1 배치 parent 조회 헬퍼

`loadParentIDsForChildrenInTx`(`blocked.go:56`)가 반환 형태(`map[childID]parentID`)는
맞지만 **childID 하나당 쿼리 1회**를 돈다(`:65-66`) — N+1 이다. 현재는
`inheritedIDs`(blocker 가 없는 blocked 이슈)에만 쓰이므로 규모가 작았으나, 전체
blocked 로 대상을 넓히면 그 N+1 이 그대로 커진다.

이 헬퍼를 `IN` 배치로 재작성한다. 패턴은 같은 저장소의
`getParentedIDSetInTx`(`ready_work.go:625-662`)를 따른다 — `queryBatchSize` 청크,
`buildSQLInClause`, `dependencies`/`wisp_dependencies` 두 테이블 순회,
`wisp_dependencies` 부재 tolerance. 타깃 컬럼 표현은 기존과 같이 `DepTargetExpr` 를
쓴다.

시그니처와 반환 형태는 바꾸지 않는다. 기존 소비자(`blocked.go:309`)는 호출부 변경
없이 N+1 이 사라지는 이득만 받는다.

### 4.2 stored blocked 경로

`GetBlockedIssuesInTx` 는 이미 `:309` 에서 `loadParentIDsForChildrenInTx` 를
`inheritedIDs` 로 호출한다. 목적은 blocker 상속(부모를 blocker 로 대입)이다.

조회를 `blockedIDList` **전체 대상 1회**로 올리고, 그 결과 맵을 두 용도로 나눠 쓴다.
대상이 `displayIDs`(= `blockerMap` 키)가 아니라 `blockedIDList` 인 이유는 순서다 —
blocker 상속이 `blockerMap` 을 보강하기 전에 parent 맵이 필요하므로 그 시점에는
`displayIDs` 가 아직 확정되지 않았다. 차집합은 "blocker 도 parent 도 없는" 항목이며
어차피 결과에서 빠지므로 낭비는 미미하다.

1. blocker 상속 — `inheritedIDs` 에 해당하는 항목만 골라 쓴다. 기존 조건
   (`blockerMap` 에 이미 있으면 건너뜀)은 그대로 유지한다.
2. `Issue.Parent` 채우기 — `results` 조립 시점에 맵을 조회해 채운다.

두 용도의 의미는 분리해 유지한다. 조회를 합치는 것은 쿼리 중복 제거일 뿐,
"parent 가 blocker 다" 와 "parent 가 parent 다" 를 같은 판단으로 섞지 않는다.

`results` 조립부(`:349-363`)에서 `Issue: *issue` 복사 후 맵에 해당 id 가 있으면
`Parent` 를 세팅한다. 원본 `issueMap` 의 `*types.Issue` 를 변형하지 않는다 —
`BlockedIssue.Issue` 는 값 복사이므로 복사본에만 쓴다.

### 4.3 external-blocked candidates 경로

`external_blocked.go` 의 candidates 조립(`:158-165`)도 같은 처리를 한다.
`candidateIDs` 로 §4.1 헬퍼를 호출해 맵을 얻고 복사본의 `Parent` 를 채운다.

external candidate 는 로컬 DB 의 이슈다 — 미충족인 것은 blocker 쪽
(`external:<project>:<capability>`)이고 `parent-child` 엣지는 로컬에 있다. 따라서
parent 가 있으면 정상적으로 계산된다. parent 가 없으면 nil 이 남아 `omitempty` 로
생략된다.

### 4.4 JSON 노출

추가 작업이 없다. `BlockedItem` 은 `Issue` 를 **값 embed** 하므로
`Issue.Parent` 의 `json:"parent,omitempty"` 태그가 그대로 적용된다. nil 이면 지금과
동일하게 키가 생략된다.

따라서 다음 파일은 **수정하지 않는다**:

- `internal/types/types.go` — 새 필드 불필요, `BuildReadyExplanation` 불변
- `cmd/bd/ready.go` — `allDeps` 조회 범위 불변
- `cmd/bd/ready_proxied_server.go` — 동일

### 4.5 백엔드 커버리지

`GetBlockedIssuesInTx` 한 곳 수정으로 세 백엔드 전부가 커버된다 — 실측 확인:

| 백엔드 | 위임 |
| --- | --- |
| `internal/storage/domain/db/issue.go:778` | `issueops.GetBlockedIssuesInTx` |
| `internal/storage/embeddeddolt/store.go:475` | `issueops.GetBlockedIssuesInTx` |
| `internal/storage/dolt/queries.go:76` | `issueops.GetBlockedIssuesInTx` |
| `internal/telemetry/storage.go:357` | inner 위임 래퍼 |

usecase(`internal/storage/domain/issue.go:1373`)는 repo 로 위임한다. 중앙 서버
모드(`dolt_mode=server`)에서 실제로 타는 경로는 `domain/db` 체인이다.

## 5. 대안 검토

- **`GetIssuesByIDsInTx` 에 parent 채우기** — 두 조립 지점이 모두 이 함수를 쓰므로
  가장 중복이 없다. 채택하지 않는 이유: 소비자가 12곳이고 대부분 parent 를 쓰지
  않는다. 범용 배치 조회에 유도 필드 쿼리를 무조건 얹으면 관계없는 경로에 비용이
  붙는다. opt-in 플래그를 추가하는 변형은 호출부 12곳의 시그니처를 건드려 오히려
  변경 표면이 커진다.
- **`BuildReadyExplanation` blocked 루프에서 `allDeps` 로 계산** — ready 루프와
  대칭이라 읽기 좋다. 채택하지 않는 이유: 두 호출자가 `allDeps` 를 `readyIDs` 로만
  조회하므로(`ready.go:522`, `ready_proxied_server.go:218`) 조회 범위를
  `readyIDs ∪ blockedIDs` 로 넓혀야 한다. `bd ready --explain` 전체의 dependency
  조회 성능 표면이 커지고, external-blocked candidates 를 포함시키려면 merge 이후
  시점에 집합을 만들어야 해서 두 호출자를 각각 손봐야 한다(`types.go` +
  `ready.go` + `ready_proxied_server.go` = 3파일). §4 설계는 2파일
  (`blocked.go`, `external_blocked.go`)로 끝나고 성능 표면을 건드리지 않는다.
- **소비자(beads-ui) 측 우회** — `bd show <ids...>` 로 누락된 parent 를 보강하는 안.
  채택하지 않는 이유: 비대칭이 남아 다른 bd 소비자가 같은 함정을 계속 밟고, 보강
  호출 비용이 상시 발생한다. 이 경로로 열었던 beads-ui `UI-toci` 는 이 Bead 로
  승계하고 닫았다.

## 6. 테스트

Go 유닛 (`internal/storage/issueops/`):

- parent 를 가진 stored blocked 이슈 → `BlockedIssue.Issue.Parent` 가 채워진다
  (failing-first: 현재는 nil).
- top-level stored blocked 이슈 → `Parent` 가 nil 로 남는다 (`omitempty` 생략 유지).
- parent 를 가진 external-blocked candidate → `Parent` 가 채워진다.
- blocker 상속 경로 회귀 — parent 가 유일 blocker 인 child 의 `BlockedBy` 가
  기존과 동일하다. §4.2 의 조회 통합이 상속 의미를 바꾸지 않음을 고정한다.
- 배치 헬퍼 — `queryBatchSize` 를 넘는 childIDs 에서 전건이 반환된다,
  `wisp_dependencies` 부재 시 tolerate 한다.

JSON 계약 — `cmd/bd/ready_proxied_integration_test.go`(중앙 서버 모드에서 실제로
타는 경로의 기존 통합 테스트):

- `bd ready --explain --json` 의 blocked 엔트리에 `parent` 키가 나온다 / top-level
  에서는 생략된다.

검증 묶음: `env TEST_TIMEOUT=10m make test` — 이 저장소의 정본 명령이다
(`~/.config/bdui/config.toml` `[worker.verify]` 실측; 자동 감지 `go test ./...` 는
ICU cgo 빌드로 실패한다).

## 7. 소비자 정합 — beads-ui 코드 변경 0줄

beads-ui 는 이 수정 후 코드 변경 없이 정합된다. 근거:

- `normalizeIssueList`(`server/list-adapters.js:100`)가 `...it` 스프레드로 원본
  필드를 보존한다.
- 보드 `IssueLite` typedef(`app/views/board/index.js:11`)에 `parent` 가 이미 있다.
- `parentIdOf`(`:1151`) → `excludeFolded`(`:1170`) 와
  `rebuildChildrenIndex`(`:334`) 가 그 키를 바로 읽는다.

이 주장은 통합 실측으로 확인한다(§8). 실측이 실패하면 소비자 측 원인을 따로
진단하고, 이 스펙의 §7 을 정정한다.

## 8. 배포와 실측

1. `make install-force` — 빌드 + `~/.local/bin/bd` 교체. 멱등.
2. bdui 서버 재시작 **불필요** — 서버가 요청마다 `bd` 를 spawn 한다
   (`~/.config/bdui/config.toml` `[worker.deploy."…/External/beads"]` 주석 실측).
3. CLI 실측 — TRACE-ICI 에서 `bd ready --explain --json` 의 `9xp.6/.7/.8` 에
   `parent: "TRACE-ICI-9xp"` 가 나오는지.
4. 소비자 실측 — 브라우저 보드탭에서 세 건이 Blocked 레인에서 사라지고 `9xp` 카드
   rollup 총계가 5 → 8 이 되는지. §7 의 "0줄" 주장은 이 관측으로만 성립한다.
5. 플릿 릴리스(태그 + `dependency.yaml` 핀 범프)는 별도 수동 레그이며 이 단위
   범위 밖이다.

## 9. 관측 잔여 (이 단위 범위 밖)

- `ReadyItem.Parent`(`types.go:1050`) 는 embed 된 `Issue.Parent` 를 shadow 하는
  중복 필드다. `bd ready --explain` 의 ready 배열은 `allDeps` 순회로,
  `bd ready` 는 SQL 조인으로 같은 값을 서로 다르게 계산한다. 저장소에 parent 계산
  경로가 셋(이 스펙이 쓰는 `parent-child` 엣지 배치 조회 포함)이 되는 셈이며,
  단일화는 별도 단위로 판단할 사안이다. Bead 는 만들지 않는다.
