package issueops

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// serverOpts are uncached resolver options in shared-server mode: each call
// discovers afresh, which is what makes the query expectations below exact.
func serverOpts() ExternalResolverOptions {
	return ExternalResolverOptions{ServerMode: true}
}

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

		if !IsCrossPrefixTarget(context.Background(), tx, "team-xyz123", "team-alpha-abc123", serverOpts()) {
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

		if IsCrossPrefixTarget(context.Background(), tx, "team-alpha-src111", "team-alpha-abc123", serverOpts()) {
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
		if IsCrossPrefixTarget(context.Background(), tx, "team-src111", "team-abc123", serverOpts()) {
			t.Fatal("team-abc123 must classify as local")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("discovery must be skipped for a plain local id: %v", err)
		}
	})

	t.Run("differing first segment is cross without discovery", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		if !IsCrossPrefixTarget(context.Background(), tx, "team-src111", "dotfiles-1tif", serverOpts()) {
			t.Fatal("dotfiles-1tif must classify as cross-prefix")
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("discovery must not run when the first segments already differ: %v", err)
		}
	})

	t.Run("non-server mode uses the first-segment comparison", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		if IsCrossPrefixTarget(context.Background(), tx, "team-src111", "team-alpha-abc123", ExternalResolverOptions{}) {
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

		if err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", serverOpts()); err != nil {
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

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-nope99", serverOpts())
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v, want a not-found rejection", err)
		}
	})

	t.Run("unknown prefix rejected", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)
		expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})

		err := ValidateExternalDepTarget(context.Background(), tx, "nosuch-abc123", serverOpts())
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

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", serverOpts())
		if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
			t.Fatalf("err = %v, want an ambiguous-prefix rejection", err)
		}
	})

	t.Run("non-server mode rejected without any query", func(t *testing.T) {
		t.Parallel()
		mock, tx := newResolverMock(t)

		err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", ExternalResolverOptions{})
		if err == nil || !strings.Contains(err.Error(), "shared-server mode") {
			t.Fatalf("err = %v, want a server-mode rejection", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("no query may run outside server mode: %v", err)
		}
	})
}

// TestPrefixDiscoveryCachedAcrossCalls locks the "once per command execution"
// requirement: options built by NewExternalResolverOptions share one discovery
// pass, so bd ready's repeated resolution and a dep add's classify+validate
// pair do not re-enumerate the server's databases.
func TestPrefixDiscoveryCachedAcrossCalls(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	// Exactly one discovery sequence is queued. A second pass would fail the
	// expectations, which is the assertion.
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	mock.ExpectQuery("SELECT 1 FROM .dotfiles..issues WHERE id = ?").WithArgs("dotfiles-1tif").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	opts := NewExternalResolverOptions(true, nil)
	if err := ValidateExternalDepTarget(context.Background(), tx, "dotfiles-1tif", opts); err != nil {
		t.Fatalf("first validate: %v", err)
	}
	// Second use of the same options must reuse the cached map: no further
	// discovery queries are queued, and the unknown prefix resolves from cache.
	err := ValidateExternalDepTarget(context.Background(), tx, "nosuch-abc123", opts)
	if err == nil || !strings.Contains(err.Error(), "unknown prefix") {
		t.Fatalf("second validate err = %v, want an unknown-prefix rejection from the cached map", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("discovery must run exactly once for cached options: %v", err)
	}
}

// TestPrefixDiscoveryUncachedOptionsRediscover documents the counterpart: a
// zero-value options struct owns no cache, so every call discovers again.
func TestPrefixDiscoveryUncachedOptionsRediscover(t *testing.T) {
	t.Parallel()
	mock, tx := newResolverMock(t)
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})
	expectDiscovery(mock, "beads", "beads", []rig{{db: "dotfiles", prefix: "dotfiles"}})

	for i := 0; i < 2; i++ {
		if err := ValidateExternalDepTarget(context.Background(), tx, "nosuch-abc123", serverOpts()); err == nil {
			t.Fatalf("call %d: want an unknown-prefix rejection", i)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("uncached options must discover per call: %v", err)
	}
}
