# beads-u4d 구현 플랜 — bd- 스킬 보정 동작 소스 흡수 (init server-mode 정책 + doctor 자가수리)

## Context

승인 스펙: `docs/superpowers/specs/2026-08-05-bd-skill-absorption-design.md` @ `77afddb3885aa4d0752cc60b0b94e8a79335e5e6` (spec_review: codex@동일 SHA).

2026-08-04 design-system 온보딩 실측에서 `bd init --server`가 git origin(GitHub URL)을 `sync.remote`로 자동 영속화하고, `resolved` 상태·`backup.enabled=false` 등 fleet 정책이 `setup_beads.sh` 실행에만 의존하며, local-dolt 전제의 `AGENTS.md`/`CLAUDE.md`가 생성되는 문제를 확인했다. bd-recover 스킬은 lock 잔재·포트 drift·손상 스토어를 수기 스크립트로 복구한다. 이 플랜은 팩 A(init server-mode 정책 4건)와 팩 B(doctor 체커/픽서 4건)를 포크 소스에 흡수한다. 후속: CLI 일관성=beads-456, revert/링크=beads-i59, #4566=beads-67k, 머지 후 전달=beads-cpy, 스킬 축소=dotfiles-ms54.

**발동 술어 (확정):** 팩 A는 `initServerMode == true`(`--server`, 또는 `--shared-server`가 강제하는 경우 포함, `init.go:271-273`)에서 적용한다. `--external` 결합이 아니다 — 실측 커맨드(`bd init --server --server-host ... --server-port ...`)와 `setup_beads.sh`(365행) 모두 `--external` 없이 실행되므로, `--external` 게이트로는 재발을 막지 못한다. 명시 플래그는 항상 우선. `--proxied-server` 경로(미구현, 조기 return)는 건드리지 않는다.

**실행 환경:** `.worktrees/beads-u4d` (branch `beads-u4d`, base `main`). PR base `main`. 검증 canonical은 `env TEST_TIMEOUT=10m make test`(`-tags gms_pure_go`, repo-ops.toml [verify]).

## Phase 1: init server-mode 정책 A1·A3·A4

`cmd/bd/init.go`의 순수 정책 분기 3건. store 상호작용 없음.

1. **A1**: git-origin→sync.remote 유도 분기(`init.go:859` `else if` 조건)에 `!initServerMode` 가드 추가. 명시 `--remote` 경로(`initSyncRemoteExplicit`, `persistInitSyncRemote`의 `initRemote != ""` 단락)는 무변경. 술어를 순수 함수로 추출해 테이블 테스트(`init_remote_test.go` 패턴).
2. **A4**: `initServerMode`이고 `cmd.Flags().Changed`가 `--skip-agents`/`--agents-file`/`--agents-template`/`--agents-profile` 모두 false일 때 `skipAgents=true` 기본값(플래그 파싱부 `init.go:80-89` 부근; 소비 지점 `init.go:1533,1568` 무변경).
3. **A3**: `createConfigYaml`(`init.go:1226`) 이후 server 모드에서 `config.GetStringFromDir(beadsDir,"backup.enabled")==""`일 때만 `SetYamlConfigInDir(beadsDir,"backup.enabled","false")`. 실패는 주변 init 스타일대로 경고 후 계속(non-fatal).

검증: `go test ./cmd/bd -run 'Init' -tags gms_pure_go` green (신규 술어 테이블 테스트 포함).

## Phase 2: A2 resolved 시딩 + 팩 A 수용 시임

Phase 1 의존 (수용 테스트가 Phase 1 동작도 단언).

1. 순수 병합 헬퍼 `mergeCustomStatusValue(existing, "resolved")` — 토큰 부재 시에만 `,resolved` append, 결과를 `types.ParseCustomStatusConfig`로 검증. 배치는 `cmd/bd`(init 정책이므로 package main, 포크-국소 additive).
2. init에서 store 핸들이 살아 있는 구간(`init.go:~1276` 이전)에 배선: `store.GetConfig("status.custom")` → 병합 → `store.SetConfig` (같은 트랜잭션에서 `issueops.SyncCustomStatusesTable`이 `custom_statuses`를 동기화, `config_helpers.go:369-387`). SetConfig는 전체 치환이므로 read-then-merge 필수. 재init 시 사용자가 의도 제거한 `resolved`가 재추가되는 동작은 스펙 결정 사항으로 기록.
3. command-level 수용 테스트: `testDoltServerPort` 실서버 하네스(`init_test.go:1878` 패턴)로 **주 시나리오는 실제 회귀 경로인 `--external` 없는 `bd init --server --server-host ... --server-port ...`** 실행 → config.yaml에 `sync.remote` 부재·`backup.enabled: false` 존재·`AGENTS.md`/`CLAUDE.md` 미생성·`bd list --status resolved --json` 성공 + 명시 override(`--remote`, `--agents-file`) 존중 케이스. `--server --external` 결합은 별도 호환 케이스로만 유지.

검증: `go test ./cmd/bd -run 'InitServerMode' -tags gms_pure_go` green (신규 수용 시임 포함).

## Phase 3: doctor 라이프사이클 체커 B2·B3

Phase 1-2와 독립.

1. `doltserver`에 공개 API 2종 신설: ① read-only 검사 `InspectPIDState(beadsDir)` — pid/port 파일의 기록 PID를 읽고 `isProcessAlive`/`isDoltProcess`(비공개, doltserver.go:399-406 — 타 패키지에서 직접 호출 불가하므로 이 API가 감싼다)로 stale 여부 보고; ② 수리 `CleanupStalePIDState(beadsDir)` — 같은 `lockPath` flock을 **TryLock(비차단)**으로 획득(실패 = 서버 활동 중으로 보고 중단; 차단 대기는 라이브 서버에서 doctor 행 유발), **획득 후 flock 안에서 PID를 다시 읽어 process identity를 재검증**하고 stale일 때만 pid/port 제거(TOCTOU 차단 — 검사 시점과 삭제 시점 사이 새 서버가 떴으면 no-op). `dolt-server.lock`은 어떤 경로에서도 삭제하지 않음. 동시 start 회귀 테스트 동반.
2. B2 체크 "Stale Server PID State": ①의 검사 API로 판정, `runDiagnostics` 등록 + `applyFixList` switch에 케이스 추가(픽서 = ② 호출). 테스트는 fabricated-PID 패턴(`federation_test.go:587-639`).
3. B3 체크 "Dolt Port Drift": `configfile.Config.DoltServerPort`(stored) vs `doltserver.DefaultConfig(beadsDir).Port`(effective, authority 체인 env→port파일→config.yaml→deprecated metadata, doltserver.go:459-524) 비교. 게이트는 `configfile.Config.IsDoltServerMode()`(dolt_mode 기반, configfile.go:239-260 — `ResolveServerMode`는 고정 포트만으로 External 판정해 과광범위이므로 체크 게이트로 불사용, 근거 기록). 픽서는 상위 authority 소스 유효 시 `cfg.DoltServerPort=0; cfg.Save(beadsDir)`(패턴 `fix/database_config.go:51`)로 stale 키 **제거**(재작성 아님).
4. **lifecycle 판별 고정(스펙 B3 요구)**: 키 제거 후 `doltserver.ResolveServerMode`(servermode.go:60-96)가 `dolt_server_port` 부재로 `Owned`로 뒤집히지 않도록, canonical 판별에 `metadata dolt_mode == "server" → External` 분기를 추가한다. 키 제거 전/후 lifecycle 판별이 동일함을 단언하는 테스트 동반.

검증: `go test ./cmd/bd ./cmd/bd/doctor/... ./internal/doltserver/... -tags gms_pure_go` green (동시 start 회귀·lifecycle 유지 테스트 포함).

## Phase 4: B4 sync.remote shape 체크

1. `config.UnsetYamlConfigInDir(beadsDir, key)` 신설 — `SetYamlConfigInDir`(yaml_config.go:223-238) 미러(기존 `UnsetYamlConfig`는 CWD 스코프 뿐). 부재 키 no-op·파일 부재 동작 단위 테스트.
2. B4 체크: ⓐ 비정규 remote — `resolveSyncRemoteFromDir` 값과 `doltremote.Normalize` 결과 불일치(예: plain `https://github.com/...`)를 경고, `git+https`/`git+ssh` 정규형은 침묵(음성 케이스); 진단 전용, 자동 재작성 없음. ⓑ server 모드 리그(`IsDoltServerMode`)의 routine `sync.remote` 존재 자체를 경고 — 픽서(unset, 1의 헬퍼 사용)는 ⓑ에만.

검증: `go test ./cmd/bd ./cmd/bd/doctor/... ./internal/config/... -tags gms_pure_go` green (정규/비정규/server-mode-remote 픽스처 포함).

## Phase 5: B1 진단 절반 (안전)

1. doctor `SharedStore`(shared_store.go:63-66)가 삼키는 store open 에러를 additive로 기록(분류된 에러를 별도 필드에 보존; 기존 nil-store 의미론은 타 체크가 의존하므로 무변경 — 회귀 리스크 기록).
2. "Local Store Health" 체크: open 실패를 분류(손상 시그니처 vs 일시 오류)하고 유효 `sync.remote` 존재 여부를 함께 보고. 유효 remote 부재 시 진단 전용 + 복구 안내.

검증: `go test ./cmd/bd ./cmd/bd/doctor/... -tags gms_pure_go` green — 손상 스토어 픽스처에서 디스크 무변경으로 손상 판정 보고 단언 포함.

## Phase 6: B1 격리+재클론 픽서 (파괴적, 절단 가능)

v1 트림 확정: **embedded/local-clone 레이아웃만 복구**. 라이브 서버가 붙어 있거나 server-mode 오케스트레이션(stop→격리→재클론→재시작)이 필요한 경우 기존 `serverModeIntegrityRecoveryGuard`(fix/database_integrity.go:79-95) 전례대로 안내 후 거부 — full server-mode 복구는 후속 Bead로.

1. **storage 경계에 open 전에도 호출 가능한 좁은 recovery capability를 정의·구현**(`internal/storage`에 인터페이스/진입점을 두고 dolt 백엔드가 구현 — 격리 대상 판별과 이동을 storage 레이어가 소유, doctor/cmd는 그 capability만 호출; `<db>/.dolt` 내부 불가침). **격리 목적지는 `.beads/` 외부**의 타임스탬프 경로(스펙 B1). 진단은 Phase 5의 분류를 재사용. 재클론은 스펙이 명시 허용한 기존 `cloneFromRemoteWithMode`(bootstrap.go:789, `applyFixList`와 같은 package main이라 export 불필요) 재사용. 기존 `fix/database_integrity.go`의 rename+shell-out 안티패턴은 복제하지 않음.
2. 픽서 게이트: 기존 `--fix`의 프롬프트/`--yes` 확인(applyFixes:82-106)만 사용 — **전용 추가 플래그는 도입하지 않음**(승인 의도에 없는 public CLI 표면이며 `--fix --yes` 계약을 깨뜨림). 추가 조건: 유효 `sync.remote` 필수(부재 시 진단 전용), 파괴 케이스 게이팅 주석은 "Corrupt Manifest"(doctor_fix.go:402-409) 전례 따름.
3. 전체 회귀 스윕: `env TEST_TIMEOUT=10m make test` 완전 green + init/doctor fallout 분류. 포크 머지-마찰 자세(모든 변경이 server-mode 게이트 or additive) diff 감사.

검증: `env TEST_TIMEOUT=10m make test` 완전 green (B1 픽서 테스트 — `.beads/` 외부 격리 위치 단언·복구 후 `bd list --json` 성공 단언 포함).

## Test scope

- **P1**: A1/A3/A4 술어 순수 테이블 테스트 (`init_remote_test.go` 패턴, RED→GREEN).
- **P2**: 병합 헬퍼 단위 테스트; command-level 수용 시임 — `testDoltServerPort` 실서버 하네스에서 `bd init --server` 전체 수용 기준 1 + override 존중 (RED→GREEN, 수용 기준 직결).
- **P3**: B2 fabricated-PID 픽스처(생존/사망 분기)·flock 내 재검증(TOCTOU) 케이스, `CleanupStalePIDState` 동시 start 회귀, B3 drift 검출·stale 키 제거, `ResolveServerMode` 키 제거 전/후 lifecycle 동일 단언 (RED→GREEN).
- **P4**: `UnsetYamlConfigInDir` 단위, B4 양성/음성/unset 픽스처 (RED→GREEN).
- **P5**: 손상 스토어 픽스처 진단 전용(디스크 무변경 단언) 테스트 (RED→GREEN).
- **P6**: 격리+재클론 픽서(프롬프트/`--yes` 게이트, `.beads/` 외부 격리 위치, 복구 후 `bd list --json` 성공), 거부 경로(라이브 서버/remote 부재) 테스트 (RED→GREEN).
- **제외**: proxied-server 경로(미구현), full server-mode B1 복구(후속 Bead), embedded/local 기존 동작(기존 characterization 테스트가 회귀 앵커 — 신규 RED 불필요).
