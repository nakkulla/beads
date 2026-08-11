# bd 구조화 recovery error 계약 설계 (beads-1nj)

## 문서 상태

- Bead: `beads-1nj`
- Route: `spec_backed`
- Workflow mode: `standard`
- 설계 승인: 2026-08-11 사용자 승인
- 구현 단위: beads 저장소의 단일 결합 유닛
- 외부 소비자 후속: dotfiles `dotfiles-3vb8` → beads `beads-1nj`
  (`blocks` dependency; consumer가 producer 완료 전까지 차단됨)

## 배경과 문제

`bd-revert`의 `beads_recovery.sh classify_failure()`와 `bd-recover`의
`contains_lock_signal()`은 현재 `bd` stderr에서 `non-fast-forward`,
`exclusive lock`, `database is locked`, `Blob not found`, `i/o timeout` 같은
문자열을 찾아 복구 정책을 결정한다. 사람용 문구가 바뀌면 소비자의 판정도
깨지고, 알 수 없는 오류를 이미 아는 오류로 잘못 분류할 위험이 있다.

`bd` 내부에도 같은 문제가 분산되어 있다.

- `cmd/bd/dolt.go`의 pull/push/commit 경로는 직접 stderr를 출력하고
  `os.Exit`하며, remote/history/dangling 오류 일부를 문자열로 분류한다.
- `cmd/bd/dolt_autopush.go`는 auto-push 실패를 성공한 primary operation의
  warning으로만 출력하고 JSON 모드에서는 이를 숨긴다.
- `cmd/bd/doctor/fix/local_store_health.go`는 store open 경계에서 typed error가
  보존되지 않아 좁은 corruption signature 목록을 사용한다.
- `doctor --agent --json`에는 진단 정보가 있지만 표시용 `name` 외에 안정적인
  join key가 없고, local store/PID 판정의 typed fact가 JSON 계약까지 이어지지
  않는다.

반면 기존 JSON 계약은 이미 지켜야 할 호환성 경계를 갖는다.
`schema_version=2`, `error`/`hint`, stdout/stderr 선택, exit code,
`BD_JSON_ENVELOPE=1`의 `.data` 래핑은 기존 소비자가 의존하는 공개 계약이다.
또한 기존 `code`는 `remote_add_failed`처럼 호출 지점의 operation을 나타내므로
failure taxonomy로 의미를 바꾸면 안 된다.

## 결정 요약

새 `bd recover` 명령이나 전 CLI 공통 error API를 만들지 않는다. 대신
복구 소비자가 실제로 관찰하는 producer에 한정해 안정적인
`failure_code`를 추가하는 **scoped producer taxonomy**를 도입한다.

1. storage 경계에 engine-agnostic `FailureCode`와 원인을 보존하는 typed
   wrapper를 둔다.
2. database open, `bd dolt pull|push|commit`, auto-push와 이 경로에서 발생하는
   lock/remote/history/schema failure만 이번 유닛에서 구조화한다.
3. in-scope `--json` failure에는 `failure_code`를 항상 제공한다. 분류하지 못한
   경우에도 누락하거나 외부 문자열 폴백을 요구하지 않고
   `operation_failed_unknown`을 제공한다.
4. 기존 `code`, human message, stream, exit code와 schema version은 보존한다.
5. `doctor`에는 표시명과 분리된 `check_code` 및 optional typed evidence를
   추가한다.
6. retry 순서, final recovery verdict와 파괴적 복구 결정은 dotfiles
   orchestration에 남긴다.

## 목표

- 외부 recovery 소비자가 stderr 문구를 해석하지 않고 `failure_code`와 typed
  evidence만으로 정책을 결정할 수 있게 한다.
- 기존 JSON·human CLI 계약을 additive하게 확장한다.
- Dolt/MySQL 내부 타입을 공개하지 않고 storage driver 경계를 지킨다.
- false positive보다 `operation_failed_unknown`을 택하는 fail-safe 분류를
  보장한다.
- auto-push의 non-fatal failure도 machine-readable하게 관찰할 수 있게 한다.

## 비목표

- 신규 `bd recover` 또는 `bd repair` 명령
- whole-CLI error taxonomy 및 모든 직접 `os.Exit` 경로 정리
- `recovered`, `bootstrap_required`, `manual_recovery_required`,
  `blocked_unknown` 같은 orchestration verdict
- 자동 retry 순서, direct-clone fallback, re-clone 또는 quarantine 결정
- `doctor --fix`와 별개인 mutation 경로
- DB schema 변경
- beads-side Dolt lock 파일 해석·삭제, engine introspection, 별도 flock/retry loop
- Dolt/MySQL error number나 driver 전용 타입의 public SDK 노출

## 공개 JSON 계약

### Fatal failure

in-scope command가 nonzero로 끝날 때 기존 JSON error와 같은 stderr에
`failure_code`와 optional `evidence`를 additive하게 출력한다.

Legacy shape:

```json
{
  "error": "failed to push to remote",
  "hint": "Pull remote changes before retrying.",
  "failure_code": "sync_remote_ahead",
  "evidence": {
    "operation": "dolt_push",
    "remote": {"name": "origin", "transport": "ssh"}
  },
  "schema_version": 2
}
```

`BD_JSON_ENVELOPE=1` shape:

```json
{
  "schema_version": 2,
  "data": {
    "error": "failed to push to remote",
    "hint": "Pull remote changes before retrying.",
    "failure_code": "sync_remote_ahead",
    "evidence": {
      "operation": "dolt_push",
      "remote": {"name": "origin", "transport": "ssh"}
    }
  }
}
```

계약 규칙:

- `error`, optional `hint`, exit code, stderr 위치를 유지한다. 해당 호출 지점에
  기존 caller-specific `code`가 있으면 그 값과 위치도 그대로 유지한다.
- `failure_code`는 in-scope JSON failure에서 필수다.
- 기존 `code`는 호출 지점/operation, `failure_code`는 관찰된 leaf fact다.
  두 필드는 서로 대체하지 않는다.
- envelope 모드에서는 두 필드와 `evidence`를 기존 payload와 함께 `.data`에
  둔다.
- optional field 추가와 enum 확장은 schema v2의 additive change다. 기존 값의
  의미는 재사용하거나 변경하지 않는다.
- 소비자는 unknown field를 무시하고 모르는 `failure_code`를
  `operation_failed_unknown`과 같은 보수적 상태로 취급해야 한다.

### Non-fatal auto-push warning

auto-push failure는 primary command의 성공을 실패로 바꾸지 않는다. 기존
primary stdout와 exit code 0을 유지하고, JSON 모드에서 stderr에 단일 warning
object를 추가한다.

```json
{
  "warning": "dolt auto-push failed",
  "failure_code": "remote_unreachable",
  "evidence": {
    "operation": "auto_push",
    "remote": {"name": "origin", "transport": "https"}
  },
  "schema_version": 2
}
```

envelope 모드는 fatal failure와 동일하게 `warning`, `failure_code`, `evidence`를
`.data`에 둔다. non-JSON warning 문구와 기존 `--quiet` 억제 의미는 유지한다.
JSON warning은 primary success payload와 같은 stdout에 섞지 않는다.

## `failure_code` taxonomy

모든 값은 lower_snake_case이며 engine-agnostic이다. 새 값은 additive하게만
추가하고 기존 값을 다른 의미로 재사용하지 않는다.

| 값 | 안정 의미 |
| --- | --- |
| `lock_conflict` | 다른 live/unknown owner와의 동시 접근 때문에 operation이 거부됨 |
| `local_store_corrupt` | positive corruption evidence로 local store 손상이 확인됨 |
| `database_not_found` | 요청한 logical database가 storage에 없음 |
| `database_open_failed` | 위 세부 코드로 안전하게 좁힐 수 없는 database open 실패 |
| `remote_not_configured` | operation에 필요한 remote가 설정되지 않음 |
| `remote_auth_failed` | remote 인증 또는 권한 검증 실패 |
| `remote_unreachable` | timeout, connection 또는 name-resolution 계열의 일시적 도달 실패 |
| `remote_data_missing` | remote가 참조한 object/blob/chunk를 제공하지 못함 |
| `sync_remote_ahead` | push가 remote의 선행 변경 때문에 non-fast-forward로 거부됨 |
| `history_diverged` | local과 remote history에 공통 ancestor가 없음 |
| `working_set_dirty` | sync/commit이 uncommitted working set 때문에 거부됨 |
| `dangling_reference` | commit/history가 존재하지 않는 chunk/reference를 가리킴 |
| `schema_migration_required` | 안전한 진행 전에 명시적 schema migration이 필요함 |
| `operation_failed_unknown` | in-scope failure지만 더 구체적인 분류 근거가 없음 |

`failure_code`는 복구 가능성이나 다음 명령을 직접 지시하지 않는다. 예를 들어
`remote_unreachable`이 곧바로 “한 번 retry”를 뜻하지 않으며,
`local_store_corrupt`가 곧바로 “re-clone”을 허가하지 않는다.

## 내부 구조

### 1. storage failure type

`internal/storage`에 다음 의미의 최소 타입을 둔다.

- `FailureCode`: 위 stable enum
- `ClassifiedError`: `Code`, 원인 `Cause`, `Error()`, `Unwrap()`
- `CodeOf(error)`: wrapped chain에서 typed code를 찾고, 없으면 false를 반환

wrapper에는 임의의 `map[string]any` evidence나 retry policy를 넣지 않는다.
operation/remote/lock/PID 같은 문맥은 해당 사실을 이미 소유한 CLI 또는 doctor
projection에서 조립한다. 이로써 storage API가 UI shape나 orchestration 정책에
결합되는 것을 막는다.

driver는 source와 가장 가까운 지점에서 typed sentinel/error를 우선 매핑한다.
기존 open/sync 경계에서 type이 소실되는 경우에만 Dolt adapter 내부의 좁고
테스트된 legacy signature classifier를 이행 폴백으로 사용한다. generic
`unknown database`, `lock`, `timeout` 단어 하나만으로 분류하지 않으며, 충분한
근거가 없으면 unknown으로 둔다.

### 2. producer mapping

| producer | 우선 근거 | projection |
| --- | --- | --- |
| database open | typed schema/gate error, backend health inspection, narrow legacy open signatures | main open failure JSON + doctor local-store check |
| `dolt pull`/`push`/`commit` | typed storage/dberrors sentinel 우선, command-local context와 narrow compatibility fallback | 동일 stderr의 JSON error |
| auto-push | pull/push와 같은 shared classifier | stderr JSON warning, primary success 보존 |
| lock | storage-owned lock classification과 typed PID inspection | `lock_conflict` + optional `evidence.lock` |
| schema gate | `SchemaSkewError`, `RemoteMigrateGateError` 등 기존 typed error | `schema_migration_required` |

이번 유닛에서는 `bd dolt pull|push|commit`만 `RunE`/공유 projection 경로로
전환한다. 다른 `bd dolt` 하위 명령의 직접 `os.Exit` 정리는 별도 범위다.

### 3. evidence

`evidence`는 optional이며 join/판정에 쓸 수 있는 typed fact만 담는다.
사람용 `message`, `reason`, 원문 stderr, 파일 경로를 안정 join key로 사용하지
않는다.

```json
{
  "operation": "dolt_push",
  "lock": {
    "kind": "database",
    "pid_state": "live",
    "recorded_pid": 1234,
    "process_alive": true,
    "is_dolt_server": true
  },
  "remote": {
    "name": "origin",
    "transport": "ssh"
  },
  "schema": {
    "pending_migrations": 2
  }
}
```

- `operation`: `database_open`, `dolt_pull`, `dolt_push`, `dolt_commit`,
  `auto_push` 중 해당 값
- `lock.pid_state`: 기존 `absent|live|corrupt|dead|reused` 재사용
- `recorded_pid`, `process_alive`, `is_dolt_server`: 판정된 경우에만 포함
- remote에는 credential-bearing URL, token, username을 넣지 않는다. 이름과
  credential-free transport kind만 허용한다.
- unknown/remote/malformed lock은 stale 또는 dead로 단정하지 않는다.
- evidence 필드 자체는 additive하게 확장할 수 있으며 소비자는 unknown
  namespace와 field를 무시한다.

## doctor 계약

normal JSON과 `doctor --agent --json`의 각 check에 표시용 `name`과 독립된
stable `check_code`를 추가한다. optional `failure_code`와 namespaced
`evidence`를 같은 check에 붙인다.

- `check_code`는 explicit lower_snake_case constant다. serialization 시점에
  human `Name`을 변환해 만들지 않으며, 한 doctor result 안에서 nonempty·unique여야
  한다.
- enricher는 더 이상 human `Name` 문자열로 join하지 않고 `check_code`로
  join한다.
- local store check는 기존 `StoreOpenClass`/`LocalStoreRecoveryPlan`에서 얻은
  사실을 projection하되, destructive fix 여부를 `failure_code` 의미에 섞지
  않는다.
- server PID check는 `internal/doltserver.InspectPIDState`의 stable state와
  `recorded_pid`, `process_alive`, `is_dolt_server`, `stale` 사실을 재사용한다.
- stale mtime 파일은 cleanup 가능성일 뿐 active `lock_conflict` 증거로 쓰지
  않는다.
- embedded/proxied에서 doctor가 지원되지 않는 기존 경로는 JSON 모드에서
  `supported: false`, stable mode/check code와 설명을 반환한다. 기존 성공-exit
  의미는 유지한다.
- mutation은 기존 `doctor --fix`와 storage recovery capability를 통해서만
  수행한다. 이 설계는 doctor 전체 fix dispatch를 자동 호출하지 않는다.

`bd bootstrap --dry-run --json`의 기존 `BootstrapPlan.action`은 그대로
재사용한다. `action`은 plan이지 failure/verdict가 아니며, 특히 `action:none`은
healthy existing DB와 no-workspace를 모두 나타낼 수 있으므로 reason 문자열을
판정 키로 사용하지 않는다. 외부 소비자는 bootstrap action과 이 스펙의 stable
fact를 조합해 자기 정책을 결정한다.

## 호환성과 안전 경계

- human output 문구와 기존 exit code를 유지한다.
- in-scope JSON error는 기존 command가 사용하던 stderr에 출력한다.
- primary JSON success payload는 stdout에만 남긴다.
- `schema_version=2`와 envelope 규칙을 유지한다.
- `error`/`hint`는 사람용 설명이며 machine join key가 아니다.
- legacy string classifier는 producer 내부 이행 어댑터일 뿐 공개 fallback
  계약이 아니다. 외부 소비자는 raw message를 다시 해석하지 않는다.
- storage driver가 lock ownership/liveness를 확인할 수 없으면 beads가
  추측하거나 lock 파일을 삭제하지 않는다.
- recovery mutation은 `LocalStoreRecoverer` 등 기존 backend-owned interface를
  통과한다. `.dolt` 내부를 CLI에서 읽거나 재구현하지 않는다.

## 라우팅과 cross-repo 소비자

이 설계는 beads 내부 한 구현 유닛으로 결합되어 있다. shared failure type,
producer mapping, CLI/doctor projection과 contract test가 함께 바뀌어야 중간
상태에서 불완전한 공개 계약을 노출하지 않는다. 현재 독립 구현 phase나 별도
위임 단위가 없어 `full_plan` 승격 조건은 관찰되지 않았다.

| unit | disposition | durable tracking | 재진입 조건 |
| --- | --- | --- | --- |
| beads producer | 현재 `spec_backed` 유닛 | `bead:beads-1nj` | 이 스펙 승인 후 구현·검증·리뷰 |
| dotfiles consumer | `split + bead` / `deferred_required` | `bead:dotfiles-3vb8` → `bead:beads-1nj` (`blocks`) | beads 구현이 배포되고 설치된 `bd`의 JSON readback 확인 후 |

dotfiles 후속은 다음을 소유한다.

- `bd-revert/scripts/beads_recovery.sh`와
  `bd-recover/scripts/recover_runtime.sh`의 raw stderr matcher 제거
- `failure_code`, doctor `check_code`/`evidence`, bootstrap `action` 소비
- 모르는 code를 `blocked_unknown`으로 처리하고 raw message fallback 금지
- `recovered`, `bootstrap_required`, `manual_recovery_required`,
  `blocked_unknown`, retry-once 같은 최종 policy 유지

## Test scope (RED-GREEN seams)

이 섹션의 시임만 TDD 실행 권한 범위다.

1. **storage taxonomy unit seam**: 모든 typed code, wrapped typed error,
   `errors.As`/`Unwrap` 보존, 분류 없는 error의 false 결과.
2. **legacy adapter table seam**: 현재 지원해야 하는 lock/remote/history/open
   signature 변형과 typed 우선순위, 일반 단어를 포함한 false-positive 사례,
   불충분한 근거의 unknown 처리.
3. **pull/push/commit command seam**: legacy/envelope JSON stderr가
   `failure_code`를 포함하고 기존 `error`/`hint`/exit/stream 및 존재하는
   caller `code`를 보존. human mode 문구와 exit 불변.
4. **database-open seam**: corrupt, database missing, schema gate, unknown open
   failure의 stable code와 JSON stderr. 기존 fresh-clone 오탐 방지.
5. **auto-push warning seam**: JSON stderr 단일 warning object, exit 0,
   primary stdout payload 보존, legacy/envelope, human 및 `--quiet` 의미 불변.
6. **doctor contract seam**: 모든 check의 stable `check_code`, local-store
   `failure_code`, PID `absent|live|corrupt|dead|reused` evidence,
   `check_code` join, embedded/proxied `supported:false` JSON.
7. **credential non-leak seam**: URL userinfo/token/query credential이 error 및
   evidence JSON에 나타나지 않고 remote name/transport만 남음.
8. **consumer-shaped fixture seam**: 외부 matcher가 사용하던 대표 failure가
   raw 문구와 무관하게 stable code로 구분되며 unknown은 반드시
   `operation_failed_unknown`.

command/integration test는 격리된 temp repo와 fake/injected storage/remote를
사용한다. 프로덕션 Beads DB, 실제 remote network, 실제 lock 삭제를 사용하지
않는다.

## 문서 변경

- `docs/JSON_SCHEMA.md`: `failure_code`, 기존 `code`와의 차이, evidence,
  envelope 예시, additive enum 규칙
- `docs/ERROR_HANDLING.md`: fatal JSON error와 non-fatal JSON warning의
  stdout/stderr/exit 계약
- `docs/RECOVERY.md`: doctor/bootstrap stable fact 소비, policy 경계, raw stderr
  fallback 금지

## 검증

- Test scope의 focused package/command tests
- `make test` 또는 `./scripts/test.sh`
- `go build ./...`
- `go vet ./...`
- 변경 Go 파일 `gofmt` clean
- `git diff --check`
- canonical test가 실패하면 pinned base에서 동일 실패인지 비교하고 신규
  failure는 0이어야 한다.

## 수용 기준

1. in-scope `--json` fatal failure가 기존 stream/exit/payload를 보존하면서
   stable `failure_code`를 항상 제공한다.
2. 모르는 in-scope failure는 누락이나 외부 문자열 판정 없이
   `operation_failed_unknown`으로 표현된다.
3. auto-push failure가 primary exit/stdout을 바꾸지 않고 machine-readable
   JSON stderr warning으로 관찰된다.
4. 기존 caller-specific `code`와 새 `failure_code` 의미가 문서·테스트에서
   분리되고 envelope의 `.data`에 함께 위치한다.
5. doctor check가 stable `check_code`로 join되며 local store/PID의 typed fact를
   optional evidence로 노출한다.
6. 공개 타입과 JSON에 Dolt/MySQL 내부 타입·error number·credential이
   노출되지 않는다.
7. lock liveness를 확인할 수 없는 경우 stale/dead로 추정하거나 lock을
   삭제하지 않는다.
8. 구현과 문서가 Test scope/검증을 통과하고 기존 테스트의 신규 실패가 없다.
9. 설치된 producer 계약을 readback한 뒤 dotfiles `dotfiles-3vb8`이 raw stderr
   matcher를 제거할 수 있다.
