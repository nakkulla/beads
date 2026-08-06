package issueops

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestIsCrossPrefixTarget_LongestMatchClassification covers the classification
// rule that replaced the raw first-hyphen ExtractPrefix comparison: with a
// local prefix of "team" and a discovered prefix of "team-alpha", an id under
// the longer prefix is foreign even though its first hyphen segment matches.
func TestIsCrossPrefixTarget_LongestMatchClassification(t *testing.T) {
	t.Parallel()

	t.Run("longer discovered prefix wins", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "team_db", "team", []rig{{db: "team_alpha", prefix: "team-alpha"}})

		if !IsCrossPrefixTarget(context.Background(), tx, "team-xyz123", "team-alpha-abc123", true) {
			t.Fatal("team-alpha-abc123 must classify as cross-prefix relative to team-xyz123")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("query expectations not met: %v", err)
		}
	})

	t.Run("same longer prefix on both sides stays local", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "team_db", "team", []rig{{db: "team_alpha", prefix: "team-alpha"}})

		if IsCrossPrefixTarget(context.Background(), tx, "team-alpha-src111", "team-alpha-abc123", true) {
			t.Fatal("two ids under the same discovered prefix must classify as local to each other")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("query expectations not met: %v", err)
		}
	})

	t.Run("plain same-prefix pair skips discovery", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		// No hyphen past the first segment on either side, so no longer prefix
		// can apply and discovery must not run at all.
		if IsCrossPrefixTarget(context.Background(), tx, "team-src111", "team-abc123", true) {
			t.Fatal("team-abc123 must classify as local")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("discovery must be skipped for a plain local id: %v", err)
		}
	})

	t.Run("differing first segment is cross without discovery", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		if !IsCrossPrefixTarget(context.Background(), tx, "team-src111", "dotfiles-1tif", true) {
			t.Fatal("dotfiles-1tif must classify as cross-prefix")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("discovery must not run when the first segments already differ: %v", err)
		}
	})

	t.Run("non-server mode uses the first-segment comparison", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		if IsCrossPrefixTarget(context.Background(), tx, "team-src111", "team-alpha-abc123", false) {
			t.Fatal("non-server mode must fall back to the first-segment comparison")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("discovery must not run outside server mode: %v", err)
		}
	})
}

func TestValidateExternalDepTarget(t *testing.T) {
	t.Parallel()

	t.Run("existing target accepted", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
		mock.ExpectQuery("SELECT 1 FROM .dotfiles..issues WHERE id = ?").WithArgs("dotfiles-1tif").
			WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

		if err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", true); err != nil {
			t.Fatalf("ValidateExternalDepTarget: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("query expectations not met: %v", err)
		}
	})

	t.Run("missing target rejected", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
		mock.ExpectQuery("SELECT 1 FROM .dotfiles..issues WHERE id = ?").WithArgs("dotfiles-nope99").
			WillReturnRows(sqlmock.NewRows([]string{"1"}))

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-nope99", true)
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v, want a not-found rejection", err)
		}
	})

	t.Run("unknown prefix rejected", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})

		err := ValidateExternalDepTarget(context.Background(), tx, "nosuch-abc123", true)
		if err == nil || !strings.Contains(err.Error(), "unknown prefix") {
			t.Fatalf("err = %v, want an unknown-prefix rejection", err)
		}
	})

	t.Run("ambiguous prefix rejected", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "beads", "beads", []rig{
			{db: "dotfiles_a", prefix: "dotfiles"},
			{db: "dotfiles_b", prefix: "dotfiles"},
		})

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", true)
		if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
			t.Fatalf("err = %v, want an ambiguous-prefix rejection", err)
		}
	})

	t.Run("non-server mode rejected without any query", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", false)
		if err == nil || !strings.Contains(err.Error(), "shared-server mode") {
			t.Fatalf("err = %v, want a server-mode rejection", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("no query may run outside server mode: %v", err)
		}
	})
}
