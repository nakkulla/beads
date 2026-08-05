# bd- 스킬 보정 동작의 소스 흡수 — init server-mode 정책 + doctor 자가수리

- Bead: `beads-u4d` (route: full_plan)
- 후속 분리: `beads-456`(CLI 일관성 팩), `beads-i59`(revert/링크 팩), `beads-67k`(#4566 server-mode 교착 — 기존 Bead)
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
- **A3. `backup.enabled=false` 기본 기록.** external-server 모드 init이 config.yaml에 `backup.enabled: false`를 기록한다(`createConfigYaml` — `cmd/bd/init_templates.go:11` — 또는 직후 `SetYamlConfigInDir`). 2026-07-18 central 정책(`setup_beads.sh ensure_auto_backup_disabled` 91-191행 대체).
- **A4. agents 문서 기본 skip.** external-server 모드에서 `skipAgents`(`cmd/bd/init.go:1736`) 기본값을 true로 한다. 현재 생성물이 local-dolt 전제(`bd dolt push/pull`, `.beads/dolt/`)라 central 리그에서 오정보이기 때문. 사용자가 `--agents-file`/`--agents-template`/`--agents-profile`을 명시하면 생성한다(명시 의도 우선). `--skip-agents=false` 명시도 생성으로 존중한다. `.gitignore` Dolt 항목 추가는 무해하므로 유지.
- **A5. `.beads` 0700 — 변경 없음.** `internal/config/permissions.go:13`(`BeadsDirPerm=0700`)이 이미 보장. 스킬 쪽 `ensure_beads_private_permissions`는 dotfiles-ms54에서 정리.

### 팩 B — doctor 체커/픽서

기존 doctor 체크/픽서 프레임(`cmd/bd/doctor/`, `cmd/bd/doctor_fix.go`)에 추가한다. 진단은 `bd doctor`, 수리는 `bd doctor --fix`. 파괴적 수리는 확인 없이는 실행하지 않는다.

- **B1. 손상 로컬 스토어 격리+재클론.** 로컬 `.beads/dolt/<database>` 스토어가 열리지 않는 경우를 감지하는 체커를 추가하고, `--fix`는 손상 스토어를 `.beads/` 밖 격리 경로(타임스탬프 포함)로 이동한 뒤 `cmd/bd/bootstrap.go`의 기존 클론 로직(`cloneFromRemote` 계열)을 재사용해 재클론한다. 데이터 이동을 수반하므로 명시 확인(예: `--fix` + 확인 프롬프트/`--yes`) 없이는 절차 안내만 출력한다. 유효한 `sync.remote`가 없으면 수리 불가로 진단만 남긴다. (주 대상은 local-dolt 레이아웃 — central fleet에서 빈도는 낮지만 bd-recover 직접 클론 폴백 ~125줄을 대체.)
- **B2. dolt-server lock 잔재 인지 + PID 생존 판정.** Lock Files 체커(`cmd/bd/doctor/locks.go` — 현재 `dolt.bootstrap.lock`, `.sync.lock`, `*.startlock`만 인지)에 `dolt-server.lock`/`dolt-server.pid`/`dolt-server.port`를 추가하고, 나이 기준이 아니라 기록된 PID의 생존 여부로 판정한다. 홀더가 죽어 있을 때만 `--fix` 제거 대상.
- **B3. `dolt_server_port` drift 수리.** `.beads/metadata.json`의 `dolt_server_port`와 유효 포트(현재 `bd dolt show`가 계산하는 값)를 비교하는 체커 + `--fix` 재작성. 재작성 전 대상 host/port 도달성을 확인한다. (`recover_runtime.sh external_port_drift_recovery` 332-413행 대체.)
- **B4. sync.remote shape 검증.** 체커가 두 가지를 경고한다: ① `sync.remote`가 Dolt remote로 해석 불가능한 shape(일반 GitHub 코드 URL 등 — 단, plain git origin도 유효한 Dolt remote가 될 수 있으므로 판정은 refs/dolt/data 관점 휴리스틱이 아니라 명백한 오설정만), ② external-server 모드 리그에 routine `sync.remote`가 설정된 상태 자체. `--fix`는 ②에 한해 unset을 제안·수행한다. fleet 고유 URL 문자열 비교는 스킬 잔류.

### 수용 기준

1. git origin이 있는 리포에서 `bd init --server ...` 직후: `bd config list`에 `sync.remote` 없음, `status.custom`에 `resolved` 포함, `bd list --status resolved --json` 성공, config.yaml에 `backup.enabled: false`, `AGENTS.md`/`CLAUDE.md` 미생성.
2. `--remote <url>` 명시 시 sync.remote가 그 값으로 기록(A1이 명시 입력을 막지 않음).
3. embedded/local 모드 init의 기존 동작(origin 유도 포함) 회귀 없음 — 기존 테스트 전부 green.
4. B2: 죽은 PID의 `dolt-server.lock/pid/port`가 doctor에 검출되고 `--fix`로 제거; 살아 있는 PID면 보존.
5. B3: metadata 포트를 임의로 틀리게 만든 픽스처에서 doctor가 drift를 검출하고 `--fix`가 유효 포트로 재작성.
6. B4: GitHub 코드 URL이 sync.remote로 설정된 리그와 server 모드 + sync.remote 리그에서 각각 경고; `--fix`가 후자를 unset.
7. B1: 손상 픽스처에서 확인 플래그 없이는 이동이 일어나지 않고, 확인 시 격리+재클론 후 `bd list --json` 성공.

## Test scope

RED→GREEN 시임 (구현 플랜에서 phase별로 재배치):

- `cmd/bd/init` server-mode 정책: A1(sync.remote 미설정/명시 remote 존중), A2(resolved 시딩·병합·no-op), A3(backup.enabled 기록), A4(agents skip 기본·명시 생성 존중) — `init_remote_test.go`/`init_safety_test.go`/`init_embedded_test.go` 패턴의 테이블 테스트.
- `cmd/bd/doctor` 신규 체커/픽서: B1(확인 게이트 포함), B2(PID 생존 분기), B3(drift 검출·재작성), B4(shape 판정·unset) — doctor 패키지 기존 단위 테스트 패턴.
- 회귀 앵커: 기존 init/doctor 테스트 전체 green (embedded/local 경로 characterization은 기존 테스트가 담당).

## 전달

1. 구현·검증 green 후 PR (base: `main`).
2. 머지 후 포크 릴리스 라인 버전 범프(1.1.0-fork.x, `.goreleaser.fork.yml` 라인) 및 `make install-force` dogfood 설치 — 이 리포에 deploy.json이 없으므로 머지 후 설치·실측은 Bead 후속 크로싱(beads-9mv.6 전례)으로 기록한다.
3. 신규 리포 온보딩 스모크: 임시 리포에서 `bd init --server` 후 수용 기준 1 확인.
4. `dotfiles-ms54` 재진입 조건 충족 통지(스킬 축소 착수 가능).
