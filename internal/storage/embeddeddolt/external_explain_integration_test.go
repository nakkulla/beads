//go:build cgo

package embeddeddolt_test

import (
	"context"
	"sort"
	"testing"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// explainFilter is what runReadyExplain passes: open issues, priority sort.
func explainFilter() types.WorkFilter {
	return types.WorkFilter{Status: types.StatusOpen, SortPolicy: types.SortPolicyPriority}
}

func (e *dualEngine) exec(t *testing.T, ctx context.Context, query string, args ...any) {
	t.Helper()
	if _, err := e.conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// readyExplain runs the combined explain query the CLI uses and returns the
// ready IDs plus the external-blocked half.
func (e *dualEngine) readyExplain(t *testing.T, ctx context.Context, filter types.WorkFilter, opts issueops.ExternalResolverOptions) ([]string, *types.ExternalBlocked) {
	t.Helper()
	if _, err := e.conn.ExecContext(ctx, "USE `mainpx`"); err != nil {
		t.Fatalf("USE mainpx: %v", err)
	}
	tx, err := e.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	ready, blocked, err := issueops.GetReadyWorkWithExternalBlockedInTx(ctx, tx, filter, opts)
	if err != nil {
		t.Fatalf("GetReadyWorkWithExternalBlockedInTx: %v", err)
	}
	return idsOf(ready), blocked
}

func candidateRefs(t *testing.T, eb *types.ExternalBlocked, id string) []string {
	t.Helper()
	if eb == nil {
		t.Fatalf("no external-blocked result, want candidate %s", id)
	}
	for _, c := range eb.Candidates {
		if c.Issue.ID != id {
			continue
		}
		if c.BlockedByCount != len(c.BlockedBy) {
			t.Fatalf("%s: blocked_by_count = %d, want %d (len(blocked_by))", id, c.BlockedByCount, len(c.BlockedBy))
		}
		refs := append([]string(nil), c.BlockedBy...)
		sort.Strings(refs)
		return refs
	}
	t.Fatalf("candidate %s missing from external-blocked: %+v", id, candidateIDsOf(eb))
	return nil
}

func candidateIDsOf(eb *types.ExternalBlocked) []string {
	if eb == nil {
		return nil
	}
	ids := make([]string, 0, len(eb.Candidates))
	for _, c := range eb.Candidates {
		ids = append(ids, c.Issue.ID)
	}
	sort.Strings(ids)
	return ids
}

func assertNotCandidate(t *testing.T, eb *types.ExternalBlocked, ids ...string) {
	t.Helper()
	got := candidateIDsOf(eb)
	for _, want := range ids {
		for _, id := range got {
			if id == want {
				t.Fatalf("%s must not be an external-blocked candidate, got %v", want, got)
			}
		}
	}
}

// Acceptance 1 + 6a: an external-only open issue lands in the blocked half
// with the raw ref as its only blocker, and never appears in ready.
func TestExternalExplain_ExternalOnlyIsBlocked(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	e.insertIssue(t, ctx, "mainpx", "m-free", "open")
	e.insertIssue(t, ctx, "mainpx", "m-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-unsat", "m-unsat", "external:beads:cap-b")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, []string{"m-free"})
	if got := candidateRefs(t, eb, "m-unsat"); len(got) != 1 || got[0] != "external:beads:cap-b" {
		t.Fatalf("m-unsat blocked_by = %v, want [external:beads:cap-b]", got)
	}
	// Mutual exclusion: nothing may sit in both halves of one run.
	for _, id := range ready {
		assertNotCandidate(t, eb, id)
	}
}

// Acceptance 3: the same holds under fail-closed verdicts — non-server mode
// and an unmapped project are unresolvable, and unresolvable blocks like
// unsatisfied does (union semantics).
func TestExternalExplain_FailClosedIsBlocked(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// cap-a IS satisfied in the provider DB, but non-server mode never asks.
	e.insertIssue(t, ctx, "extpx", "ext-closed", "closed")
	e.insertLabel(t, ctx, "extpx", "ext-closed", "provides:cap-a")
	e.insertIssue(t, ctx, "mainpx", "m-sat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-sat", "m-sat", "external:beads:cap-a")
	e.insertIssue(t, ctx, "mainpx", "m-nomap", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-nomap", "m-nomap", "external:other:cap-x")

	// Non-server mode: every ref unresolvable.
	ready, eb := e.readyExplain(t, ctx, explainFilter(), issueops.ExternalResolverOptions{})
	assertIDs(t, ready, nil)
	if got := candidateRefs(t, eb, "m-sat"); len(got) != 1 || got[0] != "external:beads:cap-a" {
		t.Fatalf("non-server mode: m-sat blocked_by = %v", got)
	}
	if got := candidateRefs(t, eb, "m-nomap"); len(got) != 1 || got[0] != "external:other:cap-x" {
		t.Fatalf("non-server mode: m-nomap blocked_by = %v", got)
	}

	// Server mode with a mapping for beads only: cap-a resolves satisfied,
	// the unmapped project stays unresolvable and blocked.
	ready, eb = e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, []string{"m-sat"})
	assertNotCandidate(t, eb, "m-sat")
	if got := candidateRefs(t, eb, "m-nomap"); len(got) != 1 || got[0] != "external:other:cap-x" {
		t.Fatalf("unmapped project: m-nomap blocked_by = %v", got)
	}
}

// Acceptance 4: wisps reach the blocked half through wisp_dependencies.
func TestExternalExplain_WispIsBlocked(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	e.insertWisp(t, ctx, "mainpx", "w-free", "open")
	e.insertWisp(t, ctx, "mainpx", "w-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "wisp_dependencies", "wd-unsat", "w-unsat", "external:beads:cap-b")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, []string{"w-free"})
	if got := candidateRefs(t, eb, "w-unsat"); len(got) != 1 || got[0] != "external:beads:cap-b" {
		t.Fatalf("w-unsat blocked_by = %v", got)
	}
}

// Acceptance 5: a satisfied ref (provider issue closed with the provides
// label) puts the issue back in ready and out of the blocked half entirely.
func TestExternalExplain_SatisfiedReturnsToReady(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	e.insertIssue(t, ctx, "extpx", "ext-closed", "closed")
	e.insertLabel(t, ctx, "extpx", "ext-closed", "provides:cap-a")
	e.insertIssue(t, ctx, "mainpx", "m-sat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-sat", "m-sat", "external:beads:cap-a")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, []string{"m-sat"})
	if eb != nil && (len(eb.Candidates) > 0 || len(eb.StoredBlockedRefs) > 0) {
		t.Fatalf("satisfied ref must produce no external-blocked rows, got %+v", eb)
	}
}

// Acceptance 7: rows ready work rejects for a non-external reason must not
// leak in as new blocked entries. A stored-blocked row is merge material only
// (StoredBlockedRefs), never a candidate.
func TestExternalExplain_NoLeakageFromNonExternalExclusions(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	for _, id := range []string{"m-closed", "m-deferred", "m-pinned", "m-stored", "m-clean"} {
		status := "open"
		if id == "m-closed" {
			status = "closed"
		}
		e.insertIssue(t, ctx, "mainpx", id, status)
		e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-"+id, id, "external:beads:cap-b")
	}
	e.exec(t, ctx, "UPDATE `mainpx`.issues SET defer_until = DATE_ADD(UTC_TIMESTAMP(), INTERVAL 7 DAY) WHERE id = 'm-deferred'")
	e.exec(t, ctx, "UPDATE `mainpx`.issues SET pinned = 1 WHERE id = 'm-pinned'")
	e.exec(t, ctx, "UPDATE `mainpx`.issues SET is_blocked = 1 WHERE id = 'm-stored'")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, nil)
	assertNotCandidate(t, eb, "m-closed", "m-deferred", "m-pinned", "m-stored")
	if got := candidateIDsOf(eb); len(got) != 1 || got[0] != "m-clean" {
		t.Fatalf("candidates = %v, want [m-clean]", got)
	}
	refs, ok := eb.StoredBlockedRefs["m-stored"]
	if !ok || len(refs) != 1 || refs[0] != "external:beads:cap-b" {
		t.Fatalf("m-stored must be merge-only material, got StoredBlockedRefs = %v", eb.StoredBlockedRefs)
	}
}

// Acceptance 6a under cross-table duplication: when an ID exists in BOTH the
// issues and wisps tables, the wisp record is canonical (be-iabdi). Whichever
// side carries the external edge, the ID must land in exactly one half.
func TestExternalExplain_CrossTableIDStaysInOneHalf(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// x-wisp-dep: canonical wisp is external-blocked, stale issues row is clean.
	e.insertIssue(t, ctx, "mainpx", "x-wisp-dep", "open")
	e.insertWisp(t, ctx, "mainpx", "x-wisp-dep", "open")
	e.insertExternalDep(t, ctx, "mainpx", "wisp_dependencies", "wd-x", "x-wisp-dep", "external:beads:cap-b")

	// x-issue-dep: stale issues row carries the edge, canonical wisp is clean.
	e.insertIssue(t, ctx, "mainpx", "x-issue-dep", "open")
	e.insertWisp(t, ctx, "mainpx", "x-issue-dep", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-x", "x-issue-dep", "external:beads:cap-b")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())

	// The canonical wisp decides: blocked for the first ID, ready for the second.
	assertIDs(t, ready, []string{"x-issue-dep"})
	if got := candidateRefs(t, eb, "x-wisp-dep"); len(got) != 1 || got[0] != "external:beads:cap-b" {
		t.Fatalf("x-wisp-dep blocked_by = %v", got)
	}
	assertNotCandidate(t, eb, "x-issue-dep")
	for _, id := range ready {
		assertNotCandidate(t, eb, id)
	}
}

// Acceptance 2 (storage half): a row with BOTH a live local blocker and an
// external ref is reported as merge material, so the CLI can complete the
// blocked entry the stored path already emits for it.
func TestExternalExplain_MixedLocalAndExternal(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	e.insertIssue(t, ctx, "mainpx", "m-blocker", "open")
	e.insertIssue(t, ctx, "mainpx", "m-mixed", "open")
	e.exec(t, ctx,
		"INSERT INTO `mainpx`.dependencies (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_by) VALUES ('d-local', 'm-mixed', 'm-blocker', NULL, NULL, 'blocks', 'tester')")
	e.exec(t, ctx, "UPDATE `mainpx`.issues SET is_blocked = 1 WHERE id = 'm-mixed'")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-ext", "m-mixed", "external:beads:cap-b")

	ready, eb := e.readyExplain(t, ctx, explainFilter(), beadsOpts())
	assertIDs(t, ready, []string{"m-blocker"})
	assertNotCandidate(t, eb, "m-mixed")
	refs, ok := eb.StoredBlockedRefs["m-mixed"]
	if !ok || len(refs) != 1 || refs[0] != "external:beads:cap-b" {
		t.Fatalf("m-mixed StoredBlockedRefs = %v, want [external:beads:cap-b]", eb.StoredBlockedRefs)
	}
}
