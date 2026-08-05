# bd- 스킬 보정 동작의 소스 흡수 — init server-mode 정책 + doctor 자가수리

- Bead: `beads-u4d` (route: full_plan)
- 후속 분리: `beads-456`(CLI 일관성 팩), `beads-i59`(revert/링크 팩), `beads-67k`(#4566 server-mode 교착 — 기존 Bead)
- Post-merge 전달 owner: `beads-cpy` (deferred_required — 릴리스 범프·dogfood·온보딩 스모크, 재진입 조건 `beads-u4d PR merged`)
- Cross-repo 후속: `dotfiles-ms54` (스킬·스크립트 축소, 이 Bead 머지+배포 후 재진입)

## 배경

2026-08-04 design-system 온보딩 세션(05d71627) 실측과 bd- 스킬 5종(bd-setup 939줄, bd-recover 1,430줄, bd-revert 905줄, bd-usage 172줄, seed-beads-from-plan은 ADR 0010 아카이브로 제외) 전수 분석 결과, 스킬이 손으로 우회·보정하는 동작 중 beads 포크 소스가 스스로 처리해야 할 것들을 확인했다.

실측된 재발성 이상 동작:

- `bd init --server`가 git origin(GitHub URL)을 `sync.remote`로 자동 영속화 — central-server 정책 위반, 매 온보딩마다 수동 `bd config unset sync.remote` 필요.
- `resolved` 커스텀 상태·`backup.enabled=false` 등 fleet 정책이 `setup_beads.sh` 실행에만 의존 — raw `bd init --server`를 먼저 실행하면 스크립트가 "이미 초기화됨" no-op으로 빠져 정책 미적용 상태가 남음.
- `bd init`이 local-dolt 모드 전제의 `AGENTS.md`/`CLAUDE.md`를 생성 — central-server 리그에서 오정보 문서.
- bd-recover가 lock 잔재·포트 drift·손상 스토어를 stderr 문자열 매칭과 수기 스크립트(~600줄)로 복구.

## 목표

1. `bd init --server`(external-server 모드)가 추가 스크립트 없이 fleet central 정책에 맞는 최종 상태를 만든다 — 어떤 경로로 init해도(raw 실행 포함) 정책 위반 상태가 생기지 않는다.
2. `bd doctor` / `bd doctor --fix`가 bd-recover 스킬의 수기 수리 대상 4종을 진단·자가수리한다.

## 비목표

- CLI JSON shape 통일, `--set-metadata` 강제변환 제거, `bd edit` TTY 가드, `bd show` 프로젝션 → `beads-456`.
- 이슈↔워크트리↔PR 링크, revert 스냅샷, `--unset-metadata-prefix`, 구조화 에러 taxonomy → `beads-i59`.
- #4566 dirty-working-set + migration gate 교착의 server-mode lenient open → `beads-67k`.
- embedded/local-dolt 모드의 기존 동작 변경. external-server 모드 밖에서는 upstream 동작을 유지한다.
- dotfiles 쪽 스킬·스크립트 실제 축소(→ `dotfiles-ms54`).

## 설계

### 팩 A — init external-server 모드 정책

발동 조건: external-server 모드(`bd init --server`, 기존 `externalServer`/`initServerMode` 판별)일 때 자동. 플래그·config 없이 적용하되, 명시 입력은 항상 우선한다.

- **A1. sync.remote git-origin 자동 유도 skip.** `cmd/bd/init.go:859-884`의 "명시 remote 없으면 git origin에서 유도" 분기를 external-server 모드에서 건너뛴다. 명시 `--remote <url>`은 기존대로 동작(explicit wins). 결과적으로 `persistInitSyncRemote`(`cmd/bd/init.go:2441`)는 유도 URL을 받지 않아 config.yaml에 `sync.remote`를 쓰지 않는다.
- **A2. `resolved` 커스텀 상태 시딩.** external-server 모드 init 성공 후 DB config `status.custom`에 `resolved`를 병합한다(기존 값 보존, 이미 있으면 no-op). `custom_statuses` 테이블 반영은 기존 자동 채움 경로(`internal/storage/issueops/config_helpers.go`)를 활용하고, 자동 채움이 해당 시점에 동작하지 않으면 시딩 시 직접 upsert한다. 수용 기준은 init 직후 `bd list --status resolved --json` 성공.
- **A3. `backup.enabled=false` 기본 기록.** external-server 모드 init이 config.yaml에 `backup.enabled: false`를 기록한다(`createConfigYaml` — `cmd/bd/init_templates.go:11` — 또는 직후 `SetYamlConfigInDir`). **키가 이미 존재하면(명시 `true`든 `false`든) 건드리지 않는다** — 기본값 주입이지 정책 강제가 아니다. 2026-07-18 central 정책(`setup_beads.sh ensure_auto_backup_disabled` 91-191행 대체).
- **A4. agents 문서 기본 skip.** external-server 모드에서 `skipAgents`(`cmd/bd/init.go:1736`) 기본값을 true로 한다. 현재 생성물이 local-dolt 전제(`bd dolt push/pull`, `.beads/dolt/`)라 central 리그에서 오정보이기 때문. 사용자가 `--agents-file`/`--agents-template`/`--agents-profile`을 명시하면 생성한다(명시 의도 우선). `--skip-agents=false` 명시도 생성으로 존중한다. `.gitignore` Dolt 항목 추가는 무해하므로 유지.
- **A5. `.beads` 0700 — 변경 없음.** `internal/config/permissions.go:13`(`BeadsDirPerm=0700`)이 이미 보장. 스킬 쪽 `ensure_beads_private_permissions`는 dotfiles-ms54에서 정리.

### 팩 B — doctor 체커/픽서

기존 doctor 체크/픽서 프레임(`cmd/bd/doctor/`, `cmd/bd/doctor_fix.go`)에 추가한다. 진단은 `bd doctor`, 수리는 `bd doctor --fix`. 파괴적 수리는 확인 없이는 실행하지 않는다.

- **B1. 손상 로컬 스토어 격리+재클론 — storage interface 경유.** 로컬 `.beads/dolt/<database>` 스토어가 열리지 않는 경우를 감지하는 체커를 추가한다. 진단·격리·재클론은 `docs/PROJECT_CHARTER.md` Storage Boundary(44-56행)에 따라 **storage/driver interface에 노출된 연산으로 구현**하고(필요하면 interface를 넓힌다), doctor는 그 interface만 호출한다 — doctor가 `.beads/dolt` 내부 파일을 직접 판별·이동하지 않는다. `--fix`는 손상 스토어를 `.beads/` 밖 격리 경로(타임스탬프 포함)로 이동한 뒤 `cmd/bd/bootstrap.go`의 기존 클론 로직(`cloneFromRemote` 계열)을 재사용해 재클론한다. 데이터 이동을 수반하므로 명시 확인(예: `--fix` + 확인 프롬프트/`--yes`) 없이는 절차 안내만 출력한다. 유효한 `sync.remote`가 없으면 수리 불가로 진단만 남긴다. (주 대상은 local-dolt 레이아웃 — central fleet에서 빈도는 낮지만 bd-recover 직접 클론 폴백 ~125줄을 대체.)
- **B2. dolt-server 잔재 인지 + PID 생존 판정 — lock 파일은 삭제 금지.** `dolt-server.lock`은 PID 소유 lock이 아니라 `doltserver` `Start()`의 동시 시작 직렬화 flock 앵커다(`internal/doltserver/doltserver.go:316,709-727` — `O_CREATE`+flock). 파일을 삭제하면 경쟁 중인 start가 기존 inode를 잠근 사이 새 inode가 생겨 직렬화가 깨질 수 있으므로 **lock 파일 자체는 제거 대상에서 제외**한다. 체커는 `dolt-server.pid`/`dolt-server.port`의 기록 PID 생존 여부를 판정하고, `--fix` 정리는 `doltserver` 패키지 내부에서 같은 flock을 획득한 뒤 process identity를 확인하고 pid/port 파일만 정리하는 연산으로 구현한다(doctor는 그 연산 호출). 동시 start 회귀 테스트를 동반한다.
- **B3. `dolt_server_port` drift — 재작성이 아니라 authority 정리.** `.beads/metadata.json`의 `dolt_server_port`는 상위 소스(런타임 port 파일/env)가 있는 deprecated git-tracked fallback이므로, effective port로 다시 써넣으면 폐기된 소스를 재영속화하고 cross-project 누출·재-drift를 남긴다. 체커는 stored 값과 effective 값(`bd dolt show` 계산 경로)의 불일치를 검출하되, `--fix`는 **포트 소스 authority를 명시하고 상위 소스가 유효하면 stale `dolt_server_port` 키를 제거**한다. 제거가 external lifecycle 판별을 `Owned`로 뒤집지 않도록 external 판별을 `dolt_mode: server` 기반으로 함께 고정한다. (`recover_runtime.sh external_port_drift_recovery` 332-413행 대체.)
- **B4. sync.remote shape 검증.** 체커가 두 가지를 경고한다: ① `sync.remote`가 Dolt remote로 해석 불가능한 shape — 판정은 기존 remote parser(정규화 경로) 기준으로 하고, `git+ssh://`·`git+https://` 등 지원 transport의 GitHub URL은 유효하므로 경고하지 않는다(음성 테스트 동반); plain `https://github.com/...` 코드 URL처럼 parser가 Dolt remote로 받지 못하는 명백한 오설정만 경고. ② external-server 모드 리그에 routine `sync.remote`가 설정된 상태 자체. `--fix`는 ②에 한해 unset을 제안·수행한다. fleet 고유 URL 문자열 비교는 스킬 잔류.

### 수용 기준

1. git origin이 있는 리포에서 `bd init --server ...` 직후: `bd config list`에 `sync.remote` 없음, `status.custom`에 `resolved` 포함, `bd list --status resolved --json` 성공, config.yaml에 `backup.enabled: false`, `AGENTS.md`/`CLAUDE.md` 미생성.
2. `--remote <url>` 명시 시 sync.remote가 그 값으로 기록(A1이 명시 입력을 막지 않음).
3. embedded/local 모드 init의 기존 동작(origin 유도 포함) 회귀 없음 — 기존 테스트 전부 green.
4. B2: 죽은 PID의 `dolt-server.pid`/`dolt-server.port`가 doctor에 검출되고 `--fix`가 flock 하에 pid/port 파일만 정리; `dolt-server.lock` 파일은 어떤 경우에도 삭제되지 않음; 살아 있는 PID면 보존; 동시 start 회귀 테스트 green.
5. B3: metadata 포트를 임의로 틀리게 만든 픽스처에서 doctor가 drift를 검출하고, 상위 소스 유효 시 `--fix`가 stale `dolt_server_port` 키를 제거하며 external lifecycle 판별이 유지됨.
6. B4: plain `https://github.com/...` 코드 URL이 sync.remote로 설정된 리그와 server 모드 + sync.remote 리그에서 각각 경고, 지원 transport(`git+ssh://`/`git+https://`) URL은 비경고(음성 케이스); `--fix`가 후자(②)를 unset.
7. B1: 손상 픽스처에서 확인 플래그 없이는 이동이 일어나지 않고, 확인 시 격리+재클론 후 `bd list --json` 성공.

## Test scope

RED→GREEN 시임 (구현 플랜에서 phase별로 재배치):

- `cmd/bd/init` server-mode 정책 (단위/테이블): A1(sync.remote 미설정/명시 remote 존중), A2(resolved 시딩·병합·no-op), A3(키 부재 시에만 `false` 기록, 명시 `true`/`false` 각각 보존), A4(agents skip 기본·명시 생성 존중) — `init_remote_test.go`/`init_safety_test.go`/`init_embedded_test.go` 패턴의 테이블 테스트.
- **command-level seam (수용 기준 1 직결):** 도달 가능한 테스트 dolt sql-server를 띄우는 기존 server-backed 테스트 하네스(`cmd/bd/doctor/fresh_clone_server_test.go` 등의 패턴)를 사용해, 실제 `bd init --server` 커맨드 경로로 external-server init을 수행하고 DB `status.custom` 시딩·`bd list --status resolved` 동작·config.yaml persistence(`sync.remote` 부재, `backup.enabled: false`)·문서 미생성·명시 override(`--remote`, `--agents-file`)를 한 시나리오에서 검증한다. 단위 테스트만으로는 수용 기준 1을 증명하지 못한다.
- `cmd/bd/doctor` 신규 체커/픽서: B1(확인 게이트 + storage interface 경유), B2(PID 생존 분기·lock 파일 비삭제·동시 start 회귀), B3(drift 검출·stale 키 제거·lifecycle 판별 유지), B4(shape 판정 양성/음성·unset) — doctor 패키지 기존 단위 테스트 패턴.
- 회귀 앵커: 기존 init/doctor 테스트 전체 green (embedded/local 경로 characterization은 기존 테스트가 담당).

## 전달

1. 구현·검증 green 후 PR (base: `main`).
2. `make install-force` 설치는 이 리포의 `docs/agents/repo-ops.toml` `[deploy]` 선언(2026-08-05 신설, a72bc2c64)이 커버한다 — 머지 후 worker deploy 표면이 자동 실행하고 bdui는 바이너리 교체만으로 즉시 반영.
3. `[deploy]`가 커버하지 않는 나머지 머지 후 작업(포크 릴리스 범프 1.1.0-fork.x·신규 리포 온보딩 스모크·설치 실측 확인·`dotfiles-ms54` 통지)의 durable owner는 **`beads-cpy`** (deferred_required, `beads-u4d`에 `blocks` 의존 — 재진입 조건: `beads-u4d` PR merged, 완료 기준은 해당 Bead description에 기록).
