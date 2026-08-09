# bd revert/링크 팩 설계 — 이슈-워크트리 링크·`bd show --links` 스냅샷·metadata prefix 해제 (beads-i59)

## 배경

bd-revert(905줄)·bd-recover 스킬은 bd에 기계 판독 표면이 없어 스킬 쪽 워크어라운드로 보정하고 있다(출처: beads-u4d 스킬 분석 리프, 2026-08-05). beads-i59가 지목한 4건 중 이 스펙은 다음 3건을 다룬다.

1. 이슈↔워크트리↔브랜치↔PR 링크가 1급이 아니어서 bd-revert Phase 0이 `metadata.branch` → worktree substring 매칭 → 사용자 질문의 3단 폴백 휴리스틱을 유지 중.
2. revert 스냅샷 수집이 `inspect_revert_target.py`(203줄)로 bd show + bd children + git worktree list + gh pr view/list 5개 명령을 손으로 엮음.
3. `reset.md` Phase 5가 metadata 키 ~37개를 `--unset-metadata` 열거로 리셋 — 스키마 진화 때마다 열거 목록이 드리프트.

항목 4(구조화 에러 taxonomy / `bd recover` 판정)는 bd 에러 표면 전반 설계 규모라 이 스펙에서 제외하고 신규 Bead로 분리한다.

## 스코프와 분해

- **포함**: 항목 1(링크 기록·노출), 항목 2(`bd show --links`), 항목 3(`--unset-metadata-prefix`). 변경 축은 `cmd/bd/worktree_cmd.go`, `cmd/bd/show.go`, `cmd/bd/update.go`(+ proxied 입력 경로 `update_input.go`).
- **제외 → 신규 Bead A(이 리포)**: 항목 4 — 구조화 에러 taxonomy 또는 `bd recover` 진단 판정 서브커맨드. `cmd/bd/doctor/`(103파일)의 기존 진단 인프라 활용을 설계 입력으로 명시. 핸드오프 시 `discovered-from beads-i59`로 생성.
- **제외 → 신규 Bead B(dotfiles)**: 스킬 정합(cross-repo split, beads-u4d 선례). 이 유닛의 bd 기능 릴리스 후 진행. 상세는 `## 스킬 소비처 영향표` 참조.

## 결정 로그 (2026-08-10, 사용자 확정)

1. **유닛 분해**: 1+2+3 이 스펙 유지, 4 분리. 1·2는 강결합(스냅샷이 링크를 소비), 3은 소규모라 동봉 비용이 낮음.
2. **링크 저장**: bd-owned metadata 규약. `bd worktree create --issue`가 `metadata.branch`를 기록하고 이 키를 유일한 join key로 사용. 1급 DB 필드 승격은 하지 않음(포크 스키마 drift·upstream 리베이스 마찰·Dolt 마이그레이션 비용). per-worktree git config(`beads.issue`)도 제외 — 진실 소스 이원화와 `extensions.worktreeConfig` 리포 설정 부작용을 피함.
3. **스냅샷 형태**: 신규 명령이 아니라 `bd show --json`에 `--links` enrichment 추가. gh(GitHub CLI) 의존은 bd 안으로 들이지 않고 스킬에 잔류 — bd 경계는 자기 DB + git.
4. **pr_url**: metadata 문서화 키로 유지. 쓰기는 기존 `bd update --set-metadata pr_url=...` 재사용, 새 명령 없음(YAGNI).

## 설계

### 항목 1: `bd worktree create --issue` / `bd worktree list` 링크 노출

**`bd worktree create [<name>] --issue <id> [--branch <branch>]`**

- `--issue` 지정 시 `<name>` 생략 가능: name/branch 기본값 = 이슈 ID(플릿 네이밍 관례 basename == branch == Bead ID와 일치). `--issue` 없이는 기존과 동일하게 `<name>` 필수.
- 동작 순서: ① 이슈 존재 검증(스토리지 조회; 없으면 워크트리 생성 전에 실패) → ② 이슈의 기존 `metadata.branch` 검사 — 이미 다른 값이 있으면 힌트와 함께 거부(변경은 명시적 `bd update --set-metadata branch=...`로 유도) → ③ git worktree 생성(기존 로직 그대로) → ④ 성공 후 `metadata.branch=<branch>` 기록.
- ④ 기록이 실패해도 생성된 워크트리는 롤백하지 않고 남기며, 수동 기록 힌트와 함께 에러를 보고한다(부분 성공을 성공으로 덮지 않음).
- `--json` 출력에 `issue_id` 필드 추가. 기존 `metadata.branch`와 같은 값을 재기록하는 재실행은 충돌로 보지 않는다(멱등).

**`bd worktree list --json` enrichment**

- 각 워크트리 항목에 `issue_id`, `issue_source` 필드 추가(무매칭 시 생략, omitempty).
- 해석 순서(정확 일치만, substring 매칭 도입 안 함):
  1. 어떤 이슈의 `metadata.branch` == 워크트리 브랜치 → `issue_source: "metadata"`
  2. 워크트리 브랜치명 == 존재하는 이슈 ID → `issue_source: "branch-name"` (bd를 거치지 않고 만든 harness/git 워크트리 커버)
- 복수 이슈가 같은 branch를 가리키면 이슈 ID 정렬 순 첫 항목을 선택하고 stderr로 모호성 경고를 낸다(결정적 출력 보장).
- DB 조회 불가 경로(`listWorktreesWithoutBeads`)에서는 enrichment를 생략하고 현행 degrade 동작을 유지한다.
- human 테이블에는 `ISSUE` 컬럼을 추가한다.

**`bd worktree remove`**: `metadata.branch`를 건드리지 않는다. 워크트리 삭제 ≠ 브랜치 삭제이며, `metadata.branch` 제거는 브랜치를 실제 삭제했을 때만이라는 bd-revert 규칙과 일치.

### 항목 2: `bd show <id> --json --links`

`--links` 플래그가 이슈 JSON에 계산(비영속) 섹션을 추가한다:

```json
"links": {
  "branch": "beads-x",
  "pr_url": "https://github.com/owner/repo/pull/5",
  "worktree": {"name": "beads-x", "path": "/abs/path/beads-x", "branch": "beads-x", "exists": true}
}
```

- `branch`·`pr_url`은 metadata에서 그대로 읽고, 없으면 필드 생략.
- `worktree`는 `git worktree list --porcelain`(CWD repo, 기존 `parseWorktreeList` 재사용) 실행 후 브랜치 정확 매칭으로 계산. `metadata.branch`가 없으면 브랜치명 == 이슈 ID 폴백 — list와 동일한 해석기를 공유한다. 매칭 없으면 `worktree: null`. `exists`는 경로 존재 여부(`os.Stat`).
- git 실행 실패(리포 밖 등)는 전체 실패가 아니라 `worktree: null` + stderr 경고로 degrade — 스냅샷은 부분 정보라도 반환해야 revert가 진행된다.
- `--links`는 `--json`이 주 계약이며 human 모드에서는 간단한 Links 섹션을 추가 표시한다. `--as-of`와 조합 시 `links`는 항상 현재 시점 계산임을 help 텍스트에 명시한다.
- 목표 소비 형태: `bd show <id> --json --children --links` 1콜 + 스킬 쪽 `gh pr view` 1콜. 이것으로 `inspect_revert_target.py`의 bd/git 수집 전부와 PR 폴백 매칭(`gh pr list --head`)의 필요가 사라진다 — pr_url이 기록 규약으로 안정 공급되기 때문.

### 항목 3: `bd update --unset-metadata-prefix <prefix>`

- repeatable `StringArray` 플래그. `applyMetadataEdits`(embedded)와 proxied 입력 경로(`update_input.go`) 양쪽에 반영.
- 의미: 기존 `--unset-metadata`(정확 일치)의 집합 확장 — prefix 일치(`strings.HasPrefix`)하는 모든 키를 같은 단계에서 삭제한다. `--set-metadata`와의 우선순위 등 상호작용 규칙은 기존 `--unset-metadata`와 동일하게 따른다.
- 빈 문자열 prefix는 에러(전체 metadata 삭제 방지). `--metadata`와는 기존 규칙대로 배타, `--set-metadata`/`--unset-metadata`와 조합 가능.
- 안전 가이드(스킬 문서화 대상, Bead B): family 단위 prefix만 사용한다 — `spec_review`·`plan_review`·`impl_review`·`implementation_review` 등. `spec_` 같은 광폭 prefix는 보존 대상 `spec_handoff_*`를 삼킨다. 이 가이드로 reset.md Phase 5의 37개 인자가 family prefix + 단독 키(`plan_approval`, `plan_check`, `last_checked_sha`, `execution_base_sha` 등) ~10개 인자로 축소된다.

## 에러 처리 요약

| 지점 | 동작 |
| --- | --- |
| `create --issue` 이슈 없음 | 워크트리 생성 전에 실패 |
| `create --issue` 기존 branch 충돌 | 거부 + `--set-metadata` 힌트 |
| `create --issue` metadata 기록 실패 | 워크트리 유지 + 에러 보고(수동 기록 힌트) |
| `show --links` git 표면 실패 | `worktree: null` + stderr 경고, 명령은 성공 |
| `list` 복수 이슈 동일 branch | ID 정렬 첫 항목 + stderr 경고 |
| `--unset-metadata-prefix ""` | 에러 |
| `--unset-metadata-prefix` + `--metadata` | 기존 배타 규칙대로 에러 |

## Test scope (RED-GREEN seams)

command-level seam, 기존 embedded 테스트 하네스(`worktree_cmd_test.go`, `update_embedded_test.go`, show 테스트) 기준:

1. `worktree create --issue`: 정상 링크 기록(readback으로 `metadata.branch` 확인) / 이슈 없음 실패 / 기존 branch 충돌 거부 / 같은 값 재실행 멱등 / name 생략 시 이슈 ID 기본값.
2. `worktree list --json`: metadata 매칭(`issue_source: "metadata"`) / branch-name 폴백 / 무매칭 생략 / 복수 매칭 결정성(정렬 첫 항목).
3. `show --links`: branch+pr_url+worktree 전체 / 워크트리 무매칭 `null` / metadata 없이 ID 폴백 / `--children` 병행 1콜 형태.
4. `update --unset-metadata-prefix`: prefix 일치 일괄 삭제 / 비일치 보존 / 빈 prefix 에러 / `--metadata` 배타 / `--unset-metadata`·`--set-metadata` 조합.
5. proxied 미러: update 계열은 `update_proxied_integration_test.go`에 등가 케이스가 있는 범위에서 미러.

## 검증

- `go build ./...` · `go vet` · `gofmt` clean · `cmd/bd` 테스트 — 실패 집합이 pinned base와 동일(신규 0).
- 수용 데모(이 리포 워크스페이스): `bd worktree create --issue <테스트 Bead>` → `bd worktree list --json` → `bd show <id> --json --children --links` → readback 확인.

## 스킬 소비처 영향표 (Bead B 범위, dotfiles)

| 소비처 | 현행 | 이 유닛 릴리스 후 |
| --- | --- | --- |
| bd-revert `inspect.md` Phase 0 | 3단 폴백 휴리스틱 | `bd show --json --children --links` 1콜 |
| `inspect_revert_target.py` + `resolve_github_repo.py` | 245줄 수집 스크립트 | 삭제 — bd 1콜 + `gh pr view` 1콜 |
| bd-revert `reset.md` Phase 5 | `--unset-metadata` ×37 열거 | family prefix + 단독 키 ~10 인자 |
| bd-usage cheat sheet | 해당 항목 없음 | `create --issue`·`--links`·`--unset-metadata-prefix` 등재 |
| `beads_recovery.sh` classify_failure · bd-recover `contains_lock_signal` | raw stderr 문자열 매칭 | 현행 유지 — Bead A(항목 4) 대상 |

## 수용 기준

1. `bd worktree create --issue`가 `metadata.branch`를 기록하고 `bd show --json` readback으로 확인된다.
2. `bd worktree list --json`이 `issue_id`/`issue_source`를 노출하고 substring 매칭 없이 두 정확-일치 규칙만 사용한다.
3. `bd show <id> --json --children --links` 1콜이 `inspect_revert_target.py`의 bd/git 수집 전부를 대체할 정보를 담는다(gh 제외).
4. `bd update --unset-metadata-prefix`로 reset.md Phase 5 열거가 family 단위로 축소 가능하다.
5. 신규 테스트가 Test scope 시임을 커버하고, 기존 테스트 실패 집합이 pinned base와 동일하다.
