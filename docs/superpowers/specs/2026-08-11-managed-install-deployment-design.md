# managed release 기반 bd 설치 배포 설계

## 상태

- 사용자 설계 승인: 2026-08-11
- owning Bead: `beads-loy`
- 원인 incident: `beads-1nj` / PR #10 post-merge cleanup
- protocol provider: beads-ui `UI-16ep` / PR #118 (closed·merged)
- provider spec: beads-ui `docs/superpowers/specs/2026-08-10-one-click-deployment-reconciler-design.md`

## 문제

`beads-1nj`의 PR merge와 pinned post-merge `make test`는 성공했지만, deploy 직전
workspace checkout이 dirty여서 `deploy_base_not_synced:checkout_dirty`로 cleanup이 멈췄다.
현재 `docs/agents/repo-ops.toml`의 `[deploy] cmd = ["make", "install-force"]`는 shared
checkout을 cwd로 사용한다. guard를 없애면 verify하지 않은 working-tree bytes나 local-only
commit을 `~/.local/bin/bd`에 설치할 수 있다.

Beads core는 orchestration policy를 소유하지 않는다. 해결은 core command/schema를 바꾸는 것이
아니라 repo-local deploy entry를 beads-ui의 existing managed Adapter protocol에 맞춰 exact
candidate release에서 실행하는 것이다.

## 목표

1. `make install-force`의 source를 shared checkout에서 verified candidate release `D`로 옮긴다.
2. installed `bd`와 `beads` alias를 exact candidate build artifact에 bind해 readback한다.
3. protocol v1 receipt가 repo/remote/base/floor/candidate/attempt/source와 installed binary hash를
   증명한다.
4. install/readback failure에서는 receipt와 cleanup을 전진시키지 않는다.
5. 기존 developer-facing `make install`/`make install-force` 의미는 바꾸지 않는다.

## 비목표

- Beads issue/schema/CLI에 deploy나 Worker 개념 추가
- shared checkout 자동 stash/reset/clean/checkout
- Homebrew, release tag, npm/MCP package publication
- cross-platform system-wide install 위치 변경
- installed binary rollback UI 또는 managed release GC
- beads-ui Reconciler protocol 재정의

## Architecture

### Candidate-local Adapter

새 executable `scripts/bdui-managed-deploy.sh`는 Reconciler가 materialize한 release cwd에서만
실행된다. `docs/agents/repo-ops.toml`은 다음 exact declaration으로 전환한다.

```toml
[deploy]
adapter = "managed"
cmd = ["scripts/bdui-managed-deploy.sh"]
timeout_ms = 600000
```

Adapter는 shell interpolation 없이 전달된 `BDUI_DEPLOY_*` protocol v1 env를 읽고 다음을
검증한다.

- source repo, target remote/base, merged floor, candidate, attempt, release, receipt가 모두 존재
- floor가 candidate의 ancestor
- release realpath가 data home의 `bdui/deploy/<repo-id>/releases/<D>` exact child
- release `HEAD == D`, clean tracked status, expected remote identity
- receipt path가 worker-owned state path이며 pre-existing symlink/special file이 아님

검증 실패 전에는 build/install/receipt mutation을 시작하지 않는다. Adapter는 source shared
checkout을 status/fetch/checkout/reset/stash/clean하거나 cwd로 사용하지 않는다.

### Build, install, readback

Adapter는 release에서 canonical `make install-force`를 실행한다. 이 target은
`gms_pure_go` build tag와 repo Makefile의 version ldflag를 사용해 release-local `bd`를 만들고
`$HOME/.local/bin/bd`에 copy한 뒤 `$HOME/.local/bin/beads -> bd` alias를 만든다.

command exit 0만으로 성공하지 않는다. 다음 readback을 모두 확인한다.

1. release-local `bd`와 installed `bd`가 executable regular file이다.
2. 두 파일의 SHA-256이 같다.
3. installed `bd version --json`이 성공하고 build identity가 candidate short SHA와 일치한다.
4. installed `beads`가 같은 directory의 `bd`를 가리키는 relative symlink다.
5. source release의 `HEAD`, status, remote identity가 install 뒤에도 변하지 않았다.

installed path는 existing Makefile contract가 선택한 `$HOME/.local/bin`을 사용하며, Adapter가 별도
install root를 발명하지 않는다. readback 실패 시 이미 copy된 partial artifact를 success로
표시하지 않는다. 같은 candidate retry가 idempotent하게 build/copy/symlink를 다시 수렴시킨다.

### Receipt와 provider 전진 순서

Adapter는 provider protocol v1의 terminal receipt를 worker-owned path에 temp file + fsync +
atomic rename으로 기록한다. 필수 binding은 다음과 같다.

- protocol version, absolute source repo, target remote/base
- attempt ID, merged floor SHA, candidate SHA
- verified release path/HEAD와 verify outcome
- `action_outcomes`: build, install, binary hash readback, alias readback
- action plan digest
- deployment source path/HEAD
- exact installed binary path/hash/version과 alias target
- terminal success timestamp

stdout prose, installed binary mtime, PATH 첫 항목은 authority가 아니다. receipt는 credential,
전체 environment, remote userinfo를 기록하지 않는다.

전진 순서는 `installed artifact mutation -> exact readback -> atomic terminal receipt ->
Reconciler receipt validation -> provider deployment state/cleanup/Parent close`로 고정한다.
Adapter는 provider의 marker, cleanup state, Bead lifecycle을 소유하거나 직접 변경하지 않는다.
같은 attempt의 terminal receipt가 이미 있으면 exact binding과 live installed artifact를 다시
검증한 뒤 동일한 성공으로 취급하고, 내용이 다르거나 검증할 수 없으면 conflict로 실패한다.

## 실패와 복구

- release materialize/fetch/spawn timeout: beads-ui Reconciler의 same-attempt bounded retry
- floor ancestry, release identity, dirty release: fail closed; source를 자동 clean/reset하지 않음
- build/install/hash/alias/version failure: terminal receipt 없음, provider state·cleanup 미전진
- receipt write/validation failure: provider가 terminal success를 인정하지 않고 state·cleanup 미전진
- terminal receipt 전 interruption: same candidate install을 idempotent하게 재실행
- terminal receipt 뒤 provider state 전 interruption: Reconciler가 existing receipt와 live artifact를
  다시 검증한 뒤 provider 전진을 재개하며 Adapter-local state 복구는 만들지 않음
- shared checkout dirt/branch/HEAD: Adapter가 읽지 않으므로 outcome에 영향 없음
- credentials/permission/global toolchain failure: 자동 code repair가 아니라 terminal evidence

tracked script/Makefile/repo-ops regression은 beads-ui completion intent가 `deploy_failed`로 판정할
수 있지만, agent가 global toolchain·permissions를 수정하거나 test를 약화해서 green으로 만들지
않는다.

## Rollout

1. closed provider `UI-16ep`의 managed Adapter protocol을 installed beads-ui가 제공하는지
   확인한다.
2. candidate-local Adapter와 isolated tests를 추가한다.
3. 같은 PR에서 `docs/agents/repo-ops.toml`을 managed declaration으로 전환한다.
4. 이 PR의 merged candidate에 포함된 새 Adapter를 old installed command 없이 release에서 직접
   실행한다.
5. exact installed binary receipt가 merge floor를 포함하면 cleanup과 `beads-loy` close를
   완료한다.

첫 rollout acceptance는 실제 `$HOME/.local/bin/bd` mutation을 포함하므로 PR merge 뒤 managed
cleanup에서만 수행한다. implementation worktree에서 live install을 실행하지 않는다.

## Test scope

RED-GREEN seam은 candidate-local install/readback과 protocol receipt다.

- 새 isolated shell/integration test
  - temp source/release/HOME/XDG에서 valid protocol receipt
  - floor ancestry, release path/HEAD/status/remote binding 거부
  - release-local/installed binary SHA-256 일치와 `beads -> bd` alias
  - install/hash/version/alias failure에서 terminal receipt가 없음
  - receipt write/validation failure에서 provider terminal success가 성립하지 않음
  - terminal receipt 전 interruption의 same-candidate idempotent retry
  - terminal receipt 뒤 provider interruption에서 existing receipt/live artifact 재검증
  - dirty/feature/local-ahead shared checkout 불변
- repo-ops declaration contract test
  - `adapter = "managed"`, exact relative argv, `detached` absent, timeout

다음은 RED-GREEN seam이 아니라 pinned base에서 먼저 통과해야 하는 baseline
characterization/regression이다.

- 새 `scripts/install_targets_test.go`
  - `TestMakeInstallChecksForUpdates`: isolated fixture와 temp `HOME`에서 `make install`이
    `check-up-to-date`를 거치고 `$HOME/.local/bin/bd`를 설치하며 relative `beads -> bd` alias를 만듦
  - `TestMakeInstallForceSkipsUpdateCheck`: 같은 fixture에서 `make install-force`가 update check만
    건너뛰고 install path와 alias 계약은 동일하게 보존함
  - 두 test는 live `$HOME`을 건드리지 않으며 Adapter/repo-ops 변경 전 pinned base에서도 통과해야 함

검증 bundle:

- focused Adapter/repo-ops/install tests
- `make test`
- `golangci-lint run ./...` when available, with baseline warnings separated
- `git diff --check`
- PR CI required checks

## Acceptance criteria

1. managed deploy가 shared checkout의 branch, dirt, HEAD를 읽거나 변경하지 않는다.
2. verified candidate `D`에서 만든 binary와 installed `bd`의 SHA-256이 같다.
3. installed version/build identity와 `beads -> bd` alias readback이 receipt에 bind된다.
4. exact readback과 terminal receipt validation 전에는 provider deployment state, cleanup,
   Parent close가 전진하지 않는다.
5. crash/retry에서 duplicate logical deploy나 candidate regression이 없다.
6. Beads core issue/schema/CLI에는 orchestration-specific surface가 추가되지 않는다.
7. first managed merge cleanup이 exact receipt로 `beads-loy`를 close한다.
