package issueops

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const providedLabelsQueryRegex = `SELECT DISTINCT l\.label FROM`
const refCollectRegex = `SELECT DISTINCT depends_on_external FROM`

func newResolverMock(t *testing.T) (sqlmock.Sqlmock, DBTX) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return mock, db
}

func TestResolveExternalRefs_NonServerMode(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a", "external:gt:cap-b"}

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs, ExternalResolverOptions{ServerMode: false})
	wantUnsat := []string{"external:beads:cap-a", "external:gt:cap-b"}
	if !reflect.DeepEqual(unsatisfied, wantUnsat) {
		t.Fatalf("unsatisfied = %v, want %v", unsatisfied, wantUnsat)
	}
	// One diag per project, reason "server mode required".
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

func TestResolveExternalRefs_MissingMapping(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a"}

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{}})
	if !reflect.DeepEqual(unsatisfied, refs) {
		t.Fatalf("unsatisfied = %v, want %v", unsatisfied, refs)
	}
	if len(diags) != 1 || diags[0].Reason != "no external_databases mapping" || diags[0].Project != "beads" {
		t.Fatalf("diags = %+v, want single no-mapping diag for beads", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected queries: %v", err)
	}
}

func TestResolveExternalRefs_InvalidIdentifier(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a"}

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "bad name;DROP"}})
	if !reflect.DeepEqual(unsatisfied, refs) {
		t.Fatalf("unsatisfied = %v, want %v", unsatisfied, refs)
	}
	if len(diags) != 1 || diags[0].Project != "beads" {
		t.Fatalf("diags = %+v, want single diag for beads", diags)
	}
	if got := diags[0].Reason; got == "" || got[:7] != "invalid" {
		t.Fatalf("diag reason = %q, want invalid-identifier reason", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected queries (must not query invalid db): %v", err)
	}
}

func TestResolveExternalRefs_MalformedRef(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	// Missing capability, and a fully malformed ref.
	refs := []string{"external:beads", "garbage"}

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "beadsdb"}})
	if len(unsatisfied) != 2 {
		t.Fatalf("unsatisfied = %v, want both malformed refs blocking", unsatisfied)
	}
	for _, d := range diags {
		if d.Reason != "malformed external reference" {
			t.Errorf("diag reason = %q, want malformed external reference", d.Reason)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected queries for malformed refs: %v", err)
	}
}

func TestResolveExternalRefs_QueryError(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a"}
	mock.ExpectQuery(providedLabelsQueryRegex).WillReturnError(errors.New("unknown database 'beadsdb'"))

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "beadsdb"}})
	if !reflect.DeepEqual(unsatisfied, refs) {
		t.Fatalf("unsatisfied = %v, want %v (fail-closed on query error)", unsatisfied, refs)
	}
	if len(diags) != 1 || diags[0].Project != "beads" {
		t.Fatalf("diags = %+v, want single diag for beads", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectation not met: %v", err)
	}
}

func TestResolveExternalRefs_Satisfied(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a"}
	mock.ExpectQuery(providedLabelsQueryRegex).
		WithArgs("provides:cap-a").
		WillReturnRows(sqlmock.NewRows([]string{"label"}).AddRow("provides:cap-a"))

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "beadsdb"}})
	if len(unsatisfied) != 0 {
		t.Fatalf("unsatisfied = %v, want empty (satisfied)", unsatisfied)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none (satisfied is not unresolvable)", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectation not met: %v", err)
	}
}

func TestResolveExternalRefs_UnsatisfiedLabelAbsent(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a"}
	// The label is absent (or only present on a non-closed issue, which the
	// status='closed' filter excludes): the query returns no rows.
	mock.ExpectQuery(providedLabelsQueryRegex).
		WithArgs("provides:cap-a").
		WillReturnRows(sqlmock.NewRows([]string{"label"}))

	unsatisfied, diags := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "beadsdb"}})
	if !reflect.DeepEqual(unsatisfied, refs) {
		t.Fatalf("unsatisfied = %v, want %v", unsatisfied, refs)
	}
	if len(diags) != 0 {
		t.Fatalf("diags = %v, want none (unsatisfied is not unresolvable)", diags)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectation not met: %v", err)
	}
}

func TestResolveExternalRefs_MixedCapabilitiesOneQueryPerProject(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	refs := []string{"external:beads:cap-a", "external:beads:cap-b"}
	// One query for the whole project; only cap-a is provided by a closed issue.
	mock.ExpectQuery(providedLabelsQueryRegex).
		WillReturnRows(sqlmock.NewRows([]string{"label"}).AddRow("provides:cap-a"))

	unsatisfied, _ := resolveExternalRefs(context.Background(), tx, refs,
		ExternalResolverOptions{ServerMode: true, Databases: map[string]string{"beads": "beadsdb"}})
	want := []string{"external:beads:cap-b"}
	if !reflect.DeepEqual(unsatisfied, want) {
		t.Fatalf("unsatisfied = %v, want %v", unsatisfied, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly one project query: %v", err)
	}
}

func TestCollectBlockingExternalRefs_UnionDedup(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).
			AddRow("external:beads:cap-a").AddRow("external:gt:cap-b"))
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).
			AddRow("external:beads:cap-a").AddRow("external:wisp:cap-c"))

	refs, err := collectBlockingExternalRefs(context.Background(), tx)
	if err != nil {
		t.Fatalf("collectBlockingExternalRefs: %v", err)
	}
	want := []string{"external:beads:cap-a", "external:gt:cap-b", "external:wisp:cap-c"}
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
	// Both collection queries return no rows; the resolver must NOT run any
	// per-project satisfaction query (zero overhead / no cross-db query).
	mock.ExpectQuery(refCollectRegex).WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))
	mock.ExpectQuery(refCollectRegex).WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))

	var sinkCalled bool
	unsatisfied, err := ResolveReadyExternalBlocksInTx(context.Background(), tx,
		ExternalResolverOptions{ServerMode: true, DiagSink: func([]ExternalDiag) { sinkCalled = true }})
	if err != nil {
		t.Fatalf("ResolveReadyExternalBlocksInTx: %v", err)
	}
	if len(unsatisfied) != 0 {
		t.Fatalf("unsatisfied = %v, want empty", unsatisfied)
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
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}).AddRow("external:beads:cap-a"))
	mock.ExpectQuery(refCollectRegex).
		WillReturnRows(sqlmock.NewRows([]string{"depends_on_external"}))

	var got []ExternalDiag
	unsatisfied, err := ResolveReadyExternalBlocksInTx(context.Background(), tx,
		ExternalResolverOptions{ServerMode: false, DiagSink: func(d []ExternalDiag) { got = d }})
	if err != nil {
		t.Fatalf("ResolveReadyExternalBlocksInTx: %v", err)
	}
	if !reflect.DeepEqual(unsatisfied, []string{"external:beads:cap-a"}) {
		t.Fatalf("unsatisfied = %v", unsatisfied)
	}
	if len(got) != 1 || got[0].Reason != "server mode required" {
		t.Fatalf("sink diags = %+v, want one server-mode-required diag", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query expectations not met: %v", err)
	}
}
