package issueops

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	currentDBQueryRegex    = `SELECT DATABASE\(\)`
	showDatabasesRegex     = `SHOW DATABASES`
	selfPrefixQueryRegex   = "SELECT value FROM config WHERE"
	closedIssuesQueryRegex = `SELECT id FROM .*\.issues WHERE status = 'closed'`
	refCollectRegex        = `SELECT DISTINCT depends_on_external FROM`
)

func newResolverMock(t *testing.T) (sqlmock.Sqlmock, DBTX) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return mock, db
}

// rig is one database on the shared server and the issue prefix its config
// table declares.
type rig struct{ db, prefix string }

// expectDiscovery queues the exact query sequence discoverPrefixMap issues:
// current database, database list, the local issue_prefix, then one
// config.issue_prefix probe per foreign database in sorted name order.
func expectDiscovery(mock sqlmock.Sqlmock, selfDB, selfPrefix string, others []rig) {
	mock.ExpectQuery(currentDBQueryRegex).
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow(selfDB))

	names := []string{selfDB}
	for _, o := range others {
		names = append(names, o.db)
	}
	sort.Strings(names)
	dbRows := sqlmock.NewRows([]string{"Database"})
	for _, n := range names {
		dbRows.AddRow(n)
	}
	mock.ExpectQuery(showDatabasesRegex).WillReturnRows(dbRows)

	mock.ExpectQuery(selfPrefixQueryRegex).WithArgs("issue_prefix").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(selfPrefix))

	byName := make(map[string]string, len(others))
	for _, o := range others {
		byName[o.db] = o.prefix
	}
	for _, n := range names {
		if n == selfDB {
			continue
		}
		mock.ExpectQuery("SELECT value FROM ." + n + "..config").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(byName[n]))
	}
}

func TestResolveExternalRefs_NonServerMode(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"dotfiles-1tif", "UI-kfl4"}

	blocking, diags := resolveExternalRefs(context.Background(), tx, refs, ExternalResolverOptions{ServerMode: false})
	want := []string{"UI-kfl4", "dotfiles-1tif"}
	if !reflect.DeepEqual(blocking, want) {
		t.Fatalf("blocking = %v, want %v", blocking, want)
	}
	if len(diags) != 2 {
		t.Fatalf("diags = %v, want 2 entries", diags)
	}
	for _, d := range diags {
		if d.Reason != "server mode required" {
			t.Errorf("diag reason = %q, want server mode required", d.Reason)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected queries in non-server mode: %v", err)
	}
}

func TestResolveExternalRefs_SatisfiedWhenTargetClosed(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	mock.ExpectQuery(closedIssuesQueryRegex).WithArgs("dotfiles-1tif").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("dotfiles-1tif"))

	blocking, diags := resolveExternalRefs(context.Background(), tx,
		[]string{"dotfiles-1tif"}, ExternalResolverOptions{ServerMode: true})
	if len(blocking) != 0 {
		t.Fatalf("blocking = %v, want empty (target closed)", blocking)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestResolveExternalRefs_UnsatisfiedWhenTargetNotClosed(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	// Open, resolved, or absent all look the same here: not in the closed set.
	mock.ExpectQuery(closedIssuesQueryRegex).WithArgs("dotfiles-1tif").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	blocking, diags := resolveExternalRefs(context.Background(), tx,
		[]string{"dotfiles-1tif"}, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, []string{"dotfiles-1tif"}) {
		t.Fatalf("blocking = %v, want the ref", blocking)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none (unsatisfied is not unresolvable)", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestResolveExternalRefs_UnknownPrefix(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})

	blocking, diags := resolveExternalRefs(context.Background(), tx,
		[]string{"nosuch-abc123"}, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, []string{"nosuch-abc123"}) {
		t.Fatalf("blocking = %v, want the ref (fail-closed)", blocking)
	}
	if len(diags) != 1 || diags[0].Reason != "unknown prefix" || diags[0].Prefix != "nosuch" {
		t.Fatalf("diags = %+v, want a single unknown-prefix diag for nosuch", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("must not query any database for an unknown prefix: %v", err)
	}
}

func TestResolveExternalRefs_AmbiguousPrefix(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{
		{db: "dotfiles_a", prefix: "dotfiles"},
		{db: "dotfiles_b", prefix: "dotfiles"},
	})

	blocking, diags := resolveExternalRefs(context.Background(), tx,
		[]string{"dotfiles-1tif"}, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, []string{"dotfiles-1tif"}) {
		t.Fatalf("blocking = %v, want the ref (fail-closed)", blocking)
	}
	if len(diags) != 1 || diags[0].Reason != "ambiguous prefix (dotfiles_a, dotfiles_b)" {
		t.Fatalf("diags = %+v, want an ambiguous-prefix diag naming both databases", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("must not query either database for an ambiguous prefix: %v", err)
	}
}

func TestResolveExternalRefs_DiscoveryFailureFailsClosed(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	mock.ExpectQuery(currentDBQueryRegex).
		WillReturnRows(sqlmock.NewRows([]string{"DATABASE()"}).AddRow("beads"))
	mock.ExpectQuery(showDatabasesRegex).WillReturnError(errors.New("access denied"))

	refs := []string{"dotfiles-1tif"}
	blocking, diags := resolveExternalRefs(context.Background(), tx, refs, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, refs) {
		t.Fatalf("blocking = %v, want %v (fail-closed on discovery error)", blocking, refs)
	}
	if len(diags) != 1 || diags[0].Prefix != "dotfiles" {
		t.Fatalf("diags = %+v, want one diag attributed to dotfiles", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestResolveExternalRefs_TargetQueryErrorFailsClosed(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	mock.ExpectQuery(closedIssuesQueryRegex).WillReturnError(errors.New("unknown database 'dotfiles'"))

	refs := []string{"dotfiles-1tif"}
	blocking, diags := resolveExternalRefs(context.Background(), tx, refs, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, refs) {
		t.Fatalf("blocking = %v, want %v (fail-closed on query error)", blocking, refs)
	}
	if len(diags) != 1 || diags[0].Prefix != "dotfiles" {
		t.Fatalf("diags = %+v, want one diag attributed to dotfiles", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestResolveExternalRefs_OneQueryPerDatabase(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	// Both refs belong to the same database: a single IN query covers them.
	mock.ExpectQuery(closedIssuesQueryRegex).WithArgs("dotfiles-1tif", "dotfiles-2aaa").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("dotfiles-1tif"))

	blocking, _ := resolveExternalRefs(context.Background(), tx,
		[]string{"dotfiles-1tif", "dotfiles-2aaa"}, ExternalResolverOptions{ServerMode: true})
	if !reflect.DeepEqual(blocking, []string{"dotfiles-2aaa"}) {
		t.Fatalf("blocking = %v, want only the still-open ref", blocking)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly one satisfaction query: %v", err)
	}
}

func TestResolveExternalRefs_LongestPrefixWins(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{
		{db: "team_alpha", prefix: "team-alpha"},
		{db: "team_root", prefix: "team"},
	})
	// team-alpha-abc123 must route to team_alpha, not to the shorter "team".
	mock.ExpectQuery("SELECT id FROM .team_alpha..issues").WithArgs("team-alpha-abc123").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("team-alpha-abc123"))

	blocking, diags := resolveExternalRefs(context.Background(), tx,
		[]string{"team-alpha-abc123"}, ExternalResolverOptions{ServerMode: true})
	if len(blocking) != 0 {
		t.Fatalf("blocking = %v, want empty (closed in team_alpha)", blocking)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestCollectBlockingExternalRefs_UnionDedup(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).
			AddRow("dotfiles-1tif").AddRow("UI-kfl4"))
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).
			AddRow("dotfiles-1tif").AddRow("gt-9zz9"))

	refs, err := collectBlockingExternalRefs(context.Background(), tx)
	if err != nil {
		t.Fatalf("collectBlockingExternalRefs: %v", err)
	}
	want := []string{"UI-kfl4", "dotfiles-1tif", "gt-9zz9"}
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs = %v, want %v (deduped+sorted)", refs, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}

func TestResolveReadyExternalBlocks_EmptyShortCircuit(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	// Both collection queries return no rows; the resolver must NOT run
	// discovery or any satisfaction query (zero overhead / no cross-db query).
	mock.ExpectQuery(refCollectRegex).WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))
	mock.ExpectQuery(refCollectRegex).WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))

	var sinkCalled bool
	blocking, err := ResolveReadyExternalBlocksInTx(context.Background(), tx,
		ExternalResolverOptions{ServerMode: true, DiagSink: func([]ExternalDiag) { sinkCalled = true }})
	if err != nil {
		t.Fatalf("ResolveReadyExternalBlocksInTx: %v", err)
	}
	if len(blocking) != 0 {
		t.Fatalf("blocking = %v, want empty", blocking)
	}
	if sinkCalled {
		t.Fatal("DiagSink must not fire when there are no external refs")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("must issue only the two collection queries: %v", err)
	}
}

func TestResolveReadyExternalBlocks_DiagSinkFires(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).AddRow("dotfiles-1tif"))
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))

	var got []ExternalDiag
	blocking, err := ResolveReadyExternalBlocksInTx(context.Background(), tx,
		ExternalResolverOptions{ServerMode: false, DiagSink: func(d []ExternalDiag) { got = d }})
	if err != nil {
		t.Fatalf("ResolveReadyExternalBlocksInTx: %v", err)
	}
	if !reflect.DeepEqual(blocking, []string{"dotfiles-1tif"}) {
		t.Fatalf("blocking = %v", blocking)
	}
	if len(got) != 1 || got[0].Reason != "server mode required" {
		t.Fatalf("sink diags = %+v, want one server-mode-required diag", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}
