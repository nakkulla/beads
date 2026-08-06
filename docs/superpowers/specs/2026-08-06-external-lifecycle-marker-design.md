# external 리그 lifecycle 명시 마커 설계 (beads-ode)

- Bead: `beads-ode` (route: spec_backed)
- 선행: `beads-u4d` (PR #5, merged) — 재진입 조건 충족
- 작성일: 2026-08-06

## 배경과 문제

external 리그(사용자가 dolt sql-server lifecycle을 직접 관리하는 리그)의 유일한 영속
판별 신호가 `metadata.json`의 `dolt_server_port` 존재 여부다
(`internal/doltserver/servermode.go:114-128`). 이 때문에:

- beads-u4d의 B3 픽서(`cmd/bd/doctor/fix/dolt_port_drift.go:99-114`)는 상위 권위
  포트 소스가 유효해도, 키 제거가 lifecycle 판정을 External→Owned로 뒤집는 리그에서는
  stale `dolt_server_port` 제거를 거부할 수밖에 없다(섀도 서버 방지 가드).
- `dolt_mode`는 lifecycle 표지가 될 수 없다: CGO_ENABLED=0 빌드에서 `usesSQLServer()`가
  무조건 true라(`cmd/bd/store_factory_nocgo.go:15-17`) 모든 `bd init`이
  `dolt_mode: server`를 기록한다(`cmd/bd/init.go:1211-1218`). 즉 `server`는 기본값이지
  external 신호가 아니다.

본 스펙은 lifecycle 소유권을 명시하는 별도 마커를 신설해 port 키 의존을 제거한다.

## 결정 사항 (사용자 확정)

1. 마커 형태: **`dolt_server_lifecycle` 문자열 enum** (`dolt_mode` 값 확장·불리언 기각).
2. 기존 리그 마이그레이션: **전용 doctor 체크 + 픽서 신설** (port-drift 픽서 통합 기각).
3. init 기록 조건: **`--server-port` 명시 시에만** (host-only 신호 확장 기각).

## 비범위

- `--server-host`만 있고 포트가 없는 리그의 Owned 분류 비일관(원격 접속 + 로컬
  auto-start 공존). 알려진 한계로만 기록하며 본 스펙은 동작을 바꾸지 않는다.
- shared-server 모드의 마커 기록. env/config.yaml 런타임 해석(`IsSharedServerMode`)이
  이미 최우선 권위이고, git-tracked 마커는 shared 설정이 없는 클론에 잘못 전파된다.
- `"owned"` 값의 실사용. 값 공간만 예약하고 인식·기록하지 않는다.
- proxied-server 경로(`usesProxiedServer()` 분기)와 `configfile.ExternalDoltConfig`
  (proxied-server 백엔드 접속 설정 — 이름만 유사한 다른 개념).

## 설계

### 1. 마커 정의 — `internal/configfile`

- `Config`에 필드 추가 (`configfile.go`의 dolt_* 필드군 옆):
  `DoltServerLifecycle string` / JSON `dolt_server_lifecycle,omitempty`.
- 상수 `DoltServerLifecycleExternal = "external"` 신설. 인식 값은 현재 이 하나.
- 접근자 `(c *Config) HasExternalServerLifecycle() bool`: **raw 값이 `""`가 아니면
  true** — 정규화 없이 원문 기준으로 판정한다(공백만 있는 값도 External). 오타·미래
  값·깨진 값이 Owned로 조용히 굴러떨어져 섀도 서버를 fork하는 것보다, auto-start
  억제(접속 에러로 표면화)가 안전하다는 fail-safe 원칙. trim + lower 정규화는
  doctor의 인식/미인식 값 진단(§4)에서만 사용한다.
- 재init 보존: init은 기존 config를 로드해 수정하므로(`cmd/bd/init.go:1140-1152`)
  별도 필드는 자동 보존된다. `dolt_mode` 무조건 재기록(1211-1218)의 영향을 받지 않는다.

### 2. `ResolveServerMode` 권위 체인 — `internal/doltserver/servermode.go`

`serverModeFromFileConfig`의 판별 순서(함수 주석 47-52 동반 갱신):

1. env 2단계 (`BEADS_DOLT_SERVER_MODE=1`, shared-server) — 변경 없음, 최우선 유지.
2. `dolt_mode == "embedded"` → Embedded — **마커보다 우선 유지**. embedded는 서버
   자체가 없어 lifecycle 마커가 무의미하며, 모순 조합은 doctor 진단 대상(§4).
3. **신설**: `HasExternalServerLifecycle()` → External — port보다 상위 권위(완료 기준 2).
4. `DoltServerPort > 0` → External — 미마이그레이션 리그용 레거시 폴백으로 존치.
5. 기본 Owned.

`ServerModeForConfig`는 같은 함수를 공유하므로 자동 반영된다 — B3 가드가 추가 수정
없이 마커를 존중하게 되는 지점.

### 3. init 기록 경로 — `cmd/bd/init.go`

- 기록 조건은 순수 함수 시임으로 분리한다:
  `shouldRecordExternalLifecycle(portFlagChanged, sharedServer bool) bool` —
  `--server-port` 플래그가 **명시적으로 주어졌고**(`cmd.Flags().Changed("server-port")`,
  `serverPort != 0` 값 비교가 아님) **shared-server가 아닐 때**(resolved:
  `--shared-server` 플래그 ∨ `BEADS_DOLT_SHARED_SERVER` env ∨ config.yaml)만 true.
  live server 없이 단위 테스트 가능한 결정 시임(F4 대응).
- 메타데이터 기록 블록(1220-1233)의 `!usesProxiedServer()` 분기와
  `initTimeCloneConfig`(2592-2608, 원격 부트스트랩 경로) 양쪽에 위 판정 결과를 전달해,
  true일 때 `cfg.DoltServerLifecycle = configfile.DoltServerLifecycleExternal` 기록.
- 마커를 쓰지 않는 경로(명시): `--server` 단독(포트 없음 = Owned auto-start 리그),
  `BEADS_DOLT_SERVER_MODE=1` env(런타임 최우선 권위로 이미 해석됨), shared-server
  init — **`--shared-server --server-port X` 조합 포함**(port가 있어도 shared 판정이
  우선, 비범위 선언과 정합).

### 4. doctor 체크 + 픽서 — 마이그레이션 경로

기존 port-drift 쌍(`cmd/bd/doctor/port_drift.go` + `cmd/bd/doctor/fix/dolt_port_drift.go`)
패턴을 따른다.

- `fix.InspectExternalLifecyclePin(beadsDir)` — 판정 구조체 반환:
  - **Applicable(핀 대상)**: metadata.json 존재 ∧ `IsDoltServerMode()`(dolt_mode 게이트,
    port-drift와 동일) ∧ 마커 부재 ∧ `DoltServerPort > 0` (= port로만 External 판정 중).
  - **Skip**: shared-server env 모드(port-drift와 동일 사유 — 판정이 이 리그 소유가 아님),
    metadata.json 부재, embedded 모드, 마커 이미 존재(멱등), port 부재.
  - **진단 전용(자동 수정 없음)**: ① 마커가 비어있지 않은 미인식 값 — fail-safe로
    External 동작 중임을 경고. ② `dolt_mode == "embedded"` ∧ 마커 존재 — 모순 config 경고.
- 픽서 `ExternalLifecyclePin`: `DoltServerLifecycle = "external"` 기록 후 `Save`.
  멱등(마커 존재 → not applicable). metadata.json은 git-tracked이므로 사용자가 커밋하면
  fleet 클론 전체에 전파된다 — 이것이 기존 리그 마이그레이션 경로의 전부(완료 기준 1).
- 등록: doctor 체크 목록(카테고리 `CategoryData`)과 `cmd/bd/doctor_fix.go`의 체크명
  디스패치에 추가.
- **마이그레이션은 2회 실행 모델이다**: doctor의 픽스 워크리스트는 체크 평가 시점에
  확정되므로(`doctor_fix.go` — `Fix != ""`인 warning/error만 수집, 픽스 적용 후 재평가
  없음), 마커 없는 drift 리그에서 첫 `bd doctor --fix`는 핀만 기록하고, port-drift
  키 제거는 가드가 통과되는 **다음 실행**에서 Removable로 잡힌다. 같은 실행 안에서
  핀→제거를 잇는 워크리스트 커플링은 의도적으로 만들지 않는다(픽서 단일 책임 유지;
  키 잔존은 무해하고 제거는 drift가 실재할 때만 필요). 핀 픽서의 사용자 출력에
  "다음 `bd doctor --fix` 실행에서 stale port 키 제거가 가능해진다"를 안내한다.

### 5. B3 가드 상호작용 (완료 기준 4)

코드 변경 없음 — §2에 의해 마커 보유 리그는 `ModeBefore == ModeAfter == External`이
되어 `Removable=true`. 테스트로 단언한다(Test scope 참조).

### 6. 표시·문서

- `cmd/bd/config_show.go:233-235` 필드 목록에 `dolt_server_lifecycle` 추가.
- `docs/TROUBLESHOOTING.md:287-301` ("auto-start를 원하면 `dolt_server_port`를
  제거하라") 갱신: 마커 도입 후에는 `dolt_server_lifecycle`도 함께 제거해야 auto-start가
  복원됨을 명시.

## 엣지 케이스

| 상황 | 동작 |
|---|---|
| 마커 + port 둘 다 존재 | External (마커 권위, port는 무해한 잔존 키) |
| 마커만 존재, port 없음 | External — 키 제거 후 목표 상태 |
| 마커 미인식 값 (예: 오타, 공백만 있는 값) | External (raw non-empty fail-safe) + doctor 경고 |
| `--shared-server --server-port X` init | 마커 기록 안 함 (shared 판정 우선, 런타임 env 권위로 External) |
| `dolt_mode=embedded` + 마커 | Embedded (기존 우선순위 유지) + doctor 모순 경고 |
| 재init | 기존 config 로드 경로라 마커 보존; `--server-port` 재명시 시 재기록(멱등) |
| 클론 (git-tracked 전파) | 마커 커밋 후 모든 클론이 External — port 키와 동일한 전파 의미론, 악화 없음 |
| 구버전 bd 바이너리가 신 metadata.json 읽음 | 미지 JSON 필드 무시(Go unmarshal) → 기존 port 기반 판정으로 동작 |

## Test scope

RED→GREEN 시임은 아래 4개. 각 시임은 마커 도입 전 실패(또는 부재)하고 도입 후 통과한다.

1. **servermode 판별** (`internal/doltserver/servermode_forconfig_test.go`):
   마커→External(port 무관), 미인식·공백 값→External(raw non-empty fail-safe),
   embedded+마커→Embedded, 마커 부재+port→External(레거시 폴백 유지),
   마커 부재+port 부재→Owned.
2. **init 기록 판정 시임** (신규 untagged 테스트, 예:
   `cmd/bd/init_lifecycle_marker_test.go`): `shouldRecordExternalLifecycle`의
   결정 매트릭스 — port 플래그 명시+비shared → true, port 미명시 → false,
   shared+port → false. live server 불요, 결정적 RED→GREEN(F4 대응).
   기존 `cmd/bd/init_server_mode_acceptance_test.go`(`//go:build cgo`,
   `skipIfNoDolt`)에는 마커 기록/부재의 command-level 수용 케이스를 **보조 통합
   검증**으로 추가하되, 시임 권위는 untagged 테스트에 둔다.
3. **doctor 핀 픽서** (신규 테스트 파일): Applicable 판정 매트릭스(§4의 대상/Skip/진단
   케이스), 핀 기록·readback, 멱등성.
4. **B3 가드 언블록** (`cmd/bd/doctor/fix/dolt_port_drift_test.go`):
   마커+port+상위 권위 포트 소스 리그에서 `Removable=true`이고 픽스 실행 후에도
   `ResolveServerMode == External`; 마커 없는 리그는 여전히 차단(기존 테스트 유지).

## 검증

```
go build ./... && go vet ./...
go test ./cmd/bd/... ./internal/doltserver/... ./internal/configfile/...
```

실패 집합은 pinned base와 비교해 신규 실패 0을 기준으로 한다.

## 완료 기준 매핑 (beads-ode)

| Bead 완료 기준 | 본 스펙 |
|---|---|
| 1. 마커 설계·신설 + 기존 리그 마이그레이션 | §1 + §4 |
| 2. `ResolveServerMode`가 마커를 port보다 상위 권위로 | §2 |
| 3. `bd init --server`/`--server-port` 경로가 마커 기록 | §3 (port 명시 시 기록; `--server` 단독은 Owned라 기록하지 않음 — 사용자 확정) |
| 4. B3 가드가 마커 보유 리그에서 키 제거 허용을 테스트로 단언 | §5 + Test scope 4 |
