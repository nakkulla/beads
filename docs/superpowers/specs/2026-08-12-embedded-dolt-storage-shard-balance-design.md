# Embedded Dolt Storage shard 균형화 설계

## 문서 상태

- Bead: `beads-yvf`
- Route: `spec_backed`
- Workflow mode: `standard`
- 설계 승인: 2026-08-12 사용자 전체 설계 승인
- 기준 브랜치/SHA: `main` / `a33863a82c2853eb9f6f595b8f9b68adaf2a0819`
- 구현 단위: `PR Risk`의 Embedded Dolt Storage 실행 tail을 줄이는 독립 PR
- 선행 기준선: `beads-am7`의 승인된 spec/plan이 정의한 CI single-owner DAG

## 배경과 문제

`PR Risk`는 risky PR에서 Embedded Dolt binary를 한 번 만들고 storage, embedded
conformance, server conformance, `cmd/bd` shards를 병렬로 실행한다. 이 중
`test-embedded-storage`는 `/tmp/embeddeddolt-test`의 모든 non-conformance top-level
test를 한 job에서 직렬 실행한다. 반면 `cmd/bd` surface는 이미 20개 matrix shard로
나뉜다.

`beads-yvf`에 기록된 장기 관측치는 Embedded Dolt Storage job median 12.75분,
p90 17.35분이다. 2026-08-06~11의 origin green `PR Risk` 6회에서도 이 job이
11.55~13.98분을 사용하며 gate tail을 지배했다.

- `31096079438`
- `31405176315`
- `31445762355`
- `31458011217`
- `31467456407`
- `31503834104`

같은 6개 run의 verbose test log에서 top-level duration을 모으면 현재 binary의
59개 `Test*` 중 다음 두 개를 제외한 runnable root는 57개다.

- `TestConformance`: dedicated embedded conformance job이 소유한다.
- `TestHelperProcess`: `TestConcurrencyMultiProcess`가 subprocess entry point로 호출하며
  독립 coverage root가 아니다.

57개 test의 평균 duration 합은 773.09초다. 비용 내림차순 Longest Processing Time
(LPT) 배치를 5개 shard에 적용하면 순수 test duration 예측치는 153.24~155.28초로
수렴한다. 단순 test-count round-robin은 `TestCreateIssue` 평균 125.02초,
`TestCreateIssues` 95.76초, `TestAddDependency` 76.88초 같은 long pole을 반영하지
못한다.

## 목표

1. `test-embedded-storage`를 deterministic 5-way matrix로 나눈다.
2. 현재 binary의 모든 runnable top-level test를 정확히 한 shard에 배정한다.
3. manifest에 아직 없는 신규 test도 deterministic fallback으로 누락 없이 실행한다.
4. selected-test 목록, per-test duration, shard wall duration, exit status, full log를
   artifact로 남긴다.
5. 기존 job ID, tier detector, `fail-fast`, timeout, aggregate gate와 고유 test surface를
   보존한다.
6. 실제 `PR Risk` run에서 기존 p90 대비 tail 감소와 runner-minute 변화를 함께 기록한다.

## 비목표

- `internal/storage/embeddeddolt` production code, Dolt driver, schema, migration 변경
- test body 또는 fixture를 성능 목적으로 축소하거나 skip 처리
- `TestConformance` subtest sharding
- server Dolt conformance 또는 `cmd/bd` shard 재배치
- `ci-embedded-tier.sh`의 risky-path 판정 변경
- `PR Risk / CI Gate / Required` owner 집합 변경
- `main.yml` 또는 `nightly.yml`의 storage job sharding
- Go module/build cache key, writer, restore 정책 변경
- `beads-am7`의 policy/lint/artifact ownership 변경

`main.yml`은 현재 비활성이고 `nightly.yml`의 storage job은 별도 40분 budget을 가진다.
새 runner와 manifest는 재사용 가능하게 만들지만 이번 Bead는 활성 `PR Risk` gate만
변경한다. 다른 workflow 적용은 실제 PR Risk evidence 이후 별도 routing 대상이다.

## 소유권과 sibling 정합성

이 문서는 parent spec의 “후속 집중 PR” 분리를 따른다.

- `beads-am7`이 `pr.yml`/`main.yml` single-owner DAG와 canonical workflow contract
  test를 소유한다.
- `beads-yvf`는 `pr-risk.yml`의 `test-embedded-storage` matrix, storage shard runner,
  manifest와 timing artifact만 소유한다.
- `beads-ou6`는 `build-embedded`의 Go cache restore/save와 key family를 소유한다.
  이 문서는 cache step을 추가·삭제·변경하지 않는다.
- `beads-gkw`는 `regression.yml` detector/concurrency를 소유한다. 이 문서는 Regression
  workflow를 변경하지 않는다.

구현은 `beads-am7`의 최종 target base를 먼저 반영한다. `beads-ou6`가 먼저 landed이면
그 cache steps를 그대로 보존하고 storage matrix만 바꾼다. `beads-yvf`가 먼저 landed이면
`beads-ou6`가 이 matrix를 보존한 채 `build-embedded`에만 cache contract를 적용한다.

## 선택한 설계

### 1. binary 기반 test inventory

새 `.github/scripts/embedded-storage-test-shard.sh`는 다음 인자를 받는다.

```text
embedded-storage-test-shard.sh <shard_number> <total_shards> [test-binary args...]
```

runner는 source grep이 아니라 실제 prebuilt binary를 사용한다.

```bash
"$STORAGE_BINARY" -test.list '^Test'
```

출력 중 `^Test[A-Za-z0-9_]+$`만 top-level inventory로 인정한다. 여기서
`TestConformance`와 `TestHelperProcess`를 명시적 special owner로 제외한다. binary가
없거나 list command가 실패하거나 runnable inventory가 비면 fail closed한다. 이 방식은
build tag와 compiled test surface를 그대로 반영하므로 source 파일명·grep 규약 drift로
test가 누락되지 않는다.

기본 binary 경로는 `/tmp/embeddeddolt-test`이며
`BEADS_TEST_EMBEDDED_TEST_BINARY`로 재정의할 수 있다. list-only contract test를 위해
`BEADS_TEST_SHARD_LIST_ONLY=1`을 지원한다.

### 2. committed duration-weighted manifest

`.github/scripts/embedded-storage-test-shards.txt` 형식은 기존 cmd manifest와 맞춘다.

```text
<total_shards> <shard_number> <top_level_test_name>
```

초기 manifest는 위 6개 green run의 top-level 평균 duration을 사용해 5-way LPT로
생성한다. algorithm은 다음으로 고정한다.

1. test를 평균 duration 내림차순, 동률이면 이름 오름차순으로 정렬한다.
2. 현재 누적 duration이 가장 작은 shard에 배치한다.
3. shard load 동률이면 낮은 shard 번호를 선택한다.
4. manifest는 test 이름 오름차순의 안정된 출력으로 저장한다.

초기 predicted load는 다음 범위여야 한다.

| Shard | 예측 test duration | 배정 수 |
|---|---:|---:|
| 1 | 154.98초 | 6 |
| 2 | 154.34초 | 9 |
| 3 | 155.28초 | 10 |
| 4 | 153.24초 | 19 |
| 5 | 155.26초 | 13 |

배정 수가 다른 것은 fixture 비용 차이를 반영한 의도된 결과다. 구현에서 current base의
inventory가 달라졌으면 같은 6-run lineage 이후의 최신 green run을 추가해 동일 algorithm으로
재생성하고, 사용한 run IDs와 predicted load를 manifest header와 PR body에 기록한다.

runner는 manifest의 malformed row, out-of-range shard, duplicate test, special-owner test,
현재 binary에 없는 stale test를 오류로 처리한다. 다른 total shard 수의 row는 기존 cmd
runner와 같이 무시할 수 있지만, workflow가 사용하는 total `5`에 대한 row는 strict하다.

### 3. 신규 test의 fail-safe fallback

현재 binary에 있으나 manifest에 없는 runnable test는 다음 식으로 정확히 한 shard에
배정한다.

```text
(cksum(test_name) % total_shards) + 1
```

fallback은 누락을 막는 안전망이지 지속적인 balance 정책이 아니다. runner는 fallback
test마다 warning을 출력하고 artifact metadata에 `assignment=fallback`을 기록한다. contract
test는 신규 synthetic test가 반복 실행에서 같은 shard를 선택하고 전체 5개 shard 중 정확히
한 곳에만 나타나는지 검증한다. live run에서 fallback이 관측되면 다음 manifest refresh의
입력으로 사용하되 현재 PR을 자동 실패시키지는 않는다.

### 4. workflow matrix와 gate 보존

`pr-risk.yml`의 job ID `test-embedded-storage`를 유지하고 다음 matrix만 추가한다.

```yaml
strategy:
  fail-fast: false
  matrix:
    shard: [1, 2, 3, 4, 5]
```

display name은 `Test (Embedded Dolt Storage N/5)`가 된다. 각 leg는 현재와 같이
`detect-ci-tier`와 `build-embedded`를 필요로 하고, `full_embedded == 'true'`일 때만
실행한다. build artifact 이름과 binary 경로는 바꾸지 않는다.

test command는 runner를 통해 다음 의미를 유지한다.

- `BEADS_TEST_EMBEDDED_DOLT=1`
- `-test.v`
- `-test.count=1`
- `-test.timeout=20m`
- exact top-level `-test.run` regex

`TestConformance`는 storage shard regex에 들어가지 않으므로 기존
`test-embedded-conformance` job이 계속 소유한다. `test-server-storage`,
`test-embedded-cmd`, `test-nix`도 변경하지 않는다.

matrix job의 aggregate result는 어느 leg든 failure/cancel이면 success가 아니다. 따라서
`ci-gate.needs`, `CI_GATE_REQUIRED`, `TEST_EMBEDDED_STORAGE` env와
`CI_GATE_SKIPPED_OK` 계산은 그대로 둔다. 이 mapping을 수정하는 diff는 scope violation이다.

### 5. timing과 failure artifact

runner는 test binary 출력을 `tee`하면서 원래 exit status를 보존한다. 각 shard는 최소 다음
파일을 생성한다.

```text
artifacts/embedded-storage-shard-N-of-5-selected.txt
artifacts/embedded-storage-shard-N-of-5-timing.tsv
artifacts/embedded-storage-shard-N-of-5-summary.txt
artifacts/embedded-storage-shard-N-of-5.log
```

`selected.txt`는 test 이름, `manifest|fallback` source와 shard를 담는다.
`timing.tsv`는 verbose log에서 읽은 top-level test duration을 담고, `summary.txt`는 wall
seconds, selected count, manifest/fallback count, process exit status를 담는다. parse 실패는
test 성공을 실패로 바꾸지 않되 summary에 `timing_parse=unavailable`로 기록한다. test
process의 nonzero status는 반드시 runner의 최종 status가 된다.

workflow는 `if: always()`로 shard별 artifact를 업로드하고 retention은 7일로 고정한다.
artifact upload 실패를 test 성공으로 위장하지 않는다. test 실패와 upload 실패가 함께 있으면
둘 다 log에 보존하고 job은 nonzero다.

## 실패 처리

- binary/list 실패: shard를 실행하지 않고 nonzero로 종료한다.
- malformed/duplicate/stale manifest: nonzero로 종료한다.
- empty shard: manifest/fallback 결과가 실제로 비면 명시적 오류로 처리한다. 초기 5-way
  manifest는 모든 shard를 non-empty로 보장한다.
- 신규 test: deterministic fallback으로 실행하고 warning/artifact를 남긴다.
- test failure: 나머지 matrix leg는 `fail-fast:false`로 계속 실행하고 해당 leg는 실패한다.
- artifact parse failure: raw log와 process status를 보존하고 timing만 unavailable로 표시한다.
- artifact upload failure: 해당 leg 실패로 aggregate gate에 전달한다.

rollback은 workflow, runner, manifest, contract/docs 변경을 한 PR로 revert하는 것이다. test를
임시 skip하거나 aggregate gate에서 storage result를 허용하는 부분 rollback은 금지한다.

## upstream 기여 보존

upstream [PR #5117](https://github.com/gastownhall/beads/pull/5117)은 maphew가
manifest-driven 5-way storage sharding, `TestConformance` 분리, hash fallback과 job ID 보존을
구현해 2026-07-29 merge되었다. 현재 fork는 test inventory, workflow 경로와 duration evidence가
달라 commit을 그대로 cherry-pick할 수 없다.

구현은 #5117의 runner/manifest 구조와 safety reasoning을 먼저 검토하고 재사용 가능한 diff와
tests를 보존한다. rewrite가 필요하면 PR body에서 차이를 설명하고 commit에 적절한
`Co-authored-by` 또는 명시적 design attribution을 남긴다. 단순히 같은 아이디어를 새 구현처럼
제시하지 않는다.

## Test scope

RED-GREEN seam은 storage test assignment completeness, deterministic fallback, workflow
matrix/gate 보존, failure artifact status다.

### RED

현재 base에는 storage shard runner/manifest가 없고 `test-embedded-storage`가 단일 job이므로
다음 contract가 실패해야 한다.

1. compiled binary inventory의 57 runnable root가 정확히 한 shard에 배정된다.
2. `TestConformance`와 `TestHelperProcess`가 manifest/fallback root에 들어가지 않는다.
3. manifest duplicate/stale/malformed row가 거부된다.
4. unknown synthetic test가 누락 없이 deterministic shard에 들어간다.
5. workflow storage job이 5-way, `fail-fast:false`, unchanged job ID/gate mapping을 가진다.
6. 실패한 fake test binary status가 `tee`와 artifact 생성 뒤에도 보존된다.

### GREEN

`scripts/ci_workflow_test.go`의 YAML contract와 Linux/Bash fixture가 다음을 검증한다.

- matrix shard set이 정확히 `[1,2,3,4,5]`다.
- runner invocation의 shard total이 matrix와 일치한다.
- build artifact/download path와 test timeout이 유지된다.
- embedded conformance/server/cmd/Nix jobs와 gate owner mapping이 바뀌지 않는다.
- selected/timing/summary/log artifact가 `always()` upload된다.
- list-only fixture 전체 합집합이 expected inventory와 같고 교집합이 비어 있다.
- fallback, malformed manifest, stale row, empty inventory, binary failure, test failure를
  behavioral하게 검증한다.

contract fixture는 Ubuntu Actions와 같은 Bash 4+를 명시한다. 기존 associative-array
runner를 재사용할 경우 macOS system Bash 3.2 compatibility를 주장하지 않는다. Bash 3.2
지원이 필요하면 associative array가 없는 구현을 선택하고 별도 test로 증명해야 한다.

## 검증 bundle

구현 PR의 최소 로컬 검증은 다음이다.

```bash
bash -n .github/scripts/embedded-storage-test-shard.sh
go test ./scripts -run 'EmbeddedStorage|CIWorkflow'
go test ./scripts
git diff --check
```

가능하면 `shellcheck`와 `actionlint`를 추가하되 설치 불가를 성공으로 주장하지 않는다. 전체
storage suite를 로컬에서 5번 중복 실행하는 것은 필수 gate가 아니다. assignment/failure
semantics는 fixture로 검증하고 실제 성능은 GitHub-hosted `PR Risk`에서 확인한다.

## runtime acceptance와 성능 판정

같은 head의 green `PR Risk`에서 다음을 수집한다.

1. 5개 storage shard의 selected inventory와 fallback count
2. 각 shard test duration, wall duration, queue/start delay
3. storage matrix max/median/p90
4. `Build (Embedded Dolt)` 종료부터 `PR Risk / CI Gate / Required` 종료까지의 critical span
5. 전체 `PR Risk` runner-minutes

최소 3개의 green risky PR run을 비교한다. 기계적 hard gate는 누락 0, duplicate 0, fallback
determinism, 모든 shard success, aggregate gate success다. 성능 acceptance는 storage tail이
Bead의 기존 p90 17.35분보다 유의미하게 낮고 더 이상 반복 run의 지배적 long pole이 아닌지로
판단한다. GitHub queue와 cold cache 편차 때문에 고정 percentage를 merge hard gate로 두지
않는다. runner-minutes가 늘면 tail 감소와 함께 명시적으로 보고한다.

## Acceptance criteria

1. 현재 compiled binary의 모든 runnable storage test가 정확히 한 shard에서 실행된다.
2. manifest에 없는 신규 test는 deterministic fallback으로 누락 없이 실행된다.
3. `TestConformance`, server conformance와 `cmd/bd` shard surface가 유지된다.
4. `test-embedded-storage` job ID, tier condition, `fail-fast:false`, timeout과 aggregate mapping이
   유지된다.
5. selected tests, assignment source, per-test duration, wall duration, raw log와 exit status가
   shard artifact로 남는다.
6. contract/behavioral tests가 completeness, determinism, manifest 오류와 nonzero propagation을
   고정한다.
7. 실제 `PR Risk` evidence가 기존 p90 대비 tail 감소와 runner-minute trade-off를 기록한다.
8. cache, Regression, parent artifact DAG와 production storage code가 diff에 섞이지 않는다.
9. upstream PR #5117의 contributor value와 attribution이 보존된다.
