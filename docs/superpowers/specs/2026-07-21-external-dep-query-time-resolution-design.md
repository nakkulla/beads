# bd external 의존 쿼리 시점 해석·차단 설계 (beads-19q)

- Bead: `beads-19q` — "bd external: 의존 쿼리시점 해석·차단 구현 — repair-v1.1.0 기반"
- 기반: repair-v1.1.0 브랜치 `7c87e51d75e0aa8f35459a35ce8d345136d94b6f`
- 작성일: 2026-07-21
- 사용자 확정 사항: 해석 불가 시 **fail-closed** / 프로젝트→DB 매핑은 **명시적 config** / 구현은 **접근안 A(사전 해석 + SQL 제외 조건 주입)**

## 1. 배경과 실측 사실

`external:<project>:<capability>` 의존은 현재 저장·문법검증·`bd ship` 라벨링까지만 존재하고,
쿼리 시점 해석·차단이 미구현이다. 이번 실측(repair-v1.1.0 기준)으로 확인한 사실:

- `bd ready`는 비정규화 칼럼 `issues.is_blocked = 0`만 필터한다. 구현 기준
  repair-v1.1.0에서 이 조건은 공유 SQL 빌더 `internal/storage/sqlbuild/ready.go:96`
  (`BuildReadyWorkWhere`)에 있고, `issueops`(`ready_work.go:43`)와
  `domain/db`(`ready_work.go:31`) 두 스택이 모두 이 빌더를 사용한다. external
  타깃은 direct 마킹(`internal/storage/issueops/dependencies.go:278`의
  `default: return nil`)뿐 아니라 재계산 패스
  (`internal/storage/issueops/blocked_state.go`)에도 분기가 없어, external 의존만
  있는 이슈는 어떤 쓰기 경로로도 `is_blocked=1`이 되지 않는다.
  → external blocking 의존이 걸린 이슈가 오늘도 `bd ready`에 노출된다.
- `bd ship`(`cmd/bd/ship.go`)은 로컬 스토어에 `provides:<capability>` 라벨을 쓰는 것이
  전부이며, 이 라벨을 읽는 소비자는 코드 전체에 없다. `ResolveExternalProjectPath`
  (`internal/config/config.go:799-830`)는 프로덕션 호출자 0.
- 크로스 DB 조회는 기술적으로 실증돼 있다: `internal/doltserver/doltserver.go:986-1035`
  `FlushWorkingSet`이 단일 커넥션에서 `` `<db>`.dolt_status `` qualified query를 수행한다.
  중앙 dolt 단일 서버(shared server) 환경에서 프로젝트별 DB는 같은 서버에 공존한다.
- 표시 누락 원인: `GetDependenciesWithMetadataInTx`가 issues/wisps만 hydrate하고
  external 행을 조용히 drop한다(`internal/storage/issueops/dependencies.go:901-902`,
  함수는 `:853`).
  `bd show`·단일 ID `bd dep list`·`--direction=up`에서 재현되고, `bd dep tree`와
  배치 다중 ID down 경로는 external을 표시한다. counts 경로
  (`GetDependencyCountsInTx`, `internal/storage/dolt/counts.go:88-104`)는 external을
  포함해 세므로 목록과 개수가 어긋난다.
- bead 원문이 참조한 `blocked_issues_cache`는 SQLite 백엔드와 함께 제거된 테이블이다
  (현행 문서 `docs/INTERNALS.md:184-232`는 stale). 현재 메커니즘은 쓰기 시점 동기
  재계산이며 TTL 캐시는 존재하지 않는다.
- ready 진입점은 하나가 아니다: `cmd/bd/ready.go`는 `GetReadyWork`(260·270행,
  499행은 claim 흐름)와 별도 counts 진입점 `GetReadyWorkWithCounts`(233·242행,
  구현 `internal/storage/issueops/ready_work_counts.go:13`)를 함께 쓰고,
  dolt/embedded/domain-db 스토어와 proxied-server 모드가 각각 이 진입점들을
  노출한다. WHERE 절 자체는 `sqlbuild.BuildReadyWorkWhere`로 수렴하며, 사전 계산
  ID 집합을 전달하는 `sqlbuild.ReadyWorkWhereInputs`(`ready.go:74`)가 이미
  존재한다.

## 2. 목표

1. blocking 타입 external 의존이 `bd ready`에서 쿼리 시점에 실제로 차단된다.
   충족 판정: 대상 프로젝트 DB에 `provides:<capability>` 라벨을 가진 **closed** 이슈가
   존재하면 satisfied (`ship.go` docstring 준수 — `--force`로 open 이슈에 ship한 경우
   closed 전까지 미충족).
2. `bd show`·`bd dep list`(단일 ID·`--direction=up`)에서 external 엣지 표시를 복원해
   counts와 목록의 정합을 회복한다.
3. 해석 불가(매핑 누락, DB 부재, 쿼리 오류, 비서버 모드)는 **fail-closed**:
   차단 유지 + 사유 보존. 오타·설정 누락이 조용히 통과하는 일이 없어야 한다.

## 3. 비목표

- stored `is_blocked`는 변경하지 않는다(bead 원칙: 원격 ship 시 로컬 unblock 트리거
  부재 문제를 쿼리 시점 해석으로 회피).
- TTL/영속 캐시를 만들지 않는다. `bd ready`는 1회성 CLI 호출이므로 호출 내
  메모이제이션으로 충분하다. bead 원문의 `blocked_issues_cache` 패턴 참조는 제거된
  테이블에 대한 stale 참조로 판단한 의도적 델타.
- `checkBeadGate`(`cmd/bd/gate.go:836-844`, 죽은 multi-rig 유물)는 external 의존과
  무관한 별개 개념이므로 수정하지 않는다.
- `bd blocked` 등 다른 명령의 external 인지 확장은 하지 않는다.
- upstream(steveyegge/beads) PR은 만들지 않는다. fork(nakkulla/beads)의
  repair-v1.1.0 라인 전용.

## 4. 설계

### 4.1 설정: `external_databases`

`.beads` config에 새 YAML 맵을 추가한다.

```yaml
external_databases:
  beads-ui: beads_ui
  dotfiles: dotfiles
```

- 의미: `external:<project>:<capability>`의 `<project>` → 중앙 dolt 서버의 DB 이름.
- 기존 `external_projects`(이름→경로)는 건드리지 않고 미사용 상태로 둔다.
- config 키 레지스트리(`cmd/bd/config.go`), YAML dotted-key 처리
  (`internal/config/yaml_config.go`), `bd config show` 표시(`cmd/bd/config_show.go`)에
  함께 등록한다.
- 매핑에 없는 프로젝트는 unresolvable → fail-closed.
- in-repo 문서 정합화도 구현 범위다: `docs/CONFIG.md`에 `external_databases` 키와
  쿼리 시점 게이팅·fail-closed 동작을 문서화한다. `cmd/bd/dep.go`의 기존 help text
  ("block the issue until the capability is shipped")는 이 구현으로 비로소 사실이
  되므로 수정 불요.

### 4.2 해석기

`internal/storage/issueops`에 헬퍼를 추가한다(예: `external_resolution.go`).

- 입력: tx(또는 커넥션), distinct external ref 목록, 그리고 스토리지 어댑터가
  **명시적으로 전달하는 resolver options** — `serverMode`(현재 스토어의 dolt_mode가
  server 계열인지)와 `external_databases` 매핑. 두 backend가 같은 `DBTX` 형태를
  쓰므로 모드 판별을 커넥션에서 추론하지 않는다.
- 출력: `map[ref] → {satisfied | unsatisfied | unresolvable(reason)}` + 구조화된
  diagnostics(프로젝트별 사유·ref 목록 — §4.3의 UX·diagnostics 계약 참조).
- 비서버 모드: 쿼리를 시도하지 않고 전 ref를 unresolvable("server mode required")로
  처리한다(fail-closed 확정 요구사항 보장).
- DB 이름 안전성: 매핑 값은 공유 식별자 검증기로 MySQL 식별자 유효성을 확인한 뒤
  backtick quote해 사용한다. 검증 실패 값은 unresolvable 처리한다.
- 같은 프로젝트의 여러 capability는 IN 절로 묶어 **프로젝트당 크로스 DB 1쿼리**.
  쿼리 형태(개념): 대상 DB의 issues·라벨 테이블을 조인해
  `status='closed' AND label IN ('provides:<c1>', ...)`인 라벨 집합을 가져온다.
- 판정 규칙:
  - 매핑 존재 + 쿼리 성공 + closed 이슈에 라벨 존재 → satisfied.
  - 매핑 존재 + 쿼리 성공 + 라벨 부재(또는 open 이슈에만 존재) → unsatisfied.
  - 매핑 누락 / 매핑 값 식별자 검증 실패 / DB 부재 / 쿼리 오류 / 비서버 모드 →
    unresolvable(사유 보존).
  - ready 필터 관점에서 unsatisfied와 unresolvable은 동일하게 차단(fail-closed),
    단 사유는 구분 보존해 UX 경고에 사용한다.
- 호출 내 메모이제이션: 한 top-level ready 호출 안에서 같은 ref를 재해석하지
  않는다. counts 재조회 등 명령 내 반복 호출의 경고 중복 방지는 §4.3의
  diagnostics 계약(cmd 계층 dedup)이 담당한다.

### 4.3 ready 경로 통합 (접근안 A)

해석은 **top-level ready 호출당 1회** 수행하고, 결과를 공유 SQL 빌더 입력으로
주입한다. 구현 기준(repair-v1.1.0) 호출 그래프에 맞춘 통합 지점:

1. top-level ready 진입점 — `GetReadyWork`·`GetReadyWorkWithCounts`
   (dolt/embedded/domain-db 스토어, proxied-server 모드, `cmd/bd/ready.go:499`의
   claim 흐름 포함) — 마다 해석 패스를 1회 실행한다: 로컬 1쿼리로 blocking 타입
   external ref의 distinct 목록을 수집하고(blocking 타입 집합은 `blocked_state.go`와
   동일 상수 공유), 해석기를 호출해 미충족(unsatisfied + unresolvable) ref 목록을
   얻는다.
2. 미충족 ref 목록을 `sqlbuild.ReadyWorkWhereInputs`의 새 필드로 전달하고,
   `BuildReadyWorkWhere`(`sqlbuild/ready.go:86`)가 `NOT EXISTS` 조건을 렌더링한다
   (개념):

   ```sql
   AND NOT EXISTS (
     SELECT 1 FROM dependencies d
     WHERE d.issue_id = issues.id
       AND d.type IN (<blocking types>)
       AND d.depends_on_external IN (<미충족 ref 목록>)
   )
   ```

   공유 빌더 주입이므로 issueops·domain/db 두 스택과 counts 경로가 동일 조건을
   얻고, LIMIT/정렬 정합성이 유지된다. ref가 하나도 없으면 필드를 비워 predicate
   미주입(오버헤드 0).
3. wisp ready 경로(`getReadyWispsInTx`, `ready_work.go:155`; wisp 재확인 쿼리
   `:387`)에도 같은 미충족 ref 목록으로 `wisp_dependencies.depends_on_external`
   대상 동일 predicate를 적용한다.

**UX·diagnostics 계약**: 해석 결과의 unresolvable 사유는 구조화된 diagnostics로
top-level 호출자까지 반환한다 — 스토리지 계층은 stderr에 직접 쓰지 않는다.
`cmd` 계층이 **명령 실행당 프로젝트별 최대 1줄**의 stderr 경고를 출력한다:
`bd ready`가 counts 재조회 등으로 해석을 반복해도 경고는 중복되지 않고,
`--json` 출력(stdout)은 오염되지 않는다. 정상 미충족(unsatisfied)은 조용히
제외한다(그것이 게이트의 정상 동작이므로).

### 4.4 표시 복원

`GetDependenciesWithMetadataInTx`(`internal/storage/issueops/dependencies.go:853`)가
external 행을 drop하지 않고 합성 엔트리로 반환한다:

- ID = external ref 문자열, dependency_type = 엣지의 타입 유지. 로컬에 존재하지 않는
  대상이므로 status/title 등은 비우거나 external임을 나타내는 최소 표현만 채운다.
- `bd show`·단일 ID `bd dep list`·`--direction=up` 표시가 복원되고 counts와 정합.
- `bd show`에서 크로스 DB 충족 조회는 하지 않는다(충족 판정은 ready 전용 —
  show가 중앙 서버 가용성에 종속되지 않게).
- `bd show --json`에는 additive 변경. dotfiles 쪽 readback 파서 영향이 없는지
  구현 중 확인 항목으로 포함한다.

### 4.5 리스크

- **tx 내 크로스 DB SELECT의 Dolt 동작**: qualified query 실증(`FlushWorkingSet`)은
  tx 밖 단일 커넥션 사례다. 구현 초기에 tx 내 동작을 통합 테스트로 먼저 검증하고,
  막히면 해석기를 tx 밖 별도 커넥션으로 우회한다(해석은 읽기 전용이라 tx 일관성
  요구가 낮다).
- **비서버 모드**: embedded/owned 모드는 resolver options의 `serverMode`로 명시
  판별해 쿼리 시도 없이 전부 unresolvable → 차단된다(커넥션에서 추론하지 않음).
  fail-closed 정책의 의도된 결과이며, 사용자 확정 사항(2026-07-21). 경고 사유는
  "server mode required".

## 5. 테스트·수용 기준

- 단위: ref 파싱·매핑 조회·판정 상태머신(4.2의 판정 규칙 전 분기 — 비서버 모드
  전 ref unresolvable, 매핑 값 식별자 검증 실패 unresolvable 포함).
- 통합(`BEADS_TEST_EMBEDDED_DOLT=1` 포함): 한 서버에 2 DB 픽스처 —
  - provides 라벨 + closed → ready 노출, 라벨 부재/open → 제외.
  - 매핑 누락 → fail-closed 제외 + stderr 경고.
  - wisp 경로 동일 동작.
  - LIMIT 정합(미충족 이슈가 LIMIT 안에서 제외돼도 개수 유지).
  - ready 진입점 변형 열거: classic `GetReadyWork` plain/counts,
    proxied-server 모드 plain/counts, claim 흐름(`cmd/bd/ready.go:499`) 각각에서
    external 차단이 적용됨.
  - 경고 계약: counts 재조회를 포함한 한 번의 `bd ready` 실행에서 프로젝트별
    stderr 경고 최대 1줄(중복 없음), `--json` stdout 무오염.
  - 표시 복원: `bd show`/`dep list` external 엣지와 counts 정합.
- 빌드: `make build`(gms_pure_go) + 수리 동봉 테스트 전체 green.
- 설치 게이트: 스크래치 워크스페이스 auto-import 회귀 실측 통과 전
  `make install-force` 금지(1.0.4 계열 auto-import/JSONL clobber 회귀 방지).
- 라이브 수용(설치 후): dotfiles에서 `external:beads-ui:plan-review-runner-authz`
  의존이 걸린 Bead가 `bd ready`에서 제외되고, beads-ui에서 `bd ship` 후 재노출.

## 6. 브랜치·전달·롤아웃

- repair-v1.1.0(`7c87e51d`) 기반 feature 브랜치에서 구현, origin(nakkulla/beads)의
  repair-v1.1.0 대상 PR. fork main 반영 여부는 별도 판단으로 남긴다.
- 롤아웃: 기존 플릿 레포의 `external_databases` 등록은 운영 단계로 수행
  (`scripts/nas-dolt-server/repo-db-registry.yaml`을 소스로 수동 작성).
- 후속(cross-workspace, admission=user_request): dotfiles 워크스페이스의 스킬 정합화
  follow-up bead **dotfiles-5geh** 생성 완료(2026-07-21, status=blocked) —
  ① `bd-usage`에 external 의존 쿼리 시점 게이팅(fail-closed)·`external_databases`·
  `provides:`(closed) 충족 판정 의미론 문서화, ② `bd-setup`/`bd-recover`에 신규 레포
  온보딩 시 `external_databases` 등록 절차 추가, ③ dotfiles 프록시 게이트
  Bead(dotfiles-ua03) 전환/해제 검토. 착수 조건: 본 기능 머지 + auto-import 회귀
  게이트 통과 + `make install-force` 완료 후(스킬 문서는 설치된 bd의 현재 동작을
  기술하므로). 이 스펙의 구현 범위에는 포함하지 않는다.
