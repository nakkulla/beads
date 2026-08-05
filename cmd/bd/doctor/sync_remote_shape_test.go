package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

// seedSyncRemoteRig writes a rig with the given dolt_mode and, when yaml is
// non-empty, that literal config.yaml body.
func seedSyncRemoteRig(t *testing.T, doltMode, yaml string) string {
	t.Helper()
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

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
	return tmpDir
}

func flatRemoteYaml(url string) string {
	// The shape bd init actually persists: the rendered template is all
	// comments, so SetYamlConfigInDir appends a flat root key.
	return "# Beads Configuration File\n\nsync.remote: \"" + url + "\"\n"
}

func nestedRemoteYaml(url string) string {
	return "sync:\n  remote: \"" + url + "\"\n"
}

func TestCheckSyncRemoteShape_NoBeadsDir(t *testing.T) {
	check := CheckSyncRemoteShape(t.TempDir())
	if check.Name != SyncRemoteShapeCheckName {
		t.Errorf("Name = %q, want %q", check.Name, SyncRemoteShapeCheckName)
	}
	if check.Status != StatusOK {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
	}
}

func TestCheckSyncRemoteShape_NoRemoteConfigured(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeServer, "# nothing configured\n")

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusOK {
		t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty", check.Fix)
	}
}

// Positive case (a): a plain git code URL is not a Dolt remote.
func TestCheckSyncRemoteShape_PlainGitURLWarns(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeEmbedded, flatRemoteYaml("https://github.com/org/repo.git"))

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusWarning {
		t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
	}
	if !strings.Contains(check.Message+check.Detail, "https://github.com/org/repo.git") {
		t.Errorf("message/detail must name the configured value: %q / %q", check.Message, check.Detail)
	}
	// Shape findings are diagnostic only: bd must never rewrite the value.
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty: an unparseable remote is diagnostic only", check.Fix)
	}
}

// Negative cases (a): supported Dolt transports must stay silent, GitHub host
// included.
func TestCheckSyncRemoteShape_SupportedTransportsAreSilent(t *testing.T) {
	for _, url := range []string{
		"git+ssh://git@github.com/org/repo.git",
		"git+https://github.com/org/repo.git",
		"dolthub://org/repo",
		"file:///srv/dolt/repo",
	} {
		t.Run(url, func(t *testing.T) {
			tmpDir := seedSyncRemoteRig(t, configfile.DoltModeEmbedded, flatRemoteYaml(url))

			check := CheckSyncRemoteShape(tmpDir)

			if check.Status != StatusOK {
				t.Errorf("Status = %q, want %q for %s (%s / %s)", check.Status, StatusOK, url, check.Message, check.Detail)
			}
		})
	}
}

func TestCheckSyncRemoteShape_NestedShapeIsRead(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeEmbedded, nestedRemoteYaml("https://github.com/org/repo.git"))

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusWarning {
		t.Errorf("Status = %q, want %q for the nested shape (%s)", check.Status, StatusWarning, check.Message)
	}
}

// Positive case (b): a server-mode rig must not carry a routine sync.remote at
// all, even when the value itself is canonical.
func TestCheckSyncRemoteShape_ServerModeRemoteWarnsAndIsFixable(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeServer, flatRemoteYaml("git+https://github.com/org/repo.git"))

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusWarning {
		t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
	}
	if check.Fix == "" {
		t.Errorf("Fix is empty; a server-mode routine sync.remote is the auto-fixable finding")
	}
	if !strings.Contains(strings.ToLower(check.Message+check.Detail), "server") {
		t.Errorf("message/detail must explain the server-mode finding: %q / %q", check.Message, check.Detail)
	}
}

// Both findings at once: the report must carry both, and say which one --fix
// acts on.
func TestCheckSyncRemoteShape_ServerModeAndUnparseableReportsBoth(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeServer, flatRemoteYaml("https://github.com/org/repo.git"))

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusWarning {
		t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
	}
	combined := strings.ToLower(check.Message + " " + check.Detail)
	if !strings.Contains(combined, "dolt remote") {
		t.Errorf("combined output must carry the unparseable-shape finding: %q / %q", check.Message, check.Detail)
	}
	if !strings.Contains(combined, "server") {
		t.Errorf("combined output must carry the server-mode finding: %q / %q", check.Message, check.Detail)
	}
	if check.Fix == "" {
		t.Errorf("Fix is empty; the server-mode finding is auto-fixable even when the value is also unparseable")
	}
	if !strings.Contains(strings.ToLower(check.Fix), "unset") && !strings.Contains(strings.ToLower(check.Fix), "remov") {
		t.Errorf("Fix = %q, want it to name the unset as the repair", check.Fix)
	}
}

// An embedded rig with a canonical remote is the healthy baseline.
func TestCheckSyncRemoteShape_EmbeddedWithCanonicalRemoteIsOK(t *testing.T) {
	tmpDir := seedSyncRemoteRig(t, configfile.DoltModeEmbedded, flatRemoteYaml("git+ssh://git@github.com/org/repo.git"))

	check := CheckSyncRemoteShape(tmpDir)

	if check.Status != StatusOK {
		t.Errorf("Status = %q, want %q (%s / %s)", check.Status, StatusOK, check.Message, check.Detail)
	}
	if check.Fix != "" {
		t.Errorf("Fix = %q, want empty", check.Fix)
	}
}
