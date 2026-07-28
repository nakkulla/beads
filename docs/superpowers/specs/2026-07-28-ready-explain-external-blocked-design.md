# bd ready --explain: external-unsatisfied 이슈 blocked 배열 포함

- Bead: beads-rhw
- 날짜: 2026-07-28
- 선행 스펙: `2026-07-21-external-dep-query-time-resolution-design.md` (external 의존 쿼리 시점 게이팅)

## 1. 문제

open 상태이면서 유일한 blocker가 미충족 `external:<project>:<capability>` 의존인 이슈는:

- `bd ready`에서 SQL WHERE로 제외된다 (`internal/storage/sqlbuild/ready.go:183-201`, 의도된 동작).
- 저장 플래그 `is_blocked`는 external 타깃에 대해 절대 세워지지 않는다
  (`markBlockedTemplateForIssues`는 `depends_on_external`을 검사하지 않음;
  `sqlbuild/ready.go:84-85` 주석으로 명문화).
- `bd ready --explain`의 `blocked` 배열은 `is_blocked=1` 후보에서만 조립되므로
  (`internal/storage/issueops/blocked.go:245-248`) 이런 이슈는 조립 전에 탈락한다.

결과: `--explain` 출력의 `ready`에도 `blocked`에도 없는 사각지대가 생긴다.
소비자(beads-ui 보드)는 Blocked 컬럼을 stored `status=blocked` + `--explain`
`blocked` 배열로 구성하므로, 해당 이슈가 보드의 모든 컬럼에서 사라진다.
발견 경위: beads-ui `UI-hs11`에 `external:dotfiles:revise-disposition-contract`
의존 결선 직후 보드에서 소실.

추가로, 로컬 blocker와 external ref를 동시에 가진 이슈는 blocked 배열에는
있지만 `blocked_by`에서 external ref가 누락된다.

## 2. 목표 / 비목표

목표:

- `bd ready --explain`의 `blocked` 배열이 external-blocked 이슈(와 wisp)를
  포함한다. 포함 기준은 ready 제외에 이미 쓰이는 **union** —
  unsatisfied(정상 미충족) + unresolvable(fail-closed: 매핑 미등록·DB 접근
  불가·비서버 모드) — 과 동일하다. "(ready에 없음) ∧ (후보 status) ⇒ (blocked에
  있음)" 일관성이 성립한다.
- 로컬+external 혼합 이슈의 기존 blocked 엔트리에 external ref를 merge한다.

비목표:

- `bd ready`(비-explain), `bd list --status blocked`의 동작 변경 없음.
- JSON 스키마 필드 추가·`schema_version` 변경 없음.
- `is_blocked` 쓰기 시점 의미 변경 없음 (external 충족 상태는 다른 DB에서
  변하므로 저장하면 stale — 쿼리 시점 해석 원칙 유지).
- unsatisfied vs unresolvable의 출력상 구분 없음 (진단은 기존 stderr 경고 유지).
- dotfiles `bd-usage.md`의 "excluded silently" 문구 정정은 계약 소유가
  dotfiles이므로 범위 밖 — follow-up으로 기록 (§6).

## 3. 설계

### 3.1 신규 storage 헬퍼

`internal/storage/issueops/`에 신규 파일(예: `external_blocked.go`):

- `GetExternallyBlockedInTx(ctx, tx, unsatisfiedRefs []string)` —
  `dependencies`·`wisp_dependencies` 양쪽에서
  `type IN ('blocks','conditional-blocks') AND depends_on_external IN (refs)`인
  이슈/wisp를 수집해 `issueID → []ref` 맵과 이슈 본체를 반환한다.
- status 필터는 **ready 후보 status 집합과 동일**하게 적용한다(닫힘·resolved 등
  ready 후보가 아닌 status는 제외). 기존 ready 쿼리가 쓰는 술어 상수를
  재사용한다.
- ref 배치는 기존 `QueryBatchSize` 규약을 따른다.
- ref 집합은 호출자가 `ResolveReadyExternalBlocksInTx`(union 반환,
  `external_resolution.go:235-248`)에서 얻어 전달한다 — 이 헬퍼는 재해석하지
  않는다.

### 3.2 --explain 조립 합류

`cmd/bd/ready.go` `runReadyExplain`(493-618)에서 `GetBlockedIssues` 결과에
합류한 뒤 `BuildReadyExplanation`을 호출한다:

- blocked 목록에 없는 이슈 → `types.BlockedIssue{BlockedBy: []string{ref...}}`
  로 append.
- 이미 있는 이슈(로컬 blocker 동시 보유) → 해당 엔트리 `BlockedBy`에 ref를
  append (중복 없이).
- `BuildReadyExplanation`·`BlockedItem`·`BlockerInfo` 타입은 무변경. external
  ref는 blockerMap 미스로 기존 missing-blocker 경로(`explain_test.go:154-176`
  선례)대로 **ID-only `BlockerInfo{ID: "external:<project>:<capability>"}`**
  로 렌더된다 — Title/Status/Priority 날조 없음(`NewExternalDepEntry` 관례와
  일치).
- summary 카운트는 합류된 입력으로 계산되므로 자동 정합.
- 텍스트 모드 `--explain`도 같은 데이터로 렌더 — 별도 처리 없음.
- 저장소 스택 패리티: proxied-server 스택(`domain/db`)의 `--explain` 경로도
  같은 합류를 적용한다 (`external_resolution.go:233-234` 패리티 주석 준수).

## 4. 소비자 영향

- beads-ui `server/list-adapters.js` `extractBlockerIds`는 `entry.id`를 읽으므로
  ID-only 엔트리를 그대로 소화한다(실측 확인). Blocked 컬럼에
  `external:...` blocker 칩과 함께 복귀. beads-ui 코드 변경 불요.

## 5. 검증 (acceptance)

1. external-only open 이슈가 `--explain` `blocked`에 ID-only blocker로 등장.
2. 로컬+external 혼합 이슈의 `blocked_by`에 external ref가 merge되어 등장.
3. 비서버 모드/매핑 미등록(fail-closed)에서도 1이 성립 (union 의미론).
4. wisp에도 1이 성립.
5. 충족된(provides 라벨 closed) ref는 blocked에 나타나지 않고 ready 복귀.
6. `bd ready` 비-explain 출력·`bd list --status blocked`·`schema_version` 불변.
7. 기존 explain 단위/통합 테스트 전부 green + 신규 케이스(1-5) 추가
   (`internal/types/explain_test.go`는 무변경 통과가 기대치 — 타입 무변경이므로).

## 6. Follow-up

- dotfiles `bd-usage.md` external 게이팅 문단의 "excluded silently"에
  `--explain` blocked 표시를 반영하는 1줄 정정 — dotfiles 워크스페이스 별도
  Bead로 라우팅.
