# Beads workflow friction root cause 수리 디자인

## 배경

`dotfiles`의 `workflow friction guardrails` spec은 Beads 관련 실패를 workflow guardrail로 조기에 감지하려는 방향이었다. 하지만 그중 일부는 Beads CLI와 Dolt/JSONL 저장 흐름의 root cause를 고치는 편이 더 낫다. 이 spec은 guardrail-only 대응을 줄이고, `/Users/isy_macstudio/External/beads` repo 자체에서 재현 테스트와 코드 수정을 통해 원인을 해결하는 작업을 정의한다.

현재 전역 `bd`는 이 checkout에서 빌드한 binary로 전환되어 있다.

- `command -v bd` → `$HOME/.local/bin/bd`
- `bd --version` → `bd version 1.0.5 (c90f4ba5b)`
- Homebrew `beads 1.0.4`는 설치되어 있지만 unlinked 상태이며 PATH의 active `bd`가 아니다.

이 전역 binary는 source tree를 실시간 참조하지 않는다. 코드 수정 후 dogfood하려면 `make install-force`로 다시 설치해야 한다.

## 목표

1. Beads write 성공 후 `bd show`, JSONL export/source, Dolt state가 서로 다르게 보이거나 rollback되는 원인을 재현 테스트로 고정하고 수정한다.
2. embedded Dolt와 JSONL auto-import 경계에서 stale configured import JSONL이 현재 DB state를 덮거나 반복 auto-import되는 조건을 제거한다. 기본 경로는 `.beads/issues.jsonl`이며, documented `import.path` custom path는 보존한다.
3. server-mode에서는 startup auto-import가 실행되지 않는 현재 boundary를 명시적으로 유지하고, stale JSONL이 server-backed writes/readbacks를 덮지 않음을 테스트로 고정한다.
4. `bd dolt push` dangling refs 실패가 발생하는 경로를 재현하거나, 이미 known-limitation인 경우 CLI가 더 안전하게 classify하고 사용자가 명시적으로 실행할 복구 안내를 출력하도록 만든다.
5. plan-backed child Bead seeding에 필요한 `bd create --id` + `--parent`, dependency/readback/reuse 흐름을 idempotent하게 만든다.
6. `.beads/.~issues.jsonl.*` 같은 editor/temp JSONL 파일이 import/export/sync source로 잘못 취급되지 않음을 보장한다.
7. 수정 후 전역 `bd`를 이 repo build로 다시 설치하고, active `bd`가 Homebrew가 아니라 local build임을 검증한다.

## 비목표

- `dotfiles` workflow-only guardrail은 이 spec에서 수정하지 않는다. 다음 항목은 dotfiles 쪽 별도 작업으로 남긴다.
  - `execution_base_sha` target-base ancestor 검증
  - PR Delivery checks/thread readiness reporting
  - plan lint / plan-review preflight
  - post-merge source verification과 live runtime install verification 분리
- Beads storage architecture 전체를 재설계하지 않는다.
- Beads-side에서 Dolt engine internals, schema tables, lock files, chunk store, commit graph를 직접 들여다보는 workaround를 추가하지 않는다. 필요한 state 판정이 현재 storage interface로 표현되지 않으면 driver interface를 좁게 확장하거나 driver-local helper로 둔다.
- Dolt upstream 원격 저장소의 chunk atomicity 한계를 근본적으로 해결하지 않는다. 이 repo에서 가능한 commit/push ordering, error classification, user-facing guidance까지만 다룬다. `bd dolt push` 실패 복구로 command layer가 implicit pull/merge 또는 storage-specific retry loop를 수행하지 않는다.
- Homebrew formula를 삭제하거나 uninstall하지 않는다. active global `bd`는 `$HOME/.local/bin/bd`로 유지하고, Homebrew `beads`는 unlinked 상태로 둔다.

## 현재 코드 관찰

직접 확인한 관련 표면은 다음과 같다.

- `cmd/bd/auto_import_upgrade.go`
  - `maybeAutoImportJSONL`은 `configuredImportJSONLPath(beadsDir)`의 JSONL이 존재하고 DB가 empty일 때 upgrade-recovery import를 수행한다.
  - `autoImportStampMatches`는 size/mtime 기반으로 동일 JSONL 반복 import를 막는다.
  - embedded path는 `jsonlImporter.ImportJSONLData`, fallback path는 `importFromLocalJSONLConflictSkip`를 사용한다.
- `cmd/bd/import_shared.go`
  - `ImportOptions.ConflictSkip`와 `filterStaleImportIssues`가 stale import 방어를 담당한다.
  - explicit `bd import`는 UPSERT semantic을 유지해야 하며, auto-import recovery만 insert-if-new/skip semantics여야 한다.
- `cmd/bd/import_path.go`
  - 기본 import path는 `.beads/issues.jsonl`이다.
  - `docs/CONFIG.md`와 `TestMaybeAutoImportJSONL_UsesConfiguredImportPath`는 `import.path` custom relative path도 empty-DB auto-import source로 지원한다.
- `cmd/bd/dolt_autopush.go`
  - `dolt.auto-push`는 opt-in이며, 기존 주석은 concurrent git+ssh Dolt push가 dangling reference를 만들 수 있음을 설명한다.
  - auto-push failure는 warning과 local `push-state.json` throttle로 처리된다.
- `cmd/bd/create.go`
  - `--id`, `--parent`, `--deps`, `--metadata`, `--spec-id`가 single issue create path에 모인다.
  - 현재 `--id`와 `--parent` 동시 사용은 hard error이며, plan task child seeding의 stable ID 요구와 충돌한다.
- `tests/regression/*`와 `cmd/bd/*_test.go`
  - 기존 regression harness와 command tests가 isolated workspace를 만들고 CLI-level behavior를 검증하는 패턴을 제공한다.

## 설계 원칙

- **재현 먼저**: root cause를 추정해서 바로 고치지 않는다. 각 failure class마다 최소 하나의 failing regression 또는 command-level test를 먼저 만든다.
- **source of truth 명확화**: auto-import recovery, explicit import, export/readback, Dolt commit/push의 책임을 섞지 않는다.
- **idempotency 우선**: create/import/child-seeding은 중간 실패 후 재실행해도 중복 issue나 stale overwrite를 만들지 않아야 한다.
- **global dogfood는 마지막 단계**: code/test가 통과한 뒤에만 `make install-force`로 전역 binary를 갱신한다.
- **destructive cleanup 금지**: Homebrew binary를 직접 `rm`하지 않는다. Homebrew는 `brew unlink beads` 상태를 유지한다.

## 설계

### 1. Auto-import rollback 재현과 수정

#### 문제 가설

사용자가 관찰한 rollback류 실패는 stale configured import JSONL(기본 `.beads/issues.jsonl`, 또는 `import.path`가 지정한 project-local relative file)이 DB empty 판정 또는 fallback import path를 통해 현재 Dolt rows 위로 재적용되는 경우와 관련될 수 있다. 현재 코드에는 size/mtime stamp, `GetStatistics` emptiness guard, `ConflictSkip`, stale `UpdatedAt` filter가 있다. 이 방어가 embedded auto-import path와 non-server fallback import path에서 실제 CLI 시나리오를 충분히 커버하는지 검증해야 한다. Server-mode startup auto-import는 현재 `shouldRunAutoImportJSONL(..., serverMode=true) == false`이므로 disabled-by-design boundary로 유지한다.

#### 구현 방향

- `cmd/bd/auto_import_upgrade_test.go` 또는 regression harness에 다음 시나리오를 추가한다.
  1. stale configured import JSONL에 오래된 issue state가 있다.
  2. Dolt DB에는 같은 ID의 더 최신 update/close/spec metadata가 있다.
  3. 일반 `bd show`, `bd update`, `bd close`, `bd list` invocation을 반복한다.
  4. stale JSONL state가 최신 DB state를 덮지 않는지 확인한다.
- `import.path` custom relative path를 쓰는 fixture도 추가하거나 기존 `TestMaybeAutoImportJSONL_UsesConfiguredImportPath`를 보존해, temp-file fix가 custom import path를 regress하지 않게 한다.
- Server-mode는 startup auto-import disabled boundary를 유지한다. CLI-level test는 server-mode read/write/readback 중 stale configured JSONL이 import source로 재적용되지 않는지를 확인하고, `shouldRunAutoImportJSONL(..., serverMode=true)` unit test는 계속 false를 기대한다.
- Non-server fallback path는 unit substitute와 CLI-level fixture가 가능한 범위에서 검증한다.
- `GetStatistics`가 temporary bootstrap/import state에서 false empty를 반환할 수 있는지 확인한다.
- false empty가 재현되면, auto-import 진입 조건을 storage boundary 안에서 강화한다.
  - Beads command code에서 Dolt directory/schema/table/chunk internals를 직접 inspect하지 않는다.
  - 필요한 판정이 storage abstraction에 없으면, `storage.Store`/driver interface에 narrow capability를 추가하거나 Dolt driver-local helper로 구현한다.
  - configured import JSONL보다 최신의 driver-reported durable write marker가 있으면 auto-import를 금지한다.
  - auto-import stamp를 성공/실패 모두에 대해 더 명시적으로 기록하되, empty DB recovery가 필요한 fresh clone path는 유지한다.

#### 수용 기준

- stale configured import JSONL이 최신 DB row를 덮지 않는 regression test가 추가된다.
- default `.beads/issues.jsonl`와 custom `import.path`의 existing contract가 모두 보존된다.
- embedded path와 non-server fallback path 중 실제 취약한 쪽이 확인되며, 수정 후 테스트가 통과한다.
- server-mode startup auto-import는 disabled-by-design으로 유지되고 stale configured JSONL이 server-mode read/write를 덮지 않는다.
- explicit `bd import`의 UPSERT semantic은 변경하지 않는다.

### 2. Write/readback/export 일관성 보장

#### 문제 가설

CLI write가 성공했지만 이후 readback에서 이전 상태가 보이는 경우는 다음 중 하나일 수 있다.

- write 후 Dolt commit/export hook 순서 문제
- `bd show`가 다른 store mode 또는 stale server instance를 보는 문제
- `.beads/issues.jsonl` export가 current DB가 아닌 stale source를 반영하는 문제
- auto-import recovery가 write 이후 다시 동작하는 문제

#### 구현 방향

- command-level test를 만들어 `bd create`, `bd update --set-metadata`, `bd close` 후 즉시 다음을 비교한다.
  - `bd show <id> --json`
  - `bd list --json`의 같은 ID
  - `.beads/issues.jsonl` export/source가 활성화된 workspace에서는 해당 line
  - 가능한 경우 Dolt current commit 또는 status
- JSON shape는 object/single-item array/list 모두 normalize하는 test helper를 사용한다.
- 문제가 stale server/cache에서 발생하면 write 후 read path가 같은 store lifecycle을 쓰는지 확인하고, 필요한 경우 write 후 cache invalidation 또는 reopen boundary를 추가한다.
- 문제가 export ordering이면 write transaction commit 이후 export가 수행되도록 순서를 고친다.

#### 수용 기준

- create/update/close 후 second readback consistency를 검증하는 regression 또는 command test가 있다.
- `bd show`와 `bd list`가 같은 issue 상태를 반환한다.
- export-enabled workspace에서 `.beads/issues.jsonl`가 write 결과와 모순되지 않는다.

### 3. `bd dolt push` dangling refs handling

#### 문제 가설

`cmd/bd/dolt_autopush.go` 주석에 따르면 concurrent git+ssh Dolt pushes는 remote manifest가 missing chunk를 참조하는 dangling reference를 만들 수 있다. 이번 작업은 Dolt upstream의 atomicity를 해결하지 않고, Beads CLI가 이 상태를 더 안전하게 분류하고 사용자에게 명시적 복구 절차를 안내하도록 한다. Command layer는 dangling refs 복구를 위해 implicit pull/merge 또는 storage-specific retry loop를 수행하지 않는다.

#### 구현 방향

- `bd dolt push` manual path와 auto-push path를 분리해 본다.
- 이미 `dolt.auto-push`는 opt-in이므로 기본 auto-push가 dangling refs를 만들지 않는지 확인한다.
- manual `bd dolt push`에서 dangling/missing chunk 계열 error message를 classify하는 helper를 추가하거나 기존 helper를 확장한다.
- Command-layer recovery는 classify-and-guide only로 제한한다.
  - `bd dolt push`가 dangling/missing chunk 계열로 실패하면 user-facing message에 `bd dolt pull && bd dolt push` one-time recovery guidance를 제공한다.
  - `bd dolt push` 자체가 recovery 목적으로 implicit `pull`, merge, second push, storage-specific retry loop를 실행하지 않는다.
  - 자동 retry/recovery가 정말 필요하다고 판단되면 이 spec의 즉시 구현 범위에서 제외하고, storage driver interface/driver-local contract 확장으로 별도 설계한다.
- remote corruption 자체를 은폐하지 않는다. dangling/missing chunk classifier가 match되면 actionable guidance와 함께 non-zero exit을 유지한다.

#### 수용 기준

- dangling/missing chunk error classifier test가 있다.
- manual push failure가 actionable message를 출력한다.
- auto-push는 기본 off 상태를 유지하고, opt-in failure는 warning/throttle을 유지한다.

### 4. Child Bead seeding idempotency

#### 문제 가설

plan-backed execution에서 child Bead seeding이 실패한 경우, `bd create --id`, `--parent`, sequential dependency edge 생성, metadata/label writes 중 일부만 성공하고 재실행 시 중복 child가 생길 수 있다.

#### 구현 방향

- CLI primitive를 `bd create --id <parent.N> --parent <parent>`가 동작하도록 확장한다.
  - `--id`와 `--parent` 동시 사용은 explicit child creation mode로 취급한다.
  - explicit ID는 `<parent>.` prefix를 가져야 한다. prefix가 없으면 parent hierarchy가 애매하므로 error를 유지한다.
  - parent validation, `--no-inherit-labels`가 없을 때 parent label inheritance, parent-child dependency creation은 일반 `--parent` path와 동일하게 수행한다.
  - explicit `--id`는 documented behavior대로 counter를 increment하지 않는다. 대신 future `--parent` auto-ID generation이 existing children을 scan/skip해서 기존 explicit child와 충돌하지 않음을 테스트한다.
  - 같은 ID로 재실행하면 duplicate error가 명확히 나오며 새 issue를 만들지 않는다.
- `cmd/bd/create.go`, validation layer, proxied create path가 같은 semantics를 공유하는지 확인한다.
- CLI primitive는 정상인데 orchestration만 문제라면, repo-local seeding helper 또는 docs/guidance는 별도 후속으로 분리한다. 이 spec의 구현은 CLI root cause에 집중한다.
- idempotent pattern은 read-before-create이다.
  1. expected child ID를 `bd show`로 확인한다.
  2. 있으면 metadata/parent/dependency parity만 보정한다.
  3. 없으면 `bd create --id <expected> --parent <parent>`로 create한다.
  4. create output parse 실패 시 즉시 두 번째 create를 하지 않고 independent readback으로 already-created 여부를 확인한다.

#### 수용 기준

- `bd create --id <parent.N> --parent <parent>` flow의 success/readback test가 있다.
- invalid `--id <non-parent-prefix> --parent <parent>`는 명확한 error를 반환한다.
- duplicate create 재시도 시 중복 issue를 만들지 않고 기존 issue를 식별할 수 있는 behavior 또는 helper contract가 있다.
- explicit child create 후 다음 auto child create가 ID collision 없이 다음 child ID를 사용한다.
- parent-child dependency와 child metadata/labels가 readback에서 검증된다.

### 5. Temp JSONL 파일 안전성

#### 문제 가설

`.beads/.~issues.jsonl.*` 같은 editor/temp file은 default import path나 configured `import.path`와 다르므로 직접 import되지는 않아야 한다. 하지만 cleanup, glob, sync helper가 잘못된 파일을 source로 삼으면 readback 혼란을 만들 수 있다. 이 spec은 custom `import.path` 지원을 보존하면서, CLI가 exact configured file만 source로 사용하고 `.beads/*.jsonl` glob으로 temp file을 자동 선택하지 않는 것을 보장한다.

#### 구현 방향

- `configuredImportJSONLPath`가 exact configured file만 반환함을 unit test로 고정한다.
  - config가 없으면 `.beads/issues.jsonl`.
  - `import.path=beads.jsonl`이면 `.beads/beads.jsonl`.
  - 어느 경우에도 `.beads/.~issues.jsonl.*` 같은 sibling temp file을 fallback/glob source로 선택하지 않는다.
- import/export/sync code에서 `.beads/*.jsonl` glob을 사용하는 곳이 있는지 확인한다.
- temp 파일 cleanup이 필요하다면 zsh glob에 의존하지 않고 Go 또는 `find` equivalent 방식으로 처리한다. 단, CLI가 임의 cleanup을 자동 수행하는 것은 데이터 삭제로 오해될 수 있으므로 기본은 “무시 보장”이다.

#### 수용 기준

- `.beads/.~issues.jsonl.*`가 있어도 auto-import source가 되지 않는 test가 있다.
- documented custom `import.path`가 계속 auto-import source로 동작한다.
- temp file 존재가 `bd show`, `bd list`, `bd update` 결과를 바꾸지 않는다.

### 6. Global dogfood install

#### 동작

수정과 검증 후 전역 binary를 갱신한다.

```bash
make install-force
hash -r
command -v bd
bd --version
type -a bd
```

기대 상태:

- `command -v bd`는 `$HOME/.local/bin/bd`이다.
- `bd --version`은 수정 commit SHA를 포함한다.
- `/opt/homebrew/bin/bd`는 존재하지 않거나 active PATH에서 사용되지 않는다.
- Homebrew `beads 1.0.4`는 uninstall하지 않고 unlinked 상태로 둔다.

## 데이터 흐름

### Auto-import recovery

1. CLI startup이 beads dir과 configured import path를 찾는다.
2. Exact configured import JSONL file이 존재하고 non-empty인지 확인한다. 기본은 `.beads/issues.jsonl`이고, `import.path`가 있으면 그 relative file만 사용한다.
3. Server-mode이면 startup auto-import를 건너뛴다.
4. auto-import stamp와 DB state를 확인한다.
5. DB가 진짜 empty이고 recovery가 필요한 경우에만 import한다.
6. import는 insert-if-new/stale-skip semantic으로 수행한다.
7. import 후 commit/stamp가 기록된다.
8. 이후 일반 write/read는 stale JSONL을 다시 적용하지 않는다.

### Write/readback

1. `bd create/update/close`가 storage write를 수행한다.
2. write transaction/commit/export hook이 완료된다.
3. 같은 issue를 `bd show`와 `bd list`로 즉시 읽는다.
4. export-enabled path에서는 `.beads/issues.jsonl`도 같은 state를 반영한다.
5. mismatch가 있으면 테스트 실패이며, 구현은 성공으로 보고하지 않는다.

### Child seeding

1. expected child ID를 계산한다.
2. existing child readback을 먼저 시도한다.
3. 없을 때만 `bd create --id <expected> --parent <parent>`를 수행한다.
4. create output parse 실패 시 independent readback을 수행한다.
5. dependency/metadata/label parity를 확인하고 필요한 보정 write를 idempotent하게 수행한다.

## 오류 처리

- **stale JSONL**: 최신 DB row보다 오래된 JSONL record는 auto-import에서 skip한다.
- **false empty DB**: empty 판정이 불확실하면 auto-import하지 않고 warning 또는 recovery guidance를 출력한다. 이 판정은 storage interface 또는 driver-local capability를 통해 수행하고 Beads command code가 Dolt internals를 직접 inspect하지 않는다.
- **write/readback mismatch**: command success로 보지 않고 test failure 또는 actionable error로 처리한다.
- **dangling refs**: known dangling/missing chunk 계열이면 classify-and-guide only로 처리한다. `bd dolt push`는 implicit pull/merge/retry를 실행하지 않고, `bd dolt pull && bd dolt push` 안내와 함께 non-zero exit을 유지한다.
- **partial child create**: 재실행 시 duplicate create를 반복하지 않고 existing child를 readback/reuse한다.
- **Homebrew conflict**: `/opt/homebrew/bin/bd`를 직접 삭제하지 않고 `brew unlink beads` 상태를 유지한다.

## 테스트 전략

우선 scoped tests를 추가하고, 마지막에 repo 기본 test를 실행한다.

- Prerequisite:
  - `scripts/pr-preflight.sh --search "auto import JSONL readback child create dolt push" --repo gastownhall/beads`
- Auto-import:
  - `cmd/bd/auto_import_upgrade_test.go`
  - `cmd/bd/auto_import_upgrade_unit_test.go`
  - 신규 stale configured JSONL vs latest Dolt row regression
  - server-mode startup auto-import disabled boundary test
  - configured `import.path` preservation test
- Import/readback:
  - `cmd/bd/import_from_jsonl_test.go`
  - 신규 create/update/close second-readback command test
- Dolt push:
  - `cmd/bd/dolt_autopush.go` 주변 classifier unit test
  - 가능한 경우 `cmd/bd/sync_push_pull_test.go` 또는 Dolt remote smoke 확장
- Child create:
  - `cmd/bd/create.go` command test for `--id <parent.N> --parent <parent>`
  - proxied create coverage when applicable
  - `internal/validation/bead_test.go` ID validation coverage
- Full validation:
  - `make test`
  - 필요 시 macOS CGO path는 raw `CGO_ENABLED=1 go test ./...` 대신 repo script/Make target을 사용한다.
- Install validation:
  - `make install-force`
  - `bd --version`
  - `type -a bd`
  - `bd show beads-urc --json`

## Follow-up disposition

- No-create: dotfiles workflow-only guardrails는 이 spec의 범위가 아니며, 이미 dotfiles spec의 별도 scope로 남아 있다.
- No-create: Homebrew formula uninstall은 필요하지 않다. active global `bd`는 `$HOME/.local/bin/bd`이며 Homebrew formula는 unlinked 상태로 충분하다.
- Blocked: Dolt upstream remote chunk atomicity 자체가 원인으로 확정되면, Beads CLI는 classify/guidance까지만 처리하고 upstream Dolt 수정은 별도 외부 이슈가 필요하다. 현재 spec은 그 외부 이슈를 미리 만들지 않는다.

## Execution lane

- Selected lane: `plan`
- Rationale: 이 작업은 auto-import, import semantics, create/parent flow, Dolt push handling, tests, 전역 install 검증을 함께 다룬다. 재현 → 수정 → scoped test → full test → dogfood install 순서가 필요하므로 별도 implementation plan이 필요하다.
- Spec replaces plan: no. 이 spec은 목표와 설계를 정의하며, 구현 전 별도 plan-review 가능한 implementation plan이 필요하다.
- Expected topology: `worktree_feature_pr`
- Child-generation policy: plan execution은 owning parent Bead 아래에 실행 task별 child Bead를 만들거나 재사용한다. child 생성은 read-before-create 방식이어야 하며, create output parse 실패 후 즉시 중복 create하지 않는다.
