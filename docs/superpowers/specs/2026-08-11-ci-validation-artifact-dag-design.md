# CI 검증 단일 소유권과 artifact DAG 단축 설계

## 문서 상태

- Bead: `beads-am7`
- Route: `full_plan`
- Workflow mode: `standard`
- 설계 승인: 2026-08-11 사용자 섹션별 승인
- 기준 브랜치/SHA: `main` / `56d6f5d0431ba9755f158b6503f47832f7b78341`
- 구현 단위: 4개 집중 CI 개선 중 첫 번째 PR

## 배경과 문제

현재 활성 PR CI는 같은 head SHA에서 `PR`, `PR Risk`, `Regression Tests`를 함께
실행한다. 최근 PR #10의 세 workflow는 합계 138.23 runner-minutes를 사용했고,
baseline `PR` workflow만 23.68 runner-minutes와 10.43분의 critical span을 사용했다.

`PR`의 `build-artifacts`는 reusable binary를 만들기 전에 다음 작업을 직렬로
수행한다.

1. `golangci-lint` 설치
2. `make ci-pr-policy`
3. `make ci-pr-lint`

같은 workflow의 `pr-policy-wrapper`와 `pr-lint-wrapper`가 같은 Make target을 다시
실행한다. PR #10에서 artifact job 안의 이 중복 block은 233초였고, artifact를
필요로 하는 `pr-core-wrapper`가 직후 시작했으므로 전부 critical path에 있었다.
최근 성공 PR 12개의 같은 block 중앙값은 236.5초다.

중복은 artifact producer 안에만 있지 않다.

- `ci-pr-policy`가 포함하는 build-tag/install-guidance/version/docs/`.beads`
  검사를 별도 jobs가 다시 수행한다.
- `ci-pr-lint`가 포함하는 `gofmt`와 `golangci-lint`를 `fmt-check`와 `lint`가 다시
  수행한다.
- standalone lint는 `latest`, wrapper는 `v2.9.0`을 사용해 동일 검증의 tool version도
  다르다.
- 비활성 `main.yml`에도 같은 중복이 있고, migration hygiene가 이미 포함하는
  duplicate migration scan을 별도 job이 다시 수행한다.

upstream PR #5264는 artifact producer에서 policy/lint를 분리하고 ownership contract
test를 추가했다. upstream issue #5629는 standalone fmt/lint와 lint wrapper의 3중
실행을 같은 문제로 기록한다. 이 설계는 fork의 현재 topology에 맞춰 #5264의
ownership contract를 가져오고, 사용자가 승인한 범위에 따라 policy/lint 전체를 단일
owner로 확장한다. upstream workflow 전체 cherry-pick은 현재 fork와 job/action topology가
달라 사용하지 않는다.

현재 origin `nakkulla/beads`의 `main`에는 branch protection이나 required status context가
없다. 그래도 향후 보호 규칙이 참조할 안정적인 aggregate 이름
`PR / CI Gate / Required`는 유지하고 내부 gate 계약을 명시적으로 검증한다.

## 목표

1. `build-artifacts`를 reusable binary 생성·검증·upload 전용 producer로 만든다.
2. policy와 lint 검증을 각각 하나의 canonical wrapper job에만 둔다.
3. 동일 검증을 수행하는 standalone jobs를 제거하되 검증 항목과 실패 진단은 보존한다.
4. aggregate gate가 새 owner 집합을 누락 없이 평가하도록 정적 계약 테스트를 둔다.
5. PR의 green critical path와 runner-minutes를 함께 줄인다.
6. 비활성 `main.yml`도 같은 소유권 계약으로 정리해 재활성화 시 중복이 되살아나지
   않게 한다.

## 비목표

- `pr-risk.yml`의 embedded storage/cmd fan-out 변경
- `regression.yml`의 path gating 또는 concurrency 변경
- Go module/build cache topology 추가
- package gate의 job-level skip 전환
- PR Core, domain/UOW/tracker, pure-Go compile, migration hygiene의 test surface 축소
- `main.yml` workflow 재활성화 또는 branch protection 변경
- cross-version, release, nightly, Nix workflow 변경
- 정확한 wall-clock 절감치를 merge hard gate로 고정

위 항목은 검증 소유권과 독립된 실패 모드와 rollout을 가지므로 후속 spec/Bead/PR로
분리한다.

## 검증 소유권

### Artifact producer

`.github/workflows/pr.yml`과 `.github/workflows/main.yml`의 `build-artifacts`는 다음만
담당한다.

- source checkout과 Go setup
- `.buildflags`의 canonical build tags로 `bd-linux-gms-pure` 생성
- executable bit, `SHA256SUMS`, build manifest 생성
- `ci-build-artifacts` upload

다음은 artifact job에서 금지한다.

- `golangci-lint` 설치
- `make ci-pr-policy`
- `make ci-pr-lint`
- policy/lint의 개별 script 직접 호출

artifact를 소비하는 `pr-core-wrapper`, `test-domain-uow`, package jobs의 `needs`와
checksum 검증은 유지한다.

### Policy owner

`pr-policy-wrapper`가 다음 검증의 유일한 owner다.

- build-tag policy
- `go install` guidance
- version consistency
- docs binary build
- doc flags와 doc freshness
- `testing.Short` boundary
- `.beads/issues.jsonl` 변경 guard

PR/merge queue에서는 wrapper에 아래 두 base를 같은 exact base SHA로 전달한다.

- `BD_DOCS_DIFF_BASE`
- `CI_BEADS_DIFF_BASE`

값은 `${{ github.event.pull_request.base.sha || github.event.merge_group.base_sha }}`다.
이를 통해 merge queue에서도 docs attribution과 `.beads` guard가 `origin/main` 추측에
의존하지 않는다.

PR wrapper의 `make ci-pr-policy` step에는
`DOC_DRIFT_PATCH_OUT=${{ runner.temp }}/cli-docs-freshness.patch`를 전달한다. 실패 시
현재 standalone docs job과 같은 `cli-docs-freshness-patch` artifact를 업로드해 진단
기능을 보존한다.

Main wrapper는 push의 비교 기준으로 `github.event.before`를 docs와 `.beads` base에
전달한다. force push 등으로 base를 해석할 수 없을 때는 기존 scripts의 fail-safe
warning/skip 의미를 유지한다.

### Lint owner

`pr-lint-wrapper`가 `make ci-pr-lint`의 유일한 owner다. 이 target이 `make fmt-check`와
`golangci-lint run --timeout=5m --build-tags=gms_pure_go ./...`를 수행한다.
wrapper는 `golangci-lint v2.9.0`을 설치해 tool version을 고정한다.

standalone `lint`의 `version: latest` 경로는 제거한다. 별도 annotations를 위해 같은
lint를 다시 실행하지 않으며, canonical CLI의 exit status와 log를 gate authority로
사용한다.

### 제거·유지 표

| 현재 job | 결정 | canonical owner 또는 이유 |
| --- | --- | --- |
| `check-build-tags` | 제거 | `pr-policy-wrapper` |
| `check-version-consistency` | 제거 | `pr-policy-wrapper` |
| `check-doc-flags` | 제거 | policy wrapper로 patch artifact까지 이동 |
| `check-no-beads-changes` | PR에서 제거 | policy wrapper에 exact base 전달 |
| `fmt-check` | 제거 | `pr-lint-wrapper` |
| `lint` | 제거 | pinned `pr-lint-wrapper` |
| Main `check-no-duplicate-migrations` | 제거 | `check-migration-hygiene`의 부분집합 |
| `check-cmd-bd-puregeo-tests` | 유지 | CGO=0 compile/subset이라는 고유 surface |
| `check-migration-hygiene` | 유지 | duplicate·nondeterminism·frozen migration 검사 |
| `pr-core-wrapper` | 유지 | race/short broad Go test owner |
| `test-domain-uow` | 유지 | Dolt image를 보장하는 고유 DB-backed surface |
| package detector/gates | 유지 | package별 고유 검증 |
| `pr-risk.yml`, `regression.yml` jobs | 유지 | 이번 소유권 단위 밖의 고유 workflow |

`main.yml`의 post-merge test, integration, embedded, Windows, Nix jobs도 전부 유지한다.
현재 workflow가 `disabled_manually`여도 source topology와 contract test는 PR workflow와
같은 소유권을 요구한다.

## 실행 DAG와 aggregate gate

PR 시작 시 artifact, policy, lint, pure-Go compile, migration hygiene, package detector는
서로 독립적으로 시작한다. artifact 완료 뒤에만 실제 artifact consumer가 이어진다.
policy/lint를 core의 선행 조건으로 추가하지 않는다. 이는 red PR에서 일부 runner 비용을
더 쓸 수 있지만 green critical path와 독립 실패 피드백을 보존한다.

`ci-gate`는 `if: ${{ always() }}`와 `name: CI Gate / Required`를 유지한다. 새 baseline
required owner 집합은 정확히 다음과 같다.

- `build-artifacts`
- `check-cmd-bd-puregeo-tests`
- `check-migration-hygiene`
- `detect-package-gates`
- `package-mcp`
- `package-npm`
- `package-website`
- `pr-policy-wrapper`
- `pr-core-wrapper`
- `pr-lint-wrapper`
- `test-domain-uow`

각 `needs` ID는 uppercase token, `${{ needs.<job>.result }}` env와 정확히 1:1이어야
한다. 삭제한 job의 ID/token/env는 남기지 않는다.

PR-only `check-no-beads-changes`가 사라지므로 baseline gate의 merge-group
`CI_GATE_SKIPPED_OK` 예외도 제거한다. package jobs는 현재처럼 applicability를 job 내부에서
판정하고 비해당 시 job 자체는 success로 끝나므로 baseline owner 집합에 계속 포함한다.
upstream failure/cancel/skipped를 놓치지 않기 위해 gate의 `always()`는 변경하지 않는다.

## 문서 계약

`docs/CI_REQUIRED_CHECK_TOPOLOGY.md`의 현재 YAML 예시는 실제 workflow와 이미 어긋나
있다. `CHECK_NO_DUPLICATE_MIGRATIONS`를 서술하지만 실제 PR gate는
`CHECK_MIGRATION_HYGIENE`를 사용한다.

이번 PR은 문서를 새 single-owner topology에 맞춘다. workflow YAML을 길게 복제하는 예시는
줄이고 다음 안정 계약을 설명한다.

- PR/merge-group trigger를 workflow-level path filter 없이 유지
- aggregate name과 `always()` 유지
- gate owner 집합과 1:1 result mapping
- policy/lint wrapper가 standalone 이름 대신 canonical owner
- risk-tier skipped allowance는 `pr-risk.yml`에만 존재

실행 authority는 workflow YAML과 Go contract test이며, 문서는 maintainer-facing 설명이다.

## 실패 처리와 rollback

- artifact build/upload 실패: artifact consumer는 skip되고 aggregate gate는 실패한다.
- policy 실패: timed subcheck가 실패 지점을 출력하고 docs drift면 fix patch를 업로드한다.
- lint 실패: pinned CLI log와 nonzero status가 wrapper와 aggregate gate를 실패시킨다.
- base SHA 해석 실패: `.beads` guard는 기존대로 warning 뒤 diff guard만 skip한다. docs
  strict probe가 실패한 상태라면 attribution을 생략한 성공으로 바꾸지 않고 strict 실패를
  유지한다. 어느 경로도 임의 base를 성공 근거로 만들지 않는다.
- aggregate mapping 불일치: workflow contract test가 merge 전에 실패한다.
- Main 재활성화 전 runtime 차이: contract test로만 보증하고 runtime 성공을 주장하지 않는다.

runtime data, schema, release artifact를 변경하지 않으므로 rollback은 이 PR revert 하나다.
개별 standalone job을 임시 복원하는 부분 rollback은 ownership 중복을 재도입하므로 사용하지
않는다.

## Test scope

RED-GREEN seam은 workflow validation ownership과 aggregate mapping이다.

새 `scripts/ci_workflow_test.go`는 이미 선언된 `gopkg.in/yaml.v3`를 사용해 다음을
검증한다.

1. PR/Main `build-artifacts`에 lint install, policy, lint command가 없다.
2. 각 workflow에서 `make ci-pr-policy`, `make ci-pr-lint`, `make ci-pr-core`가 지정
   wrapper에 정확히 1회 존재한다.
3. 제거 대상으로 승인된 standalone job IDs가 존재하지 않는다.
4. PR `ci-gate.needs`, uppercase required tokens, result env가 같은 owner 집합과 1:1이다.
5. gate name과 `always()`가 유지되고 baseline skipped allowlist가 없다.
6. policy wrapper가 PR/merge-group exact docs/`.beads` base를 전달한다.
7. PR docs patch output과 failure upload artifact가 policy wrapper에 존재한다.
8. Main policy wrapper는 `github.event.before`를 base로 사용하고 inline duplicate migration
   scan은 없다.
9. PR/Main migration hygiene job과 event별 `BASE_SHA`는 유지된다.

테스트는 현재 topology에서 중복/오래된 gate를 검출해 RED가 되고, workflow 변경과 함께
GREEN이 된다. action SHA ratchet이나 fork에 없는 upstream jobs는 이번 계약에 넣지 않는다.

로컬 검증 bundle:

- `go test ./scripts`
- `make ci-pr-policy`
- `make ci-pr-lint`
- `git diff --check`
- 변경 파일 전체 diff와 workflow contract 문서 대조

PR runtime 검증:

- `PR / CI Gate / Required` success
- 같은 head의 `PR Risk`와 `Regression Tests`가 이번 변경으로 깨지지 않음
- `Build Artifacts` step 목록이 build/upload 전용임
- artifact 종료 뒤 `PR Core`가 정상 시작함
- policy failure patch와 merge-group base는 정적 계약으로 검증하고, 실패를 만들기 위한
  production PR은 생성하지 않음

storage/core 전체 suite는 실행 명령과 code가 바뀌지 않으므로 이번 workflow-only PR의
필수 로컬 gate로 중복 실행하지 않는다. 실제 GitHub PR에서는 기존 workflow가 해당
surface를 그대로 실행한다.

## 성능 기준

기계적 acceptance:

1. artifact producer 안의 policy/lint 중복 명령 0개
2. 승인된 중복 standalone jobs 0개
3. canonical owner와 aggregate mapping 누락 0개
4. 고유 검증 삭제 0개
5. PR aggregate gate success

관측 성능은 PR #10 baseline인 23.68 runner-minutes / 10.43분 critical span과 비교해 PR
본문 또는 handoff에 기록한다. 최근 artifact 중복 block 중앙값 3.94분과 삭제 jobs를
고려하면 baseline PR workflow runner-minutes 20% 이상 감소를 기대한다. GitHub runner
queue와 cold cache 편차가 크므로 특정 wall-clock 수치를 merge hard gate로 삼지 않는다.
속도 개선 주장은 step 목록, artifact duration, PR Core start, 전체 runner-minutes라는 같은
run의 evidence로 뒷받침한다.

## 후속 집중 PR

이번 unit은 아래 작업을 구현하거나 부분 선행하지 않는다.

1. 활성 workflow에 맞는 cache ownership과 안전한 writer 설계
2. Regression Tests의 변경 경로 gating과 stale-run concurrency
3. Embedded Dolt Storage의 독립 sharding과 duration 균형

upstream #5278 cache topology는 disabled Main writer와 fork에 없는 jobs를 전제로 하므로 직접
backport하지 않는다. 각 후속은 별도 Bead, brainstorming, spec, router, PR을 갖고 현재 PR의
관측 결과를 새 baseline으로 사용한다.

## Acceptance criteria

1. policy와 lint의 모든 기존 검증 항목이 각각 하나의 canonical wrapper에서 실행된다.
2. `build-artifacts`는 binary build/checksum/manifest/upload 외 검증을 수행하지 않는다.
3. docs drift patch와 PR/merge-group exact base 검사가 wrapper 이동 뒤에도 유지된다.
4. pure-Go compile, migration hygiene, package, core, domain/UOW 고유 surface는 유지된다.
5. aggregate name, `always()`, owner result mapping이 workflow contract test로 고정된다.
6. PR/Main workflow와 required-check 문서가 같은 single-owner topology를 설명한다.
7. 집중 로컬 검증과 실제 PR aggregate CI가 통과한다.
8. PR 성능 evidence가 baseline과 같은 방식으로 기록되며, cache/regression/storage 후속은
   이번 diff에 섞이지 않는다.
