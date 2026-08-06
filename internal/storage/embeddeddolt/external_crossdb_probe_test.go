//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// TestExternalCrossDBQualifiedSelectInTx proves the resolver's design premise:
// a database-qualified SELECT against a SECOND database resolves from within
// the same transaction/connection the ready path uses on the embedded engine.
// If this fails, the resolver must run outside the tx on a separate connection.
func TestExternalCrossDBQualifiedSelectInTx(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	ctx := t.Context()
	dir := t.TempDir()

	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dir, "", "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()

	if _, err := db.ExecContext(ctx, "CREATE DATABASE mainpx"); err != nil {
		t.Fatalf("CREATE DATABASE mainpx: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE `mainpx`.issues (id VARCHAR(255) PRIMARY KEY, status VARCHAR(32) NOT NULL)"); err != nil {
		t.Fatalf("create primary issues: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE DATABASE extpx"); err != nil {
		t.Fatalf("CREATE DATABASE extpx: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE `extpx`.issues (id VARCHAR(255) PRIMARY KEY, status VARCHAR(32) NOT NULL)"); err != nil {
		t.Fatalf("create ext issues: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO `extpx`.issues (id, status) VALUES ('e-1','closed'),('e-2','open')"); err != nil {
		t.Fatalf("insert ext issues: %v", err)
	}
	if _, err := db.ExecContext(ctx, "USE `mainpx`"); err != nil {
		t.Fatalf("USE mainpx: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		"SELECT id FROM `extpx`.issues WHERE status = 'closed' AND id IN (?, ?)",
		"e-1", "e-2")
	if err != nil {
		t.Fatalf("cross-db query in tx: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	// Only e-1 is closed; e-2 is still open.
	if len(got) != 1 || got[0] != "e-1" {
		t.Fatalf("cross-db in-tx query returned %v, want [e-1]", got)
	}
}
