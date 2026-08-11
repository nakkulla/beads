package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestManagedDeployWritesProviderCompatibleReceipt(t *testing.T) {
	h := newManagedDeployHarness(t)
	h.run(t)
	h.assertMakeInvocation(t, 1)

	data, err := os.ReadFile(h.receipt)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatalf("parse receipt: %v", err)
	}
	if receipt["protocol_version"] != float64(1) || receipt["repo"] != h.source || receipt["target_remote"] != h.remote || receipt["target_base"] != "main" || receipt["attempt_id"] != "attempt/one" || receipt["merged_floor_sha"] != h.floor || receipt["candidate_sha"] != h.candidate {
		t.Fatalf("receipt bindings are invalid: %#v", receipt)
	}
	if receipt["previous_marker"] != nil || receipt["deployed_marker"] != h.candidate || receipt["outcome"] != "success" {
		t.Fatalf("receipt terminal binding is invalid: %#v", receipt)
	}
	verify, ok := receipt["verify"].(map[string]any)
	if !ok || verify["candidate_sha"] != h.candidate || verify["outcome"] != "success" {
		t.Fatalf("receipt verify binding is invalid: %#v", receipt["verify"])
	}
	actions, ok := receipt["action_outcomes"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatalf("receipt action_outcomes are invalid: %#v", receipt["action_outcomes"])
	}
	for _, action := range actions {
		result, ok := action.(map[string]any)
		if !ok || result["outcome"] != "success" {
			t.Fatalf("receipt action is not a success object: %#v", action)
		}
	}
	if got, want := actionNames(actions), []string{"build", "install", "binary_hash_readback", "version_readback", "alias_readback"}; !slicesEqual(got, want) {
		t.Fatalf("receipt action outcomes = %q, want %q", got, want)
	}
	compact, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(compact)
	if receipt["action_plan_digest"] != hex.EncodeToString(digest[:]) {
		t.Fatalf("receipt action digest does not bind compact outcomes: %#v", receipt)
	}
	source, ok := receipt["deployment_source"].(map[string]any)
	if !ok || source["path"] != h.release || source["head_sha"] != h.candidate {
		t.Fatalf("receipt deployment source is invalid: %#v", receipt["deployment_source"])
	}
	readback, ok := receipt["readback"].(map[string]any)
	if !ok || readback["outcome"] != "success" || readback["deployed_marker"] != h.candidate || readback["source_path"] != h.release || readback["source_head"] != h.candidate {
		t.Fatalf("receipt readback is invalid: %#v", receipt["readback"])
	}
	installed := filepath.Join(h.home, ".local", "bin", "bd")
	hash := fileSHA256(t, installed)
	if readback["installed_binary_path"] != installed || readback["installed_binary_sha256"] != hash || readback["installed_binary_build"] != h.candidate[:7] || readback["alias_path"] != filepath.Join(h.home, ".local", "bin", "beads") || readback["alias_target"] != "bd" {
		t.Fatalf("receipt installed-artifact binding is invalid: %#v", readback)
	}
	assertInstalledAlias(t, h.home)
}

func TestManagedDeployRejectsInvalidReleaseBindingsWithoutReceipt(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(t *testing.T, h *managedDeployHarness)
		override map[string]string
	}{
		{
			name:     "wrong floor",
			override: map[string]string{"BDUI_DEPLOY_MERGED_FLOOR_SHA": strings.Repeat("f", 40)},
		},
		{
			name:     "wrong release path",
			override: map[string]string{"BDUI_DEPLOY_RELEASE_PATH": "/tmp/wrong-release"},
		},
		{
			name:     "missing source",
			override: map[string]string{"BDUI_DEPLOY_SOURCE_REPO": filepath.Join(t.TempDir(), "missing-source")},
		},
		{
			name:     "wrong receipt path",
			override: map[string]string{"BDUI_DEPLOY_RECEIPT_PATH": "/tmp/wrong-receipt"},
		},
		{
			name:     "missing attempt",
			override: map[string]string{"BDUI_DEPLOY_ATTEMPT_ID": ""},
		},
		{
			name:     "bad protocol version",
			override: map[string]string{"BDUI_DEPLOY_PROTOCOL_VERSION": "2"},
		},
		{
			name: "wrong head",
			prepare: func(t *testing.T, h *managedDeployHarness) {
				if err := os.WriteFile(filepath.Join(h.release, "tracked"), []byte("other\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				git(t, h.release, "add", "tracked")
				git(t, h.release, "commit", "--quiet", "-m", "wrong head")
			},
		},
		{
			name: "dirty release",
			prepare: func(t *testing.T, h *managedDeployHarness) {
				if err := os.WriteFile(filepath.Join(h.release, "dirty"), []byte("dirty\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong remote",
			prepare: func(t *testing.T, h *managedDeployHarness) {
				git(t, h.release, "remote", "set-url", "origin", "wrong-remote")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newManagedDeployHarness(t)
			if tt.prepare != nil {
				tt.prepare(t, &h)
			}
			h.runFailure(t, tt.override)
			h.assertNoReceipt(t)
			h.assertNoMakeInvocation(t)
		})
	}
}

func TestManagedDeployReadbackFailuresDoNotWriteReceipt(t *testing.T) {
	for _, mode := range []string{"install-fail", "hash-mismatch", "version-mismatch", "invalid-version-json", "missing-version-build", "alias-mismatch", "dirty-release-after-install", "remote-mutation-after-install"} {
		t.Run(mode, func(t *testing.T) {
			h := newManagedDeployHarness(t)
			h.runFailure(t, map[string]string{"BDUI_TEST_MAKE_MODE": mode})
			h.assertNoReceipt(t)
		})
	}
}

func TestManagedDeployParsesVersionJSONRatherThanMatchingText(t *testing.T) {
	h := newManagedDeployHarness(t)
	out, err := h.command(map[string]string{"BDUI_TEST_MAKE_MODE": "escaped-build-string"}).CombinedOutput()
	if err != nil {
		t.Fatalf("valid JSON version readback failed: %v\n%s", err, out)
	}
	if _, err := os.Stat(h.receipt); err != nil {
		t.Fatalf("valid JSON version readback did not write receipt: %v", err)
	}
}

func TestManagedDeployRetryAfterInstallFailureIsIdempotent(t *testing.T) {
	h := newManagedDeployHarness(t)
	h.runFailure(t, map[string]string{"BDUI_TEST_MAKE_MODE": "hash-mismatch"})
	h.assertNoReceipt(t)
	h.run(t)
	h.assertMakeInvocation(t, 2)
	if _, err := os.Stat(h.receipt); err != nil {
		t.Fatalf("retry did not write receipt: %v", err)
	}
}

func TestManagedDeployNeverOverwritesExistingReceipt(t *testing.T) {
	h := newManagedDeployHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.receipt), 0o700); err != nil {
		t.Fatal(err)
	}
	const previous = "existing receipt"
	if err := os.WriteFile(h.receipt, []byte(previous), 0o600); err != nil {
		t.Fatal(err)
	}
	h.runFailure(t, nil)
	got, err := os.ReadFile(h.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != previous {
		t.Fatalf("existing receipt was overwritten: %q", got)
	}
	if _, err := os.Stat(h.makeLog); !os.IsNotExist(err) {
		t.Fatalf("adapter installed before rejecting existing receipt: %v", err)
	}
}

func TestManagedDeployRejectsSymlinkReceiptTarget(t *testing.T) {
	h := newManagedDeployHarness(t)
	if err := os.MkdirAll(filepath.Dir(h.receipt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(filepath.Dir(h.receipt), "other"), h.receipt); err != nil {
		t.Fatal(err)
	}
	h.runFailure(t, nil)
	if info, err := os.Lstat(h.receipt); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("receipt target changed: %v, %v", info, err)
	}
	if _, err := os.Stat(h.makeLog); !os.IsNotExist(err) {
		t.Fatalf("adapter installed before rejecting symlink receipt: %v", err)
	}
}

func TestManagedDeployUsesProviderUTF16SlugAndAttemptPath(t *testing.T) {
	h := newManagedDeployHarnessWithSourceBase(t, "é😀repo")
	attempt := "é😀attempt"
	slug := managedWorkspaceSlug(h.source)
	h.receipt = filepath.Join(h.stateHome, "bdui", slug, "deploy-receipts", managedSafeComponent(attempt, "attempt")+".json")

	if slug != "___repo-"+managedWorkspaceHash(h.source) || filepath.Base(h.receipt) != "___attempt.json" {
		t.Fatalf("provider-derived paths are wrong: slug=%q receipt=%q", slug, h.receipt)
	}
	if out, err := h.command(map[string]string{
		"BDUI_DEPLOY_ATTEMPT_ID":   attempt,
		"BDUI_DEPLOY_RECEIPT_PATH": h.receipt,
	}).CombinedOutput(); err != nil {
		t.Fatalf("provider-compatible Unicode paths rejected: %v\n%s", err, out)
	}
}

func TestManagedDeployAcceptsNormalizedAbsoluteXDGPathsWithSymlinkAncestors(t *testing.T) {
	h := newManagedDeployHarness(t)
	dataLink := filepath.Join(filepath.Dir(h.dataHome), "data-link")
	stateLink := filepath.Join(filepath.Dir(h.stateHome), "state-link")
	if err := os.Symlink(h.dataHome, dataLink); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.stateHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(h.stateHome, stateLink); err != nil {
		t.Fatal(err)
	}
	h.dataHome = dataLink + "/."
	h.stateHome = stateLink + "/."
	slug := managedWorkspaceSlug(h.source)
	h.release = filepath.Join(dataLink, "bdui", "deploy", slug, "releases", h.candidate)
	h.receipt = filepath.Join(stateLink, "bdui", slug, "deploy-receipts", "attempt_one.json")

	h.run(t)
	if _, err := os.Stat(h.receipt); err != nil {
		t.Fatalf("receipt at normalized symlinked state path: %v", err)
	}
}

func TestManagedDeployDoesNotReplaceReceiptPublishedDuringFinalWrite(t *testing.T) {
	h := newManagedDeployHarness(t)
	const previous = "receipt published in final write race"
	out, err := h.command(map[string]string{
		"BDUI_TEST_RACE_RECEIPT": h.receipt,
		"BDUI_TEST_RACE_CONTENT": previous,
	}).CombinedOutput()
	if err == nil {
		t.Fatalf("managed deploy unexpectedly succeeded:\n%s", out)
	}
	got, readErr := os.ReadFile(h.receipt)
	if readErr != nil {
		t.Fatalf("read concurrent receipt after %v:\n%s", readErr, out)
	}
	if string(got) != previous {
		t.Fatalf("final receipt publish replaced concurrent receipt: %q", got)
	}
	h.assertMakeInvocation(t, 1)
}

func TestManagedDeployDoesNotTouchDirtyFeatureSharedSource(t *testing.T) {
	h := newManagedDeployHarness(t)
	prepareDirtySharedSource(t, h.source)
	beforeHead := git(t, h.source, "rev-parse", "HEAD")
	beforeBranch := git(t, h.source, "branch", "--show-current")
	beforeStatus := git(t, h.source, "status", "--porcelain")
	h.run(t)
	if got := git(t, h.source, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("shared source HEAD changed: got %s want %s", got, beforeHead)
	}
	if got := git(t, h.source, "branch", "--show-current"); got != beforeBranch {
		t.Fatalf("shared source branch changed: got %s want %s", got, beforeBranch)
	}
	if got := git(t, h.source, "status", "--porcelain"); got != beforeStatus {
		t.Fatalf("shared source status changed: got %q want %q", got, beforeStatus)
	}
}

type managedDeployHarness struct {
	source, release, receipt, home, dataHome, stateHome, candidate, floor, remote, makeLog, systemPython string
}

func newManagedDeployHarness(t *testing.T) managedDeployHarness {
	return newManagedDeployHarnessWithSourceBase(t, "shared source")
}

func newManagedDeployHarnessWithSourceBase(t *testing.T, sourceBase string) managedDeployHarness {
	t.Helper()
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(tmp, sourceBase)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	dataHome := filepath.Join(tmp, "data")
	stateHome := filepath.Join(tmp, "state")
	candidate := strings.Repeat("a", 40)
	slug := managedWorkspaceSlug(source)
	release := filepath.Join(dataHome, "bdui", "deploy", slug, "releases", candidate)
	if err := os.MkdirAll(release, 0o755); err != nil {
		t.Fatal(err)
	}

	git(t, release, "init", "--quiet")
	git(t, release, "config", "user.email", "test@example.invalid")
	git(t, release, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(release, ".gitignore"), []byte("bd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "tracked"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), "scripts", "bdui-managed-deploy.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(release, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "scripts", "bdui-managed-deploy.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, release, "add", ".gitignore", "tracked", "scripts/bdui-managed-deploy.sh")
	git(t, release, "commit", "--quiet", "-m", "candidate")
	candidate = git(t, release, "rev-parse", "HEAD")
	actualRelease := filepath.Join(dataHome, "bdui", "deploy", slug, "releases", candidate)
	if actualRelease != release {
		if err := os.Rename(release, actualRelease); err != nil {
			t.Fatal(err)
		}
		release = actualRelease
	}
	remote := filepath.Join(tmp, "target-remote")
	git(t, release, "remote", "add", "origin", remote)

	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	makeScript := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >> \"$BDUI_TEST_MAKE_LOG\"\nif [ \"${BDUI_TEST_MAKE_MODE:-}\" = install-fail ]; then exit 7; fi\nshort=$(git rev-parse --short HEAD)\ncase \"${BDUI_TEST_MAKE_MODE:-}\" in version-mismatch) short=wrong ;; esac\nprintf '#!/bin/sh\\nprintf %s\\n' '{\\\"build\\\":\\\"'\"$short\"'\\\"}' > bd\ncase \"${BDUI_TEST_MAKE_MODE:-}\" in invalid-version-json) printf '#!/bin/sh\\nprintf %s\\n' not-json > bd ;; missing-version-build) printf '#!/bin/sh\\nprintf %s\\n' '{}' > bd ;; escaped-build-string) printf '#!/bin/sh\\nprintf \"%%s\\\\n\" \"$BDUI_TEST_VERSION_JSON\"\\n' > bd ;; esac\nchmod +x bd\nmkdir -p \"$HOME/.local/bin\"\ncp bd \"$HOME/.local/bin/bd\"\nif [ \"${BDUI_TEST_MAKE_MODE:-}\" = hash-mismatch ]; then printf x >> \"$HOME/.local/bin/bd\"; fi\nrm -f \"$HOME/.local/bin/beads\"\nif [ \"${BDUI_TEST_MAKE_MODE:-}\" = alias-mismatch ]; then ln -s wrong \"$HOME/.local/bin/beads\"; else ln -s bd \"$HOME/.local/bin/beads\"; fi\ncase \"${BDUI_TEST_MAKE_MODE:-}\" in dirty-release-after-install) touch release-dirty ;; remote-mutation-after-install) git remote set-url origin changed-remote ;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "make"), []byte(makeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	systemPython, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	pythonWrapper := `#!/bin/sh
set -eu
if [ "${1-}" = "-" ] && [ -n "${BDUI_TEST_RACE_RECEIPT-}" ]; then
  exec "$BDUI_TEST_SYSTEM_PYTHON" -c '
import os
import sys
source = sys.stdin.read()
sys.argv = sys.argv[1:]
publish = "os.link(temporary, receipt_path)" if "os.link(temporary, receipt_path)" in source else "os.rename(temporary, receipt_path)"
if publish in source:
    race = "with open(os.environ[\"BDUI_TEST_RACE_RECEIPT\"], \"w\") as raced:\n        raced.write(os.environ.get(\"BDUI_TEST_RACE_CONTENT\", \"\"))\n    " + publish
    source = source.replace(publish, race, 1)
exec(compile(source, "<bdui-managed-deploy>", "exec"))
' "$@"
fi
exec "$BDUI_TEST_SYSTEM_PYTHON" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte(pythonWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	return managedDeployHarness{
		source: source, release: release, home: filepath.Join(tmp, "home"), dataHome: dataHome, stateHome: stateHome, systemPython: systemPython,
		candidate: candidate, floor: candidate, remote: remote, makeLog: filepath.Join(tmp, "make.log"),
		receipt: filepath.Join(stateHome, "bdui", slug, "deploy-receipts", "attempt_one.json"),
	}
}

func (h managedDeployHarness) run(t *testing.T) {
	t.Helper()
	out, err := h.command(nil).CombinedOutput()
	if err != nil {
		t.Fatalf("managed deploy: %v\n%s", err, out)
	}
}

func (h managedDeployHarness) runFailure(t *testing.T, overrides map[string]string) {
	t.Helper()
	out, err := h.command(overrides).CombinedOutput()
	if err == nil {
		t.Fatalf("managed deploy unexpectedly succeeded:\n%s", out)
	}
}

func (h managedDeployHarness) command(overrides map[string]string) *exec.Cmd {
	cmd := exec.Command(filepath.Join(h.release, "scripts", "bdui-managed-deploy.sh"))
	cmd.Dir = h.release
	env := append(os.Environ(),
		"HOME="+h.home,
		"XDG_DATA_HOME="+h.dataHome,
		"XDG_STATE_HOME="+h.stateHome,
		"PATH="+filepath.Join(filepath.Dir(h.home), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BDUI_TEST_MAKE_LOG="+h.makeLog,
		"BDUI_TEST_SYSTEM_PYTHON="+h.systemPython,
		"BDUI_TEST_VERSION_JSON={\"build\":\""+h.candidate[:7]+"\",\"note\":\"escaped \\\"build\\\":\\\"wrong\\\"\"}",
		"BDUI_DEPLOY_PROTOCOL_VERSION=1",
		"BDUI_DEPLOY_SOURCE_REPO="+h.source,
		"BDUI_DEPLOY_TARGET_REMOTE="+h.remote,
		"BDUI_DEPLOY_TARGET_BASE=main",
		"BDUI_DEPLOY_MERGED_FLOOR_SHA="+h.floor,
		"BDUI_DEPLOY_CANDIDATE_SHA="+h.candidate,
		"BDUI_DEPLOY_RELEASE_PATH="+h.release,
		"BDUI_DEPLOY_RECEIPT_PATH="+h.receipt,
		"BDUI_DEPLOY_ATTEMPT_ID=attempt/one",
	)
	for key, value := range overrides {
		env = replaceEnv(env, key, value)
	}
	cmd.Env = env
	return cmd
}

func (h managedDeployHarness) assertMakeInvocation(t *testing.T, count int) {
	t.Helper()
	makeCalls, err := os.ReadFile(h.makeLog)
	if err != nil || strings.Count(string(makeCalls), "install-force\n") != count {
		t.Fatalf("adapter did not use canonical make install-force: %q, %v", makeCalls, err)
	}
}

func (h managedDeployHarness) assertNoReceipt(t *testing.T) {
	t.Helper()
	if _, err := os.Lstat(h.receipt); !os.IsNotExist(err) {
		t.Fatalf("terminal receipt exists after failure: %v", err)
	}
}

func (h managedDeployHarness) assertNoMakeInvocation(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(h.makeLog); !os.IsNotExist(err) {
		t.Fatalf("adapter invoked make before rejecting binding: %v", err)
	}
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	filtered := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func prepareDirtySharedSource(t *testing.T, source string) {
	t.Helper()
	git(t, source, "init", "--quiet")
	git(t, source, "config", "user.email", "test@example.invalid")
	git(t, source, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(source, "tracked"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "tracked")
	git(t, source, "commit", "--quiet", "-m", "base")
	git(t, source, "switch", "-c", "feature", "--quiet")
	if err := os.WriteFile(filepath.Join(source, "feature"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, source, "add", "feature")
	git(t, source, "commit", "--quiet", "-m", "ahead")
	if err := os.WriteFile(filepath.Join(source, "dirty"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func managedWorkspaceSlug(source string) string {
	base := filepath.Base(source)
	name := managedSafeComponent(base, "ws")
	if len(name) > 40 {
		name = name[:40]
	}
	return name + "-" + managedWorkspaceHash(source)
}

func managedWorkspaceHash(source string) string {
	digest := sha256.Sum256([]byte(source))
	return hex.EncodeToString(digest[:])[:12]
}

func managedSafeComponent(value, fallback string) string {
	if value == "" {
		value = fallback
	}
	var safe strings.Builder
	for _, unit := range utf16.Encode([]rune(value)) {
		if (unit >= 'A' && unit <= 'Z') || (unit >= 'a' && unit <= 'z') || (unit >= '0' && unit <= '9') || strings.ContainsRune("._-", rune(unit)) {
			safe.WriteByte(byte(unit))
		} else {
			safe.WriteByte('_')
		}
	}
	if safe.Len() == 0 {
		return fallback
	}
	return safe.String()
}

func actionNames(actions []any) []string {
	names := make([]string, 0, len(actions))
	for _, action := range actions {
		result := action.(map[string]any)
		names = append(names, result["action"].(string))
	}
	return names
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
