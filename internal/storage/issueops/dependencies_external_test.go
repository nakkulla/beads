package issueops

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// TestGetDependenciesWithMetadataInTxSynthesizesExternalRefs verifies that an
// external:<project>:<capability> dependency target — which has no row in the
// issues/wisps tables — is surfaced as a synthesized entry rather than silently
// dropped, so the listed edges stay consistent with the dependency counts.
func TestGetDependenciesWithMetadataInTxSynthesizesExternalRefs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	const localID = "loc-blocker"
	const extRef = "external:beads:cap-a"

	mock.ExpectBegin()
	// One local blocking dep + one external blocking ref in the dependencies
	// table; wisp_dependencies has none.
	expectDependencies(mock, "root", []dependencyRow{
		{id: localID, depType: string(types.DepBlocks)},
		{id: extRef, depType: string(types.DepBlocks)},
	})
	// GetIssuesByIDsInTx: wisps table empty, then the issues batch returns only
	// the local blocker — the external ref has no backing row.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1 FROM wisps LIMIT 1")).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT "+IssueSelectColumns+" FROM issues WHERE id IN (?,?)")).
		WithArgs(localID, extRef).
		WillReturnRows(issueRows().AddRow(issueRowValues(localID, "Local Blocker")...))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?,?) ORDER BY issue_id, label")).
		WithArgs(localID, extRef).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
	mock.ExpectRollback()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	got, err := GetDependenciesWithMetadataInTx(context.Background(), tx, "root")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("GetDependenciesWithMetadataInTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(deps) = %d, want 2 (local + external); external must not be dropped", len(got))
	}

	var localEntry, extEntry *types.IssueWithDependencyMetadata
	for _, d := range got {
		switch d.ID {
		case extRef:
			extEntry = d
		case localID:
			localEntry = d
		}
	}
	if localEntry == nil {
		t.Fatalf("local dependency %s missing from results: %+v", localID, got)
	}
	if extEntry == nil {
		t.Fatalf("external dependency %s dropped from results: %+v", extRef, got)
	}
	if extEntry.ID != extRef {
		t.Errorf("external entry ID = %q, want %q", extEntry.ID, extRef)
	}
	if extEntry.DependencyType != types.DepBlocks {
		t.Errorf("external entry dep type = %q, want %q", extEntry.DependencyType, types.DepBlocks)
	}
	// The synthesized entry carries only the ref; no fabricated status/title/priority.
	if extEntry.Title != "" || extEntry.Status != "" || extEntry.Priority != 0 {
		t.Errorf("external entry has fabricated fields: title=%q status=%q priority=%d",
			extEntry.Title, extEntry.Status, extEntry.Priority)
	}
}

// TestNewExternalDepEntry documents the synthesized-entry field contract.
func TestNewExternalDepEntry(t *testing.T) {
	const ref = "external:other-project:cross-cap"
	e := NewExternalDepEntry(ref, string(types.DepBlocks))
	if e.ID != ref {
		t.Errorf("ID = %q, want %q", e.ID, ref)
	}
	if e.DependencyType != types.DepBlocks {
		t.Errorf("DependencyType = %q, want %q", e.DependencyType, types.DepBlocks)
	}
	if e.Title != "" || e.Status != "" || e.Priority != 0 || e.IssueType != "" {
		t.Errorf("expected zero-valued display fields, got title=%q status=%q priority=%d type=%q",
			e.Title, e.Status, e.Priority, e.IssueType)
	}
}
