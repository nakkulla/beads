package issueops

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
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
