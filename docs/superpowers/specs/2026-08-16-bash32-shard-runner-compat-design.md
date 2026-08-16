# Embedded storage shard runner Bash 3.2 호환 설계

## 문서 상태

- owning Bead: `beads-iov`
- route: `spec_backed`
- workflow mode: `standard`
- 사용자 설계 승인: 2026-08-16
- 기준 브랜치/SHA: `main` / `6154290bb0c3066b75cc15ea545e9e2099484716`
- parent behavior spec:
  `docs/superpowers/specs/2026-08-12-embedded-dolt-storage-shard-balance-design.md`
- incident row: beads-ui `beads-yvf`

## 배경과 root cause

`.github/scripts/embedded-storage-test-shard.sh`는 manifest-driven storage shard를 실행하며
shebang으로 `#!/usr/bin/env bash`를 선언한다. 구현은 inventory membership과 manifest mapping에
두 associative array를 사용한다.

```bash
declare -A ALL_TEST_SET=()
declare -A MANIFEST_SHARDS=()
```

macOS system `/bin/bash`는 3.2이며 associative array를 지원하지 않는다. 따라서 runner는
argument와 binary/manifest가 정상이어도 첫 `declare -A`에서 exit 2로 종료한다. 첫 failure를
제거해도 두 번째 declaration에서 동일하게 실패하므로 두 associative array가 모두 같은
portability defect의 active root cause다.

기존 parent spec은 Linux/Bash 4+ fixture를 명시했고 Bash 3.2 compatibility를 주장하지 않았다.
따라서 이 failure는 과거 durable row만의 stale 현상이 아니라 현재 main에서 macOS
`/bin/bash`로 재현되는 source bug다.

beads-ui의 `beads-yvf` 행에 남은 과거 `verify_cmd_failed`는 현재 cleanup config와 무관해
stale이다. 행 정리는 beads-ui `UI-bwpk`의 cleanup mutation으로 수행한다. 이 Bead는 그 행을
삭제하는 대신 독립적인 runner portability root cause만 수정한다.

## 사용자 결과

1. macOS system `/bin/bash` 3.2에서 list-only shard assignment가 정상 완료된다.
2. compiled inventory의 모든 runnable test가 이전과 같은 manifest 또는 deterministic fallback
   shard에 정확히 한 번 배정된다.
3. duplicate, stale, malformed, special-owner, out-of-range manifest는 이전과 같이 fail closed한다.
4. normal execution의 exact regex, timing artifacts, process exit status와 workflow gate contract는
   바뀌지 않는다.
5. Linux/Bash 4+에서도 기존 behavior와 output artifact가 그대로 유지된다.

## 목표

1. runner에서 Bash 4 전용 associative array 사용을 제거한다.
2. Bash 3.2가 지원하는 indexed arrays와 exact linear lookup으로 membership/mapping semantics를
   보존한다.
3. `/bin/bash` behavioral fixture와 forbidden-feature assertion으로 regression을 고정한다.
4. parent shard balance spec의 inventory, fallback, artifact, exit-status contract를 변경하지
   않는다.

## 비목표

- shard manifest 또는 5-way assignment 재생성
- test inventory, special-owner set, `cksum` fallback algorithm 변경
- `pr-risk.yml`, job ID, matrix, timeout, aggregate gate 또는 artifact upload 변경
- runner를 POSIX `sh`로 재작성
- Bash 4 설치를 macOS prerequisite로 추가
- shell portability framework나 새 dependency 도입
- storage test body, fixture, Go production code 변경
- beads-ui durable queue 직접 수정
- GitHub Actions workflow 추가 또는 CI owner 변경

## 소유권과 영향 표면

canonical runtime owner는
`.github/scripts/embedded-storage-test-shard.sh`다. active behavioral checker는
`scripts/ci_workflow_test.go`의 `EmbeddedStorageShardRunner` tests다.

수정 범위는 다음 두 파일로 제한한다.

- `.github/scripts/embedded-storage-test-shard.sh`
- `scripts/ci_workflow_test.go`

manifest, workflow, generated artifact와 production Go package는 변경하지 않는다. parent spec의
기존 contract를 이 문서에 재정의하지 않고 Bash compatibility seam만 확장한다.

## 선택한 설계

### 1. inventory exact membership

`ALL_TESTS`는 이미 filtered, sorted, newline-delimited top-level test 목록이다. 이를 associative
set으로 다시 복제하지 않고 Bash 3.2-compatible helper가 exact line equality로 검색한다.

```text
inventoryContains(test_name):
  ALL_TESTS를 line 단위로 순회
  candidate == test_name이면 success
  끝까지 없으면 failure
```

substring, regex 또는 unquoted word matching은 사용하지 않는다. 따라서 `TestFoo`와
`TestFooBar`는 서로 다른 이름으로 유지된다. manifest stale-row validation은 이 helper를
사용한다.

inventory는 현재 수십 개 수준이고 manifest parse 시 각 row를 한 번 확인하므로 O(n²)의 작은
bounded scan은 별도 dependency나 임시 파일 index보다 단순하고 충분하다.

### 2. manifest mapping

`MANIFEST_SHARDS[test_name]=shard` associative map은 같은 index를 공유하는 두 indexed array로
대체한다.

```text
MANIFEST_TESTS[i]  = test_name
MANIFEST_SHARDS[i] = shard_number
```

manifest row를 읽을 때 기존 validation 순서를 유지한다. 현재 total shard와 일치하는 row만
대상으로 삼고, `MANIFEST_TESTS`를 exact equality로 선형 검색해 duplicate를 거부한 뒤 두
array에 같은 index로 append한다.

parse 후 stale validation은 각 index의 test name에 `inventoryContains()`를 적용한다. inventory
assignment 단계에서는 `manifestShardFor(test_name)`이 동일 index를 검색해 shard를 반환한다.
없을 때만 기존 `cksum` fallback을 실행한다.

indexed array 두 개의 length와 index alignment는 runner 내부 invariant다. append는 한 block에서
연속 수행하고 array를 독립적으로 수정하는 다른 경로를 만들지 않는다.

### 3. Bash 3.2 compatibility boundary

runner는 Bash script로 유지하며 다음 현재 기능은 Bash 3.2 지원 범위이므로 그대로 사용한다.

- indexed arrays
- `[[ ... ]]`와 regex match
- arithmetic expressions
- here-string
- `PIPESTATUS`
- parameter expansion과 `set -euo pipefail`

`declare -A`, `mapfile`/`readarray`, nameref, case conversion expansion처럼 Bash 4+ 전용 기능을
새 구현에 도입하지 않는다. 이번 seam의 static checker는 최소한 associative array declaration이
runner에 없음을 요구한다. behavioral checker는 runner를 `env bash` wrapper가 아니라 명시적인
`/bin/bash`로 실행한다.

macOS에서는 이 runtime check가 실제 Bash 3.2를 사용한다. Linux CI의 `/bin/bash`가 더
새 버전이어도 static assertion이 known failure mechanism의 재도입을 막는다.

### 4. behavior preservation

lookup representation 외 동작은 바꾸지 않는다.

- binary inventory filter와 `TestConformance` / `TestHelperProcess` exclusion 유지
- malformed, duplicate, stale, special-owner, out-of-range manifest rejection 유지
- other-total rows ignore contract 유지
- fallback 식 `(cksum(test_name) % total_shards) + 1` 유지
- empty shard fail-closed 유지
- selected/timing/summary/log artifact format 유지
- exact top-level `-test.run` regex 유지
- `tee` 이후 `PIPESTATUS[0]` exit status 유지
- list-only summary contract 유지

새 temporary state, cache, manifest rewrite 또는 fallback file은 추가하지 않는다.

## 실패 처리

- inventory/manifest validation failure는 기존 message와 nonzero status를 유지한다.
- indexed arrays가 비거나 lookup이 없으면 manifest hit으로 추정하지 않고 fallback 또는 stale
  failure로 분기한다.
- helper는 exact match만 허용하며 regex-special test name을 해석하지 않는다.
- actual test process failure는 artifact 생성 뒤 원래 exit status로 반환한다.
- `/bin/bash` fixture failure는 compatibility regression으로 취급한다. 다른 shell로 재시도해
  성공으로 덮지 않는다.

## Test scope

RED-GREEN seam은 current main에서 macOS `/bin/bash`가 `declare -A` exit 2를 내는 exact
portability failure다.

### RED

1. source runner가 `declare -A`를 포함하지 않아야 한다는 assertion은 현재 실패한다.
2. fixture를 explicit `/bin/bash`로 list-only 실행하면 현재 macOS에서 artifact 생성 전에 exit
   2로 실패한다.

### GREEN

`scripts/ci_workflow_test.go`에 focused test를 추가하거나 기존 runner contract test를 확장해
다음을 검증한다.

- runner source에 `declare -A`가 없다.
- explicit `/bin/bash` list-only run이 성공한다.
- 5개 shard의 합집합과 교집합이 기존 expected inventory를 유지한다.
- `TestNew` fallback이 반복 실행에서도 동일한 한 shard에만 배정된다.
- duplicate/stale/malformed/zero-total/special/out-of-range manifest가 계속 실패한다.
- test process status 37과 selected/timing/summary/log artifact가 보존된다.
- exact regex에서 special-owner tests가 제외된다.

focused verification:

```bash
/bin/bash -n .github/scripts/embedded-storage-test-shard.sh
go test ./scripts -run 'EmbeddedStorageShardRunner|CIWorkflow'
git diff --check
```

pre-handoff verification:

```bash
go test ./scripts
make test
git diff --check
```

`make test`가 환경 prerequisite 또는 unrelated pinned-base failure로 실행 불가하면 exact failure와
next-best check를 보고하고 성공으로 주장하지 않는다.

## Delivery와 upstream policy

이 변경은 Beads 저장소의 별도 non-empty PR로 전달한다. implementation 전
`CONTRIBUTING.md`와 maintainer preflight 절차를 따르고 관련 upstream issue/PR 중복을 검색한다.

nontrivial code PR은 author와 다른 human reviewer의 substantive review가 필요하므로 Codex가
self-merge하지 않는다. PR head, tests와 review requirement를 completion report에 남기고 human
review/merge boundary에서 멈춘다. beads-ui의 `beads-yvf` durable row cleanup은 이 PR merge를
기다리지 않으며 `UI-bwpk` rollout의 no-config cleanup evidence로 별도 완료한다.

## 완료 조건

1. runner에 associative array가 없고 macOS `/bin/bash` 3.2 list-only fixture가 성공한다.
2. manifest validation, fallback, artifact와 exit-status contracts가 모두 유지된다.
3. focused tests, `go test ./scripts`, 가능한 전체 `make test`가 통과한다.
4. 별도 Beads PR이 생성되고 human review가 필요한 상태로 전달된다.
