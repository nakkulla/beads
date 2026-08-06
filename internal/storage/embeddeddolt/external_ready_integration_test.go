//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/types"
)

// dualEngine holds a single embedded engine hosting two beads databases
// (mainpx = the workspace, extpx = the external provider database), so a
// database-qualified resolver query runs against the second database from
// within the same connection the ready path uses.
type dualEngine struct {
	conn *sql.Conn
}

func newDualEngine(t *testing.T) *dualEngine {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dir, "", "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = cleanup()
		t.Fatalf("pin conn: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = cleanup()
	})
	for _, name := range []string{"mainpx", "extpx"} {
		if _, err := conn.ExecContext(ctx, "CREATE DATABASE `"+name+"`"); err != nil {
			t.Fatalf("CREATE DATABASE %s: %v", name, err)
		}
		if _, err := conn.ExecContext(ctx, "USE `"+name+"`"); err != nil {
			t.Fatalf("USE %s: %v", name, err)
		}
		if _, err := schema.MigrateUp(ctx, conn); err != nil {
			t.Fatalf("MigrateUp %s: %v", name, err)
		}
	}
	// Prefix discovery reads each database's own config.issue_prefix, so the
	// two rigs must declare theirs exactly like a real bd rig does.
	e := &dualEngine{conn: conn}
	e.setIssuePrefix(t, ctx, "mainpx", "m")
	e.setIssuePrefix(t, ctx, "extpx", "ext")
	return e
}

func (e *dualEngine) insertIssue(t *testing.T, ctx context.Context, db, id, status string) {
	t.Helper()
	q := "INSERT INTO `" + db + "`.issues (id, title, description, design, acceptance_criteria, notes, status, priority, issue_type) VALUES (?, ?, '', '', '', '', ?, 2, 'task')"
	if _, err := e.conn.ExecContext(ctx, q, id, id, status); err != nil {
		t.Fatalf("insert issue %s.%s: %v", db, id, err)
	}
}

func (e *dualEngine) insertWisp(t *testing.T, ctx context.Context, db, id, status string) {
	t.Helper()
	q := "INSERT INTO `" + db + "`.wisps (id, title, description, design, acceptance_criteria, notes, status, priority, issue_type) VALUES (?, ?, '', '', '', '', ?, 2, 'task')"
	if _, err := e.conn.ExecContext(ctx, q, id, id, status); err != nil {
		t.Fatalf("insert wisp %s.%s: %v", db, id, err)
	}
}

func (e *dualEngine) setIssuePrefix(t *testing.T, ctx context.Context, db, prefix string) {
	t.Helper()
	q := "INSERT INTO `" + db + "`.config (`key`, value) VALUES ('issue_prefix', ?)"
	if _, err := e.conn.ExecContext(ctx, q, prefix); err != nil {
		t.Fatalf("set issue_prefix %s: %v", db, err)
	}
}

func (e *dualEngine) insertExternalDep(t *testing.T, ctx context.Context, db, depTable, depID, issueID, extRef string) {
	t.Helper()
	q := "INSERT INTO `" + db + "`.`" + depTable + "` (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_by) VALUES (?, ?, NULL, NULL, ?, 'blocks', 'tester')"
	if _, err := e.conn.ExecContext(ctx, q, depID, issueID, extRef); err != nil {
		t.Fatalf("insert external dep %s.%s: %v", db, depID, err)
	}
}

func (e *dualEngine) ready(t *testing.T, ctx context.Context, filter types.WorkFilter, opts issueops.ExternalResolverOptions) []string {
	t.Helper()
	if _, err := e.conn.ExecContext(ctx, "USE `mainpx`"); err != nil {
		t.Fatalf("USE mainpx: %v", err)
	}
	tx, err := e.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	issues, err := issueops.GetReadyWorkInTx(ctx, tx, filter, opts)
	if err != nil {
		t.Fatalf("GetReadyWorkInTx: %v", err)
	}
	return idsOf(issues)
}

func (e *dualEngine) readyWithCounts(t *testing.T, ctx context.Context, filter types.WorkFilter, opts issueops.ExternalResolverOptions) []string {
	t.Helper()
	if _, err := e.conn.ExecContext(ctx, "USE `mainpx`"); err != nil {
		t.Fatalf("USE mainpx: %v", err)
	}
	tx, err := e.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	items, err := issueops.GetReadyWorkWithCountsInTx(ctx, tx, filter, opts)
	if err != nil {
		t.Fatalf("GetReadyWorkWithCountsInTx: %v", err)
	}
	var ids []string
	for _, it := range items {
		if it != nil && it.Issue != nil {
			ids = append(ids, it.Issue.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func idsOf(issues []*types.Issue) []string {
	ids := make([]string, 0, len(issues))
	for _, iss := range issues {
		ids = append(ids, iss.ID)
	}
	sort.Strings(ids)
	return ids
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ready ids = %v, want %v", got, want)
	}
}

// serverOpts enables cross-database resolution. There is no mapping to
// configure: the resolver discovers extpx from its own issue_prefix.
func serverOpts() issueops.ExternalResolverOptions {
	return issueops.ExternalResolverOptions{ServerMode: true}
}

func TestExternalReady_IssueBlocking(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// External provider database: one closed issue (satisfies), one open
	// issue (must NOT satisfy), one resolved issue (PR-delivered, not merged).
	e.insertIssue(t, ctx, "extpx", "ext-closed", "closed")
	e.insertIssue(t, ctx, "extpx", "ext-open", "open")
	e.insertIssue(t, ctx, "extpx", "ext-resolved", "resolved")

	// Workspace: one free issue, one satisfied, one blocked on an open target,
	// one blocked on a resolved target, one whose prefix nothing declares.
	e.insertIssue(t, ctx, "mainpx", "m-free", "open")
	e.insertIssue(t, ctx, "mainpx", "m-sat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-sat", "m-sat", "ext-closed")
	e.insertIssue(t, ctx, "mainpx", "m-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-unsat", "m-unsat", "ext-open")
	e.insertIssue(t, ctx, "mainpx", "m-resolved", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-resolved", "m-resolved", "ext-resolved")
	e.insertIssue(t, ctx, "mainpx", "m-missing", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-missing", "m-missing", "nosuch-cap0x1")

	got := e.ready(t, ctx, types.WorkFilter{}, serverOpts())
	assertIDs(t, got, []string{"m-free", "m-sat"})
}

func TestExternalReady_CountsPathBlocking(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	e.insertIssue(t, ctx, "extpx", "ext-closed", "closed")
	e.insertIssue(t, ctx, "extpx", "ext-open", "open")

	e.insertIssue(t, ctx, "mainpx", "m-free", "open")
	e.insertIssue(t, ctx, "mainpx", "m-sat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-sat", "m-sat", "ext-closed")
	e.insertIssue(t, ctx, "mainpx", "m-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-unsat", "m-unsat", "ext-open")

	got := e.readyWithCounts(t, ctx, types.WorkFilter{}, serverOpts())
	assertIDs(t, got, []string{"m-free", "m-sat"})
}

func TestExternalReady_LimitCorrectness(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// The external target is open, so it is unsatisfied. Build more free issues
	// than the limit plus one unsatisfied issue; the unsatisfied issue is
	// excluded in the WHERE (pre-LIMIT), so the page must still fill.
	const limit = 3
	for i := 0; i < limit+1; i++ {
		e.insertIssue(t, ctx, "mainpx", fmt.Sprintf("m-free-%02d", i), "open")
	}
	e.insertIssue(t, ctx, "extpx", "ext-open", "open")
	e.insertIssue(t, ctx, "mainpx", "m-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "dependencies", "d-unsat", "m-unsat", "ext-open")

	got := e.ready(t, ctx, types.WorkFilter{Limit: limit, SortPolicy: types.SortPolicyOldest}, serverOpts())
	if len(got) != limit {
		t.Fatalf("ready len = %d, want %d (page must fill): %v", len(got), limit, got)
	}
	for _, id := range got {
		if id == "m-unsat" {
			t.Fatalf("m-unsat must be excluded, got %v", got)
		}
	}
}

func TestExternalReady_WispPlainPathExcluded(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// A free wisp is ready; a wisp with an unsatisfied external dep on the
	// plain path (Limit<=0) must be excluded via wisp_dependencies.
	e.insertIssue(t, ctx, "extpx", "ext-open", "open")
	e.insertWisp(t, ctx, "mainpx", "w-free", "open")
	e.insertWisp(t, ctx, "mainpx", "w-unsat", "open")
	e.insertExternalDep(t, ctx, "mainpx", "wisp_dependencies", "wd-unsat", "w-unsat", "ext-open")

	got := e.ready(t, ctx, types.WorkFilter{}, serverOpts())
	// w-free must be present, w-unsat absent.
	hasFree, hasUnsat := false, false
	for _, id := range got {
		switch id {
		case "w-free":
			hasFree = true
		case "w-unsat":
			hasUnsat = true
		}
	}
	if !hasFree {
		t.Fatalf("expected w-free in ready, got %v", got)
	}
	if hasUnsat {
		t.Fatalf("w-unsat must be excluded on the plain wisp path, got %v", got)
	}
}

func TestExternalReady_EmptyShortCircuit(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	e := newDualEngine(t)
	ctx := t.Context()

	// No external deps at all: the resolver must never fire (DiagSink stays
	// silent) and the free issue is ready.
	e.insertIssue(t, ctx, "mainpx", "m-free", "open")

	sinkCalled := false
	opts := serverOpts()
	opts.DiagSink = func([]issueops.ExternalDiag) { sinkCalled = true }
	got := e.ready(t, ctx, types.WorkFilter{}, opts)
	assertIDs(t, got, []string{"m-free"})
	if sinkCalled {
		t.Fatal("DiagSink must not fire when there are no external deps")
	}
}
