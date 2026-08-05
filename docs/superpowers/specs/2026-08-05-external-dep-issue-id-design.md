# bd external 의존 이슈 ID 전환 설계 (beads-o53)

- Bead: `beads-o53` — "bd external 의존을 capability 문법에서 이슈 ID 기반으로 전환"
- 작성일: 2026-08-05
- 사용자 확정 사항: capability 문법 **YAGNI 제거** / external 의존 = **맨 크로스-프리픽스 이슈 ID** / 수동 `external_databases` 등록 폐기, **서버 자동 발견** / **fail-closed 유지** / `bd dep add` **쓰기 시점 실존 검증** / ship 제거는 **소비자 선행 순서**(beads-ui → dotfiles → External/beads) / 유닛 분해는 **3개 split Bead**
- 실행 범위: 이 스펙의 구현 대상은 **External/beads 레포 변경만**이다. 소비자 유닛(beads-ui `UI-knf8`, dotfiles `dotfiles-jvhr`)의 실행 범위는 각 Bead가 소유하고, 이 문서는 전체 설계와 랜딩 순서의 canonical 출처다.

## 1. 배경과 실측 사실 (2026-08-05)

- **사건**: beads_ui DB의 `UI-kfl4`가 `depends_on_external='dotfiles-1tif'`(맨 크로스-프리픽스 ID)를 갖고 있었고, 해석기는 `external:<project>:<capability>` 문법만 이해하므로 malformed → fail-closed **영구 block**. 전제 `dotfiles-1tif`가 closed여도 해제 불가였다. (해당 레코드는 즉시 조치로 제거 완료 — `bd dep remove UI-kfl4 dotfiles-1tif`, readback `dependency_count=0`, ready 복귀 확인.)
- **write/read 비대칭이 근본 결함**: 쓰기 경로 `internal/storage/issueops/dependencies.go:56`(`ClassifyDepTarget`)은 cross-prefix ID를 external로 분류해 존재 검증 없이(`:151` 스킵) `depends_on_external`에 저장하지만, 읽기 경로 `internal/storage/issueops/external_resolution.go:59`(`parseExternalRef`)는 `external:` 접두 3분절 문법만 수용한다.
- **capability 판정식**: 대상 DB에 `provides:<capability>` 라벨을 단 closed 이슈가 존재해야 충족(`external_resolution.go:196` `queryProvidedLabels`). 충족의 전제인 `bd ship`(공급 이슈에 `provides:` 라벨 부착, `cmd/bd/ship.go`)은 수동 절차다.
- **중앙 DB 전수 실측**: `depends_on_external` 레코드는 6건. 맨 ID 1건(`UI-kfl4`→`dotfiles-1tif`, 제거됨), capability 문법 5건 — `UI-lhwp`→`external:dotfiles:repo-base-declaration`, `UI-4ii4`→`external:dotfiles:ship-close-choreography`, `dotfiles-w1la`→`external:beads-ui:plan-review-runner-authz`, `dotfiles-pjm8`→같은 것 + `external:beads-ui:zz-live-probe-unshipped`. **보유 이슈 4건 전부 closed** → 5건 모두 비활성(ready/blocked 판정은 open 이슈에만 작용).
- **자동 발견 실증**: 각 beads DB는 중앙 dolt 서버(127.0.0.1:13307)의 자기 `config` 테이블에 `issue_prefix`를 저장한다(실측: `beads_ui.config` → `issue_prefix=UI`; bd rig 생성 시 자동 기록). `SHOW DATABASES` + 각 DB의 `issue_prefix` 조회만으로 prefix→DB 맵이 완성된다. 크로스 DB 단일 커넥션 qualified query는 `internal/doltserver/doltserver.go:986-1035`에서 이미 실증돼 있다.
- **ID 문법 실측**: 이슈 ID는 `<prefix>-<hex 6-8>`(`internal/types/id_generator.go:11-41`), 자식은 `<parent>.<n>`(`:43-50`, 최대 깊이 3), wisp/mol은 `<prefix>-wisp-<hex>`/`<prefix>-mol-<hex>`(`internal/types/types.go:1471-1477`).
- **CLI 표면 불일치(부수 결함)**: `bd dep list <id> --json`이 external 엣지를 누락한다(실측: UI-kfl4에서 `[]` 반환, `dependency_count=1`과 모순) — 진단을 어렵게 만든 원인.
- **provides:/ship 실사용**: 활성 소비 0건. 유일한 만족 라벨 3건(`dotfiles-0gpe`, `dotfiles-28dy`, `UI-19yr`)도 전부 닫힌 소비자만 가리킨다.

## 2. 목표

1. external 의존을 맨 크로스-프리픽스 이슈 ID로 통일하고, 충족 판정을 "대상 DB에서 그 이슈의 `status='closed'`"로 교체한다. query-time 해석 구조(beads-19q; 저장 플래그 없음, `bd ready`마다 재판정)는 유지한다.
2. prefix→DB 해석을 서버 자동 발견으로 전환하고 `external_databases` config를 제거한다.
3. `bd dep add` 쓰기 시점에 대상 이슈 실존을 검증해 해석 불가능한 레코드 생성을 원천 차단한다.
4. `bd ship`·`provides:` 판정·`external:` 문법을 제거한다.
5. `bd dep list --json`의 external 엣지 누락을 수정한다.

## 3. 비목표

- capability 모델의 재도입 또는 이중 지원 (사용자 확정: YAGNI 제거)
- wontfix/중복 closed와 정상 closed의 구분 (단일 사용자 플릿에서 전제 이슈가 wontfix로 닫히면 의존자 스펙도 재검토 대상 — 판정식 단순성 우선)
- 비서버(로컬 dolt) 모드에서의 external 해석 (현행 fail-closed 유지)
- doctor의 cross-DB 실존 검사 확장 (현행 external 제외 규칙 유지: `cmd/bd/doctor/validation.go:57-64`, `deep.go:201`, `fix/validation.go:38-45`)
- federation·upstream(gastownhall/beads) 호환성 유지 — 이 포크 전용 설계
- 크로스 DB **wisp** 참조 지원: external ID 조회는 대상 DB `issues` 테이블만 본다. wisp ID가 external로 참조되면 미발견 → unsatisfied(fail-closed 관성). 필요해지면 별도 유닛.
- `website/versioned_docs/version-*` 과거 스냅샷 수정 (히스토리 보존)

## 4. 설계

### 4.1 문법과 저장

- external 의존의 유일한 문법은 **맨 이슈 ID**(예: `dotfiles-1tif`, `beads-o53.1`)다. 저장 칼럼은 기존 `depends_on_external` 그대로(스키마 변경 없음), 값이 ID로 통일된다.
- **cross/own 분류 규칙 교체**: 현행 분류는 `types.ExtractPrefix`(첫 하이픈까지, `internal/types/id_generator.go:92-100`) 비교(`internal/storage/dolt/dependencies.go:13-16` `isCrossPrefixDep`)라서 하이픈 포함 prefix(예: `team-alpha` vs `team-beta`)를 같은 prefix로 오분류한다. 신 규칙: 서버 모드에서는 대상 ID를 **{자기 `config.issue_prefix`} ∪ 발견된 prefix 집합**과 `startswith(prefix + "-")` 최장 일치로 대조해, 최장 일치가 자기 prefix면 일반 FK 경로·아니면 external 경로로 분류한다. 비서버 모드는 `startswith(자기 prefix + "-")` 폴백(비서버 cross-prefix 쓰기는 4.3에서 거부되므로 오분류 창이 없다). `ExtractPrefix` 기반 분류 호출처는 전부 이 규칙으로 교체한다.
- `external:` 접두 입력은 `bd dep add`에서 **에러**로 거부하고 "대상 이슈 ID를 직접 지정하라"는 안내를 출력한다. `validateExternalRef`/`IsExternalRef`(`cmd/bd/dep.go:1492-1517`)는 이 거부 검사로 교체된다.

### 4.2 해석기 (query-time, 자동 발견)

- **판정식**: 대상 DB에서 `SELECT status FROM <db>.issues WHERE id=?` → `closed`면 satisfied. `resolved`는 미충족(PR 배달·미머지 상태 — 머지 완료가 의존 해소의 바다). open/blocked/기타 전부 미충족.
- **자동 발견**: 명령 실행당 1회, blocking external ref가 존재할 때만 수행(현행 `collectBlockingExternalRefs` 게이트 유지).
  1. `information_schema`로 `config` 테이블을 가진 DB 열거 (또는 `SHOW DATABASES` 후 개별 probe — 구현 선택; DB명은 기존 `externalDBNameRE` allowlist로 검증)
  2. 각 DB에서 `` SELECT value FROM `<db>`.config WHERE `key`='issue_prefix' `` → prefix→DB 맵 구성
  3. 자기 DB는 맵에서 제외(자기 prefix는 애초에 external로 분류되지 않음)
- **prefix 매칭**: ref ID를 발견된 prefix 집합과 `id가 prefix + "-"로 시작` 조건으로 대조, 복수 일치 시 **최장 일치**(하이픈 포함 prefix 안전). 자식 ID(`.n`)·wisp형 ID는 변형 없이 **전체 ID 그대로** 대상 DB에 조회한다.
- **fail-closed 유지** (unsatisfied와 unresolvable은 동등하게 차단, `ExternalResolverOptions.DiagSink` 진단 구조 유지):
  - 미지 prefix(발견 맵에 없음) → unresolvable, 진단 `unknown prefix`
  - **중복 prefix**(서로 다른 두 DB가 같은 `issue_prefix`) → 그 prefix를 쓰는 ref 전부 unresolvable, 진단 `ambiguous prefix (<db1>, <db2>)`
  - 비서버 모드 → 전부 unresolvable(현행 `server mode required` 유지)
  - 발견 쿼리·대상 조회 실패 → unresolvable, 사유 보존
  - 대상 DB에 그 ID 없음 → unsatisfied(쓰기 검증 도입 후엔 오타 레코드가 새로 생기지 않지만, 판정은 여전히 보수적으로)
- **쿼리 비용**: 발견(±1 information_schema + DB당 1) + 프로젝트당 IN 절 1쿼리(현행 구조 계승 — capability 라벨 IN 대신 이슈 ID IN, `status='closed'`인 ID 집합 반환). CLI는 단명 프로세스이므로 실행당 1회 캐시로 충분하다.
- `ExternalResolverOptions.Databases`(명시 매핑)는 발견 결과로 대체·제거된다. `ServerMode` 게이트는 유지.

### 4.3 쓰기 검증 (`bd dep add`)

- cross-prefix ID 추가 시(서버 모드): 발견 맵으로 대상 DB를 해석하고 **그 이슈의 실존을 조회**한다. 미존재·미지 prefix·중복 prefix면 거부(에러에 사유 명시). 오타가 쓰는 순간 드러난다.
- 비서버 모드에서 cross-prefix 추가는 **거부**한다(해석 불가능한 레코드의 신규 생성 차단). 기존 저장 레코드의 판정은 해석기 몫(위 4.2).
- 대상 이슈의 status는 쓰기 시점에 검사하지 않는다(open 대상에 의존을 거는 것이 정상 사용).
- `bd dep remove <id> <foreign-id>`는 현행대로 동작(실측 검증됨). proxied-server 경로(`cmd/bd/dep_proxied_server.go:140,209,262,391`)도 동일 규칙으로 정합한다.

### 4.4 ship/capability 철거 (External/beads 내부)

제거·교체 대상 (2026-08-05 전수 수집):

- `cmd/bd/ship.go` 전체 삭제 + 루트 커맨드 등록 해제
- `cmd/bd/label.go:105-106`, `:297-298` — `provides:` 예약 가드 삭제(일반 라벨로 환원)
- `cmd/bd/dep.go:1492-1517` — `validateExternalRef`/`IsExternalRef` → `external:` 거부 + ID 검증으로 교체; `:249` help 문구 갱신
- `internal/storage/issueops/external_resolution.go` — `parseExternalRef`·`refByCapLabel`·`queryProvidedLabels`를 ID 발견·판정 구현으로 교체
- `internal/config/config.go:269,274,806-815`, `internal/config/yaml_config.go:97` — `external_databases` 기본값·getter·YAML prefix 등록 제거
- `cmd/bd/config.go` `recognizedConfigPrefixes`의 `"external_databases."` 항목, `cmd/bd/config_show.go:124-171` 컨테이너 목록의 `"external_databases"` — CLI 설정 표면에서 제거(`bd config set external_databases.<name> ...`가 unrecognized로 거부되게)
- 표시·그래프 경로의 `external:` 접두 분기(`cmd/bd/dep.go:333,633,879,942`, `swarm.go:291`, `graph.go:332,405`, `show_format.go:150`, `ready.go:29`) — 맨 ID 표시로 정리(external 마커는 "cross-prefix" 판정으로 대체)
- 스토리지 계층 `HasPrefix("external:")` 분기(`dependencies.go:56,121,151,849,922`, `domain/db/dependency.go:42`, `dolt/transaction.go:671`, `dolt/dependencies.go:33,181`, `dolt/wisps.go:734`) — cross-prefix 분류만 남기고 `external:` 문자열 분기 제거
- `cmd/bd/info.go:1111` 배너, `docs/CLI_REFERENCE.md`·`docs/CONFIG.md:234-271`·`docs/DEPENDENCIES.md:152`·`website/docs/**`(현행 버전만), `plugins/beads/skills/beads/resources/MOLECULES.md:243-357` — 신 문법으로 갱신
- 스키마 마이그레이션(`schema/cli_migrations.go:79`, `migration_repairs.go:121`)은 과거 데이터 호환 코드이므로 **유지**
- 버전 문자열 bump(`1.1.0-fork.1` → 다음 포크 버전)

### 4.5 CLI 표면 정합

- `bd dep list <id> --json`이 external 엣지를 포함하도록 수정(counts와 목록 정합 — beads-19q U2가 표시 경로를 복원했으나 `--json` 단일 ID 경로가 여전히 누락).

## 5. 크로스 레포 유닛과 랜딩 순서 (ledger)

| 순서 | 유닛 | Bead | 내용 |
|---|---|---|---|
| ① | beads-ui | `UI-knf8` (split) | 머지 클릭 choreography의 ship 단계 폐지 — `server/worker/ship-capabilities.js`·`bd-metadata.js ship()`·`pr-actions.js CLEANUP_STEPS` 마지막 단계·`running-grid.js` 배너·관련 테스트. 상세는 Bead 기재 |
| ② | dotfiles | `dotfiles-jvhr` (split) | `workflow.yaml` sweep_order 9단계(`ship_exported_capabilities`) 폐지 + checker + 계약 테스트 + bd-usage/bd-setup/bd-recover/finishing/pr-finish 문서의 external 절 개정. 상세는 Bead 기재 |
| ③ | External/beads | `beads-o53` (본 스펙) | 4절 전체 |
| 즉시 조치(완료) | beads_ui DB | — | `UI-kfl4`→`dotfiles-1tif` 의존 제거, ready 복귀 확인 |

- 순서 근거: bd 바이너리에서 ship이 사라지기 전에 호출처(①②)가 먼저 사라져야 한다. ①② 랜딩 사이·②③ 사이에 bd ship은 계속 동작하므로 중간 상태가 안전하다.
- ①의 배포는 beads-ui `docs/agents/repo-ops.toml [deploy]`(`bdui-shared restart`, detached) 커버리지, ③의 배포는 External/beads `[deploy]`(`make install-force`; bdui는 요청마다 bd를 spawn하므로 교체 즉시 반영) 커버리지가 실재한다 — 처분 불요.
- 유닛 간 Bead 의존은 걸지 않는다(크로스 DB 의존이 바로 이 유닛의 개조 대상이므로 자기참조 회피). 순서는 이 표가 canonical이다.

## 6. Test scope

RED→GREEN 시임 (External/beads):

1. **해석기 판정식**: `internal/storage/issueops/external_resolution_test.go` 재작성 — 맨 ID ref가 (a) 대상 DB closed → satisfied, (b) open/resolved → unsatisfied, (c) 미지 prefix → unresolvable 진단, (d) 중복 prefix → unresolvable 진단, (e) 비서버 모드 → unresolvable. RED: 신 판정 테스트가 현행 malformed 처리로 실패.
2. **분류 규칙(4.1)**: 신규 테스트 — 하이픈 포함 prefix 겹침 구성(예: 자기 prefix `team` vs 발견 prefix `team-alpha`)에서 `team-alpha-xyz`가 external로, `team-xyz`가 own으로 분류. RED: 현행 `ExtractPrefix` 첫-하이픈 비교로 실패.
3. **자동 발견**: 신규 테스트 — 2개 DB(config.issue_prefix 보유) + 1개 비-beads DB(무시) 구성에서 prefix→DB 맵 구성, 중복 prefix 감지. embeddeddolt 통합(`external_ready_integration_test.go` 계열 재작성): cross-DB에서 대상 close 후 다음 `bd ready`에 소비자 노출. **주의: embeddeddolt 스위트는 `BEADS_TEST_EMBEDDED_DOLT=1` env 게이트 뒤에 있어(`create_issue_test.go:171-175`) canonical `make test`만으로는 돌지 않는다** — 아래 검증 명령에 명시된 env 부여 실행이 필수이며, env 없는 실행을 RED/GREEN 증거로 쓰지 않는다.
4. **쓰기 검증**: `bd dep add` — (a) 실존 cross-prefix ID 수용, (b) 미존재 ID 거부, (c) `external:` 문법 거부(안내 문구), (d) 비서버 모드 cross-prefix 거부. RED: 현행은 전부 무검증 수용.
5. **config 표면**: `bd config set external_databases.foo bar`가 unrecognized key로 거부. RED: 현행 `recognizedConfigPrefixes`가 수용.
6. **dep list 정합**: 단일 ID `--json`에 external 엣지 포함(counts 일치). RED: 현행 `[]`.
7. **철거의 역방향 RED**(테스트 삭제만으로 GREEN이 되는 것을 방지): (a) `bd ship <x>`가 unknown command로 실패, (b) `bd label add <id> provides:foo`가 일반 라벨로 **수용**(현행 예약 거부의 반전 — `label_embedded_test.go:323` 반전 재작성). 기존 `ship_embedded_test.go`·`external_ref_test.go`는 삭제, `ready_explain_merge_test.go`·`graph`·`sqlbuild`·`dolt` 계층의 `external:` 픽스처는 맨 ID로 치환.
8. **태그 제외 fixture 정리**: `tests/regression/discovery_test.go:987`은 `//go:build regression && discovery` 태그로 `make test`에서 제외되지만 제거 대상 문법(`external:otherproject:some-capability`)을 사용하므로, 같은 변경에서 맨 ID fixture로 치환한다(제외 스위트가 향후 실행 가능하도록). 이 fixture는 RED/GREEN 증거로 쓰지 않는다.

검증 명령(둘 다 필수): `env TEST_TIMEOUT=10m make test` (repo-ops.toml [verify] canonical) **및** `BEADS_TEST_EMBEDDED_DOLT=1 go test ./internal/storage/embeddeddolt/...`.

## 7. Ordered apply procedure

머지 이후 라이브 반영 절차. **소유권**: 1·2단계는 각 소비자 유닛(UI-knf8, dotfiles-jvhr)의 표준 finish 절차가 소유한다. 3단계의 바이너리 교체는 이 레포 `[deploy]` 선언(`make install-force`)의 커버리지이고, **4~6단계(라이브 probe·데이터 정리·최종 검증)는 deploy 커버리지 밖이므로 `beads-o53`의 close 조건으로 묶는다** — 실행 영수증(probe 판정 결과, snapshot 위치, 삭제 행 수, grep 전수 결과)을 Bead notes에 readback으로 기록하기 전에는 `beads-o53`을 close하지 않는다. 워커 무인 레인이 이 유닛을 집더라도 4~6단계는 세션(대화형) 몫으로 남는다.

1. **① UI-knf8 머지·배포**: beads-ui 워커/세션 레인 표준 절차. 배포는 `[deploy]` 자기재시작(detached) — 머지 클릭 레인이면 자동, 세션 레인이면 재시작은 운영자/세션 몫. 검증: 재시작 후 머지 클릭 1건에서 cleanup 파이프라인이 ship 단계 없이 `branch_cleanup → parent_close → 종료`로 완주.
2. **② dotfiles-jvhr 머지·설치**: 표준 post-merge install 의무(`./install.sh claude codex shell` 해당분) + `check-workflow-contract.py` green + 계약 테스트 green. 검증: 설치본 스킬에서 `bd ship` 문구 부재 grep.
3. **③ beads-o53 머지·배포**: `make install-force`로 `~/.local/bin/bd` 교체(bdui 재시작 불요 — spawn-per-request). 플릿 타 머신 배포는 기존 채널(dotfiles-otze) 경유, 이 절차의 완료 조건은 Mac Studio 설치본 교체까지. 검증: `bd version` 신 버전 + `bd ship`이 unknown command.
4. **의미론 라이브 probe** (③ 직후, 데이터 정리 **전**): 전용 일회용 probe 이슈를 생성해 검증한다 — 실작업 이슈를 쓰지 않는다. 절차: (a) 한 rig(예: External/beads의 `beads` DB)에 probe 이슈 생성, ID 기록. (b) probe에 cross-prefix **closed** 이슈 의존 추가 → `bd ready`에 probe 유지 확인. (c) cross-prefix **open** 이슈 의존 추가 → blocked 전환 확인. (d) 의존 전부 제거 + probe 이슈 close, readback으로 잔여 0 확인. 중단 시 재개 절차: 기록된 probe ID로 (d)를 먼저 완료한 뒤 재시도 — probe는 전용 이슈이므로 실작업이 blocked로 남는 일이 없다. probe 실패 시 여기서 멈추고 롤백(아래) — 5단계로 진행하지 않는다.
5. **데이터 정리** (probe green 후에만, 중앙 dolt): (a) **snapshot 선행**: 삭제 대상 행 전체를 `SELECT *`로 덤프해 파일로 보존하고, `.beads/config.yaml` 원본을 백업 복사한다(복구 = snapshot 재삽입·파일 복원). (b) capability 레코드 5건 삭제 — 대상은 1절 실측 목록(전부 closed 이슈 소속). 삭제 전 `depends_on_external LIKE 'external:%'` 전수 조회가 그 5건과 일치하는지 확인하고, 불일치 시 멈추고 재실측. (c) 각 레포 `.beads/config.yaml`의 `external_databases:` 블록 제거(실측 보유: External/beads, beads-ui; 삭제 전 전 레포 grep 전수). bd config 키 인식 제거는 ③ 코드에 포함.
6. **최종 readback**: beads-ui 워크스페이스에서 `bd ready --explain --json` — stderr 경고 0건, blocked 목록에 unresolvable 잔존 0건. 영수증 일체를 `beads-o53` notes에 기록.

롤백: ③ 이전 어느 시점이든 무해(ship은 여전히 존재). ③ 이후 4단계 실패 시 직전 bd 바이너리 재설치(`git checkout <prev> && make install-force`)로 즉시 복귀 — 이 시점에는 데이터·config가 아직 원형이다. 5단계 이후 문제 시 바이너리 복귀 + snapshot 재삽입·config 복원.

## 8. 잔여 위험

- 구 bd 바이너리를 가진 플릿 타 머신이 ③ 이후 capability 레코드를 새로 쓸 가능성 — 5단계 정리 후에도 이론상 재유입 가능하나, 쓰기 주체가 이 Mac Studio(중앙 워커) 중심이라 실위험 낮음. 플릿 배포(dotfiles-otze 채널)로 수렴.
