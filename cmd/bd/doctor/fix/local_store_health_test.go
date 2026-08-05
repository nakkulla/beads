package fix

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// Real error texts bd produces on the store-open path. Sources:
//   - internal/storage/dolt/store.go newServerMode (unreachable / auto-start)
//   - internal/doltserver/doltserver.go Start (corrupt manifest wrapper)
//   - internal/doltserver/manifest_recovery.go corruptJournalRecoveryHint
const (
	corruptManifestOpenErr = "Dolt server unreachable at 127.0.0.1:3307 and auto-start failed: " +
		"failed to start dolt server after 3 attempts: dolt sql-server exited immediately on port 3307 (attempt 3/3)\n" +
		"Corrupt manifest with no recoverable data detected (GH#3290) in:\n" +
		"  /repo/.beads/dolt/beads/.dolt/noms\n" +
		"Run 'bd doctor --fix' to back up the corrupt database(s) and reinitialize.\n" +
		"Check logs: /repo/.beads/dolt-server.log"

	corruptJournalOpenErr = "Dolt server unreachable at 127.0.0.1:3307 and auto-start failed: " +
		"server started (PID 4242) but not accepting connections on port 3307: timeout\n\n" +
		"Dolt journal corruption detected in /repo/.beads/dolt-server.log.\n\n" +
		"bd will not run automatic journal repair because Dolt's repair mode can discard data."

	unreachableOpenErr = "Dolt server unreachable at 127.0.0.1:3307: dial tcp 127.0.0.1:3307: connect: connection refused\n\n" +
		"The Dolt server may not be running. Try:\n  bd dolt start"

	autoStartFailedOpenErr = "Dolt server unreachable at 127.0.0.1:3307 and auto-start failed: " +
		"failed to start dolt server after 3 attempts: dolt sql-server exited immediately on port 3307 (attempt 3/3)\n" +
		"Check logs: /repo/.beads/dolt-server.log"

	circuitOpenErr = "dolt circuit breaker is open: server appears down, failing fast (cooldown 30s)"

	pingFailedOpenErr = "failed to ping Dolt database: driver: bad connection"

	permissionOpenErr = "loading config: open /repo/.beads/metadata.json: permission denied"
)

func TestClassifyStoreOpenError(t *testing.T) {
	tests := map[string]struct {
		err           error
		wantClass     StoreOpenClass
		wantSignature string
	}{
		"nil error is unclassified": {
			err:       nil,
			wantClass: StoreOpenClassNone,
		},
		"corrupt manifest wrapper": {
			err:           errors.New(corruptManifestOpenErr),
			wantClass:     StoreOpenClassCorrupt,
			wantSignature: "Corrupt manifest with no recoverable data",
		},
		"journal corruption hint": {
			err:           errors.New(corruptJournalOpenErr),
			wantClass:     StoreOpenClassCorrupt,
			wantSignature: "Dolt journal corruption detected in",
		},
		"raw dolt root-hash line": {
			err:           errors.New("failed to start dolt server: error: root hash doesn't exist: abc123"),
			wantClass:     StoreOpenClassCorrupt,
			wantSignature: "root hash doesn't exist",
		},
		"raw dolt corrupted-journal line": {
			err:           errors.New("possible data loss detected in journal file at offset 1080309: corrupted journal"),
			wantClass:     StoreOpenClassCorrupt,
			wantSignature: "corrupted journal",
		},
		"server not running": {
			err:       errors.New(unreachableOpenErr),
			wantClass: StoreOpenClassTransient,
		},
		"auto-start failure without a corruption signature": {
			err:       errors.New(autoStartFailedOpenErr),
			wantClass: StoreOpenClassTransient,
		},
		"circuit breaker open": {
			err:       errors.New(circuitOpenErr),
			wantClass: StoreOpenClassTransient,
		},
		"ping failure": {
			err:       errors.New(pingFailedOpenErr),
			wantClass: StoreOpenClassTransient,
		},
		"permission denied": {
			err:       errors.New(permissionOpenErr),
			wantClass: StoreOpenClassTransient,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			class, signature := ClassifyStoreOpenError(tt.err)
			if class != tt.wantClass {
				t.Errorf("class = %q, want %q", class, tt.wantClass)
			}
			if signature != tt.wantSignature {
				t.Errorf("signature = %q, want %q", signature, tt.wantSignature)
			}
		})
	}
}

func TestClassifyStoreOpenError_WrappedCorruption(t *testing.T) {
	wrapped := fmt.Errorf("opening shared store: %w", errors.New(corruptManifestOpenErr))
	class, _ := ClassifyStoreOpenError(wrapped)
	if class != StoreOpenClassCorrupt {
		t.Fatalf("class = %q, want %q", class, StoreOpenClassCorrupt)
	}
}

// seedLocalStoreRig writes a rig whose sync remote and dolt layout are under
// test control. corruptLayout writes the GH#3290 shape (log signature plus a
// noms dir with no recoverable data).
func seedLocalStoreRig(t *testing.T, doltMode, yaml string, corruptLayout bool) string {
	t.Helper()
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database:     "beads.db",
		Backend:      configfile.BackendDolt,
		DoltMode:     doltMode,
		DoltDatabase: "beads",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if corruptLayout {
		writeCorruptDoltLayout(t, beadsDir)
	}
	return beadsDir
}

// writeCorruptDoltLayout writes the exact shape doltserver.DetectCorruptManifest
// recognizes: the "root hash doesn't exist" signature in the server log, plus a
// .dolt/noms dir with a manifest, an empty journal.idx and a header-sized
// journal file (no recoverable data).
func writeCorruptDoltLayout(t *testing.T, beadsDir string) {
	t.Helper()
	nomsDir := filepath.Join(beadsDir, "dolt", "beads", ".dolt", "noms")
	if err := os.MkdirAll(filepath.Join(nomsDir, "oldgen"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nomsDir, "manifest"), []byte("manifest-stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nomsDir, "journal.idx"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(nomsDir, strings.Repeat("v", 32))
	if err := os.WriteFile(journal, make([]byte, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	logBody := "starting dolt sql-server\n" +
		strings.Repeat("noise\n", 20) +
		"error: root hash doesn't exist: abc123\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.log"), []byte(logBody), 0o600); err != nil {
		t.Fatal(err)
	}
}

type treeEntry struct {
	mode  os.FileMode
	size  int64
	mtime int64
	hash  string
}

// snapshotTree records name, mode, size, mtime and content hash for every file
// under root so a diagnostic-only code path can be proven not to touch disk.
func snapshotTree(t *testing.T, root string) map[string]treeEntry {
	t.Helper()
	snap := map[string]treeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entry := treeEntry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			data, readErr := os.ReadFile(path) //nolint:gosec // test fixture path
			if readErr != nil {
				return readErr
			}
			sum := sha256.Sum256(data)
			entry.hash = hex.EncodeToString(sum[:])
		}
		snap[rel] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func assertTreeUnchanged(t *testing.T, root string, before map[string]treeEntry) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("tree entry count changed: before=%d after=%d\nbefore=%v\nafter=%v",
			len(before), len(after), before, after)
	}
	for rel, want := range before {
		got, ok := after[rel]
		if !ok {
			t.Fatalf("%s disappeared from %s", rel, root)
		}
		if got != want {
			t.Fatalf("%s changed: before=%+v after=%+v", rel, want, got)
		}
	}
}

func TestInspectLocalStoreHealth_CorruptFixtureIsDiagnosticOnly(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n", true)
	before := snapshotTree(t, beadsDir)

	report := InspectLocalStoreHealth(beadsDir, errors.New(corruptManifestOpenErr))

	if report.Class != StoreOpenClassCorrupt {
		t.Errorf("Class = %q, want %q", report.Class, StoreOpenClassCorrupt)
	}
	if report.OpenErr == nil {
		t.Error("OpenErr = nil, want the preserved open error")
	}
	if len(report.CorruptDirs) == 0 {
		t.Error("CorruptDirs is empty, want the structurally corrupt noms dir")
	}
	for _, dir := range report.CorruptDirs {
		if !strings.HasPrefix(dir, beadsDir) {
			t.Errorf("CorruptDirs entry %q is outside %q", dir, beadsDir)
		}
	}

	assertTreeUnchanged(t, beadsDir, before)
}

// A rig whose store is merely unreachable must never be classified as corrupt:
// the Phase 6 fixer is destructive and keys off this verdict.
func TestInspectLocalStoreHealth_TransientIsNotCorrupt(t *testing.T) {
	for name, openErr := range map[string]string{
		"connection refused": unreachableOpenErr,
		"circuit breaker":    circuitOpenErr,
		"ping failure":       pingFailedOpenErr,
		"permission denied":  permissionOpenErr,
		"auto-start exit":    autoStartFailedOpenErr,
	} {
		t.Run(name, func(t *testing.T) {
			beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded,
				"sync.remote: \"git+https://github.com/org/repo.git\"\n", false)
			before := snapshotTree(t, beadsDir)

			report := InspectLocalStoreHealth(beadsDir, errors.New(openErr))

			if report.Class != StoreOpenClassTransient {
				t.Errorf("Class = %q, want %q", report.Class, StoreOpenClassTransient)
			}
			if len(report.CorruptDirs) != 0 {
				t.Errorf("CorruptDirs = %v, want none", report.CorruptDirs)
			}
			assertTreeUnchanged(t, beadsDir, before)
		})
	}
}

func TestInspectLocalStoreHealth_HealthyOpen(t *testing.T) {
	beadsDir := seedLocalStoreRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n", false)

	report := InspectLocalStoreHealth(beadsDir, nil)

	if report.Class != StoreOpenClassNone {
		t.Errorf("Class = %q, want %q", report.Class, StoreOpenClassNone)
	}
	if report.OpenErr != nil {
		t.Errorf("OpenErr = %v, want nil", report.OpenErr)
	}
	if len(report.CorruptDirs) != 0 {
		t.Errorf("CorruptDirs = %v, want none", report.CorruptDirs)
	}
}

func TestInspectLocalStoreHealth_RemoteAvailability(t *testing.T) {
	tests := map[string]struct {
		doltMode       string
		yaml           string
		wantRemote     string
		wantRemoteKey  string
		wantUsable     bool
		wantServerMode bool
	}{
		"canonical dolt remote is usable": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          "sync.remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote:    "git+https://github.com/org/repo.git",
			wantRemoteKey: SyncRemoteKey,
			wantUsable:    true,
		},
		"plain git URL still counts as a re-clone source": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          "sync.remote: \"https://github.com/org/repo.git\"\n",
			wantRemote:    "https://github.com/org/repo.git",
			wantRemoteKey: SyncRemoteKey,
			wantUsable:    true,
		},
		"deprecated key still resolves": {
			doltMode:      configfile.DoltModeEmbedded,
			yaml:          "sync.git-remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote:    "git+https://github.com/org/repo.git",
			wantRemoteKey: SyncRemoteLegacyKey,
			wantUsable:    true,
		},
		"no remote at all": {
			doltMode:   configfile.DoltModeEmbedded,
			yaml:       "# nothing configured\n",
			wantUsable: false,
		},
		"server mode is reported": {
			doltMode:       configfile.DoltModeServer,
			yaml:           "# nothing configured\n",
			wantUsable:     false,
			wantServerMode: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			beadsDir := seedLocalStoreRig(t, tt.doltMode, tt.yaml, false)

			report := InspectLocalStoreHealth(beadsDir, errors.New(unreachableOpenErr))

			if report.Remote != tt.wantRemote {
				t.Errorf("Remote = %q, want %q", report.Remote, tt.wantRemote)
			}
			if report.RemoteKey != tt.wantRemoteKey {
				t.Errorf("RemoteKey = %q, want %q", report.RemoteKey, tt.wantRemoteKey)
			}
			if report.RemoteUsable != tt.wantUsable {
				t.Errorf("RemoteUsable = %v, want %v", report.RemoteUsable, tt.wantUsable)
			}
			if report.ServerMode != tt.wantServerMode {
				t.Errorf("ServerMode = %v, want %v", report.ServerMode, tt.wantServerMode)
			}
		})
	}
}
