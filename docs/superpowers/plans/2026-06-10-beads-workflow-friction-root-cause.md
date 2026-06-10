# Beads Workflow Friction Root Cause Implementation Plan

> **For agentic workers:** REQUIRED EXECUTION SKILL: use the workflow-selected execution skill to implement this plan task-by-task. For Beads-backed work, use `superpowers:executing-plans` by default; use `superpowers:subagent-driven-development` only when the parent Bead has `metadata.execution_mode=subagent_driven` or the user explicitly requested subagent implementation. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the Beads-side root causes and regression gaps behind workflow friction around JSONL auto-import, write/readback consistency, Dolt push error guidance, child Bead seeding, and dogfood installation.

**Architecture:** Keep Beads command code on the documented storage boundary: commands classify and guide, while storage-driver-specific integrity work remains in `internal/storage/dolt`. Add regression tests before each behavior change, then implement the smallest code change that satisfies the reviewed spec. Keep `.beads/issues.jsonl` and custom `import.path` as passive/exact JSONL files, not sync protocols.

**Tech Stack:** Go, Cobra CLI, Dolt/EmbeddedDolt storage, Beads `bd` command integration tests, `make test`, `scripts/pr-preflight.sh`.

---

## File Structure

- Modify: `docs/superpowers/specs/2026-06-10-beads-workflow-friction-root-cause-design.md`
  - Already reviewed spec authority; do not edit unless implementation reveals a spec contradiction.
- Create: `docs/superpowers/plans/2026-06-10-beads-workflow-friction-root-cause.md`
  - This implementation plan and execution checklist.
- Modify: `cmd/bd/auto_import_upgrade_unit_test.go`
  - Unit coverage for exact configured import path, temp JSONL sibling ignore, and server-mode auto-import disabled boundary.
- Modify: `cmd/bd/auto_import_upgrade_test.go`
  - Embedded CLI regression coverage for stale configured JSONL not clobbering current DB state.
- Create: `cmd/bd/write_readback_embedded_test.go`
  - Embedded CLI consistency regression for create/update/close/readback/list/auto-export.
- Modify: `cmd/bd/dolt.go`
  - Add dangling/missing chunk classifier and user-facing guidance for manual `bd dolt push` and `bd dolt push --remote` failures.
- Modify: `cmd/bd/dolt_autopush.go`
  - Reuse dangling/missing chunk classifier for opt-in auto-push warning text without retrying.
- Modify: `cmd/bd/dolt_autopush_test.go`
  - Unit tests for dangling/missing chunk classifier and guidance coverage.
- Modify: `cmd/bd/create.go`
  - Allow explicit child creation with `--id <parent.N> --parent <parent>` while keeping invalid parent-prefix combinations fatal.
- Modify: `cmd/bd/create_embedded_test.go`
  - CLI tests for explicit child create, invalid prefix, duplicate replay, label/dependency parity, and next auto-child ID.
- Modify: `cmd/bd/create_proxied_integration_test.go`
  - Proxied/server-mode integration parity for explicit child creation when the existing proxied harness is available.
- Modify only if required by failing tests: `cmd/bd/create_form.go`
  - Keep form flow behavior unchanged unless shared helper extraction is needed to avoid divergent CLI/form child logic.

## Execution Rules

- Work from `/Users/isy_macstudio/External/beads/.worktrees/beads-urc` on branch `beads-urc`.
- Before implementing, run the PR preflight command from the reviewed spec.
- For each task: write/adjust the focused test first, run the focused test to see the expected failure or characterization result, implement minimal code, rerun focused tests, commit.
- Do not run `make install-force` until all code tests pass.
- Do not run destructive cleanup, remove Homebrew files, or mutate live `~/.config`, `~/.codex`, `~/.claude`, or other runtime dirs from this worktree.
- When the task changes the `bd` CLI binary, final dogfood verification must run `make install-force` from this source checkout, then verify active `bd` points at `$HOME/.local/bin/bd`.

---

### Task 1: Preflight and Baseline Evidence

**Files:**
- Read: `AGENTS.md`
- Read: `docs/PROJECT_CHARTER.md`
- Read: `docs/superpowers/specs/2026-06-10-beads-workflow-friction-root-cause-design.md`

- [ ] **Step 1: Verify workspace and reviewed spec metadata**

Run:
```bash
git status --short --branch
bd show beads-urc --json | jq '.[0] | {id,status,labels,metadata,spec_id}'
shasum -a 256 docs/superpowers/specs/2026-06-10-beads-workflow-friction-root-cause-design.md
```

Expected:
```text
## beads-urc
```

Expected Bead facts:
- `labels` contains `reviewed:spec`.
- `metadata.spec_review_verdict` is `APPROVE`.
- `metadata.spec_content_hash` equals the printed SHA-256 hash.

- [ ] **Step 2: Run PR preflight search before implementation**

Run:
```bash
scripts/pr-preflight.sh --search "auto import JSONL readback child create dolt push" --repo gastownhall/beads
```

Expected: command exits 0 and reports any related PRs/issues. If it reports an external contributor PR touching the same files, stop and inspect `PR_MAINTAINER_GUIDELINES.md` before editing.

- [ ] **Step 3: Run narrow baseline tests for existing touched packages**

Run:
```bash
go test ./cmd/bd -run 'TestMaybeAutoImportJSONL|TestIsDoltAutoPushEnabled|TestPushWithContextReturnsPushError'
```

Expected: PASS. If this fails before edits, record the failing test names in Bead notes and fix only if required for this scope.

- [ ] **Step 4: Commit baseline evidence if only Beads metadata changed**

No code commit is expected for this task. If previous steps produced only terminal evidence, do not commit.

---

### Task 2: Auto-import Exact Path, Temp JSONL, and Server Boundary Tests

**Files:**
- Modify: `cmd/bd/auto_import_upgrade_unit_test.go`
- Modify: `cmd/bd/auto_import_upgrade_test.go`
- Read: `cmd/bd/import_path.go`
- Read: `cmd/bd/auto_import_upgrade.go`

- [ ] **Step 1: Add unit coverage for exact configured import path and temp siblings**

Append this test to `cmd/bd/auto_import_upgrade_unit_test.go` near `TestMaybeAutoImportJSONL_UsesConfiguredImportPath`:

```go
func TestConfiguredImportJSONLPathExactFileIgnoresTempSiblings(t *testing.T) {
	initConfigForTest(t)
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".~issues.jsonl.tmp"), []byte("temp\n"), 0o600); err != nil {
		t.Fatalf("write temp sibling: %v", err)
	}
	if got, want := configuredImportJSONLPath(dir), filepath.Join(dir, "issues.jsonl"); got != want {
		t.Fatalf("default configuredImportJSONLPath() = %q, want %q", got, want)
	}

	config.Set("import.path", "beads.jsonl")
	if err := os.WriteFile(filepath.Join(dir, ".~beads.jsonl.tmp"), []byte("temp\n"), 0o600); err != nil {
		t.Fatalf("write custom temp sibling: %v", err)
	}
	if got, want := configuredImportJSONLPath(dir), filepath.Join(dir, "beads.jsonl"); got != want {
		t.Fatalf("custom configuredImportJSONLPath() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Add explicit unit coverage for server-mode disabled boundary**

Add this test below `TestShouldRunAutoImportJSONL`:

```go
func TestShouldRunAutoImportJSONL_ServerModeDisabledBoundary(t *testing.T) {
	store := &fakeFallbackStore{}
	cmd := &cobra.Command{Use: "update"}
	if shouldRunAutoImportJSONL(cmd, store, false, false, true) {
		t.Fatal("server-mode startup auto-import must stay disabled; stale JSONL must not be applied by server-backed commands")
	}
}
```

- [ ] **Step 3: Run the new unit tests**

Run:
```bash
go test ./cmd/bd -run 'TestConfiguredImportJSONLPathExactFileIgnoresTempSiblings|TestShouldRunAutoImportJSONL_ServerModeDisabledBoundary|TestMaybeAutoImportJSONL_UsesConfiguredImportPath'
```

Expected: PASS. If `TestConfiguredImportJSONLPathExactFileIgnoresTempSiblings` fails, change only `cmd/bd/import_path.go` so `configuredImportJSONLPath` returns the exact configured relative file and never glob-selects siblings.

- [ ] **Step 4: Add embedded CLI regression for stale configured JSONL not clobbering current DB**

Append this test to `cmd/bd/auto_import_upgrade_test.go` after `TestEmbeddedAutoImportJSONLSkipsNonEmpty`:

```go
func TestEmbeddedAutoImportStaleConfiguredJSONLDoesNotClobberCurrentState(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "sj")
	issue := bdCreate(t, bd, dir, "current title", "--type", "task", "--priority", "1")

	stale := types.Issue{
		ID:        issue.ID,
		Title:     "stale JSONL title",
		Status:    types.StatusOpen,
		IssueType: types.TypeTask,
		Priority:  4,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC),
	}
	b, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("marshal stale issue: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write stale issues.jsonl: %v", err)
	}

	bdUpdate(t, bd, dir, issue.ID, "--title", "newer title", "--set-metadata", "guard=present")
	shown := bdShow(t, bd, dir, issue.ID)
	if shown.Title != "newer title" {
		t.Fatalf("bd show title = %q, want newer title", shown.Title)
	}
	if shown.MetadataRefs["guard"] != "present" {
		t.Fatalf("bd show metadata guard = %q, want present", shown.MetadataRefs["guard"])
	}

	listed := bdListJSON(t, bd, dir)
	found := false
	for _, got := range listed {
		if got.ID == issue.ID {
			found = true
			if got.Title != "newer title" {
				t.Fatalf("bd list title = %q, want newer title", got.Title)
			}
		}
	}
	if !found {
		t.Fatalf("bd list did not include %s", issue.ID)
	}
}
```

- [ ] **Step 5: Run the embedded stale JSONL regression**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run '^TestEmbeddedAutoImportStaleConfiguredJSONLDoesNotClobberCurrentState$'
```

Expected: PASS. If it fails by reverting title/metadata, fix `cmd/bd/auto_import_upgrade.go` so non-empty DB state prevents auto-import before any parse/import path can run.

- [ ] **Step 6: Commit auto-import coverage**

Run:
```bash
git add cmd/bd/auto_import_upgrade_unit_test.go cmd/bd/auto_import_upgrade_test.go cmd/bd/import_path.go
git commit -m 'auto-import JSONL 경계 회귀 테스트 추가'
```

Expected: commit succeeds. If `cmd/bd/import_path.go` did not change, omit it from `git add`.

---

### Task 3: Create/Update/Close Readback and Auto-export Consistency

**Files:**
- Create: `cmd/bd/write_readback_embedded_test.go`
- Reuse helpers from: `cmd/bd/create_embedded_test.go`, `cmd/bd/update_embedded_test.go`, `cmd/bd/close_embedded_test.go`
- Read if test fails: `cmd/bd/export_auto.go`, `cmd/bd/main.go`, `cmd/bd/update.go`, `cmd/bd/close.go`

- [ ] **Step 1: Create the embedded consistency test file**

Create `cmd/bd/write_readback_embedded_test.go` with this content:

```go
//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedWriteReadbackAndAutoExportConsistency(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wr")
	runBDForReadback(t, bd, dir, "config", "set", "export.auto", "true")
	runBDForReadback(t, bd, dir, "config", "set", "export.interval", "0s")

	issue := bdCreate(t, bd, dir, "initial readback title", "--type", "task", "--priority", "2")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "initial readback title", types.StatusOpen, "")

	bdUpdate(t, bd, dir, issue.ID, "--title", "updated readback title", "--set-metadata", "phase=updated")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "updated readback title", types.StatusOpen, "updated")

	bdClose(t, bd, dir, issue.ID, "--reason", "readback consistency test")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "updated readback title", types.StatusClosed, "updated")
}

func runBDForReadback(t *testing.T, bd, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func assertReadbackIssue(t *testing.T, bd, dir, beadsDir, id, wantTitle string, wantStatus types.Status, wantPhase string) {
	t.Helper()

	shown := bdShow(t, bd, dir, id)
	if shown.Title != wantTitle {
		t.Fatalf("bd show title = %q, want %q", shown.Title, wantTitle)
	}
	if shown.Status != wantStatus {
		t.Fatalf("bd show status = %q, want %q", shown.Status, wantStatus)
	}
	if wantPhase != "" && shown.MetadataRefs["phase"] != wantPhase {
		t.Fatalf("bd show metadata phase = %q, want %q", shown.MetadataRefs["phase"], wantPhase)
	}

	listed := bdListJSON(t, bd, dir)
	found := false
	for _, got := range listed {
		if got.ID == id {
			found = true
			if got.Title != wantTitle {
				t.Fatalf("bd list title = %q, want %q", got.Title, wantTitle)
			}
			if got.Status != wantStatus {
				t.Fatalf("bd list status = %q, want %q", got.Status, wantStatus)
			}
		}
	}
	if !found {
		t.Fatalf("bd list did not include %s", id)
	}

	exported := readExportedIssue(t, filepath.Join(beadsDir, "issues.jsonl"), id)
	if exported.Title != wantTitle {
		t.Fatalf("export title = %q, want %q", exported.Title, wantTitle)
	}
	if exported.Status != wantStatus {
		t.Fatalf("export status = %q, want %q", exported.Status, wantStatus)
	}
	if wantPhase != "" && exported.MetadataRefs["phase"] != wantPhase {
		t.Fatalf("export metadata phase = %q, want %q", exported.MetadataRefs["phase"], wantPhase)
	}
}

func readExportedIssue(t *testing.T, path, id string) *types.Issue {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export %s: %v", path, err)
	}
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			continue
		}
		if issue.ID == id {
			return &issue
		}
		_ = lineNo
	}
	t.Fatalf("export %s did not contain issue %s", path, id)
	return nil
}
```

- [ ] **Step 2: Run the focused consistency test**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run '^TestEmbeddedWriteReadbackAndAutoExportConsistency$'
```

Expected before any implementation fix: FAIL if create/update/close/readback/export order is broken. If it passes, keep it as regression coverage and continue.

- [ ] **Step 3: Fix only if the consistency test fails**

If the failure shows `bd show` and `bd list` disagree, inspect the write path in `cmd/bd/update.go` or `cmd/bd/close.go` and make the write complete before returning. If the failure shows export stale or missing, inspect `cmd/bd/export_auto.go` and ensure auto-export runs after the command's write commit/export-safe point.

Use this shape for a post-write ordering fix if needed:

```go
// After the command has completed the storage write and any required commit:
commandDidWrite.Store(true)
```

If the existing command already sets `commandDidWrite`, do not add a duplicate flag; instead move the existing mark later so `PersistentPostRun` sees a committed/current store state.

- [ ] **Step 4: Rerun focused and neighboring tests**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run 'TestEmbeddedWriteReadbackAndAutoExportConsistency|TestEmbeddedUpdate|TestEmbeddedClose'
```

Expected: PASS.

- [ ] **Step 5: Commit readback consistency work**

Run:
```bash
git add cmd/bd/write_readback_embedded_test.go cmd/bd/export_auto.go cmd/bd/update.go cmd/bd/close.go cmd/bd/main.go
git commit -m 'write readback export 일관성 회귀 테스트 추가'
```

Expected: commit succeeds. Omit files that did not change.

---

### Task 4: Dolt Dangling/Missing Chunk Classification and Guidance

**Files:**
- Modify: `cmd/bd/dolt.go`
- Modify: `cmd/bd/dolt_autopush.go`
- Modify: `cmd/bd/dolt_autopush_test.go`
- Read: `internal/storage/dolt/errors.go`
- Read: `internal/storage/dolt/store.go:2030-2185`
- Read: `docs/PROJECT_CHARTER.md`

- [ ] **Step 1: Add classifier tests**

Append these tests to `cmd/bd/dolt_autopush_test.go`:

```go
func TestIsDanglingChunkReferenceErr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "storage sentinel", err: fmt.Errorf("push failed: %w", storagedolt.ErrDanglingReference), want: true},
		{name: "dangling text", err: errors.New("dolt push failed: dangling chunk reference: hash abc referenced but not present"), want: true},
		{name: "missing chunk text", err: errors.New("remote manifest references missing chunk abc123"), want: true},
		{name: "unrelated", err: errors.New("authentication failed"), want: false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isDanglingChunkReferenceErr(tc.err); got != tc.want {
				t.Fatalf("isDanglingChunkReferenceErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDanglingChunkReferenceGuidanceText(t *testing.T) {
	t.Parallel()
	msg := danglingChunkReferenceGuidance()
	for _, want := range []string{
		"bd dolt pull && bd dolt push",
		"does not run pull or retry automatically",
		"non-zero",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("guidance missing %q:\n%s", want, msg)
		}
	}
}
```

Also add imports to `cmd/bd/dolt_autopush_test.go`:

```go
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storagedolt "github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/config"
)
```

- [ ] **Step 2: Run classifier tests and confirm failure**

Run:
```bash
go test ./cmd/bd -run 'TestIsDanglingChunkReferenceErr|TestDanglingChunkReferenceGuidanceText'
```

Expected: FAIL because `isDanglingChunkReferenceErr` and `danglingChunkReferenceGuidance` do not exist.

- [ ] **Step 3: Implement classifier and guidance in `cmd/bd/dolt.go`**

Add `storagedolt` import to `cmd/bd/dolt.go`:

```go
storagedolt "github.com/steveyegge/beads/internal/storage/dolt"
```

Add these helpers near `isDivergedHistoryErr`:

```go
func isDanglingChunkReferenceErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, storagedolt.ErrDanglingReference) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dangling chunk") ||
		strings.Contains(msg, "dangling reference") ||
		(strings.Contains(msg, "missing chunk") && strings.Contains(msg, "manifest")) ||
		strings.Contains(msg, "referenced but not present")
}

func danglingChunkReferenceGuidance() string {
	return "\nDolt remote/local chunk integrity problem detected.\n" +
		"The push was not recovered automatically: bd dolt push does not run pull or retry automatically.\n" +
		"Run this explicit one-time recovery if you want to reconcile before pushing again:\n" +
		"  bd dolt pull && bd dolt push\n" +
		"If the command still fails, keep the non-zero result and inspect the Dolt remote before forcing any history change.\n"
}

func printDanglingChunkReferenceGuidance() {
	fmt.Fprint(os.Stderr, danglingChunkReferenceGuidance())
}
```

- [ ] **Step 4: Wire guidance into manual push paths without retrying**

In `doltPushCmd` error handling, add `isDanglingChunkReferenceErr` checks after diverged-history checks for both `PushRemote` and default push paths:

```go
if isDivergedHistoryErr(err) {
	printDivergedHistoryGuidance("push --force")
} else if isDanglingChunkReferenceErr(err) {
	printDanglingChunkReferenceGuidance()
}
```

For default push:

```go
if isDivergedHistoryErr(pushErr) {
	op := "push"
	if force {
		op = "push --force"
	}
	printDivergedHistoryGuidance(op)
} else if isDanglingChunkReferenceErr(pushErr) {
	printDanglingChunkReferenceGuidance()
}
```

Do not call `st.Pull`, `st.Push`, `st.ForcePush`, `st.PushRemote`, or any merge/retry helper from this error path.

- [ ] **Step 5: Wire guidance into opt-in auto-push warning without retrying**

In `cmd/bd/dolt_autopush.go`, inside `maybeAutoPush` failure output, add:

```go
if isDivergedHistoryErr(err) {
	printDivergedHistoryGuidance("push")
} else if isDanglingChunkReferenceErr(err) {
	printDanglingChunkReferenceGuidance()
}
```

Do not change throttle behavior and do not retry auto-push.

- [ ] **Step 6: Rerun Dolt classifier and auto-push tests**

Run:
```bash
go test ./cmd/bd -run 'TestIsDanglingChunkReferenceErr|TestDanglingChunkReferenceGuidanceText|TestPushWithContextReturnsPushError|TestIsDoltAutoPushEnabled_DefaultOff'
```

Expected: PASS.

- [ ] **Step 7: Commit Dolt push guidance work**

Run:
```bash
git add cmd/bd/dolt.go cmd/bd/dolt_autopush.go cmd/bd/dolt_autopush_test.go
git commit -m 'dolt push dangling refs 안내 추가'
```

Expected: commit succeeds.

---

### Task 5: Explicit Child Create with `--id` + `--parent`

**Files:**
- Modify: `cmd/bd/create.go`
- Modify: `cmd/bd/create_embedded_test.go`
- Modify: `cmd/bd/create_proxied_integration_test.go`
- Read if behavior is surprising: `internal/storage/issueops/create.go`, `internal/storage/issueops/child_id.go`, `internal/storage/child_counter_reservation.go`

- [ ] **Step 1: Add embedded CLI tests for explicit child create**

Add this subtest block inside `TestEmbeddedCreate` in `cmd/bd/create_embedded_test.go`, near existing parent subtests:

```go
t.Run("parent_explicit_child_id", func(t *testing.T) {
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "ec")
	parent := bdCreate(t, bd, dir, "Explicit child parent", "-t", "epic", "-l", "team-a")

	child := bdCreate(t, bd, dir, "Explicit child", "--id", parent.ID+".7", "--parent", parent.ID, "-l", "child-only")
	if child.ID != parent.ID+".7" {
		t.Fatalf("explicit child ID = %q, want %q", child.ID, parent.ID+".7")
	}
	assertDepExistsWithType(t, beadsDir, "ec", child.ID, parent.ID, string(types.DepParentChild))

	store := openStore(t, beadsDir, "ec")
	labels, err := store.GetLabels(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	labelMap := map[string]bool{}
	for _, label := range labels {
		labelMap[label] = true
	}
	for _, want := range []string{"team-a", "child-only"} {
		if !labelMap[want] {
			t.Fatalf("child labels missing %q: %v", want, labels)
		}
	}

	auto := bdCreate(t, bd, dir, "Auto child after explicit", "--parent", parent.ID)
	if auto.ID != parent.ID+".8" {
		t.Fatalf("next auto child ID = %q, want %q", auto.ID, parent.ID+".8")
	}
})

t.Run("parent_explicit_child_id_rejects_wrong_prefix", func(t *testing.T) {
	dir, _, _ := bdInit(t, bd, "--prefix", "wp")
	parent := bdCreate(t, bd, dir, "Wrong prefix parent", "-t", "epic")
	out := bdCreateFail(t, bd, dir, "Wrong prefix child", "--id", "wp-other.1", "--parent", parent.ID)
	if !strings.Contains(out, "must start with parent prefix") {
		t.Fatalf("wrong-prefix error missing parent-prefix guidance:\n%s", out)
	}
})

t.Run("parent_explicit_child_id_duplicate_is_not_recreated", func(t *testing.T) {
	dir, _, _ := bdInit(t, bd, "--prefix", "dc")
	parent := bdCreate(t, bd, dir, "Duplicate child parent", "-t", "epic")
	childID := parent.ID + ".3"
	_ = bdCreate(t, bd, dir, "Duplicate child first", "--id", childID, "--parent", parent.ID)
	out := bdCreateFail(t, bd, dir, "Duplicate child second", "--id", childID, "--parent", parent.ID)
	if !strings.Contains(strings.ToLower(out), "duplicate") && !strings.Contains(strings.ToLower(out), "already exists") {
		t.Fatalf("duplicate explicit child error should mention duplicate/already exists:\n%s", out)
	}
})
```

- [ ] **Step 2: Run embedded child tests and confirm failure**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run '^TestEmbeddedCreate/parent_explicit_child_id'
```

Expected: FAIL with `cannot specify both --id and --parent flags`.

- [ ] **Step 3: Implement explicit child create mode in `cmd/bd/create.go`**

Replace the existing conflict block:

```go
if explicitID != "" && parentID != "" {
	FatalError("cannot specify both --id and --parent flags")
}
```

with:

```go
explicitChildID := explicitID != "" && parentID != ""
if explicitChildID && !strings.HasPrefix(explicitID, parentID+".") {
	FatalError("explicit child ID %q must start with parent prefix %q when --parent is set", explicitID, parentID+".")
}
```

Then replace the child ID allocation block:

```go
if parentID != "" {
	childID, err := store.GetNextChildID(rootCtx, parentID)
	if err != nil {
		FatalError("%v", err)
	}
	explicitID = childID // Set as explicit ID for the rest of the flow.
	createCtx = storage.WithReservedChildCounter(createCtx, parentID, childID)
}
```

with:

```go
if parentID != "" && !explicitChildID {
	childID, err := store.GetNextChildID(rootCtx, parentID)
	if err != nil {
		FatalError("%v", err)
	}
	explicitID = childID // Set as explicit ID for the rest of the flow.
	createCtx = storage.WithReservedChildCounter(createCtx, parentID, childID)
}
```

This preserves existing auto child allocation and lets `CreateIssue`/`ReconcileChildCounters` advance child counters for explicit hierarchical IDs. If the explicit child counter is not committed/staged, inspect `issueops.CreateIssueDirtyTables` and use `CreateIssueResult.ChangedChildCounterTables` rather than adding command-layer storage introspection.

- [ ] **Step 4: Rerun embedded child tests**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run '^TestEmbeddedCreate/(parent_explicit_child_id|parent_explicit_child_id_rejects_wrong_prefix|parent_explicit_child_id_duplicate_is_not_recreated)$'
```

Expected: PASS.

- [ ] **Step 5: Add proxied/server-mode parity subtest**

Add this subtest to `cmd/bd/create_proxied_integration_test.go` near the existing `parent_child` subtest:

```go
t.Run("parent_explicit_child_id", func(t *testing.T) {
	p := bdProxiedInit(t, bd, "pe")
	parent := bdProxiedCreate(t, bd, p.dir, "Parent epic", "-t", "epic", "-l", "team-a")
	child := bdProxiedCreate(t, bd, p.dir, "Explicit child", "--id", parent.ID+".5", "--parent", parent.ID, "-l", "child-only")

	if child.ID != parent.ID+".5" {
		t.Fatalf("explicit child ID = %q, want %q", child.ID, parent.ID+".5")
	}
	db := openProxiedDB(t, p)
	assertProxiedDepExistsWithType(t, db, child.ID, parent.ID, "parent-child")
	labels := getProxiedLabels(t, db, child.ID)
	labelMap := make(map[string]bool)
	for _, l := range labels {
		labelMap[l] = true
	}
	for _, want := range []string{"team-a", "child-only"} {
		if !labelMap[want] {
			t.Fatalf("child labels missing %q: %v", want, labels)
		}
	}
})
```

- [ ] **Step 6: Run proxied create parity test**

Run:
```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run '^TestCreateProxied/parent_explicit_child_id$'
```

Expected: PASS. If the exact parent test name differs, run:

```bash
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run 'parent_explicit_child_id'
```

- [ ] **Step 7: Commit child create work**

Run:
```bash
git add cmd/bd/create.go cmd/bd/create_embedded_test.go cmd/bd/create_proxied_integration_test.go
git commit -m 'create 명시적 child id parent 조합 지원'
```

Expected: commit succeeds.

---

### Task 6: Scoped Test Sweep and Root-Cause Gap Audit

**Files:**
- Read: changed files from Tasks 2-5
- Read if failures occur: exact failing test files

- [ ] **Step 1: Run all scoped tests for touched areas**

Run:
```bash
go test ./cmd/bd -run 'TestMaybeAutoImportJSONL|TestConfiguredImportJSONLPathExactFileIgnoresTempSiblings|TestShouldRunAutoImportJSONL|TestIsDanglingChunkReferenceErr|TestDanglingChunkReferenceGuidanceText'
CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run 'TestEmbeddedAutoImport|TestEmbeddedWriteReadbackAndAutoExportConsistency|TestEmbeddedCreate/parent_explicit_child_id|parent_explicit_child_id'
```

Expected: PASS.

- [ ] **Step 2: Audit reviewed spec goals against changed tests/code**

Run:
```bash
rg -n -e 'stale configured|ConfiguredImportJSONLPathExact|ServerMode|DanglingChunk|WriteReadback|parent_explicit_child_id|bd dolt pull && bd dolt push' cmd/bd internal docs/superpowers/plans/2026-06-10-beads-workflow-friction-root-cause.md
```

Expected:
- Matches exist for stale configured JSONL tests.
- Matches exist for exact configured import path/temp sibling coverage.
- Matches exist for dangling/missing chunk classifier/guidance.
- Matches exist for explicit child ID tests.
- Matches exist for write/readback consistency test.

- [ ] **Step 3: Commit any fixups**

If the audit reveals a missing spec goal, add the missing test/code now and commit with a narrow Korean message. If no gaps are found, do not create a commit.

---

### Task 7: Full Repository Validation

**Files:**
- Read: `Makefile`
- Read if failures occur: failing package/test files

- [ ] **Step 1: Run default local test suite**

Run:
```bash
make test
```

Expected: PASS. If it fails, inspect the first failing package and fix only failures caused by this branch.

- [ ] **Step 2: Run shipped-config CGO package tests for touched CLI package**

Run:
```bash
CGO_ENABLED=1 go test -tags gms_pure_go ./cmd/bd/...
```

Expected: PASS. If this is too slow or environment-blocked, record the exact blocker and rerun the focused `CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1` commands from Tasks 2-6.

- [ ] **Step 3: Check git status**

Run:
```bash
git status --short --branch
```

Expected: clean worktree on `beads-urc`.

---

### Task 8: Global Dogfood Install and Active Binary Verification

**Files:**
- No source edits expected.
- Runtime install target: `$HOME/.local/bin/bd`

- [ ] **Step 1: Install branch build globally**

Run from `/Users/isy_macstudio/External/beads/.worktrees/beads-urc`:

```bash
make install-force
hash -r
```

Expected: install succeeds. This step intentionally updates the global `bd` binary after tests pass.

- [ ] **Step 2: Verify active `bd` is local build, not Homebrew**

Run:
```bash
command -v bd
bd --version
type -a bd
```

Expected:
- `command -v bd` prints `$HOME/.local/bin/bd`.
- `bd --version` contains the current branch commit SHA or build version derived from this checkout.
- `/opt/homebrew/bin/bd` is absent from active resolution or appears after `$HOME/.local/bin/bd`.

- [ ] **Step 3: Dogfood current Bead readback using installed binary**

Run:
```bash
bd show beads-urc --json | jq '.[0] | {id,status,labels,metadata}'
```

Expected:
- `id` is `beads-urc`.
- `labels` still include `reviewed:spec`.
- No stale JSONL rollback occurs after readback.

- [ ] **Step 4: Commit install evidence only if source files changed during install**

Run:
```bash
git status --short --branch
```

Expected: clean. If generated source files changed, inspect them; commit only if they are tracked, intentional, and required by this branch.

---

### Task 9: Implementation Review, PR Delivery, and Bead Handoff

**Files:**
- Beads metadata: `beads-urc`
- GitHub PR target: `gastownhall/beads`

- [ ] **Step 1: Run formal implementation-review gate**

Use workflow review-gate dispatch for implementation review on the final diff:

```bash
git fetch origin main
git diff --stat origin/main...HEAD
git diff origin/main...HEAD -- cmd/bd internal docs/superpowers/plans docs/superpowers/specs
```

Expected: diff contains only `beads-urc` plan/spec and implementation files. Formal review must return `APPROVE` or `APPROVE_WITH_CHANGES` before PR Delivery.

- [ ] **Step 2: Apply required review fixes and rerun focused tests**

If implementation review returns `REVISE`, fix each finding in scope, rerun the narrow test covering the fix, commit, then re-review according to workflow review-gate rules.

- [ ] **Step 3: Push branch**

Run:
```bash
git push -u origin beads-urc
```

Expected: push succeeds.

- [ ] **Step 4: Create PR after non-blocking implementation review**

Run:
```bash
gh pr create --repo gastownhall/beads --base main --head beads-urc --title "Beads workflow friction root cause 수리" --body-file /tmp/beads-urc-pr-body.md
```

Before running, create `/tmp/beads-urc-pr-body.md` with:

```markdown
## Summary
- Add regression coverage for JSONL auto-import/readback/temp-file boundaries.
- Add dangling/missing chunk `bd dolt push` classify-and-guide behavior without implicit retry.
- Support idempotent explicit child creation with `bd create --id <parent.N> --parent <parent>`.
- Install and dogfood the branch build after validation.

## Verification
- `scripts/pr-preflight.sh --search "auto import JSONL readback child create dolt push" --repo gastownhall/beads`
- `go test ./cmd/bd -run 'TestMaybeAutoImportJSONL|TestConfiguredImportJSONLPathExactFileIgnoresTempSiblings|TestShouldRunAutoImportJSONL|TestIsDanglingChunkReferenceErr|TestDanglingChunkReferenceGuidanceText'`
- `CGO_ENABLED=1 BEADS_TEST_EMBEDDED_DOLT=1 go test -tags gms_pure_go ./cmd/bd -run 'TestEmbeddedAutoImport|TestEmbeddedWriteReadbackAndAutoExportConsistency|TestEmbeddedCreate/parent_explicit_child_id|parent_explicit_child_id'`
- `make test`
- `CGO_ENABLED=1 go test -tags gms_pure_go ./cmd/bd/...`
- `make install-force`
- `bd show beads-urc --json`
```

- [ ] **Step 5: Record PR Delivery metadata and resolve Bead**

After PR creation, update Beads metadata and resolve according to workflow lifecycle:

```bash
bd dolt pull
bd update beads-urc --set-metadata pr_url=<PR_URL> --json
bd close beads-urc --reason "PR Delivery: <PR_URL>" --json
bd show beads-urc --json | jq '.[0] | {id,status,metadata,labels}'
bd dolt push
```

Expected: Bead status becomes `resolved`, not `closed`, because PR Finish/merge is a separate explicit route.

---

## Self-Review Checklist

- Spec goal 1 (write/readback/JSONL/Dolt state mismatch): Task 3 covers create/update/close/show/list/export consistency.
- Spec goal 2 (embedded auto-import stale configured JSONL): Task 2 covers stale configured JSONL, custom `import.path`, and temp sibling exact-file behavior.
- Spec goal 3 (server-mode startup auto-import disabled): Task 2 covers `shouldRunAutoImportJSONL(..., serverMode=true) == false`.
- Spec goal 4 (dangling refs classify/guidance): Task 4 covers classifier/guidance and forbids implicit retry/pull.
- Spec goal 5 (child seeding idempotency): Task 5 covers explicit child ID, invalid prefix, duplicate replay, parent-child dependency, labels, and next auto-child ID.
- Spec goal 6 (temp JSONL safety): Task 2 covers temp siblings and no glob source behavior.
- Spec goal 7 (global dogfood install): Task 8 covers `make install-force`, `command -v bd`, `bd --version`, `type -a bd`, and `bd show beads-urc --json`.
- Follow-up disposition: spec says no dotfiles/Homebrew follow-ups and possible upstream Dolt issue only if root cause is confirmed; no plan task creates premature follow-ups.
