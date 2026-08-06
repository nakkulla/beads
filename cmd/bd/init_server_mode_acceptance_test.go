//go:build cgo

// Command-level acceptance seam for the server-mode init policy pack
// (docs/superpowers/specs/2026-08-05-bd-skill-absorption-design.md, 수용 기준 1-2).
//
// Unit tests cover each policy predicate in isolation; only the real `bd init
// --server` command path proves they compose — the observed regression was a
// raw `bd init --server ...` (no --external) that persisted the git origin as
// sync.remote, generated local-Dolt agent docs, and left `resolved` unregistered.
// These tests therefore drive rootCmd, not the helpers.
//
// They need a reachable dolt sql-server and skip without one, following the
// package convention (skipIfNoDolt).

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/steveyegge/beads/internal/config"
)

// initServerModeFixtureOrigin is a git origin that must never be contacted.
// A1 is only meaningful when init runs in a repo that HAS an origin, and the
// whole point of A1 is that server-mode init never reaches for it.
const initServerModeFixtureOrigin = "https://github.com/example/beads-u4d-fixture.git"

// newInitServerModeGitFixture creates a git repo carrying an origin, which is
// the precondition that makes the sync.remote assertion meaningful.
func newInitServerModeGitFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = testEnvNoPrompt()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test User")
	runGit("config", "core.hooksPath", ".git/hooks")
	runGit("remote", "add", "origin", initServerModeFixtureOrigin)

	got, err := runCommandInDirWithOutput(dir, "git", "config", "--get", "remote.origin.url")
	if err != nil || got != initServerModeFixtureOrigin {
		t.Fatalf("fixture origin = %q (err %v), want %q", got, err, initServerModeFixtureOrigin)
	}
	return dir
}

// resetBDCommandFlagsForTest restores the flags of the shared cobra command
// tree to their defaults. rootCmd/initCmd/listCmd are package-level singletons,
// so a flag another test set (e.g. --skip-agents) would otherwise silently
// satisfy the very default this file is asserting.
func resetBDCommandFlagsForTest(t *testing.T) {
	t.Helper()

	reset := func(fs *pflag.FlagSet) {
		fs.VisitAll(func(f *pflag.Flag) {
			if !f.Changed {
				return
			}
			// Slice/array defaults render as "[a,b]" and cannot be round-tripped
			// through Set; clearing Changed is enough for the flags used here.
			if strings.Contains(f.Value.Type(), "Slice") || strings.Contains(f.Value.Type(), "Array") {
				f.Changed = false
				return
			}
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatalf("reset --%s to default %q: %v", f.Name, f.DefValue, err)
			}
			f.Changed = false
		})
	}

	reset(rootCmd.PersistentFlags())
	reset(initCmd.Flags())
	reset(listCmd.Flags())
}

// runBDCommandInDir executes a bd command in-process from dir and returns the
// combined stdout+stderr it produced. Output goes to a file rather than a pipe
// so a chatty command cannot deadlock on a full pipe buffer.
func runBDCommandInDir(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()

	// rootCmd, viper and the os.Stdout/os.Stderr swap are process-global.
	stdioMutex.Lock()
	defer stdioMutex.Unlock()

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "bd-output.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create output log: %v", err)
	}

	origStdout, origStderr := os.Stdout, os.Stderr
	origBeadsDir, hadBeadsDir := os.LookupEnv("BEADS_DIR")

	if err := os.Chdir(dir); err != nil {
		_ = logFile.Close()
		t.Fatalf("chdir %s: %v", dir, err)
	}
	os.Stdout, os.Stderr = logFile, logFile

	config.ResetForTesting()
	resetBDCommandFlagsForTest(t)
	rootCmd.SetArgs(args)
	execErr := rootCmd.Execute()

	// Drop the process-global state the next in-process command must not
	// inherit (mirrors the cleanup in runBDInProcess).
	if store != nil {
		_ = store.Close()
		store = nil
	}
	dbPath = ""
	jsonOutput = false
	rootCtx = nil
	rootCancel = nil
	rootCmd.SetArgs(nil)
	resetCommandContext()

	os.Stdout, os.Stderr = origStdout, origStderr
	_ = os.Chdir(origWD)
	if hadBeadsDir {
		_ = os.Setenv("BEADS_DIR", origBeadsDir)
	} else {
		_ = os.Unsetenv("BEADS_DIR")
	}
	_ = logFile.Close()

	out, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command output: %v", readErr)
	}
	return string(out), execErr
}

func assertFileAbsent(t *testing.T, dir, name, why string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Errorf("%s was generated but %s", name, why)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", name, err)
	}
}

// TestInitServerModeAcceptanceHarnessWorksWithoutDolt exercises the parts of
// this file's harness that do not need a dolt sql-server: the git fixture, the
// shared-flag reset, and the in-process command runner. Without it, the three
// acceptance tests below are indistinguishable from dead code on a machine that
// skips them.
func TestInitServerModeAcceptanceHarnessWorksWithoutDolt(t *testing.T) {
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	t.Cleanup(config.ResetForTesting)

	// The fixture self-asserts the origin it configured.
	dir := newInitServerModeGitFixture(t)

	// A leftover --skip-agents from another test would silently satisfy the A4
	// assertion, so the reset must actually clear it.
	skipAgentsFlag := initCmd.Flags().Lookup("skip-agents")
	if skipAgentsFlag == nil {
		t.Fatal("init has no --skip-agents flag")
	}
	if err := skipAgentsFlag.Value.Set("true"); err != nil {
		t.Fatalf("set --skip-agents: %v", err)
	}
	skipAgentsFlag.Changed = true

	// runBDCommandInDir chdirs, redirects stdio, resets the flags and drives
	// rootCmd; `version` is the cheapest command that needs no store.
	out, err := runBDCommandInDir(t, dir, "version")
	if err != nil {
		t.Fatalf("bd version failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(strings.ToLower(out), "bd") {
		t.Errorf("bd version output looks empty or uncaptured:\n%s", out)
	}

	if skipAgentsFlag.Changed || skipAgentsFlag.Value.String() != "false" {
		t.Errorf("--skip-agents = %q (changed=%v) after reset, want %q (changed=false)",
			skipAgentsFlag.Value.String(), skipAgentsFlag.Changed, "false")
	}

	if cwd, err := os.Getwd(); err != nil {
		t.Fatalf("getwd: %v", err)
	} else if cwd == dir {
		t.Errorf("runBDCommandInDir left the process in %s; the working directory must be restored", cwd)
	}
}

// TestInitServerModeAppliesFleetPolicyDefaults is 수용 기준 1: a raw
// `bd init --server ...` (no --external, no --skip-agents) in a repo that has a
// git origin must produce a policy-complete central rig.
func TestInitServerModeAppliesFleetPolicyDefaults(t *testing.T) {
	skipIfNoDolt(t)
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	t.Cleanup(config.ResetForTesting)
	dbPath = ""
	store = nil

	dir := newInitServerModeGitFixture(t)
	beadsDir := filepath.Join(dir, ".beads")
	database := uniqueTestDBName(t)
	t.Cleanup(func() {
		dropTestDatabase(database, testDoltServerPort)
	})

	out, err := runBDCommandInDir(t, dir,
		"init",
		"--server",
		"--server-host", "127.0.0.1",
		"--server-port", fmt.Sprintf("%d", testDoltServerPort),
		"--database", database,
		"--prefix", "acc",
		"--quiet",
		"--skip-hooks",
	)
	if err != nil {
		t.Fatalf("bd init --server failed: %v\noutput:\n%s", err, out)
	}

	// A1: the git origin is a code remote, not the Dolt sync path.
	if got := existingConfigYamlValue(beadsDir, "sync.remote"); got != "" {
		t.Errorf("sync.remote = %q, want unset (git origin must not be adopted in server mode)", got)
	}

	// A3: auto-backup defaults off for server-mode rigs.
	if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != "false" {
		t.Errorf("backup.enabled = %q, want %q", got, "false")
	}

	// A4: the generated agent docs describe the local-Dolt layout, which is
	// wrong for a central rig, so they are skipped by default.
	assertFileAbsent(t, dir, "AGENTS.md", "server-mode init defaults to --skip-agents")
	assertFileAbsent(t, dir, "CLAUDE.md", "server-mode init defaults to --skip-agents")

	// A2: `resolved` is registered in the database, so the command path that
	// validates status names accepts it.
	listOut, err := runBDCommandInDir(t, dir, "list", "--status", "resolved", "--json")
	if err != nil {
		t.Fatalf("bd list --status resolved --json failed: %v\noutput:\n%s", err, listOut)
	}

	// Negative control: the assertion above only has teeth if an unregistered
	// custom status is still rejected by the same command path.
	unregisteredOut, err := runBDCommandInDir(t, dir, "list", "--status", "unregistered_status", "--json")
	if err == nil {
		t.Errorf("bd list --status unregistered_status unexpectedly succeeded; "+
			"the resolved assertion proves nothing.\noutput:\n%s", unregisteredOut)
	}
}

// TestInitServerModeHonorsExplicitOverrides is 수용 기준 2 plus the A4 override:
// the server-mode defaults are defaults, and a named flag always wins.
//
// The explicit remote is a local bare git repo and --from-jsonl selects the
// local source, so init records sync.remote without attempting a remote clone
// (same shape as TestInitFromJSONLExplicitRemoteRefusesWhenRemoteHasDoltData,
// minus the refs/dolt/data that triggers the divergence refusal there).
func TestInitServerModeHonorsExplicitOverrides(t *testing.T) {
	skipIfNoDolt(t)
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	t.Cleanup(config.ResetForTesting)
	dbPath = ""
	store = nil

	dir := newInitServerModeGitFixture(t)
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	jsonlRecord := `{"id":"ovr-1","title":"Seeded from JSONL","type":"task","status":"open","priority":2}` + "\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte(jsonlRecord), 0o600); err != nil {
		t.Fatalf("write issues.jsonl: %v", err)
	}

	bareRemote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", bareRemote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	const agentsFile = "AGENT_NOTES.md"
	database := uniqueTestDBName(t)
	t.Cleanup(func() {
		dropTestDatabase(database, testDoltServerPort)
	})

	out, err := runBDCommandInDir(t, dir,
		"init",
		"--server",
		"--server-host", "127.0.0.1",
		"--server-port", fmt.Sprintf("%d", testDoltServerPort),
		"--database", database,
		"--prefix", "ovr",
		"--remote", bareRemote,
		"--from-jsonl",
		"--agents-file", agentsFile,
		"--quiet",
		"--skip-hooks",
	)
	if err != nil {
		t.Fatalf("bd init --server with overrides failed: %v\noutput:\n%s", err, out)
	}

	// A1 suppresses the git-origin fallback only; an explicit --remote is user
	// intent and is recorded verbatim.
	if got := existingConfigYamlValue(beadsDir, "sync.remote"); got != bareRemote {
		t.Errorf("sync.remote = %q, want %q", got, bareRemote)
	}

	// A4's default yields to a named agents flag.
	if _, statErr := os.Stat(filepath.Join(dir, agentsFile)); statErr != nil {
		t.Errorf("expected --agents-file %s to be generated: %v", agentsFile, statErr)
	}
	assertFileAbsent(t, dir, "AGENTS.md", "--agents-file renamed the generated document")

	// The overrides do not disable the rest of the policy pack.
	if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != "false" {
		t.Errorf("backup.enabled = %q, want %q", got, "false")
	}
	listOut, err := runBDCommandInDir(t, dir, "list", "--status", "resolved", "--json")
	if err != nil {
		t.Fatalf("bd list --status resolved --json failed: %v\noutput:\n%s", err, listOut)
	}
}

// TestInitServerModeExternalFlagStaysCompatible pins the pre-existing
// `--server --external` combination: the policy gate is initServerMode, so
// adding --external must not change any of the outcomes above.
func TestInitServerModeExternalFlagStaysCompatible(t *testing.T) {
	skipIfNoDolt(t)
	saveAndRestoreGlobals(t)
	ensureCleanGlobalState(t)
	t.Cleanup(config.ResetForTesting)
	dbPath = ""
	store = nil

	dir := newInitServerModeGitFixture(t)
	beadsDir := filepath.Join(dir, ".beads")
	database := uniqueTestDBName(t)
	t.Cleanup(func() {
		dropTestDatabase(database, testDoltServerPort)
	})

	out, err := runBDCommandInDir(t, dir,
		"init",
		"--server",
		"--external",
		"--server-host", "127.0.0.1",
		"--server-port", fmt.Sprintf("%d", testDoltServerPort),
		"--database", database,
		"--prefix", "ext",
		"--quiet",
		"--skip-hooks",
	)
	if err != nil {
		t.Fatalf("bd init --server --external failed: %v\noutput:\n%s", err, out)
	}

	if got := existingConfigYamlValue(beadsDir, "sync.remote"); got != "" {
		t.Errorf("sync.remote = %q, want unset", got)
	}
	if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != "false" {
		t.Errorf("backup.enabled = %q, want %q", got, "false")
	}
	assertFileAbsent(t, dir, "AGENTS.md", "server-mode init defaults to --skip-agents")
	assertFileAbsent(t, dir, "CLAUDE.md", "server-mode init defaults to --skip-agents")

	listOut, err := runBDCommandInDir(t, dir, "list", "--status", "resolved", "--json")
	if err != nil {
		t.Fatalf("bd list --status resolved --json failed: %v\noutput:\n%s", err, listOut)
	}
}
