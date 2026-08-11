# Beads 지침·legacy agent/slot 표면과 generic capability 정합 설계

- Bead: `beads-zha`
- route: `full_plan`
- 작성일: 2026-08-11
- 상위 설계: dotfiles
  `docs/superpowers/specs/2026-08-11-workflow-contract-consumer-export-legacy-governance-design.md`

## 1. 배경

Beads의 project charter는 core를 issue/status/dependency/metadata/comment/CLI/storage
boundary로 한정한다. 그러나 현재 active instruction과 plugin resource에는 이 경계를
넘거나 실제 CLI와 맞지 않는 표면이 남아 있다.

1. root `AGENTS.md`, `AGENT_INSTRUCTIONS.md`, `cmd/bd/AGENTS.md`에 unconditional
   pull/push/cleanup을 요구하는 `Landing the Plane` 문구가 있다. 같은 root `AGENTS.md`의
   managed profile은 conservative/minimal에서 explicit authority 없이는 commit, push,
   Dolt sync를 금지한다.
2. `plugins/beads/skills/beads/SKILL.md`와
   `plugins/beads/skills/beads/resources/AGENTS.md`는 존재하지 않는 `bd agent`, `bd slot`,
   first-class `--type=agent|role`을 안내한다.
3. 과거 agent/slot command는 제거됐지만 현재 `bd merge-slot`과 storage `MergeSlot*`,
   metadata `SlotSet/Get/Clear`는 다시 도입되어 active primitive로 사용된다. 모든 slot을
   dead surface로 취급하면 실제 기능을 파괴한다.
4. server-mode init은 custom `resolved` status를 idempotent하게 seed하지만 doctor의
   config validator는 modern `resolved:done` category syntax를 flat token처럼 검사해
   올바른 설정을 invalid로 보고할 수 있다.
5. base `Storage` interface에 integration 성격이 강한 merge/metadata slot method가
   required method로 포함돼 generic mock/proxy/adapter가 불필요한 기능까지 구현해야 한다.

이 스펙은 workflow semantics를 Beads core로 옮기지 않는다. active instruction을 실제
authority와 CLI에 맞추고, generic primitive의 compatibility를 고정하며, 이미 존재하는
slot 기능만 optional capability seam으로 단계적으로 이동한다.

## 2. 사용자 확정 결정

- Beads core는 workflow route/model/retry/review/receipt/PR lifecycle을 소유하지 않는다.
- `resolved`는 generic custom status로만 유지하고 built-in 또는 `closed` alias로 만들지
  않는다.
- native `issue.spec_id`, arbitrary JSON metadata, append-only comment, dependency metadata,
  JSON schema v2/envelope는 compatibility authority로 보존한다.
- 존재하지 않는 `bd agent`/`bd slot`과 first-class agent/role lifecycle 문서는 dead
  surface로 제거한다.
- active `bd merge-slot`은 narrow coordination primitive로 보존한다.
- unconditional push/sync 지시는 제거하고 explicit user/repo/orchestrator authority가
  profile default보다 우선하는 한 방향 규칙으로 정합한다.
- CAS, comment idempotency, status registry SDK 같은 새 generic API는 두 개 이상의 실제
  Adapter 수요와 실패 seam이 증명되기 전에는 추가하지 않는다.
- old issue data, metadata, comments, dependency rows, historical specs/ADRs는 rewrite/delete하지
  않는다.
- instruction/plugin 정리와 storage capability migration은 같은 commit에 섞지 않는다.

## 3. 선행 dependency와 구현 admission

| Bead | 상태/소유 범위 | `beads-zha` 경계 |
|---|---|---|
| `beads-qxg` | closed, JSON schema v2 fork release delivery | v2/spec_id/metadata compatibility baseline으로 사용 |
| `beads-1nj` | structured recovery error taxonomy/doctor JSON | error code, retry/recovery policy를 재정의하지 않고 producer 결과를 사용 |

스펙 작성과 review는 지금 가능하다. 구현 진입은 `beads-1nj`가 closed이고 merged/installed
`bd`의 structured error/readback이 확인된 뒤에만 허용한다. dependency가 아직
open/in_progress/blocked이면 parent를 claim하거나 phase child를 만들지 않는다.

지침·plugin 정리, status/data conformance, storage capability migration은 독립 phase와
verification boundary를 가지므로 route는 `full_plan`이다. 이 스펙은 durable plan이나
implementation-entry authority가 아니다.

## 4. Core ownership map

| Concern | Beads core가 소유 | 외부 consumer가 소유 |
|---|---|---|
| status | built-in/custom registry, validation, category | `resolved`의 PR Delivery 의미 |
| lifecycle | exact built-in `closed`, `closed_at`, dependency satisfaction | resolved/closed 전이 순서와 merge/deploy gate |
| metadata | namespaced arbitrary JSON set/merge/unset/query | route/reviewer/runtime key의 의미 |
| comments | ID/time/order, append/list/count/iter | completion report marker/section/dedupe policy |
| dependencies | edge, readiness, closed-only satisfaction | cross-repo 작업 분해와 provider ordering |
| JSON | v2 envelope, arity, typed error/evidence | UI normalization과 workflow projection |
| storage | engine-agnostic Interface와 optional capability | scheduler/agent lifecycle/state machine |
| `SpecID` | compatibility-stable artifact locator | spec review/freshness/route authority |

Beads는 dotfiles workflow consumer artifact를 import하지 않는다. beads-ui가 필요로 하는
workflow parser/helper도 core에 추가하지 않는다.

## 5. Instruction authority와 generated source

### 5.1 Authority order

git/commit/push/Dolt sync 권한은 다음 한 방향으로 해석한다.

1. 현재 user turn이 exact operation을 승인한 경우
2. 현재 repo가 active profile을 exact `team-maintainer`로 opt-in한 경우
3. 선택된 `conservative|minimal` setup profile과 managed Beads block의 default
4. 일반 예시, inherited prose, historical documentation

remote가 존재하거나 Beads repo라는 사실만으로 team-maintainer authority를 추론하지 않는다.
`Landing the Plane`은 사용자가 그 exact workflow를 요청했거나 active repo instruction이
exact `team-maintainer` profile을 명시한 경우에만 적용한다. 단순히 “session을 끝낸다”,
“작업을 완료한다”, `MUST/NEVER push` 같은 inherited/legacy 문구는 opt-in이 아니다.
모순이 있으면 conservative/minimal이 그 legacy prose보다 우선한다. 해당 profile에서는
changed files, verification, proposed commands를 보고하고 commit/push/Dolt sync를 수행하지
않는다.

### 5.2 Source locality

- generated managed block의 canonical source는
  `internal/templates/agents/defaults/agents.md.tmpl`, `beads-section.md`,
  `beads-section-minimal.md`, `beads-section-codex.md`와
  `internal/templates/agents/{agents.go,render.go}`다.
- root `AGENTS.md`의 managed marker/hash block은 renderer 결과로만 갱신한다.
- full/minimal output의 `BEGIN/END BEADS INTEGRATION` marker와 project/global Codex setup의
  `BEGIN/END BEADS CODEX SETUP` marker를 모두 generated consumer로 검증한다.
- root local preamble, `AGENT_INSTRUCTIONS.md`, `cmd/bd/AGENTS.md`는 owner 문서를 가리키고
  push procedure를 복제하지 않는다.
- `cmd/bd/AGENTS.md`는 package-specific architecture, focused test command, non-interactive
  guard만 소유한다.
- setup의 remote/no-push 감지와 marker replacement behavior는 유지한다.

root/nested instruction은 다음 outcome을 보장해야 한다.

- explicit authority가 없으면 commit, push, pull/rebase, stash clear, remote prune, Dolt
  push/pull을 실행하지 않는다.
- explicit PR/publish workflow가 있으면 그 owner의 safety/order를 따른다.
- `bd edit` 대신 non-interactive command를 사용하고, production DB를 테스트에 쓰지 않는다.
- 같은 rule은 한 owner에만 있고 다른 instruction은 링크한다.

## 6. Plugin agent/slot dead surface

### 6.1 제거 대상

- `plugins/beads/skills/beads/SKILL.md`의 `Agent beads | bd agent --help` row와 관련 link
- `plugins/beads/skills/beads/resources/AGENTS.md`의 agent heartbeat/state machine,
  `bd agent`, `bd slot`, `--type=agent|role` 안내
- `plugins/beads/skills/beads/README.md`의 agent-bead/version 지원 주장
- `docs/MOLECULES.md`의 `bd agent list` 예시
- `website/docs/multi-agent/index.md`와
  `website/docs/multi-agent/coordination.md`의 현재 CLI에 없는 `bd pin`, `bd hook`,
  `bd agents`, `bd reserve|reservations`, `bd lock|unlock` 예시. 문서 전체를 삭제할 필요는
  없지만 현재 존재하는 assignment/dependency primitive로 다시 쓰거나 unsupported pattern으로
  명시한다.
- `cmd/bd/doctor.go` help의 `Agent bead integrity`와 그 생성물인
  `docs/CLI_REFERENCE.md`, `website/docs/cli-reference/doctor.md`의 동일 claim
- 그 밖의 active docs에 남은 존재하지 않는 `bd agent`/`bd slot` command 예시
- first-class built-in agent/role type가 현재 CLI에 있다는 주장

resource에 다른 고유한 generic guidance가 없다면 파일을 삭제하고 skill은 `bd prime`과
실제 `bd --help`를 source of truth로 유지한다. historical commit/spec을 수정하지 않는다.

`website/static/llms-full.txt`는 `scripts/generate-llms-full.sh`가 latest released Docusaurus
snapshot에서 만드는 active 배포 artifact다. current live docs를 고쳤다는 이유로 이 파일을
직접 편집하거나 frozen `website/versioned_docs/**`를 소급 수정하지 않는다. registry에는
`historical-generated` reader로 기록하고, 다음 stable docs snapshot이 cleaned live docs를
채택할 때 generated output에서 dead command가 사라지는 후속 gate를 둔다. fork prerelease는
stable docs snapshot을 만들지 않으므로 이 gate를 현재 fork release 완료로 오인하지 않는다.

### 6.2 보존·명확화 대상

- `bd merge-slot` command와 holder/waiter/queue semantics
- custom/integration-defined `agent`, `role` type string의 backward compatibility
- built-in `message` type과 현재 infra/export/router behavior
- Gastown/Gas City가 metadata/custom type/merge-slot primitive를 소비할 수 있는 extension
  boundary

`bd merge-slot`은 agent lifecycle, heartbeat, role command가 아니다. active docs와 help는
이 차이를 명시한다. `bd slot`을 alias나 compatibility command로 새로 만들지 않는다.

custom `agent`/`role` infra type의 기존 default/router는 이번 PR에서 bulk migration하지
않는다. built-in type처럼 표시하거나 새 workflow behavior를 붙이지 않고, compatibility
surface로 registry에 기록한 뒤 실제 caller/hit evidence가 있는 별도 migration에서만
제거한다. `message`는 built-in이므로 infra/export/router handling을 그대로 보존한다.

## 7. Custom `resolved` status conformance

### 7.1 유지할 의미

- built-in status는 현재 enum을 유지하며 `resolved`를 추가하지 않는다.
- server-mode init의 `status.custom` seed는 generic custom-status registration이다.
- existing flat `resolved`, categorized `resolved:done`, 다른 custom status를 보존하고
  missing `resolved`만 idempotent하게 추가한다.
- `bd list --status resolved --json`은 registry에 있을 때 성공하고 없을 때 명시적으로
  실패한다.
- `resolved`는 `closed_at`을 쓰지 않고 dependency/readiness/compaction에서
  `closed`와 동등하지 않다.

### 7.2 Doctor validator 정합

`cmd/bd/doctor/config_values.go`는 자체 comma/regex split 대신 core custom-status parser와
동일한 parse result를 사용한다.

- valid flat/category config는 같은 status/category set으로 판단한다.
- malformed token, duplicate name(같은 category 반복과 category mismatch 모두), reserved
  built-in collision(`open`, `in_progress`, `blocked`, `deferred`, `closed`, `pinned`,
  `hooked`)은 기존
  canonical validator의 typed error로 보고한다.
- doctor는 invalid config를 자동 rewrite하지 않는다.
- `bd statuses --json`, init seed, list filter, doctor가 같은 registry meaning을 공유한다.

server-only seed가 workflow fleet policy라는 점은 문서에 명시한다. generic extension hook이
생기기 전에는 현재 호환 동작을 옮기지 않지만, 다른 init mode로 확대하지도 않는다.

## 8. Generic data compatibility

### 8.1 `SpecID`와 metadata

- `Issue.SpecID`는 native artifact locator와 content hash input으로 보존한다.
- metadata는 arbitrary JSON과 namespaced key 규칙을 유지한다.
- 새 route/review/runtime field를 `Issue` struct에 추가하지 않는다.
- `--set-metadata` string과 `--set-metadata-json` typed value, query semantics를 바꾸지 않는다.

### 8.2 Comments

- normal append는 immutable ID/time/order를 유지한다.
- legacy numeric comment ID import와 current string/UUID ID round-trip을 유지한다.
- import의 existing exact duplicate handling을 normal workflow report dedupe로 일반화하지
  않는다.
- generic idempotency key API는 이번 범위에서 추가하지 않는다. 실제 독립 consumer 두 곳이
  같은 atomic append-if-absent를 요구할 때 별도 spec으로 승격한다.

### 8.3 Dependencies와 JSON

- dependency metadata와 cross-prefix closed-only satisfaction을 유지한다.
- custom `resolved`는 external dependency를 만족시키지 않는다.
- closed `beads-qxg`가 게시한 `schema_version=2`를 pinned baseline으로 삼는다. 구현 admission
  이후 legacy bare와 v2 envelope가 같은 semantic data를 내고 v2 field/arity/unknown-additive
  contract를 유지한다.
- `beads-1nj`가 추가한 structured failure code/evidence를 그대로 보존하며 raw message
  classifier를 새로 만들지 않는다.

## 9. Optional storage capability migration

### 9.1 이번에 추가하지 않는 capability

다음은 generic하게 보이지만 현재 다중 Adapter 요구가 증명되지 않았으므로 비범위다.

- issue status/version/hash CAS
- comment idempotency key
- 별도 custom status registry SDK
- workflow receipt/report transaction

필요가 생기면 independent caller 둘, atomicity failure, fallback behavior, embedded/Dolt
conformance seam을 먼저 제시해야 한다.

### 9.2 기존 slot capability 분리

현재 base `Storage`의 active method를 다음 narrow Interface로 이름 붙인다. 아래 signature와
기존 `MergeSlotStatus`/`MergeSlotResult`/error wrapping은 exact compatibility contract다.

```go
type MergeSlotStore interface {
    MergeSlotCreate(ctx context.Context, actor string) (*types.Issue, error)
    MergeSlotCheck(ctx context.Context) (*MergeSlotStatus, error)
    MergeSlotAcquire(ctx context.Context, holder, actor string, wait bool) (*MergeSlotResult, error)
    MergeSlotRelease(ctx context.Context, holder, actor string) error
}

type MetadataSlotStore interface {
    SlotSet(ctx context.Context, issueID, key, value, actor string) error
    SlotGet(ctx context.Context, issueID, key string) (string, error)
    SlotClear(ctx context.Context, issueID, key, actor string) error
}

type StorageUnwrapper interface {
    UnwrapStorage() CoreStorage
}
```

`CoreStorage`는 현재 `Storage` method set에서 merge/metadata slot 일곱 method만 제외한
engine-agnostic Interface다. compatibility window 동안 exported `Storage`는
`CoreStorage + MergeSlotStore + MetadataSlotStore`를 embed해 기존 source를 보존한다.
새 generic code는 `CoreStorage`를 받고 slot caller만 narrow capability를 요구한다.

resolver contract는 다음과 같다.

```go
var ErrUnsupportedCapability = errors.New("storage capability unsupported")

type CapabilityName string

const (
    CapabilityMergeSlot    CapabilityName = "merge_slot"
    CapabilityMetadataSlot CapabilityName = "metadata_slot"
)

type CapabilityUnsupportedReason string

const (
    CapabilityNilStore        CapabilityUnsupportedReason = "nil_store"
    CapabilityUnwrapCycle     CapabilityUnsupportedReason = "unwrap_cycle"
    CapabilityUnsupportedLeaf CapabilityUnsupportedReason = "unsupported_leaf"
)

type CapabilityUnsupportedError struct {
    Name   CapabilityName
    Reason CapabilityUnsupportedReason
}

func (e *CapabilityUnsupportedError) Error() string
func (e *CapabilityUnsupportedError) Unwrap() error { return ErrUnsupportedCapability }

func ResolveMergeSlotStore(store CoreStorage) (MergeSlotStore, error)
func ResolveMetadataSlotStore(store CoreStorage) (MetadataSlotStore, error)
```

- nil, unwrap cycle, leaf 미지원은 해당 `CapabilityName`과 stable reason을 가진
  `*CapabilityUnsupportedError`를 반환한다. caller는 `errors.Is(err,
  ErrUnsupportedCapability)`와 `errors.As` 둘 다 사용할 수 있고 message parsing으로
  capability를 복원하지 않는다.
- known decorator는 direct type assertion보다 먼저 `StorageUnwrapper`로 leaf까지 unwrap한다.
  따라서 forwarding method가 있는 wrapper가 unsupported inner를 supported로 보이게 하지
  않는다.
- leaf가 capability를 구현하면 그 exact value를 반환한다.
- `CommandContext.Store storage.DoltStorage`와 legacy global `store`는 이 compatibility PR에서
  type을 바꾸지 않는다. 대신 `cmd/bd/merge_slot.go`의 Cobra wrapper 아래에
  `runMergeSlot*WithStore(ctx, store storage.CoreStorage, ...)` command-core seam을 두고, 각
  helper가 resolver result만 사용한다. production wrapper는 `getStore()`를 넘기고 unit test는
  `CoreStorage`-only fake를 직접 주입한다.
- 실제 runtime backend는 모두 slot capability를 지원하므로 unsupported behavior의 authority는
  resolver와 command-core unit test다. production-only fake backend 선택 flag나 hidden CLI
  injection hook은 추가하지 않는다. supported runtime CLI의 JSON/human output은 기존 focused
  command test가 계속 소유한다.
- metadata-slot caller도 resolver result만 사용하되 별도 command가 없으므로 storage/use-case
  unit test에서 unsupported path를 고정한다.
- `InstrumentedStorage`와 다른 decorator는 `UnwrapStorage()`를 제공하고, capability operation
  telemetry는 resolved leaf를 감싼 narrow decorator가 기록한다.
- `DoltStorage`는 compatibility window 동안 기존 full composition을 유지하되 generic
  helper/conformance는 `CoreStorage`와 optional capability를 별도로 검증한다.
- embedded/Dolt leaf는 두 capability를 지원한다. test fake/proxy 하나는 `CoreStorage`만
  구현해 unsupported path를 고정한다.

새로운 slot semantics나 schema는 추가하지 않는다.

migration은 두 단계다.

1. `CoreStorage`, optional capability와 resolver를 추가하고 internal command/use case,
   telemetry, conformance, decorator, fake/mock가 capability discovery를 사용하도록 옮긴다.
   exported `Storage` composite는 deprecated compatibility로 남긴다.
2. current release와 다음 release 또는 30일 중 긴 기간 동안 static caller와 compatibility
   hit를 관측한다. caller 0, both backend conformance, unsupported fallback, rollback이
   검증된 뒤 별도 removal Bead/PR에서 base method를 제거한다.

decorator는 unsupported inner store를 supported처럼 보이게 해서는 안 된다. resolver는
wrapped store를 정확히 unwrap/discover하거나 stable unsupported result를 반환한다.
`bd merge-slot`은 supported backend에서 기존 JSON/human output과 transaction/holder/waiter
idempotency를 유지하고, unsupported backend에서는 panic이나 silent no-op 대신 typed
unsupported error를 낸다.

metadata slot은 arbitrary integration storage primitive로만 설명한다. core는 bytes/atomic
update Interface를 소유하고, GT와 다른 external integration은 delegation tracking,
heartbeat, workflow lease, hook state의 의미를 소유한다. 기존 reader/writer와 stored key를
재해석·삭제하지 않으며 compatibility fixture로 round-trip을 보존한다.

## 10. Local legacy registry

새 `docs/legacy-surfaces.yaml`은 active compatibility/dead surface를 기록한다.

초기 entry는 최소한 다음을 포함한다.

| ID | classification | disposition |
|---|---|---|
| `bd-agent-command` | `dead` | plugin SKILL/resource/README, MOLECULES claim 제거 + negative help test |
| `bd-slot-command` | `dead` | plugin SKILL/resource claim 제거, `merge-slot`과 구분 + negative help test |
| `first-class-agent-role` | `dead` 또는 custom-type compatibility로 분해 | built-in claim 제거, custom type data 보존 |
| `agent-bead-doctor-help` | `dead` | doctor help source 수정 + CLI reference/website 재생성 |
| `removed-multi-agent-cli-examples` | `dead` | live multi-agent docs의 `pin|hook|agents|reserve|lock` 예시를 current primitive로 교체 |
| `released-llms-snapshot` | `historical-generated` | frozen release source 보존, 다음 stable snapshot에서 cleaned live docs 채택 |
| `unconditional-plane-push` | `dead` | explicit authority rule로 교체 |
| `storage-base-merge-slot` | `compatibility` | optional `MergeSlotStore`로 이동 뒤 removal gate |
| `storage-base-metadata-slot` | `compatibility` | optional `MetadataSlotStore`로 이동 뒤 removal gate |

`active_readers`는 broad glob이 아니라 exact path를 기록한다. 최초 registry에는 최소
`plugins/beads/skills/beads/{SKILL.md,README.md,resources/AGENTS.md}`,
`docs/MOLECULES.md`, `website/docs/multi-agent/{index.md,coordination.md}`,
`cmd/bd/doctor.go`, generated `docs/CLI_REFERENCE.md`와
`website/docs/cli-reference/{doctor.md,create.md,index.md}`가 들어간다. generated CLI docs는
live Cobra help를 source로 하고 `scripts/generate-cli-docs.sh`와
`scripts/check-cli-docs-drift.sh`를 통해 갱신한다. `website/static/llms-full.txt`는
`scripts/generate-llms-full.sh`와 latest released `website/versioned_docs/**` lineage를 가진
별도 `historical-generated` reader로 기록한다.

slot capability entry의 exact reader/implementation 목록은
`internal/storage/storage.go`, `cmd/bd/{context.go,merge_slot.go}`,
`internal/storage/{merge_slot.go,hook_decorator.go}`,
`internal/storage/{dolt,embeddeddolt}/{merge_slot.go,slots.go}`,
`internal/telemetry/storage.go`, `internal/storage/conformance/conformance.go`,
`internal/storage/{dolt,embeddeddolt}/conformance_test.go`,
`internal/jira/tracker_test.go`다. 새 reader가 발견되면 registry를 먼저 늘리고 broad prose로
뭉개지 않는다.

`historical` docs/spec/commit은 registry에서 frozen reference일 수 있지만 active checker의
removal 대상은 아니다. compatibility/dead entry는 owner, replacement, active readers,
new-write 금지, static caller 목표, test, observation window, rollback evidence를 가진다.

dead command처럼 구현 자체가 없으면 runtime hit는 `not_applicable`로 둘 수 있지만
`implementation_absent`, static caller 0, negative CLI/help test가 필요하다.

## 11. 구현 phase와 commit 경계

### Phase 1 — instruction/plugin dead surface

- authority/profile 문구와 source locality 정합
- root/cmd detailed instruction 중복 제거
- plugin agent/slot resource/index 퇴역
- active docs scan과 negative CLI/help contract
- local legacy registry의 dead entries

이 phase는 Go storage/status 코드를 수정하지 않는다.

### Phase 2 — custom status와 data conformance

- doctor custom status validator를 canonical parser와 정합
- custom `resolved`/closed-only dependency invariant tests
- SpecID/metadata/comments/dependency/JSON v2 compatibility fixture

이 phase는 instruction/plugin 파일과 storage interface를 수정하지 않는다.

### Phase 3 — optional slot capability introduction

- `MergeSlotStore`, `MetadataSlotStore`, resolver/discovery
- internal caller/decorator/telemetry/conformance/fake migration
- `CoreStorage` 도입과 exported `Storage` compatibility composite 유지
- supported/unsupported backend와 rollback tests

이 phase는 base compatibility method를 삭제하지 않는다. 제거는 observation gate 뒤 별도
Bead/PR가 소유한다.

### Phase 4 — fork delivery metadata

- implementation 시작 시 최신 fork version과 `beads-1nj` delivery를 readback한다.
- `scripts/bump-version.sh`는 deprecated fail-fast stub이므로 사용하지 않는다.
- current base가 그대로인 다음 fork build(예: `1.2.0-fork.2`)는
  `cmd/bd/version.go`의 fork suffix만 올리고 base-version asset을 rewrite하지 않는다.
- base가 바뀌는 첫 fork build는 `scripts/update-versions.sh <new-base> --skip-docs`로
  CLI/plugin/marketplace/MCP/npm base surface를 올린 뒤 `cmd/bd/version.go`만
  `<new-base>-fork.1`로 고정한다. fork prerelease는 stable Docusaurus snapshot을 만들지 않는다.
  stable release에서만 `scripts/snapshot-release-docs.sh <stable-version>`을 실행한다.
- 두 경로 모두 `scripts/check-versions.sh`, `scripts/check-docs-version.sh`, exact asset
  version readback을 통과하고 breaking/compatibility note를 작성한다.
- version bump와 release note는 별도 commit으로 두고 instruction/status/storage diff에
  섞지 않는다.
- exact tag/release publication은 PR merge 뒤 post-merge operator endpoint가 소유한다.

각 phase는 독립 commit, focused verification, controller full diff acceptance 뒤 seal한다.

## 12. Test scope

이 절의 seam만 `beads-zha` 구현의 TDD authority다. production Beads DB, 실제 remote network,
실제 사용자 git hook/slot state를 사용하지 않는다.

### Seam 1 — instruction authority/render

대상: root `AGENTS.md`, `AGENT_INSTRUCTIONS.md`, `cmd/bd/AGENTS.md`, agent template
renderer/marker tests.

- conservative/minimal profile은 explicit authority 없이 commit/push/Dolt sync를 지시하지
  않는다.
- team-maintainer behavior는 explicit profile/repo instruction에서만 나타난다.
- root managed marker/hash와 remote/no-push conditional rendering이 유지된다.
- full/minimal/Codex source와 project/global generated marker가 모두 byte/current 검사를
  통과한다.
- nested `cmd/bd/AGENTS.md`는 owner pointer와 package-specific tests만 가진다.
- active instruction에 unconditional `NEVER stop before pushing` 정의가 재유입되면 실패한다.
- current-user exact authority 또는 explicit `team-maintainer` opt-in 외의 legacy/inherited
  prose가 commit/push 권한을 만들지 않는다.

RED 소재: 현재 root/detailed/cmd instruction이 conservative block과 충돌한다.

### Seam 2 — plugin dead command retirement

대상: plugin SKILL/resource, active docs, CLI/help contract.

- active source가 `bd agent` 또는 `bd slot` command 존재를 주장하지 않는다.
- `bd agent --help`, `bd slot --help`는 stable unknown-command/nonzero이고 이를 supported
  path로 문서화하지 않는다.
- `bd merge-slot --help`와 focused command tests는 계속 통과한다.
- built-in type/help에 agent/role이 재등장하지 않고 existing custom infra data fixture는
  round-trip된다.
- built-in `message`와 current infra/export/router fixture는 변하지 않는다.
- `cmd/bd/doctor.go` help를 고친 뒤 `scripts/generate-cli-docs.sh`의 check/generation path로
  `docs/CLI_REFERENCE.md`와 `website/docs/cli-reference/{doctor.md,create.md,index.md}`가 같은
  live Cobra 결과를 갖는다.
- `website/docs/multi-agent/{index.md,coordination.md}`에는 current CLI에 없는
  `pin|hook|agents|reserve|reservations|lock|unlock`을 supported command로 제시하지 않는다.
- `website/static/llms-full.txt`와 `website/versioned_docs/**`는 이 PR에서 hand edit하지 않고,
  registry의 다음 stable snapshot gate를 보존한다.

RED 소재: plugin index/resource와 active docs에 nonexistent command가 있다.

### Seam 3 — custom status/doctor conformance

대상: core parser, init/statuses/list/doctor.

- flat `resolved`, categorized `resolved:done`, 여러 custom status가 같은 registry로
  parse된다.
- server init seed가 missing resolved만 추가하고 existing bytes/meaning을 보존한다.
- doctor가 valid category syntax를 invalid로 보고하지 않는다.
- duplicate name과 모든 built-in collision은 명시적으로 실패하고 자동 rewrite되지 않는다.
- `resolved` issue는 `closed_at`이 없고 dependency를 만족하지 않는다.

RED 소재: doctor validator가 `resolved:done`을 flat token regex로 오판한다.

### Seam 4 — generic data compatibility

대상: type/storage/CLI/protocol fixtures.

- `SpecID`와 metadata가 create/update/import/export/hash에서 보존된다.
- normal comment UUID/string ID, legacy numeric import, ordering과 exact import duplicate behavior가
  유지된다.
- dependency metadata와 cross-prefix closed-only satisfaction이 유지된다.
- qxg baseline 이후 `schema_version=2`에서 legacy bare와 v2 envelope가 같은 semantic data를
  내고 v2 field/arity가 변하지 않는다.
- structured recovery code/evidence가 추가 field와 함께 보존된다.

RED 소재: 이 seam은 cleanup이 compatibility를 깨뜨리지 못하게 하는 characterization
boundary다. 새 behavior를 만드는 RED가 아니라 phase 2/3 candidate의 regression gate다.

### Seam 5 — optional capability discovery

대상: storage interface/resolver, embedded/Dolt/decorator/telemetry/fakes.

- supported backend에서 두 optional capability가 발견되고 기존 result/output이 같다.
- unsupported fake/proxy는 supported로 오인되지 않고 typed unsupported를 반환한다.
- nil/unwrap cycle/unsupported가 `errors.Is(ErrUnsupportedCapability)`와
  `errors.As(*CapabilityUnsupportedError)`를 모두 만족하고 exact `CapabilityName`/reason을
  보존한다.
- transaction/holder/waiter idempotency와 concurrent acquire behavior가 유지된다.
- metadata slot set/get/clear JSON round-trip이 유지된다.
- GT delegation/hook-state fixture의 existing key/value가 reinterpret/delete 없이 보존된다.
- internal caller는 composite `Storage`의 slot method가 아닌 resolver/capability를 사용한다.
- `runMergeSlot*WithStore` command-core에 `CoreStorage`-only fake를 주입하면 structured
  unsupported가 반환되고 panic/silent no-op/문자열 parsing이 없다. supported binary-level
  CLI output은 기존 merge-slot command fixture가 그대로 검증한다.
- deprecated `Storage` composite를 이번 phase에서 축소/삭제하는 mutation은 실패한다.

RED 소재: optional Interface/resolver가 없고 internal caller가 base method를 직접 사용한다.

### Seam 6 — local legacy registry/removal gate

대상: `docs/legacy-surfaces.yaml` parser/contract test.

- dead/compatibility/historical classification별 required/forbidden field가 검증된다.
- dead command는 implementation absence, static caller 0, negative test가 필요하다.
- slot compatibility는 replacement, readers, new-write 금지, observation window, rollback test가
  필요하다.
- frozen historical path를 active removal 대상으로 등록할 수 없다.
- registry 없는 fallback/retired command claim이 active source에 생기면 실패한다.

RED 소재: 현재 local taxonomy/removal gate가 없다.

## 13. Verification

phase focused test 뒤 repo canonical bundle을 실행한다.

```bash
env TEST_TIMEOUT=10m make test
go test -tags gms_pure_go ./internal/templates/agents/... ./internal/types/... ./internal/storage/... ./cmd/bd/...
go vet -tags gms_pure_go ./...
go build -tags gms_pure_go ./...
test -z "$(gofmt -l $(git ls-files '*.go'))"
```

instruction/plugin contract checker와 `docs/legacy-surfaces.yaml` focused test를 canonical
`make test` 경로에 포함한다. temp git repo는 repo-local `core.hooksPath`를 사용하고 temp
HOME/DB를 격리한다. baseline failure는 pinned base에서 byte/normalized failure set이 같음을
증명한 경우에만 별도로 기록하며 새 failure를 pass로 세지 않는다.

## 14. Publish·deploy·readback

1. phase commit을 하나의 `beads-zha` branch/PR에 통합하고 `main`을 target으로 한다.
2. PR lane은 self-merge하지 않고 PR Delivery에서 멈춘다.
3. merge 뒤 durable main에서 `docs/agents/repo-ops.toml [verify]`의
   `env TEST_TIMEOUT=10m make test`를 실행한다.
4. `[deploy] make install-force`를 실행하고 installed `bd` path/version을 readback한다.
5. `bd --help`, `bd agent --help`, `bd slot --help`, `bd merge-slot --help`,
   `bd statuses --json`으로 supported/dead/custom status 경계를 확인한다.
6. generated AGENTS marker/hash와 conservative/minimal output을 fixture/readback한다.
7. PR에 포함된 exact version commit이 merged main인지 확인하고
   `scripts/check-versions.sh`/`scripts/check-docs-version.sh`를 다시 통과시킨 뒤
   `v<base>-fork.<N>` tag를 게시한다. `.github/workflows/fork-release.yml`의 fork-only release와
   linux amd64/arm64, darwin amd64/arm64 네 asset이 exact tag/version을 가리키는지 readback한다.
   plugin/marketplace/MCP/npm은 fork suffix가 아니라 base version을 유지한다.
8. local legacy registry에서 dead removal과 slot compatibility gate가 정확히 반영됐는지
   확인한 뒤 completion report를 남긴다.

`make install-force`는 local runtime delivery만 소유한다. tag/fork release publication은
`[deploy]`가 커버하지 않는 required post-merge endpoint이며 credential/외부 publication
authority가 필요한 interactive-only work다. 설치, release, asset readback 중 하나라도
실패하면 `beads-zha`를 closed로 추정하지 않는다.

### 14.1 Spec-gate disposition

- seam soundness는 §12의 여섯 RED/characterization seam이 소유한다.
- live apply order는 merged main verify -> `make install-force` -> installed CLI boundary
  readback -> exact version tag/release -> asset/version readback 순서로 고정한다.
- target local deploy는 `docs/agents/repo-ops.toml [deploy]`가 소유한다.
- fork tag/release는 `[deploy]`가 커버하지 못하는 required interactive-only no-PR residue다.
- 별도 release Bead로 split하지 않고 current `beads-zha`의 post-merge endpoint로 유지하므로,
  formal spec receipt write에서는 `worker-ineligible` label을 같은 logical write에 추가하고
  exact label/readback을 확인한다. release endpoint를 dependency-backed Bead로 분리하는
  후속 spec revision이 생길 때만 label을 제거할 수 있다.

## 15. 비범위

- dotfiles workflow artifact import
- route/review/runtime/completion report/PR semantics의 core schema 추가
- `resolved` built-in/closed alias
- `bd agent`, `bd slot` compatibility alias 신설
- `bd merge-slot` 제거 또는 agent lifecycle로 확장
- custom infra type data bulk migration
- issue/metadata/comment/dependency historical data rewrite
- 새 CAS/comment-idempotency/status-registry API
- `beads-1nj` failure taxonomy 또는 consumer retry policy 재구현
- exported `Storage` compatibility composite의 이번 PR 축소/삭제
- historical specs/ADRs/versioned docs 수정

## 16. 완료 조건

1. active instruction의 git authority가 conservative default와 명시적 opt-in으로 한 방향
   정합되고 중복 push procedure가 없다.
2. active plugin/docs가 nonexistent `bd agent`/`bd slot`을 안내하지 않으며
   `bd merge-slot`은 narrow primitive로 유지된다.
3. custom `resolved`와 category parser가 init/statuses/list/doctor에서 일치하고 built-in
   closed semantics를 침범하지 않는다.
4. SpecID/metadata/comments/dependencies/JSON v2/structured error compatibility가 regression
   fixture로 고정된다.
5. existing slot method의 optional capability/discovery path가 생기고 internal caller가 이를
   사용하며 base compatibility method는 observation window 동안 유지된다.
6. dead/compatibility surface가 local registry와 removal gate를 가지고 historical data는
   보존된다.
7. focused/canonical verification, merged-main install, fork release와 installed/distributed
   version readback이 통과한다.
