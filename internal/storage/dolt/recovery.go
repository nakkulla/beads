package dolt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
)

// enginePrivateDirName is the per-database directory Dolt keeps its own state
// in. Recovery only ever tests that this name exists as a directory — that is
// what distinguishes a database directory from any other subdirectory of the
// data dir (the same marker doltExists uses). Its contents stay opaque: a store
// that failed to open is one whose internal state cannot be trusted to parse.
const enginePrivateDirName = ".dolt"

// doltLocalStoreRecovery implements storage.LocalStoreRecoverer for the local
// (embedded / CLI-cloned) Dolt layout, where each database lives at
// <dataDir>/<database>/ with its engine state in <database>/.dolt/.
//
// It is stateless and holds no connection by design: it is the capability that
// has to work when opening the store does not.
type doltLocalStoreRecovery struct{}

// LocalStoreRecoverer returns the Dolt backend's implementation of the
// storage-boundary recovery capability. It opens nothing, so it is callable on
// a rig whose store cannot be opened at all.
func LocalStoreRecoverer() storage.LocalStoreRecoverer {
	return doltLocalStoreRecovery{}
}

// LocateLocalStore implements storage.LocalStoreRecoverer.
func (doltLocalStoreRecovery) LocateLocalStore(dataDir, database string) (storage.LocalStoreLocation, error) {
	database = strings.TrimSpace(database)
	if database == "" {
		return storage.LocalStoreLocation{}, fmt.Errorf("database name must not be empty; use cfg.GetDoltDatabase() to resolve the configured name")
	}
	if strings.TrimSpace(dataDir) == "" {
		return storage.LocalStoreLocation{}, fmt.Errorf("dolt data directory must not be empty")
	}
	// A database name is a single path element. Rejecting separators keeps a
	// configured name from redirecting the quarantine outside the data dir.
	if database != filepath.Base(database) || database == "." || database == ".." {
		return storage.LocalStoreLocation{}, fmt.Errorf("invalid database name %q: must be a single path element", database)
	}

	loc := storage.LocalStoreLocation{DataDir: dataDir, Database: database}

	if info, err := os.Stat(dataDir); err != nil {
		if os.IsNotExist(err) {
			loc.Reason = fmt.Sprintf("dolt data directory %s does not exist", dataDir)
			return loc, nil
		}
		return loc, fmt.Errorf("stat dolt data directory %s: %w", dataDir, err)
	} else if !info.IsDir() {
		loc.Reason = fmt.Sprintf("%s is not a directory", dataDir)
		return loc, nil
	}

	candidate := filepath.Join(dataDir, database)
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			loc.Reason = fmt.Sprintf("no database directory at %s", candidate)
			return loc, nil
		}
		return loc, fmt.Errorf("stat %s: %w", candidate, err)
	}
	if !info.IsDir() {
		loc.Reason = fmt.Sprintf("%s is not a directory", candidate)
		return loc, nil
	}

	// Presence check only — never a read of what is inside.
	privateDir := filepath.Join(candidate, enginePrivateDirName)
	privateInfo, err := os.Stat(privateDir)
	if err != nil {
		if os.IsNotExist(err) {
			loc.Reason = fmt.Sprintf("%s has no %s directory, so it is not a Dolt database", candidate, enginePrivateDirName)
			return loc, nil
		}
		return loc, fmt.Errorf("stat %s: %w", privateDir, err)
	}
	if !privateInfo.IsDir() {
		loc.Reason = fmt.Sprintf("%s is not a directory, so %s is not a Dolt database", privateDir, candidate)
		return loc, nil
	}

	loc.Path = candidate
	loc.Found = true
	return loc, nil
}

// QuarantineLocalStore implements storage.LocalStoreRecoverer.
func (doltLocalStoreRecovery) QuarantineLocalStore(loc storage.LocalStoreLocation, destination string) error {
	if !loc.Found || loc.Path == "" {
		return fmt.Errorf("cannot quarantine: no local store was located (%s)", loc.Reason)
	}
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("cannot quarantine %s: destination must not be empty", loc.Path)
	}

	absStore, err := filepath.Abs(loc.Path)
	if err != nil {
		return fmt.Errorf("resolve store path %s: %w", loc.Path, err)
	}
	absDataDir, err := filepath.Abs(loc.DataDir)
	if err != nil {
		return fmt.Errorf("resolve dolt data directory %s: %w", loc.DataDir, err)
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve quarantine destination %s: %w", destination, err)
	}

	if pathWithin(absDest, absStore) {
		return fmt.Errorf("refusing to quarantine %s into %s: the destination is inside the store being moved", absStore, absDest)
	}
	if pathWithin(absDest, absDataDir) {
		return fmt.Errorf(
			"refusing to quarantine %s into %s: the destination is inside the dolt data directory %s, where the engine would still scan it as a database",
			absStore, absDest, absDataDir)
	}
	if _, err := os.Lstat(absDest); err == nil {
		return fmt.Errorf("refusing to quarantine %s: destination %s already exists", absStore, absDest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat quarantine destination %s: %w", absDest, err)
	}

	if err := os.MkdirAll(filepath.Dir(absDest), 0o750); err != nil {
		return fmt.Errorf("create quarantine directory %s: %w", filepath.Dir(absDest), err)
	}
	if err := os.Rename(absStore, absDest); err != nil {
		return fmt.Errorf(
			"move %s to %s: %w (a quarantine is a rename, so it cannot cross filesystems; move the directory manually if the destination is on another volume)",
			absStore, absDest, err)
	}
	return nil
}

// pathWithin reports whether path is root or lives underneath it. Both
// arguments must already be absolute and cleaned.
func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
