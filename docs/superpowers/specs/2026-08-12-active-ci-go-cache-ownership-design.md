# 활성 CI Go cache 소유권 설계

## 문서 상태

- Bead: `beads-ou6`
- Route: `spec_backed`
- Workflow mode: `standard`
- 설계 승인: 2026-08-12 사용자 전체 설계 승인
- 기준 브랜치/SHA: `main` / `a33863a82c2853eb9f6f595b8f9b68adaf2a0819`
- 구현 단위: active CI의 Go module/build cache writer와 restore consumer를 고정하는 독립 PR
- 선행 기준선: `beads-am7`의 승인된 spec/plan이 정의한 CI single-owner DAG

## 배경과 문제

현재 fork의 `PR`, `PR Risk`, `Nightly Full Tests`는 여러 job에서 `actions/setup-go`를
사용하지만 `cache:` 값을 명시하지 않는다. 따라서 cache enablement, key, restore/save와
writer 선정이 action의 implicit behavior에 의존한다. cache hit가 표시되어도 archive가 작거나
필요한 race/non-race build object가 없으면 module download와 compile 비용을 다시 지불한다.

upstream [PR #5278](https://github.com/gastownhall/beads/pull/5278)은 implicit
`setup-go` cache writer가 7,688-byte Linux entry를 먼저 publish해 required jobs가 cache hit
후에도 dependencies와 build를 다시 수행한 사례를 해결했다. module/build family를 분리하고
`main.yml`의 designated heavy jobs를 writer로 만들었다.

이 fork에서는 같은 topology를 그대로 사용할 수 없다.

- `main.yml`은 GitHub에서 `disabled_manually` 상태다.
- active PR jobs와 fork의 parent `beads-am7` target topology가 upstream과 다르다.
- `PR`/`PR Risk`는 untrusted pull-request code를 실행하므로 cache publish owner가 될 수 없다.
- `Nightly`는 active/trusted지만 하루 한 번이며 main merge 직후 cache seed freshness를 보장하지
  않는다.
- `ci-measurements.yml`은 cold/warm 비교용 surface이므로 implicit cache가 측정 의미를 흐린다.

따라서 active trusted `push main` writer를 별도 workflow로 만들고, PR/PR Risk/Nightly는
restore-only consumer로 제한한다.

## 목표

1. active workflow만으로 Linux Go module, race build, non-race build cache의 writer와 consumer가
   성립한다.
2. cache family/profile마다 trusted writer를 정확히 하나로 고정한다.
3. PR과 PR Risk에서는 restore만 허용하고 save를 금지한다.
4. exact key와 restore prefix, path와 `GOCACHE` profile을 contract test로 고정한다.
5. `setup-go` implicit cache를 끄고 cache miss/hit 의미를 repository contract로 만든다.
6. Go source뿐 아니라 현재와 미래의 unknown compiler/embed input change도 trusted writer를
   실행시킨다.
7. cold writer와 warm PR의 hit, cache size, setup/download/build 시간을 기록한다.
8. `beads-am7` artifact DAG 및 sibling Regression/storage sharding diff와 분리한다.

## 비목표

- 비활성 `main.yml`을 재활성화하거나 cache writer로 사용
- macOS/Windows cache family 추가
- Regression baseline binary cache(`~/.cache/beads-regression`) 변경
- release, migration, cross-version, Nix, package cache 변경
- GitHub cache backend 또는 action source 수정
- Go dependency/version 변경
- build/test command의 의미, tags, race mode 또는 coverage policy 변경
- `PR`/`PR Risk` aggregate owner 집합 변경
- `beads-yvf` storage matrix 또는 `beads-gkw` detector/concurrency 변경
- cache hit percentage나 고정 wall-clock 감소를 merge hard gate로 지정

이번 unit은 현재 active primary CI가 사용하는 `ubuntu-latest`만 소유한다. macOS/Windows
workflow가 active required topology로 승격되면 별도 measured writer/profile spec을 거친다.

## 소유권과 sibling 정합성

- `beads-am7`은 `pr.yml`/`main.yml` artifact, policy, lint single-owner DAG를 소유한다.
- `beads-ou6`는 `go-cache.yml`, in-scope `setup-go cache:false`, `beads-go-*` restore/save key
  family와 `GOCACHE` wiring만 소유한다.
- `beads-yvf`는 `pr-risk.yml` storage job matrix/manifest/artifact를 소유한다. 이 문서는
  `test-embedded-storage` matrix를 추가·제거하지 않고 `build-embedded` cache input만 다룬다.
- `beads-gkw`는 `regression.yml`을 소유한다. 이 문서는 Regression workflow와 baseline binary
  cache를 변경하지 않는다.

구현은 `beads-am7`이 landed된 target base에서 시작한다. `beads-yvf`가 이미 landed이면 5-way
storage matrix와 gate mapping을 그대로 보존한다. 두 diff가 같은 `pr-risk.yml`에 있어도 cache
step은 `build-embedded`, sharding은 downstream storage job이라는 semantic boundary를 유지한다.

## 실행 단위 disposition과 Worker eligibility

writer/consumer workflow, contract tests와 docs는 현재 Bead의 한 PR이 운반한다. merge commit은
`go-cache.yml` change를 포함하므로 cold writer run을 자동으로 시작한다. 하지만 writer 성공 뒤
요구하는 warm risky PR 최소 3회는 merge 후 별도 PR event를 기다려야 하며 현재
`docs/agents/repo-ops.toml`의 managed deploy command가 그 표본을 생성하지 않는다.

따라서 warm evidence 수집은 현재 Bead의 required no-PR interactive residue다. formal spec gate를
닫을 때 `spec_review`와 같은 logical write로 `worker-ineligible` label을 추가한다. cold writer와
세 warm consumer의 URL, matched key, size와 duration을 read back한 뒤 label을 제거하고 Bead를
완료할 수 있다. evidence requirement를 dependency-backed Bead+PR로 옮기거나 완화하려면 spec
delta review가 필요하다.

## 선택한 설계

### 1. explicit trusted writer workflow

새 `.github/workflows/go-cache.yml`은 `Go Cache`라는 독립 non-required workflow다.

```yaml
on:
  push:
    branches: [main]
    paths-ignore:
      - 'docs/**'
      - '.beads/**'
      - '.github/ISSUE_TEMPLATE/**'
      - '.github/PULL_REQUEST_TEMPLATE.md'
      - 'AGENTS.md'
      - 'AGENT_INSTRUCTIONS.md'
      - 'ARTICLES.md'
      - 'BENCHMARKS.md'
      - 'CHANGELOG.md'
      - 'CLAUDE.md'
      - 'CONTRIBUTING.md'
      - 'FEDERATION-SETUP.md'
      - 'NEWSLETTER.md'
      - 'PROPOSAL-pull-config-wedge.md'
      - 'PR_MAINTAINER_GUIDELINES.md'
      - 'README.md'
      - 'RELEASING.md'
      - 'SECURITY.md'
      - 'build-docs.md'
      - 'LICENSE'
      - 'NOTICE'
  workflow_dispatch:
```

이 workflow는 required check가 아니므로 path filter가 pending required check를 만들지 않는다.
positive `paths` allowlist 대신 proven-safe `paths-ignore`만 사용한다. 따라서 위 docs/metadata-only
surface만 바뀐 main push는 불필요한 warm compile을 피하고 그 밖의 unknown path는 모두 writer를
실행한다. 이 docs/metadata safe surface는 `beads-gkw` classifier와 동일하다. Regression만의 기존
scoped `_test.go` exemption은 compile cache input이므로 writer ignore에는 포함하지 않는다.

전역 `**/*.md` ignore는 금지한다. 현재 binary는
`internal/templates/skills/beads/SKILL.md`, `internal/templates/agents/defaults/*.md`,
`internal/templates/skills/beads/agents/openai.yaml`, `internal/storage/schema/migrations/*.sql`,
`plugins/beads/.copilot-plugin/plugin.json` 등을 `go:embed`로 컴파일한다. 이 target들은 ignore에
포함되지 않아 변경 시 writer를 실행한다. contract test는 모든 current `//go:embed` pattern을
tracked file로 전개하고 어느 target도 `paths-ignore`에 걸리지 않는지 검증한다. 새 compiler input은
기본적으로 실행되므로 별도 allowlist 갱신이 필요 없다.

permissions는 `contents: read`로 제한한다. concurrency group은 main ref별 하나이며
`cancel-in-progress: true`다. manual dispatch는 진단·seed 용도지만 cache save는
`github.ref == 'refs/heads/main'`일 때만 허용한다. feature branch ref에서 수동 실행하면 restore와
warm command는 볼 수 있어도 shared writer key를 publish하지 않는다.

writer job은 다음 순서다.

1. checkout
2. pinned `setup-go` with `cache: false`, `id: setup-go`
3. module/non-race/race cache restore
4. `go mod download`
5. non-race warm build
6. race warm compile
7. cache inventory/timing summary
8. successful main-ref run에서만 miss family save

warm commands는 active consumer와 같은 compiler contract를 사용한다.

```bash
source ./.buildflags
go mod download
GOCACHE="$RUNNER_TEMP/go-cache/non-race" \
  go build -tags gms_pure_go -o /tmp/beads-cache-warm-bd ./cmd/bd
BEADS_TEST_SKIP=dolt GOCACHE="$RUNNER_TEMP/go-cache/race" \
  go test -race -short -tags gms_pure_go -run '^$' -skip '^TestEmbedded' ./...
```

`-run '^$'`는 test body를 실행하지 않고 active PR core/embedded packages의 race compile cache를
채운다. `BEADS_TEST_SKIP=dolt`는 package `TestMain`이 Dolt container를 시작하지 않게 하고,
`-short`와 `-skip '^TestEmbedded'`는 PR core의 compile profile을 따른다. warm command가 external
service를 시작하지 않고 runnable test body가 0개였다는 log를 contract fixture와 첫 writer run에서
확인한다. 이 조건을 만족하지 않으면 cache를 save하지 않고 spec delta review로 돌아간다.

save는 warm commands와 summary가 모두 성공한 경우만 실행한다. partial/failed warm result를 exact
immutable key로 publish하지 않는다. `cache-hit == 'true'`이면 같은 exact key를 다시 compress/save
하지 않는다.

### 2. cache family와 exact keys

upstream #5278의 검증된 v2 key schema를 유지한다.

#### Module cache

Path:

```text
~/go/pkg/mod
```

Exact key:

```text
beads-go-mod-v2-${{ runner.os }}-${{ runner.arch }}-go-${{ steps.setup-go.outputs.go-version }}-${{ hashFiles('go.mod', 'go.sum') }}
```

Restore prefix:

```text
beads-go-mod-v2-${{ runner.os }}-${{ runner.arch }}-go-${{ steps.setup-go.outputs.go-version }}-
```

module version directory는 immutable하므로 compatible prior `go.mod` generation을 fallback으로
restore해도 안전하다. exact `go.mod`/`go.sum` hash는 current dependency set의 hit를 구분한다.

#### Build cache

Paths:

```text
${{ runner.temp }}/go-cache/non-race
${{ runner.temp }}/go-cache/race
```

Exact key:

```text
beads-go-build-v2-${{ runner.os }}-${{ runner.arch }}-go-${{ steps.setup-go.outputs.go-version }}-base-gms_pure_go-<profile>-${{ github.sha }}
```

Restore prefix:

```text
beads-go-build-v2-${{ runner.os }}-${{ runner.arch }}-go-${{ steps.setup-go.outputs.go-version }}-base-gms_pure_go-<profile>-
```

`<profile>`은 `race` 또는 `non-race`다. race objects를 non-race directory에 섞지 않는다.
추가 compiler tags/options는 Go content-addressed cache identity가 구분하므로 base
`gms_pure_go` generation을 restore하되 `GOCACHE` directory는 profile별로 고정한다. exact key에
commit SHA를 넣어 successful writer output을 immutable generation으로 만들고, prefix fallback은
최신 compatible generation을 재사용한다.

key version `v2`, segment 순서, path와 restore-prefix trailing `-`는 contract다. 일부 job만
다른 prefix/version을 쓰지 않는다.

### 3. writer cardinality와 save 조건

`beads-go-*` family의 active writer는 `go-cache.yml`의 한 job뿐이다.

- module save: 정확히 1개
- non-race build save: 정확히 1개
- race build save: 정확히 1개

각 save condition은 다음 의미를 모두 포함한다.

```text
main ref AND all warm steps succeeded AND exact restore was not a hit
```

PR, PR Risk, Nightly, reusable measurements에는 `actions/cache/save`와 monolithic
`actions/cache`를 두지 않는다. restore는 pinned `actions/cache/restore`, save는 pinned
`actions/cache/save`만 사용한다. action tag가 아니라 repository policy에 맞는 full commit SHA를
고정하고, 구현 시 current allowed major와 upstream security update를 확인한다.

### 4. restore-only consumer inventory

parent `beads-am7` target topology 기준 consumer는 다음으로 제한한다.

| Workflow/job | Module | non-race | race | 사용처 |
|---|---|---|---|---|
| `pr.yml` / `build-artifacts` | restore | restore | - | reusable binary build |
| `pr.yml` / `pr-core-wrapper` | restore | - | restore | `make ci-pr-core` |
| `pr-risk.yml` / `build-embedded` | restore | restore | restore | server conformance binary와 race embedded binaries |
| `nightly.yml` / `full-test` | restore | restore | restore | non-race build와 race integration test |
| `nightly.yml` / `embedded-storage` | restore | - | restore | race storage test binary compile |

각 job의 `setup-go`에는 `cache: false`를 명시한다. restore step은 setup-go 뒤, 첫 Go
download/build/test 앞에 둔다. 해당 command에 정확한 `GOCACHE` env를 전달한다.

나머지 in-scope setup-go job도 implicit cache publisher가 되지 않도록 `cache: false`를
명시하되, measured benefit이 없는 job에 restore boilerplate를 복제하지 않는다. restore consumer
inventory 확장은 실제 duration/compile evidence가 있는 별도 spec delta다.

`regression.yml`, release/migration/cross-version의 `~/.cache/beads-regression`은 Go build cache가
아니므로 그대로 둔다. 비활성 `main.yml`은 이번 active topology 밖이다. 재활성화 전에 이 cache
contract의 reader/writer audit를 별도로 통과해야 한다.

### 5. measurement workflow는 명시적 uncached

`nightly.yml`의 manual measurement path가 호출하는 `ci-measurements.yml`은 cold/warm 비교와
command timing을 위한 도구다. 모든 `setup-go`에 `cache: false`를 명시하고 `beads-go-*`
restore/save를 추가하지 않는다. 따라서 measurement result가 default action cache의 우연한 hit로
변하지 않는다.

warm cache 효과를 측정할 때는 measurement workflow를 암묵적으로 바꾸지 않고, active PR/PR
Risk job의 실제 action log와 timing artifact를 사용한다. 별도 warm measurement suite가 필요하면
입력으로 cold/warm mode를 드러내는 후속 spec을 작성한다.

### 6. observability와 evidence

writer는 `scripts/ci/lib/timing.sh`의 existing timer를 사용하거나 같은 출력 계약으로 다음
구간을 기록한다.

- module restore result와 `go mod download`
- non-race restore result와 build
- race restore result와 compile
- `du -sh` module/non-race/race directory
- exact hit 여부, resolved key, save attempted/skipped

secret이나 raw environment 전체를 출력하지 않는다. cache key는 source-derived non-secret이고
기록 가능하다. GitHub action 자체의 restore/save log와 job summary를 runtime evidence로 보존한다.

consumer PR에서도 build step 전후 duration과 restore hit를 기록한다. observability step이
실패해 test/build success를 덮지 않도록 size probe는 명시적으로 unavailable을 처리하지만, key
또는 restore action failure는 job failure다.

## poisoning과 신뢰 경계

1. untrusted `pull_request`/`merge_group` workflow는 v2 family를 restore만 한다.
2. save action은 trusted main-ref writer에만 존재한다.
3. exact build key는 source SHA와 compiler profile을 포함한다.
4. race/non-race directory를 분리한다.
5. setup-go implicit cache를 끈다.
6. failed/partial writer는 save하지 않는다.
7. manual feature-ref run은 save하지 않는다.
8. cache archive의 실행 파일을 직접 신뢰 근거로 사용하지 않는다. Go toolchain은 content-addressed
   cache validation을 수행하고 CI는 source에서 final binary/test를 다시 만든다.

cache restore가 실패하거나 miss이면 job은 uncached build로 계속할 수 있다. action infrastructure
error를 단순 miss로 위장하지 않는다. restore action이 명시적 failure를 반환하면 job failure다.

rollback은 writer workflow, consumer restore/GOCACHE wiring, explicit `cache:false`, tests/docs를 한
PR로 revert하는 것이다. consumer만 남기고 writer를 제거하거나 implicit setup-go cache를 다시
켜는 부분 rollback은 cold/mixed ownership을 만들므로 금지한다.

## upstream 기여 보존

upstream PR #5278은 julianknutsen이 module/build 분리, v2 key schema, race/non-race profile,
restore-only PR, designated writer와 contract tests를 구현해 2026-08-02 merge했다. fork의 비활성
Main과 active workflow 차이 때문에 writer placement는 다시 설계하지만 key/profile/safety tests의
contributor value는 그대로 활용한다.

구현자는 #5278 diff와 최신 upstream `scripts/ci_workflow_test.go`를 먼저 대조한다. 재사용한 tests,
key schema와 reasoning은 PR body에 출처를 쓰고, material code/test adaptation에는 적절한
`Co-authored-by` 또는 명시적 design attribution을 남긴다. fork topology가 다르다는 이유로 기여를
지우고 같은 설계를 무관한 새 작업처럼 작성하지 않는다.

## Test scope

RED-GREEN seam은 implicit cache 제거, exact key/prefix, unique writer, restore-only trust boundary와
profile wiring이다.

### RED

현재 base에는 active trusted writer가 없고 setup-go cache가 implicit하므로 다음 contract가
실패해야 한다.

1. `go-cache.yml`에 module/race/non-race unique writer가 존재한다.
2. PR/PR Risk에 save action이 없고 relevant setup-go가 `cache:false`다.
3. consumer가 exact v2 key/prefix/path를 restore한다.
4. Go command가 matching race/non-race `GOCACHE`를 사용한다.
5. measurement workflow가 명시적 uncached다.

### GREEN static contract

`scripts/ci_workflow_test.go`는 `gopkg.in/yaml.v3`로 changed workflows를 parse하고 다음을
검증한다.

- writer trigger/branch/proven-safe `paths-ignore`, permissions와 concurrency
- broad `**/*.md` ignore가 없고 모든 current `//go:embed` target이 ignore 밖임
- `beads-gkw` detector가 함께 존재하면 두 workflow의 docs/metadata safe class가 동일하고,
  regression-specific `_test.go` exemption은 writer ignore에 없음
- cache action이 full SHA로 pinned됨
- module/build exact key와 restore-prefix byte equality
- path, profile, restore step ID와 save `if` 조건
- family/profile별 save cardinality가 정확히 1임
- save가 writer main-ref job 밖에 없음
- PR/PR Risk/Nightly consumer inventory와 restore-before-command ordering
- all in-scope setup-go `cache:false`
- matching command에 explicit `GOCACHE`
- `ci-measurements.yml`의 setup-go는 `cache:false`이고 별도 Go cache restore/save가 없음
- parent aggregate owner/job ID와 yvf matrix가 변경되지 않음

test는 step name만 검색하지 않고 parsed `uses`, `with`, `if`, env와 job/event structure를
확인한다. key segment 하나, restore trailing dash, profile 또는 writer condition이 달라도 실패해야
한다.

### GREEN behavioral/command checks

workflow contract 외에 writer warm command를 repository 환경에서 dry compile한다.

```bash
source ./.buildflags
go mod download
GOCACHE="$(mktemp -d)/non-race" go build -tags gms_pure_go -o /tmp/beads-cache-warm-bd ./cmd/bd
```

race all-package compile은 비용이 크므로 implementation environment와 CI budget에 맞춰 실행하고,
실행하지 못하면 actual writer run을 required evidence로 남긴다. temp cache path는 명시적으로 만든
디렉터리만 사용하고 삭제 범위를 넓히지 않는다.

## 검증 bundle

```bash
go test ./scripts -run 'GoCache|CIWorkflow'
go test ./scripts
actionlint .github/workflows/go-cache.yml \
  .github/workflows/pr.yml \
  .github/workflows/pr-risk.yml \
  .github/workflows/nightly.yml \
  .github/workflows/ci-measurements.yml
git diff --check
```

`actionlint`가 설치되지 않았으면 그 사실을 blocker/next-best check로 보고하고 YAML parse만으로
runtime correctness를 주장하지 않는다. full `make test`는 workflow-only/cache topology change의
필수 local gate가 아니며, actual active workflows가 source compile/test를 다시 실행한다.

## runtime acceptance

### Cold writer

첫 trusted main seed run에서 다음을 기록한다.

- exact restore miss/fallback 상태
- module/non-race/race directory size
- `go mod download`, non-race build, race compile duration
- 세 save action 결과와 published keys

### Warm consumer

writer 성공 이후 같은 Go/toolchain generation의 risky PR 최소 3회에서 다음을 기록한다.

- module/non-race/race exact 또는 prefix hit
- restored archive size
- setup/download/build/compile duration
- `PR`/`PR Risk` critical span과 runner-minutes

key가 새 source SHA를 포함하므로 PR은 일반적으로 exact build hit보다 main의 compatible prefix
fallback을 사용한다. 이것은 설계된 동작이며 `cache-hit=false`만 보고 cache 미사용으로 오판하지
않는다. action log의 matched key와 build duration을 함께 본다.

기계적 acceptance는 writer uniqueness, untrusted save 0, exact key/prefix, successful cold save와
warm restore다. 성능 수치는 queue와 code diff 편차가 있으므로 fixed percentage hard gate가
아니다. 기대한 2~4분 절감이 관측되지 않으면 cache size/profile/matched key evidence로 원인을
분석하고, 효과 없는 consumer는 별도 reviewed change에서 제거한다.

## Acceptance criteria

1. active trusted main-ref workflow가 module/non-race/race v2 family의 유일 writer다.
2. PR과 PR Risk는 restore-only이며 save action이 0개다.
3. in-scope setup-go가 모두 `cache:false`로 implicit ownership을 제거한다.
4. exact key, restore prefix, path, race/non-race profile과 `GOCACHE` wiring이 contract test로
   고정된다.
5. docs/metadata-only만 writer를 skip하고 unknown path와 모든 current `go:embed` target change는
   writer를 실행한다.
6. Nightly는 designated cache를 restore하고 measurements workflow는 명시적 uncached다.
7. failed/partial/manual feature-ref writer가 shared cache를 save하지 않는다.
8. cold writer와 warm PR evidence가 hit/matched key, size, setup/download/build duration을 기록한다.
9. `beads-am7` artifact DAG, `beads-yvf` storage matrix, `beads-gkw` Regression과 기존 baseline
   binary cache가 diff에 섞이지 않는다.
10. upstream PR #5278의 contributor value와 attribution이 보존된다.
