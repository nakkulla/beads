package dolt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// seedLocalStore writes the on-disk shape the embedded/CLI layout produces:
// <dataDir>/<database>/.dolt/ with some engine-private state inside it.
func seedLocalStore(t *testing.T, dataDir, database string) string {
	t.Helper()
	storePath := filepath.Join(dataDir, database)
	privateDir := filepath.Join(storePath, ".dolt", "noms")
	if err := os.MkdirAll(privateDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateDir, "manifest"), []byte("manifest-stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storePath, ".dolt", "config.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return storePath
}

func TestLocalStoreRecoverer_LocateFindsNamedDatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "dolt")
	want := seedLocalStore(t, dataDir, "beads")

	loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
	if err != nil {
		t.Fatalf("LocateLocalStore: %v", err)
	}
	if !loc.Found {
		t.Fatalf("Found = false, want true (reason %q)", loc.Reason)
	}
	if loc.Path != want {
		t.Errorf("Path = %q, want %q", loc.Path, want)
	}
	if loc.Database != "beads" {
		t.Errorf("Database = %q, want %q", loc.Database, "beads")
	}
	if loc.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", loc.DataDir, dataDir)
	}
}

func TestLocalStoreRecoverer_LocateNegatives(t *testing.T) {
	t.Run("data dir missing", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "dolt")
		loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
		if err != nil {
			t.Fatalf("LocateLocalStore: %v", err)
		}
		if loc.Found {
			t.Fatalf("Found = true, want false for a missing data dir")
		}
		if loc.Reason == "" {
			t.Error("Reason is empty, want an explanation")
		}
	})

	t.Run("database dir missing", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "dolt")
		seedLocalStore(t, dataDir, "other")
		loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
		if err != nil {
			t.Fatalf("LocateLocalStore: %v", err)
		}
		if loc.Found {
			t.Fatalf("Found = true, want false — %q must not be mistaken for %q", "other", "beads")
		}
	})

	t.Run("directory without engine state is not a store", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "dolt")
		if err := os.MkdirAll(filepath.Join(dataDir, "beads"), 0o750); err != nil {
			t.Fatal(err)
		}
		loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
		if err != nil {
			t.Fatalf("LocateLocalStore: %v", err)
		}
		if loc.Found {
			t.Fatal("Found = true for a plain directory with no engine state, want false")
		}
	})

	t.Run("empty database name is rejected", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "dolt")
		if _, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "   "); err == nil {
			t.Fatal("expected an error for an empty database name")
		}
	})
}

func TestLocalStoreRecoverer_QuarantineMovesStore(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".beads", "dolt")
	storePath := seedLocalStore(t, dataDir, "beads")
	dest := filepath.Join(root, ".beads-quarantine", "beads-20260805T000000Z")

	loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
	if err != nil {
		t.Fatalf("LocateLocalStore: %v", err)
	}
	if err := LocalStoreRecoverer().QuarantineLocalStore(loc, dest); err != nil {
		t.Fatalf("QuarantineLocalStore: %v", err)
	}

	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Errorf("source %q still exists after quarantine (stat err %v)", storePath, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".dolt", "noms", "manifest")); err != nil {
		t.Errorf("quarantined store is missing its content at %q: %v", dest, err)
	}
	// The data dir itself must survive so the re-clone can populate it.
	if _, err := os.Stat(dataDir); err != nil {
		t.Errorf("data dir %q was removed: %v", dataDir, err)
	}
}

func TestLocalStoreRecoverer_QuarantineRejectsUnsafeDestinations(t *testing.T) {
	newFixture := func(t *testing.T) (string, string, storage.LocalStoreLocation) {
		t.Helper()
		root := t.TempDir()
		dataDir := filepath.Join(root, ".beads", "dolt")
		seedLocalStore(t, dataDir, "beads")
		loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
		if err != nil {
			t.Fatalf("LocateLocalStore: %v", err)
		}
		return root, dataDir, loc
	}

	t.Run("destination inside the data dir", func(t *testing.T) {
		_, dataDir, loc := newFixture(t)
		err := LocalStoreRecoverer().QuarantineLocalStore(loc, filepath.Join(dataDir, "beads.corrupt"))
		if err == nil {
			t.Fatal("expected rejection: a quarantine inside the data dir is still scanned as a database")
		}
		if _, statErr := os.Stat(loc.Path); statErr != nil {
			t.Errorf("store was moved despite the rejection: %v", statErr)
		}
	})

	t.Run("destination inside the store itself", func(t *testing.T) {
		_, _, loc := newFixture(t)
		err := LocalStoreRecoverer().QuarantineLocalStore(loc, filepath.Join(loc.Path, "quarantine"))
		if err == nil {
			t.Fatal("expected rejection for a destination inside the store being moved")
		}
		if _, statErr := os.Stat(loc.Path); statErr != nil {
			t.Errorf("store was moved despite the rejection: %v", statErr)
		}
	})

	t.Run("destination already exists", func(t *testing.T) {
		root, _, loc := newFixture(t)
		dest := filepath.Join(root, "taken")
		if err := os.MkdirAll(dest, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := LocalStoreRecoverer().QuarantineLocalStore(loc, dest); err == nil {
			t.Fatal("expected rejection for an existing destination")
		}
		if _, statErr := os.Stat(loc.Path); statErr != nil {
			t.Errorf("store was moved despite the rejection: %v", statErr)
		}
	})

	t.Run("unlocated store", func(t *testing.T) {
		root, _, _ := newFixture(t)
		var empty storage.LocalStoreLocation
		if err := LocalStoreRecoverer().QuarantineLocalStore(empty, filepath.Join(root, "dest")); err == nil {
			t.Fatal("expected rejection when the location was never found")
		}
	})
}

// TestLocalStoreRecoverer_DoesNotReadEnginePrivateState is the behavioral proof
// for the "<db>/.dolt is opaque" boundary: with the engine-private directory
// made unreadable and unlistable, both operations must still succeed. Any
// implementation that opened, read, listed, or descended into it would fail
// here.
func TestLocalStoreRecoverer_DoesNotReadEnginePrivateState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so the seal cannot be observed")
	}

	root := t.TempDir()
	dataDir := filepath.Join(root, ".beads", "dolt")
	storePath := seedLocalStore(t, dataDir, "beads")
	privateDir := filepath.Join(storePath, ".dolt")

	if err := os.Chmod(privateDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(privateDir, 0o750) })

	// Sanity: the seal is real for anything that tries to look inside.
	if _, err := os.ReadDir(privateDir); err == nil {
		t.Fatal("fixture seal is ineffective: .dolt is still listable")
	}

	loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
	if err != nil {
		t.Fatalf("LocateLocalStore read into sealed .dolt: %v", err)
	}
	if !loc.Found {
		t.Fatalf("Found = false with a sealed .dolt (reason %q) — identification must not require reading inside", loc.Reason)
	}

	dest := filepath.Join(root, ".beads-quarantine", "beads-sealed")
	if err := LocalStoreRecoverer().QuarantineLocalStore(loc, dest); err != nil {
		t.Fatalf("QuarantineLocalStore read into sealed .dolt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".dolt")); err != nil {
		t.Errorf("sealed .dolt did not survive the move: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dest, ".dolt"), 0o750) })
}

// The interface is the contract callers depend on; assert the dolt backend
// satisfies it rather than the concrete type.
func TestLocalStoreRecoverer_SatisfiesStorageInterface(t *testing.T) {
	var _ storage.LocalStoreRecoverer = LocalStoreRecoverer()
}

func TestLocalStoreRecoverer_QuarantineErrorMentionsPaths(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".beads", "dolt")
	seedLocalStore(t, dataDir, "beads")
	loc, err := LocalStoreRecoverer().LocateLocalStore(dataDir, "beads")
	if err != nil {
		t.Fatalf("LocateLocalStore: %v", err)
	}
	err = LocalStoreRecoverer().QuarantineLocalStore(loc, filepath.Join(dataDir, "inside"))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), dataDir) {
		t.Errorf("error %q does not name the data dir %q", err, dataDir)
	}
}
