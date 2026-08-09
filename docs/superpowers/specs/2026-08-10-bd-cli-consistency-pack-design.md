# bd CLI 일관성 팩 설계 — JSON shape 통일·metadata 강제변환 제거·edit 가드·show 프로젝션

- **Bead**: beads-456 · **라우트**: spec_backed · **작성일**: 2026-08-10
- **출처**: beads-u4d(bd- 스킬 소스 흡수, PR #5) 분석에서 발견된 CLI 표면 결함. cross-repo 아님.
- **목표**: bd CLI의 `--json` 출력 결함을 소스에서 제거해, fleet 스킬(`bd-usage`)과 계약(`workflow.md/yaml`)이 유지 중인 방어 텍스트를 삭제 가능하게 만든다(삭제 자체는 dotfiles 후속).

## 1. 출력 규약 (계약)

**shape는 오직 호출 형태(명시 ID 개수 + 플래그)로만 결정된다. 결과 개수·저장소 상태·라우팅 경로는 shape에 영향을 주지 않는다.**

| 호출 형태 | shape | 해당 명령 |
|---|---|---|
| 명시 ID 정확히 1개 | bare object | `show`, `update`, `close`, `reopen` |
| 명시 ID 2개 이상 | array — 일부 실패로 결과가 1건이어도 array 유지 | `show`, `update`, `close`, `reopen` |
| 생성 결과 1건 | bare object (현행 유지) | `create` |
| 질의형: 요청 1건 → 결과 M건 | 항상 array (현행 유지, 무변경) | `list`, `ready`, `dep list`, `children` |
| 부가 결과 플래그 사용 | keyed envelope — 플래그가 있으면 결과가 비어도 항상 envelope | `close --suggest-next` / `--continue` / `--claim-next` |

envelope 내부의 `closed` 키는 항상 array다. 부가 플래그를 복수 사용하면 한 envelope에 각 플래그의 키가 나란히 들어간다(`{"closed": [...], "unblocked": [...], "claimed": ...}`). 키는 결과가 없어도 존재한다(빈 array 또는 null).

`bd reopen`은 Bead 원문 5항목 외 추가다: 스윕에서 close와 동일 패턴(`reopen.go:103`, 단일 ID인데 array)이 확인됐고, 제외하면 close↔reopen 비대칭이라는 새 불일치가 생기므로 포함한다(사용자 승인 완료).

### 1a. 규약 세칙 (경계 케이스)

- **무인자 호출**: `update`/`close`는 인자 0개를 허용하고 last-touched ID로 해석한다(`update.go:29,49-53`, `close.go:34,54-58`). 해석 결과가 단일 ID이므로 **요청 ID 1개로 계산 — object**. `show --current`도 동일(단일 이슈 해석 → object).
- **show 특수 모드**: `--as-of`(단일 이슈의 과거 시점)는 JSON 출력 시 단일 규약(object)을 따른다. `--thread`/`--refs`(show.go:101-107)는 이슈 레코드가 아닌 별도 구조의 출력이며 **이번 범위 밖 — 현행 shape 유지, 규약 문서에 "별도 구조"로 명시**. `--children`(show.go:111)은 질의형(부모 1 → 자식 M)이므로 항상 array.
- **저장 런타임 불변**: embedded/server/proxied 어느 런타임이든 출력은 `cmd/bd`의 동일 call site를 경유한다. 규약 문장 "라우팅 경로는 shape에 영향을 주지 않는다"의 검증은 §5의 direct/proxied 공통 매트릭스가 담당한다.

### 1b. 기존 JSON 계약과의 관계 (envelope·schema_version)

- **`BD_JSON_ENVELOPE=1` envelope 모드는 직교 계약으로 유지한다**(`output.go:14-16,41-47`, `docs/JSON_SCHEMA.md`). arity 규약은 legacy 모드의 top-level과 envelope 모드의 `.data` payload에 **동일하게** 적용된다 — envelope 모드에서 단일 ID면 `.data`가 object, 복수면 `.data`가 array.
- **legacy 모드의 field injection**: `wrapWithSchemaVersion()`은 object 출력에 `schema_version` 필드를 주입하고 array에는 주입하지 않는다(`output.go:41-72`). 단일=object 전환 후 show/update/close/reopen의 단일 출력에도 — 현행 `create`와 동일하게 — `schema_version`이 주입된다. 이는 규약의 일부로 문서화한다.
- **`JSONSchemaVersion` 1→2 범프**: top-level shape 변경은 `docs/JSON_SCHEMA.md`의 "Output structure changes" 기준에 해당한다. 상수(`output.go:12`)를 2로 올리고, `docs/JSON_SCHEMA.md`와 `cmd/bd/protocol/json_contract_test.go`를 동반 갱신한다.

## 2. 항목별 설계

### 2a. 공통 헬퍼 + show/update/close/reopen 전환

`cmd/bd/output.go`(현행 `outputJSON`: output.go:18)에 arity 기반 헬퍼를 추가한다:

- 시그니처(예시): `outputJSONForRequest(requestedCount int, items []T) error` — `requestedCount==1 && len(items)==1`이면 `items[0]`을 object로, 아니면 array로 marshal.
- shape 결정 로직은 이 함수 한 곳에만 존재한다. call site는 `len(args)`(요청한 명시 ID 개수)를 넘긴다.
- 전환 대상 call site: `show.go:412-414`, `update.go:555-558`, `close.go:254-267`(plain·claimed 분기), `reopen.go:103`, `show --as-of` 경로(`showIssueAsOf`)의 JSON 출력. 무인자 호출은 last-touched 해석 후 요청 ID 1개로 계산한다(§1a).
- `create.go:699`는 현행 유지(이미 object).
- 단일 ID 요청이 실패(미발견 등)하면 JSON 본문이 아니라 현행 에러 경로를 탄다(무변경) — 규약은 성공 출력의 shape만 규정한다.

### 2b. close 부가 플래그 envelope 고정

현행 결함: `--suggest-next`는 unblocked가 있을 때만 envelope을 내고 없으면 plain array로 떨어진다(`close.go:192-206`). `--continue`(close.go:208-222)·`--claim-next`(close.go:224-267)도 결과 유무로 shape가 바뀐다 — "결과에 따라 shape가 바뀌는" 동종 결함.

설계: **플래그가 있으면 항상 envelope.** 결과가 없어도 키를 빈 값으로 포함한다. plain close(부가 플래그 없음)와 reopen(부가 결과 플래그가 없음 — `--reason`뿐)은 2a의 단일/복수 규약을 따른다.

### 2c. dep list 레코드 타입 고정 + `--format`

현행 결함: batch(len>1)+direction=down이면 bare edge 레코드 `{issue_id, depends_on_id, type}`(`dep.go:778-826`), 그 외에는 `IssueWithDependencyMetadata` issue 레코드(`dep.go:828-856`) — 인자 개수·방향으로 레코드 타입이 바뀐다.

설계:

- 기본 출력은 **항상 issue 레코드**. arity·방향 무관.
- `--format=edges` 명시 시에만 edge 레코드. edge는 `IssueWithDependencyMetadata`가 이미 가진 정보(대상 ID·타입)로 합성 가능하므로 up/down 모두 새 storage API 없이 지원한다.
- `--format=issues`는 기본값의 명시 표기로 수용한다. 그 외 값은 에러.
- 기존 same-store 배치 최적화(`GetDependencyRecordsForIssues`)는 내부 최적화로 유지 가능하되 출력 shape에 영향을 주지 않는다.
- 컨테이너는 항상 array(질의형 명령).

### 2d. `--set-metadata` 강제변환 제거

현행 결함: `update.go:641-660` `toJSONValue()`가 `fmt.Sscanf(%f)`+`json.Valid`로 숫자처럼 보이는 값을 JSON number로, `true`/`false`/`null`을 각 타입으로 저장한다. a-f 없는 hex/sha 값이 number가 되어 대형 정수 정밀도 위험(float64 소비자 기준 2^53 초과 손실)이 실재하고, fleet 계약의 `<reviewer>@<sha>` prefix 규약이 이 강제변환 회피용으로 존재한다.

설계:

- `toJSONValue()` 삭제. `--set-metadata key=value`는 **무조건 JSON string으로 저장**한다(number/bool/null 인식 전부 제거 — 일부만 남기면 암묵 규칙이 잔존).
- 타입 명시가 필요하면 새 플래그 `--set-metadata-json key=<raw JSON>`(반복 가능)을 쓴다. 값은 `json.Valid` 검증, 실패 시 에러.
- 같은 키가 `--set-metadata`와 `--set-metadata-json`에 동시에 오면 모호하므로 에러.
- 기존에 number로 저장된 메타데이터는 마이그레이션하지 않는다(읽기 경로 영향 없음).

### 2e. `bd edit` non-TTY 거부

현행 결함: `edit.go:115-124`가 TTY 검사 없이 `$EDITOR`를 `os.Stdin`으로 exec — 헤드리스 에이전트 셸에서 행(hang) 위험.

설계: `$EDITOR` exec 전에 stdin/stdout이 문자 디바이스인지 검사(`os.Stat`+`os.ModeCharDevice`; 코드베이스에 기존 isatty 관례가 있으면 그것을 따른다). non-TTY면 즉시 에러로 종료하며, 에러 메시지에 `bd update <id> --description=@file` 등 비대화형 대안을 안내한다. 우회 플래그는 두지 않는다(YAGNI).

### 2f. `bd show --fields` 프로젝션

현행 결함: JSON 경로가 `IssueDetails` 전체(본문 포함)만 출력해 readback 시 클라이언트측 필터를 강제한다.

설계:

- `--fields=id,status,metadata`처럼 콤마 목록. 필드명은 `IssueDetails`의 JSON 태그명 기준.
- 출력은 요청한 키만, **요청 순서대로** 포함한다.
- 미지 필드는 유효 필드 목록을 담아 에러.
- 유효하지만 미적재인 필드(예: `--include-comments` 없이 `comments` 요청)는 에러가 아니라 해당 필드의 zero/null 값을 출력한다 — 필드 적재는 `--include-*` 플래그 소관, `--fields`는 선택만 담당.
- `--include-dependents`/`--include-comments`와 조합 가능. 단일/복수 규약(2a) 그대로 적용.
- 텍스트 출력 경로는 무변경. `--brief` 프리셋은 이번 범위에서 제외(추후 얹기 쉬움).

## 3. 호환성 인벤토리 (2026-08-10 스윕 실측)

전수 스윕 결과(이 리포 + `~/.claude` + dotfiles): **shape 변경으로 깨지는 소비자는 전부 이 리포 안**이며 같은 PR에서 수정한다.

### 깨지는 소비자 (같은 PR에서 수정)

| 위치 | 전제 | 수정 |
|---|---|---|
| `cmd/bd/cli_fast_test.go:541-548, 768-782` | show array | 새 규약 기준 갱신 |
| `cmd/bd/cli_coverage_show_test.go:232-244, 261-269` (close) · `:300-308, 322-328` (show) | array | 새 규약 기준 갱신 |
| `cmd/bd/close_proxied_integration_test.go:44-61` (`bdProxiedCloseJSON`) | close array | 새 규약 기준 갱신 |
| `integrations/beads-mcp/src/beads_mcp/bd_client.py` `close()`(:607-611) · `reopen()`(:627-631) | array, 폴백 없음 | 새 규약 기준 수정 (show/update/claim은 tolerant라 무변경) |
| `cmd/bd/proxied_integration_helpers_test.go:157-185` (`bdProxiedUpdate`) | update array, `Index(s,"[")` 하드 실패 | 새 규약 기준 갱신 |
| `cmd/bd/update_proxied_integration_test.go:524-540` | update array | 새 규약 기준 갱신 |
| `cmd/bd/reopen_proxied_integration_test.go:44-60` · `cmd/bd/reopen_defer_test.go:59-65` | reopen array | 새 규약 기준 갱신 |
| `cmd/bd/metadata_edits_test.go:84-125, 186-204` | 현행 강제변환(숫자/불리언 저장)을 단언 | 새 규약(항상 string) 기준으로 기대값 반전 — §5 시임 2의 RED 소재 |
| `cmd/bd/protocol/json_contract_test.go` | schema_version=1·현행 shape 전제 | `JSONSchemaVersion` 2 범프·새 규약 반영 (§1b) |
| `AGENT_INSTRUCTIONS.md:276` | `jq '.[0] \| ...'` | object 기준으로 수정 |
| `website/docs/core-concepts/metadata.md:25` | `jq '.[0] \| ...'` | object 기준으로 수정 |
| `docs/CLI_REFERENCE.md` · website 활성 CLI reference | 새 플래그·규약 미반영 | `--format`/`--set-metadata-json`/`--fields`와 shape 규약 표 반영 |
| `docs/JSON_SCHEMA.md` | schema version 1·legacy shape 서술 | §1b 처분 반영 |

`website/versioned_docs/`의 과거 버전 스냅샷(동일 예시 3벌)은 해당 릴리스의 실제 동작 기록이므로 **수정하지 않는다**.

### 변경으로 저절로 올바르게 되는 문서 (무변경)

`website/docs/core-concepts/hash-ids.md:121`, `workflows/gates.md:175`, `core-concepts/labels.md:760` — 이미 `jq '.field'`로 object를 전제해 현행 array 출력에서 깨져 있던 예시들.

### tolerant (무변경)

리포 내: `update_embedded_test.go` `parseShowJSON`, `create_embedded_test.go` `parseIssueJSON`/`bdShow`, `close_embedded_test.go`(JSON-valid 검사만), `scripts/migration-test/lib/snapshot.sh`(jq type 분기), beads-mcp `show()`/`update()`/`claim()`.
fleet(dotfiles) 측: `bd-usage` 관용구(`d[0] if isinstance(d,list) else d`), `bd-revert` `inspect_revert_target.py` `first_issue()`, `full-plan-marker-sweep.py` `bead_status()` — **전원 tolerant, 코드 수정 불필요.**

### 스코프 밖 관찰 (이번 팩 무변경, 기록만)

`bd children --json`(질의형이라 규약상 array 유지 — `children_embedded_test.go`·`inspect_revert_target.py`의 array 전제는 계속 유효), `bd ready`/`bd list`/`bd admin compact`(질의형 array, 무변경), `bd info`/`bd where`/`bd bootstrap`(object, 무변경).

## 4. fleet 경계와 후속

- 이 유닛은 beads 리포 단독. dotfiles 쪽 방어 텍스트 삭제(`bd-usage` "JSON parsing pitfalls"·object-or-list normalize 규칙·dep list arity 경고, `workflow.md/yaml`의 `<reviewer>@<sha>` prefix 근거, `bd edit 금지` 규칙)는 **split + deferred** — 재진입 조건: 이 팩의 포크 릴리스가 전달된 후 dotfiles 워크스페이스 Bead(dotfiles-ms54 스코프 또는 신규)로 착수.
- dotfiles 계약 테스트가 해당 방어 텍스트의 존재를 단언하므로(`tests/bd_usage_skill_contract_test.sh` 등), 후속에서 텍스트와 테스트를 함께 갱신해야 한다.

## 5. Test scope

TDD 시임(RED-GREEN 승인 대상). 시임 밖 코드는 기존 스위트 회귀로 커버한다. 모든 시임의 기준 검증은 canonical 명령 `env TEST_TIMEOUT=10m make test`(`docs/agents/repo-ops.toml [verify]`, `-tags gms_pure_go`)에서 **실제로 실행되는** 테스트여야 한다 — cgo/e2e 게이트 뒤로 숨은 시임은 vacuous RED이므로 불인정.

1. **shape 계약 테스트(신설: `cmd/bd/json_shape_contract_test.go`, 기본 스위트 소속)**: §1의 규약 표를 table-driven으로 전수 검증 — (명령, 명시 ID 개수/무인자, 부가 플래그, envelope 모드 on/off) → 기대 shape. 부분 실패 시 array 유지, close envelope 키 상존(빈 결과 포함), §1b field injection 포함. **RED 소재**: 현행 shape가 규약 표와 다름. proxied 경로는 기존 proxied 스위트 파일(§3의 5개) 갱신이 동일 규약을 단언해 direct/proxied 매트릭스를 이룬다.
2. **metadata 문자열화(기존 `cmd/bd/metadata_edits_test.go` 반전·확장)**: `0123`·`12345678901234567890`·`1e5`·`true`·`null` → 전부 JSON string 저장. `--set-metadata-json` typed 저장·invalid JSON 에러·두 플래그 중복 키 에러. **RED 소재**: 84-125·186-204행의 현행 단언(숫자/불리언 저장) 반전.
3. **dep list `--format`(신설: `cmd/bd/dep_format_test.go`)**: 기본=issue 레코드(단일/배치·up/down 전수), `--format=edges`=edge 레코드, 미지 값 에러. **RED 소재**: batch+down이 현행 edge 레코드를 내고 `--format` 플래그가 미존재(unknown flag) — 플래그 부재로 인한 실패는 대상 동작 부재의 직접 증거로 인정.
4. **edit non-TTY(신설: `cmd/bd/edit_nontty_test.go`)**: `EDITOR`를 sentinel 스크립트(호출 시 마커 파일 생성)로 설정하고 non-TTY 실행 → 즉시 에러·`bd update` 안내 문자열·**sentinel 미호출**(마커 부재) 단언. 에러 단언은 unknown-flag 에러와 구별되는 가드 고유 메시지를 검사한다. **RED 소재**: 현행은 sentinel이 호출됨(가드 부재). hang 없이 RED가 판정되도록 sentinel은 즉시 종료한다.
5. **show `--fields`(신설: `cmd/bd/show_fields_test.go`)**: 요청 키만·요청 순서, 미지 필드 에러(유효 목록 포함, unknown-flag와 구별), 미적재 필드 zero/null, `--include-*` 조합, 단일/복수 규약 적용. **RED 소재**: 플래그 미존재.
6. **beads-mcp**: `close()`/`reopen()` 수정에 대응하는 기존 Python 테스트 갱신(있는 경우) 또는 shape 파싱 단위 테스트 추가.

## 6. 검증

- canonical: `env TEST_TIMEOUT=10m make test`(repo-ops.toml `[verify]`). 보조: `go build ./...` · `go vet ./...` · `gofmt` clean.
- 기존 스위트 실패 집합은 pinned base 대비 신규 0 기준(pre-existing 실패는 baseline 기록).
- beads-mcp: 해당 패키지 테스트 러너(존재 시) 실행.

## 7. 배포

- **하드 컷** — 레거시 shape 토글 없음(사용자 승인; `BD_JSON_ENVELOPE`는 §1b대로 직교 유지). 포크 마이너 버전 범프(예: 1.2.0-fork.1) + 릴리스 노트에 breaking 목록 명시: ① show/update/close/reopen 단일 ID → object(+`schema_version` 주입) ② close 부가 플래그 → 항상 envelope ③ dep list 기본 → 항상 issue 레코드 ④ `--set-metadata` → 항상 string ⑤ non-TTY `bd edit` → 즉시 에러 ⑥ `JSONSchemaVersion` 1→2.
- **머지 후 릴리스 전달은 이 리포 `[deploy]`(`make install-force`, 로컬 바이너리 교체)가 커버하지 않는 필수 후속이다** → `deferred_required` 이행 완료: 전달 Bead **beads-qxg** 생성·beads-456에 blocks 의존 연결(2026-08-10, readback 확인). 완료 조건(포크 릴리스 태그·breaking 목록·전달 확인)은 beads-qxg 본문에 명시(beads-cpy 관례).

## 8. 구현 유닛·커밋 구성

한 PR(beads-456 브랜치, `.worktrees/beads-456`), 항목별 커밋:

1. output 헬퍼 + show/update/close/reopen 전환 + shape 계약 테스트 + `JSONSchemaVersion` 2 (2a·2b·§1b)
2. dep list `--format` (2c)
3. metadata 문자열화 + `--set-metadata-json` (2d)
4. edit non-TTY 가드 (2e)
5. show `--fields` (2f)
6. 동반 수정: 리포 내 테스트(proxied 포함)·beads-mcp·문서(`CLI_REFERENCE`·`JSON_SCHEMA`·website) (§3)
7. 버전 범프 + 릴리스 노트

## 9. 비범위

- dotfiles 방어 텍스트·계약 테스트 갱신(§4 후속).
- `bd children`/`ready`/`list` 등 질의형 명령의 shape 변경(규약상 현행이 옳음).
- 기존 저장 metadata의 타입 마이그레이션.
- `--brief` 프리셋, `bd edit` 우회 플래그.
- upstream(gastownhall/beads) 동기화 — 포크 분기 유지는 기존 정책.
