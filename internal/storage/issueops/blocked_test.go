package issueops

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
	"github.com/steveyegge/beads/internal/types"
)

const parentDepQueryRegex = `FROM %s WHERE issue_id IN \(`

func newBlockedMock(t *testing.T) (sqlmock.Sqlmock, DBTX) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return mock, db
}

func idArgs(ids []string) []driver.Value {
	args := make([]driver.Value, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// The seam is the query shape, not the returned map: a per-ID implementation
// also returns every input, so only the IN-batch split can fail it.
func TestLoadParentIDsForChildrenInTx_BatchesByQueryBatchSize(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	childIDs := make([]string, 0, queryBatchSize+1)
	for i := 0; i < queryBatchSize+1; i++ {
		childIDs = append(childIDs, fmt.Sprintf("bd-child-%03d", i))
	}

	first := sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
		AddRow(childIDs[0], "bd-epic-a")
	second := sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
		AddRow(childIDs[queryBatchSize], "bd-epic-b")

	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs(idArgs(childIDs[:queryBatchSize])...).
		WillReturnRows(first)
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs(idArgs(childIDs[queryBatchSize:])...).
		WillReturnRows(second)

	got, err := loadParentIDsForChildrenInTx(context.Background(), tx, []string{"dependencies"}, childIDs)
	if err != nil {
		t.Fatalf("loadParentIDsForChildrenInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expected exactly 2 IN queries with split args: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d parents, want 2: %v", len(got), got)
	}
	if got[childIDs[0]] != "bd-epic-a" || got[childIDs[queryBatchSize]] != "bd-epic-b" {
		t.Errorf("rows from both batches must survive, got %v", got)
	}
}

func TestLoadParentIDsForChildrenInTx_ToleratesMissingWispTable(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	childIDs := []string{"bd-child-1"}
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs(idArgs(childIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child-1", "bd-epic"))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs(idArgs(childIDs)...).
		WillReturnError(errors.New("Error 1146: Table 'beads.wisp_dependencies' doesn't exist"))

	got, err := loadParentIDsForChildrenInTx(context.Background(), tx,
		[]string{"dependencies", "wisp_dependencies"}, childIDs)
	if err != nil {
		t.Fatalf("missing optional table must be tolerated, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
	if got["bd-child-1"] != "bd-epic" {
		t.Errorf("got %v, want bd-child-1 -> bd-epic", got)
	}
}

// depTables order decides which table wins for a child present in both; the
// batch rewrite must not make that resolution order-independent.
func TestLoadParentIDsForChildrenInTx_DepTableOrderPrecedence(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	childIDs := []string{"bd-child-1"}
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs(idArgs(childIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child-1", "bd-epic-stored"))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs(idArgs(childIDs)...).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child-1", "bd-epic-wisp"))

	got, err := loadParentIDsForChildrenInTx(context.Background(), tx,
		[]string{"dependencies", "wisp_dependencies"}, childIDs)
	if err != nil {
		t.Fatalf("loadParentIDsForChildrenInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
	if got["bd-child-1"] != "bd-epic-wisp" {
		t.Errorf("later depTable must win, got %q", got["bd-child-1"])
	}
}

func tableMissingErr(table string) error {
	return fmt.Errorf("Error 1146: Table 'beads.%s' doesn't exist", table)
}

// expectBlockedPreamble stages GetBlockedIssuesInTx up to (but excluding) the
// parent-child lookup: the is_blocked scan, the blocking-dep scan and, when
// blockerID is non-empty, the blocker status probe.
func expectBlockedPreamble(mock sqlmock.Sqlmock, blockedID, blockerID string) {
	mock.ExpectQuery(`SELECT id FROM issues WHERE is_blocked = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(blockedID))
	mock.ExpectQuery(`SELECT id FROM wisps WHERE is_blocked = 1`).
		WillReturnError(tableMissingErr("wisps"))

	depRows := sqlmock.NewRows([]string{"issue_id", "depends_on_id", "type", "metadata"})
	if blockerID != "" {
		depRows.AddRow(blockedID, blockerID, "blocks", nil)
	}
	mock.ExpectQuery(`type, metadata FROM dependencies WHERE issue_id = \?`).
		WithArgs(blockedID).
		WillReturnRows(depRows)
	mock.ExpectQuery(`type, metadata FROM wisp_dependencies WHERE issue_id = \?`).
		WithArgs(blockedID).
		WillReturnError(tableMissingErr("wisp_dependencies"))

	if blockerID != "" {
		mock.ExpectQuery(`SELECT id, status FROM issues WHERE id IN \(\?\)`).
			WithArgs(blockerID).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(blockerID, "open"))
		mock.ExpectQuery(`SELECT id, status FROM wisps WHERE id IN \(\?\)`).
			WithArgs(blockerID).
			WillReturnError(tableMissingErr("wisps"))
	}
}

// expectBlockedHydration stages the display-issue fetch that closes
// GetBlockedIssuesInTx for a single surviving blocked ID.
func expectBlockedHydration(mock sqlmock.Sqlmock, blockedID string) {
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM issues WHERE id IN \(\?\)`).
		WithArgs(blockedID).
		WillReturnRows(issueRows().AddRow(issueRowValues(blockedID, "Blocked child")...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN \(\?\)`).
		WithArgs(blockedID).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
}

func TestGetBlockedIssuesInTx_StoredBlockedCarriesParent(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	expectBlockedPreamble(mock, "bd-child", "bd-blocker")
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-child").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child", "bd-epic"))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs("bd-child").
		WillReturnError(tableMissingErr("wisp_dependencies"))
	expectBlockedHydration(mock, "bd-child")

	got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetBlockedIssuesInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d blocked issues, want 1", len(got))
	}
	if got[0].Parent == nil {
		t.Fatal("expected Parent to be set on stored blocked issue")
	}
	if *got[0].Parent != "bd-epic" {
		t.Errorf("Parent = %q, want bd-epic", *got[0].Parent)
	}
	// The parent is a parent, not a blocker: the direct blocker stays alone.
	if len(got[0].BlockedBy) != 1 || got[0].BlockedBy[0] != "bd-blocker" {
		t.Errorf("BlockedBy = %v, want [bd-blocker]", got[0].BlockedBy)
	}
}

func TestGetBlockedIssuesInTx_TopLevelBlockedKeepsNilParent(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	expectBlockedPreamble(mock, "bd-top", "bd-blocker")
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-top").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs("bd-top").
		WillReturnError(tableMissingErr("wisp_dependencies"))
	expectBlockedHydration(mock, "bd-top")

	got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetBlockedIssuesInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d blocked issues, want 1", len(got))
	}
	if got[0].Parent != nil {
		t.Errorf("Parent = %q, want nil", *got[0].Parent)
	}
}

// Sharing one parent map between blocker inheritance and the parent field must
// not change what a child with no stored blocker inherits.
func TestGetBlockedIssuesInTx_ParentStillInheritedAsBlocker(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	expectBlockedPreamble(mock, "bd-child", "")
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-child").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child", "bd-epic"))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs("bd-child").
		WillReturnError(tableMissingErr("wisp_dependencies"))
	expectBlockedHydration(mock, "bd-child")

	got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetBlockedIssuesInTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d blocked issues, want 1", len(got))
	}
	if len(got[0].BlockedBy) != 1 || got[0].BlockedBy[0] != "bd-epic" {
		t.Errorf("BlockedBy = %v, want inherited [bd-epic]", got[0].BlockedBy)
	}
	if got[0].BlockedByCount != 1 {
		t.Errorf("BlockedByCount = %d, want 1", got[0].BlockedByCount)
	}
	if got[0].Parent == nil || *got[0].Parent != "bd-epic" {
		t.Errorf("Parent = %v, want bd-epic", got[0].Parent)
	}
}

// The parent lookup is best-effort: promoting it to a shared map must not let
// its failure take down entries that already have a stored blocker.
func TestGetBlockedIssuesInTx_ParentLookupErrorStillReturnsDirectBlocked(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	expectBlockedPreamble(mock, "bd-child", "bd-blocker")
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-child").
		WillReturnError(errors.New("parent lookup exploded"))
	expectBlockedHydration(mock, "bd-child")

	got, err := GetBlockedIssuesInTx(context.Background(), tx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("parent lookup failure must not fail the call, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d blocked issues, want 1", len(got))
	}
	if got[0].Parent != nil {
		t.Errorf("Parent = %q, want nil after lookup failure", *got[0].Parent)
	}
	if len(got[0].BlockedBy) != 1 || got[0].BlockedBy[0] != "bd-blocker" {
		t.Errorf("BlockedBy = %v, want [bd-blocker]", got[0].BlockedBy)
	}
}

// expectExternalBlockedPreamble stages CollectExternalBlockedInTx up to (but
// excluding) the parent-child lookup for a single non-stored-blocked candidate.
func expectExternalBlockedPreamble(mock sqlmock.Sqlmock, candidateID, ref string) {
	mock.ExpectQuery(`SELECT id, is_blocked FROM issues`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "is_blocked"}).AddRow(candidateID, false))
	mock.ExpectQuery(`SELECT DISTINCT issue_id, depends_on_external FROM dependencies`).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_external"}).
			AddRow(candidateID, ref))
	mock.ExpectQuery(`SELECT id, is_blocked FROM wisps`).
		WillReturnError(tableMissingErr("wisps"))
	mock.ExpectQuery(`SELECT 1 FROM wisps LIMIT 1`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`FROM issues WHERE id IN \(\?\)`).
		WithArgs(candidateID).
		WillReturnRows(issueRows().AddRow(issueRowValues(candidateID, "External candidate")...))
	mock.ExpectQuery(`SELECT issue_id, label FROM labels WHERE issue_id IN \(\?\)`).
		WithArgs(candidateID).
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}))
}

func collectExternalBlocked(t *testing.T, tx DBTX, ref string) *types.ExternalBlocked {
	t.Helper()
	out, err := CollectExternalBlockedInTx(context.Background(), tx, types.WorkFilter{},
		[]string{ref}, sqlbuild.ReadyWorkWhereInputs{})
	if err != nil {
		t.Fatalf("CollectExternalBlockedInTx: %v", err)
	}
	return out
}

func TestCollectExternalBlockedInTx_CandidateCarriesParent(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	const ref = "external:beads:cap-a"
	expectExternalBlockedPreamble(mock, "bd-child", ref)
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-child").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}).
			AddRow("bd-child", "bd-epic"))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs("bd-child").
		WillReturnError(tableMissingErr("wisp_dependencies"))

	out := collectExternalBlocked(t, tx, ref)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(out.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(out.Candidates))
	}
	if out.Candidates[0].Parent == nil {
		t.Fatal("expected Parent to be set on external candidate")
	}
	if *out.Candidates[0].Parent != "bd-epic" {
		t.Errorf("Parent = %q, want bd-epic", *out.Candidates[0].Parent)
	}
	if len(out.Candidates[0].BlockedBy) != 1 || out.Candidates[0].BlockedBy[0] != ref {
		t.Errorf("BlockedBy = %v, want [%s]", out.Candidates[0].BlockedBy, ref)
	}
}

func TestCollectExternalBlockedInTx_TopLevelCandidateKeepsNilParent(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	const ref = "external:beads:cap-a"
	expectExternalBlockedPreamble(mock, "bd-top", ref)
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-top").
		WillReturnRows(sqlmock.NewRows([]string{"issue_id", "depends_on_id"}))
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "wisp_dependencies")).
		WithArgs("bd-top").
		WillReturnError(tableMissingErr("wisp_dependencies"))

	out := collectExternalBlocked(t, tx, ref)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(out.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(out.Candidates))
	}
	if out.Candidates[0].Parent != nil {
		t.Errorf("Parent = %q, want nil", *out.Candidates[0].Parent)
	}
}

// Same best-effort contract as the stored blocked path: a failed parent lookup
// drops the parent field, never the candidate.
func TestCollectExternalBlockedInTx_ParentLookupErrorKeepsCandidate(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	const ref = "external:beads:cap-a"
	expectExternalBlockedPreamble(mock, "bd-child", ref)
	mock.ExpectQuery(fmt.Sprintf(parentDepQueryRegex, "dependencies")).
		WithArgs("bd-child").
		WillReturnError(errors.New("parent lookup exploded"))

	out := collectExternalBlocked(t, tx, ref)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sql expectations: %v", err)
	}
	if len(out.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1", len(out.Candidates))
	}
	if out.Candidates[0].Parent != nil {
		t.Errorf("Parent = %q, want nil after lookup failure", *out.Candidates[0].Parent)
	}
}

func TestLoadParentIDsForChildrenInTx_NoChildren(t *testing.T) {
	t.Parallel()
	mock, tx := newBlockedMock(t)

	got, err := loadParentIDsForChildrenInTx(context.Background(), tx,
		[]string{"dependencies", "wisp_dependencies"}, nil)
	if err != nil {
		t.Fatalf("loadParentIDsForChildrenInTx: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("empty input must issue no query: %v", err)
	}
}
