package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/cmd/bd/doctor"
	"github.com/steveyegge/beads/cmd/bd/doctor/fix"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage/schema"
	"golang.org/x/term"
)

// --- fixture helpers ---

type storeTreeEntry struct {
	mode  os.FileMode
	size  int64
	mtime int64
	hash  string
}

func snapshotStoreTree(t *testing.T, root string) map[string]storeTreeEntry {
	t.Helper()
	snap := map[string]storeTreeEntry{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entry := storeTreeEntry{mode: info.Mode(), size: info.Size(), mtime: info.ModTime().UnixNano()}
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

func assertStoreTreeUnchanged(t *testing.T, root string, before map[string]storeTreeEntry) {
	t.Helper()
	after := snapshotStoreTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("tree entry count under %s changed: before=%d after=%d", root, len(before), len(after))
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

// seedCorruptRecoveryRig writes a local (non-server) rig whose Dolt store
// carries the GH#3290 corrupt-manifest shape on disk, and returns
// (repoPath, beadsDir).
func seedCorruptRecoveryRig(t *testing.T, doltMode, syncRemote string) (string, string) {
	t.Helper()
	// Keep the rig off any ambient server/shared-mode configuration, and keep
	// the store open attempt from launching a real dolt server.
	t.Setenv("BEADS_TEST_MODE", "1")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_DATA_DIR", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DIR", "")

	repoPath := t.TempDir()
	beadsDir := filepath.Join(repoPath, ".beads")
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
	yaml := "# no remote\n"
	if syncRemote != "" {
		yaml = "sync.remote: \"" + syncRemote + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}

	// The on-disk shape doltserver.DetectCorruptManifest recognizes: a
	// .dolt/noms dir holding a manifest but no recoverable data, plus the
	// matching signature in the server log.
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

	// No beads-dir cache reset is needed: every fixture gets a fresh t.TempDir(),
	// so it can never collide with a cached entry from another test.
	return repoPath, beadsDir
}

// --- the confirmation gate ---

// The quarantine is destructive, so the only thing standing between a corrupt
// rig and a moved store is doctor --fix's confirmation. Without it, applyFixes
// must return before applyFixList runs and leave the disk byte-identical.
func TestDoctorLocalStoreRecovery_NoConfirmationNoMove(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test stdin is a terminal, so the non-interactive branch cannot be exercised")
	}

	repoPath, beadsDir := seedCorruptRecoveryRig(t, configfile.DoltModeEmbedded, "file:///tmp/does-not-exist-remote")

	// Precondition: this rig really is on the --fix worklist, i.e. the check
	// carries a Fix. Otherwise the test would pass vacuously.
	ss := doctor.NewSharedStore(repoPath)
	check := convertDoctorCheck(doctor.CheckLocalStoreHealth(repoPath, ss))
	ss.Close()
	if check.Fix == "" {
		t.Fatalf("fixture precondition: check carries no Fix, so nothing would be applied (%s / %s)", check.Message, check.Detail)
	}
	if check.Status != statusError {
		t.Fatalf("fixture precondition: Status = %q, want %q", check.Status, statusError)
	}

	prevYes, prevInteractive := doctorYes, doctorInteractive
	doctorYes, doctorInteractive = false, false
	t.Cleanup(func() { doctorYes, doctorInteractive = prevYes, prevInteractive })

	before := snapshotStoreTree(t, repoPath)

	applyFixes(doctorResult{Path: repoPath, Checks: []doctorCheck{check}})

	assertStoreTreeUnchanged(t, repoPath, before)

	// And nothing was created outside .beads/ either.
	if _, err := os.Stat(filepath.Join(repoPath, fix.QuarantineDirName)); !os.IsNotExist(err) {
		t.Errorf("a quarantine directory was created without confirmation (stat err %v)", err)
	}
	_ = beadsDir
}

// --- refusal paths at the command level ---

func TestDoctorLocalStoreRecovery_RefusesWithoutRemote(t *testing.T) {
	repoPath, _ := seedCorruptRecoveryRig(t, configfile.DoltModeEmbedded, "")
	before := snapshotStoreTree(t, repoPath)

	err := recoverLocalStore(repoPath)
	if err == nil {
		t.Fatal("expected a refusal when no sync remote is configured")
	}
	if !strings.Contains(err.Error(), "no sync remote") {
		t.Errorf("error %q does not explain the missing remote", err)
	}
	assertStoreTreeUnchanged(t, repoPath, before)
}

func TestDoctorLocalStoreRecovery_RefusesServerModeRig(t *testing.T) {
	repoPath, _ := seedCorruptRecoveryRig(t, configfile.DoltModeServer, "file:///tmp/does-not-exist-remote")
	before := snapshotStoreTree(t, repoPath)

	err := recoverLocalStore(repoPath)
	if err == nil {
		t.Fatal("expected a refusal for a server-mode rig")
	}
	if !strings.Contains(err.Error(), "server-mode") {
		t.Errorf("error %q does not explain the server-mode refusal", err)
	}
	assertStoreTreeUnchanged(t, repoPath, before)
}

// A rig whose store is merely unreachable must never be quarantined: the
// diagnosis, not the flag, is what makes the repair destructive-safe.
func TestDoctorLocalStoreRecovery_RefusesHealthyLayout(t *testing.T) {
	repoPath, beadsDir := seedCorruptRecoveryRig(t, configfile.DoltModeEmbedded, "file:///tmp/does-not-exist-remote")
	// Remove the corruption evidence, leaving an ordinary unreachable store.
	if err := os.Remove(filepath.Join(beadsDir, "dolt-server.log")); err != nil {
		t.Fatal(err)
	}
	before := snapshotStoreTree(t, repoPath)

	err := recoverLocalStore(repoPath)
	if err == nil {
		t.Fatal("expected a refusal when no corruption was identified")
	}
	if !strings.Contains(err.Error(), "corruption") {
		t.Errorf("error %q does not explain the missing corruption verdict", err)
	}
	assertStoreTreeUnchanged(t, repoPath, before)
}

// --- end-to-end quarantine + re-clone ---

func requireDoltCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dolt"); err != nil {
		t.Skip("dolt CLI not on PATH; the re-clone leg shells out to `dolt clone`")
	}
}

func runIn(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...) //nolint:gosec // fixed test commands
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"DOLT_ROOT_PATH="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s (in %s) failed: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// seedDoltFileRemote builds a real Dolt database carrying the beads schema and
// one issue, pushes it to a file:// remote, and returns the remote URL.
func seedDoltFileRemote(t *testing.T, issueID string) string {
	t.Helper()
	base := t.TempDir()
	remoteDir := filepath.Join(base, "remote")
	sourceDir := filepath.Join(base, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	remoteURL := "file://" + remoteDir

	runIn(t, sourceDir, "dolt", "init", "--name", "beads test", "--email", "test@example.com")
	runIn(t, sourceDir, "dolt", "remote", "add", "origin", remoteURL)
	runIn(t, sourceDir, "dolt", "sql", "-q", schema.AllMigrationsSQL())
	runIn(t, sourceDir, "dolt", "sql", "-q",
		// title/description/design/acceptance_criteria/notes are NOT NULL with no
		// default (schema migration 0001); everything else defaults.
		"INSERT INTO issues (id, title, description, design, acceptance_criteria, notes) "+
			"VALUES ('"+issueID+"', 'recovered issue', 'seeded on the remote', '', '', '')")
	runIn(t, sourceDir, "dolt", "sql", "-q", "CALL DOLT_ADD('.'); CALL DOLT_COMMIT('-Am', 'seed');")
	runIn(t, sourceDir, "dolt", "push", "origin", "main")

	return remoteURL
}

// TestDoctorLocalStoreRecovery_QuarantinesOutsideBeadsAndReclones is the
// acceptance path: a confirmed repair moves the damaged store to a timestamped
// path OUTSIDE .beads/ and rebuilds the database from the configured remote,
// with the issue rows readable again afterwards.
func TestDoctorLocalStoreRecovery_QuarantinesOutsideBeadsAndReclones(t *testing.T) {
	requireDoltCLI(t)

	const issueID = "bd-recovered-1"
	remoteURL := seedDoltFileRemote(t, issueID)

	repoPath, beadsDir := seedCorruptRecoveryRig(t, configfile.DoltModeEmbedded, remoteURL)
	storePath := filepath.Join(beadsDir, "dolt", "beads")
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("fixture precondition: no corrupt store at %s: %v", storePath, err)
	}

	if err := recoverLocalStore(repoPath); err != nil {
		t.Fatalf("recoverLocalStore: %v", err)
	}

	// 1. The damaged store was moved out of .beads/ — not deleted, not copied
	//    into a sibling of itself.
	quarantineRoot := filepath.Join(repoPath, fix.QuarantineDirName)
	entries, err := os.ReadDir(quarantineRoot)
	if err != nil {
		t.Fatalf("no quarantine directory at %s: %v", quarantineRoot, err)
	}
	if len(entries) != 1 {
		t.Fatalf("quarantine holds %d entries, want exactly 1: %v", len(entries), entries)
	}
	quarantined := filepath.Join(quarantineRoot, entries[0].Name())

	absBeads, err := filepath.Abs(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	absQuarantined, err := filepath.Abs(quarantined)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(absBeads, absQuarantined)
	if err != nil {
		t.Fatal(err)
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("quarantined store %q is inside .beads/ (%q), rel=%q", absQuarantined, absBeads, rel)
	}
	// The preserved copy really is the damaged store.
	if _, err := os.Stat(filepath.Join(quarantined, ".dolt", "noms", "manifest")); err != nil {
		t.Errorf("quarantined store does not carry the damaged content: %v", err)
	}

	// 2. The store directory was rebuilt in place from the remote.
	if _, err := os.Stat(filepath.Join(storePath, ".dolt")); err != nil {
		t.Fatalf("store was not re-cloned into %s: %v", storePath, err)
	}

	// 3. The recovered database really answers issue queries — the data a
	//    subsequent `bd list --json` reads. Queried through the dolt CLI so the
	//    assertion needs no running sql-server.
	out := runIn(t, storePath, "dolt", "sql", "-r", "csv", "-q", "SELECT id FROM issues")
	if !strings.Contains(out, issueID) {
		t.Fatalf("recovered store does not contain the seeded issue %q; got:\n%s", issueID, out)
	}
}

// TestDoctorLocalStoreRecovery_ListJSONAfterRecovery is the acceptance
// assertion that the rig is usable again after recovery: `bd list --json`
// succeeds against the rebuilt store and emits well-formed JSON.
//
// It asserts success, not row contents. The fixture's remote is seeded with
// schema.AllMigrationsSQL() only, so it carries none of the config/genesis rows
// `bd init` writes and bd's list filter legitimately matches nothing. That the
// re-cloned store really holds the seeded issue is proven separately, at the
// storage level, by the sibling ...QuarantinesOutsideBeadsAndReclones test.
//
// Requires the dolt CLI: recovery shells out to `dolt clone`, and bd then
// auto-starts a real dolt sql-server for the recovered rig.
func TestDoctorLocalStoreRecovery_ListJSONAfterRecovery(t *testing.T) {
	requireDoltCLI(t)

	remoteURL := seedDoltFileRemote(t, "bd-recovered-1")
	repoPath, beadsDir := seedCorruptRecoveryRig(t, configfile.DoltModeEmbedded, remoteURL)

	if err := recoverLocalStore(repoPath); err != nil {
		t.Fatalf("recoverLocalStore: %v", err)
	}

	// Let bd own the server lifecycle for the recovered rig, and make sure the
	// server it starts does not outlive the test.
	t.Setenv("BEADS_TEST_MODE", "")
	t.Setenv("BEADS_DIR", beadsDir)
	t.Cleanup(func() { _ = doltserver.Stop(beadsDir) })

	out := runBDListJSON(t, repoPath)

	var issues []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &issues); err != nil {
		t.Fatalf("`bd list --json` after recovery emitted invalid JSON: %v\n%s", err, out)
	}
}

// runBDListJSON runs `bd list --json` in-process against dir and returns stdout.
func runBDListJSON(t *testing.T, dir string) string {
	t.Helper()

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if readErr != nil {
				break
			}
		}
		done <- sb.String()
	}()

	// rootCmd is process-global and --json is bound to the package-level
	// jsonOutput, so leaving it set would flip every later test in this package
	// into JSON mode.
	defer func() {
		if f := rootCmd.PersistentFlags().Lookup("json"); f != nil {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatalf("reset --json: %v", err)
			}
			f.Changed = false
		}
		jsonOutput = false
	}()

	rootCmd.SetArgs([]string{"list", "--json"})
	execErr := rootCmd.Execute()

	os.Stdout = origStdout
	_ = w.Close()
	out := <-done
	_ = r.Close()
	rootCmd.SetArgs(nil)

	if execErr != nil {
		t.Fatalf("`bd list --json` failed: %v\n%s", execErr, out)
	}
	return out
}
