package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// Real store-open error texts (see cmd/bd/doctor/fix/local_store_health_test.go
// for the source citations).
const (
	corruptOpenErrText = "Dolt server unreachable at 127.0.0.1:3307 and auto-start failed: " +
		"failed to start dolt server after 3 attempts: dolt sql-server exited immediately on port 3307 (attempt 3/3)\n" +
		"Corrupt manifest with no recoverable data detected (GH#3290) in:\n" +
		"  /repo/.beads/dolt/beads/.dolt/noms\n" +
		"Check logs: /repo/.beads/dolt-server.log"

	transientOpenErrText = "Dolt server unreachable at 127.0.0.1:3307: dial tcp 127.0.0.1:3307: connect: connection refused\n\n" +
		"The Dolt server may not be running. Try:\n  bd dolt start"
)

// seedLocalStoreHealthRig writes a Dolt rig at <tmp>/.beads and returns
// (repoPath, beadsDir).
func seedLocalStoreHealthRig(t *testing.T, doltMode, yaml string) (string, string) {
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
	return tmpDir, beadsDir
}

// writeCorruptLocalStoreLayout writes the GH#3290 on-disk shape: the
// "root hash doesn't exist" server-log signature plus a .dolt/noms dir with a
// manifest but no recoverable data.
func writeCorruptLocalStoreLayout(t *testing.T, beadsDir string) {
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
	if err := os.WriteFile(filepath.Join(nomsDir, strings.Repeat("v", 32)), make([]byte, 40), 0o600); err != nil {
		t.Fatal(err)
	}
	logBody := "starting dolt sql-server\nerror: root hash doesn't exist: abc123\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.log"), []byte(logBody), 0o600); err != nil {
		t.Fatal(err)
	}
}

type localStoreTreeEntry struct {
	mode  os.FileMode
	size  int64
	mtime int64
	hash  string
}

func snapshotLocalStoreTree(t *testing.T, root string) map[string]localStoreTreeEntry {
	t.Helper()
	snap := map[string]localStoreTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entry := localStoreTreeEntry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().UnixNano()}
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

func assertLocalStoreTreeUnchanged(t *testing.T, root string, before map[string]localStoreTreeEntry) {
	t.Helper()
	after := snapshotLocalStoreTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("tree entry count changed: before=%d after=%d", len(before), len(after))
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

// sharedStoreWithOpenErr builds the state NewSharedStore reaches when the store
// open fails: nil store, preserved error.
func sharedStoreWithOpenErr(beadsDir string, openErr error) *SharedStore {
	ss := &SharedStore{beadsDir: beadsDir}
	ss.recordOpenErr(openErr)
	return ss
}

func TestCheckLocalStoreHealth_NoBeadsDir(t *testing.T) {
	check := CheckLocalStoreHealth(t.TempDir(), nil)

	if check.Name != LocalStoreHealthCheckName {
		t.Errorf("Name = %q, want %q", check.Name, LocalStoreHealthCheckName)
	}
	if check.Status != StatusOK {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty", check.Fix)
	}
}

// A rig whose shared store never failed to open must produce no warning.
func TestCheckLocalStoreHealth_HealthyStoreIsSilent(t *testing.T) {
	tmpDir, _ := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n")
	clearResolveBeadsDirCache()
	t.Cleanup(clearResolveBeadsDirCache)

	ss := NewSharedStore(tmpDir)
	defer ss.Close()

	if ss.OpenErr() != nil {
		t.Fatalf("fixture precondition: OpenErr = %v, want nil", ss.OpenErr())
	}

	check := CheckLocalStoreHealth(tmpDir, ss)

	if check.Status != StatusOK {
		t.Errorf("Status = %q, want %q (%s / %s)", check.Status, StatusOK, check.Message, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty", check.Fix)
	}
}

func TestCheckLocalStoreHealth_CorruptWithRemote(t *testing.T) {
	tmpDir, beadsDir := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n")
	writeCorruptLocalStoreLayout(t, beadsDir)
	before := snapshotLocalStoreTree(t, beadsDir)

	ss := sharedStoreWithOpenErr(beadsDir, errors.New(corruptOpenErrText))
	if ss.OpenClass() != "corrupt" {
		t.Fatalf("OpenClass = %q, want %q", ss.OpenClass(), "corrupt")
	}

	check := CheckLocalStoreHealth(tmpDir, ss)

	if check.Status != StatusError {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusError, check.Message)
	}
	// A corrupt local rig with a remote is the one shape that is repairable, so
	// it carries a Fix and joins the doctor --fix worklist. (Phase 5 asserted
	// an empty Fix here; Phase 6 wires the quarantine+re-clone fixer.)
	if check.Fix == "" {
		t.Error("Fix is empty, want the quarantine+re-clone description for a repairable rig")
	}
	if !strings.Contains(check.Fix, "outside .beads/") {
		t.Errorf("Fix does not state that the quarantine is outside .beads/: %q", check.Fix)
	}
	if !strings.Contains(check.Detail, "git+https://github.com/org/repo.git") {
		t.Errorf("Detail does not name the configured remote: %q", check.Detail)
	}
	if strings.Contains(check.Message, "no sync remote") {
		t.Errorf("Message claims no remote although one is configured: %q", check.Message)
	}

	assertLocalStoreTreeUnchanged(t, beadsDir, before)
}

func TestCheckLocalStoreHealth_CorruptWithoutRemote(t *testing.T) {
	tmpDir, beadsDir := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded, "# nothing configured\n")
	writeCorruptLocalStoreLayout(t, beadsDir)
	before := snapshotLocalStoreTree(t, beadsDir)

	ss := sharedStoreWithOpenErr(beadsDir, errors.New(corruptOpenErrText))

	check := CheckLocalStoreHealth(tmpDir, ss)

	if check.Status != StatusError {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusError, check.Message)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty (diagnostic only)", check.Fix)
	}
	if !strings.Contains(check.Message, "no sync remote") {
		t.Errorf("Message does not report the missing remote: %q", check.Message)
	}
	for _, want := range []string{"cannot", "bd bootstrap"} {
		if !strings.Contains(check.Detail, want) {
			t.Errorf("Detail lacks manual-recovery guidance %q: %q", want, check.Detail)
		}
	}

	assertLocalStoreTreeUnchanged(t, beadsDir, before)
}

// The corrupt/no-remote and corrupt/remote branches must be distinguishable in
// the message, not only in the detail.
func TestCheckLocalStoreHealth_MessageVariesWithRemote(t *testing.T) {
	withDir, withBeads := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n")
	writeCorruptLocalStoreLayout(t, withBeads)
	withCheck := CheckLocalStoreHealth(withDir, sharedStoreWithOpenErr(withBeads, errors.New(corruptOpenErrText)))

	withoutDir, withoutBeads := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded, "# nothing configured\n")
	writeCorruptLocalStoreLayout(t, withoutBeads)
	withoutCheck := CheckLocalStoreHealth(withoutDir, sharedStoreWithOpenErr(withoutBeads, errors.New(corruptOpenErrText)))

	if withCheck.Message == withoutCheck.Message {
		t.Fatalf("message identical with and without a remote: %q", withCheck.Message)
	}
}

func TestCheckLocalStoreHealth_TransientIsWarningNotError(t *testing.T) {
	tmpDir, beadsDir := seedLocalStoreHealthRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n")
	before := snapshotLocalStoreTree(t, beadsDir)

	ss := sharedStoreWithOpenErr(beadsDir, errors.New(transientOpenErrText))

	check := CheckLocalStoreHealth(tmpDir, ss)

	if check.Status != StatusWarning {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty (diagnostic only)", check.Fix)
	}
	if strings.Contains(strings.ToLower(check.Message), "corrupt") {
		t.Errorf("transient failure reported as corruption: %q", check.Message)
	}

	assertLocalStoreTreeUnchanged(t, beadsDir, before)
}

// SharedStore's additive fields must not disturb the nil-store contract the
// other checks depend on.
func TestSharedStore_OpenErrIsAdditive(t *testing.T) {
	var nilSS *SharedStore
	if nilSS.OpenErr() != nil {
		t.Error("nil SharedStore should report no open error")
	}
	if nilSS.OpenClass() != "" {
		t.Errorf("nil SharedStore OpenClass = %q, want empty", nilSS.OpenClass())
	}

	ss := sharedStoreWithOpenErr("/tmp/does-not-exist/.beads", errors.New(transientOpenErrText))
	if ss.Store() != nil {
		t.Error("Store() must stay nil after a failed open")
	}
	if ss.OpenErr() == nil {
		t.Error("OpenErr() must preserve the failed open")
	}
	ss.Close()
	if ss.OpenErr() == nil {
		t.Error("Close() must not clear the preserved open error")
	}
}
