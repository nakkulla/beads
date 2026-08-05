package fix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
)

func seedRemoteShapeRig(t *testing.T, doltMode, yaml string) string {
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

func readRigConfigYaml(t *testing.T, tmpDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmpDir, ".beads", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInspectSyncRemoteShape_Findings(t *testing.T) {
	tests := map[string]struct {
		doltMode         string
		yaml             string
		wantRemote       string
		wantNotDolt      bool
		wantServerRemote bool
		wantUnsettable   bool
	}{
		"plain git URL on embedded rig": {
			doltMode:    configfile.DoltModeEmbedded,
			yaml:        "sync.remote: \"https://github.com/org/repo.git\"\n",
			wantRemote:  "https://github.com/org/repo.git",
			wantNotDolt: true,
		},
		"git+ssh is a supported transport": {
			doltMode:   configfile.DoltModeEmbedded,
			yaml:       "sync.remote: \"git+ssh://git@github.com/org/repo.git\"\n",
			wantRemote: "git+ssh://git@github.com/org/repo.git",
		},
		"git+https is a supported transport": {
			doltMode:   configfile.DoltModeEmbedded,
			yaml:       "sync.remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote: "git+https://github.com/org/repo.git",
		},
		"server mode with a canonical remote": {
			doltMode:         configfile.DoltModeServer,
			yaml:             "sync.remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote:       "git+https://github.com/org/repo.git",
			wantServerRemote: true,
			wantUnsettable:   true,
		},
		"server mode with a plain git URL trips both": {
			doltMode:         configfile.DoltModeServer,
			yaml:             "sync.remote: \"https://github.com/org/repo.git\"\n",
			wantRemote:       "https://github.com/org/repo.git",
			wantNotDolt:      true,
			wantServerRemote: true,
			wantUnsettable:   true,
		},
		"server mode with no remote": {
			doltMode: configfile.DoltModeServer,
			yaml:     "# nothing\n",
		},
		"deprecated sync.git-remote is not unsettable via sync.remote": {
			doltMode:         configfile.DoltModeServer,
			yaml:             "sync.git-remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote:       "git+https://github.com/org/repo.git",
			wantServerRemote: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := seedRemoteShapeRig(t, tc.doltMode, tc.yaml)

			report := InspectSyncRemoteShape(filepath.Join(tmpDir, ".beads"))

			if report.Remote != tc.wantRemote {
				t.Errorf("Remote = %q, want %q", report.Remote, tc.wantRemote)
			}
			if report.NotDoltRemote != tc.wantNotDolt {
				t.Errorf("NotDoltRemote = %v, want %v", report.NotDoltRemote, tc.wantNotDolt)
			}
			if report.ServerModeRemote != tc.wantServerRemote {
				t.Errorf("ServerModeRemote = %v, want %v", report.ServerModeRemote, tc.wantServerRemote)
			}
			if report.Unsettable != tc.wantUnsettable {
				t.Errorf("Unsettable = %v, want %v", report.Unsettable, tc.wantUnsettable)
			}
		})
	}
}

func TestSyncRemoteShape_UnsetsServerModeRemote(t *testing.T) {
	tmpDir := seedRemoteShapeRig(t, configfile.DoltModeServer,
		"# Beads Configuration File\n\nbackup.enabled: false\nsync.remote: \"git+https://github.com/org/repo.git\"\n")

	if err := SyncRemoteShape(tmpDir); err != nil {
		t.Fatalf("SyncRemoteShape: %v", err)
	}

	report := InspectSyncRemoteShape(filepath.Join(tmpDir, ".beads"))
	if report.Remote != "" {
		t.Errorf("sync.remote = %q after fix, want it gone", report.Remote)
	}
	content := readRigConfigYaml(t, tmpDir)
	if !strings.Contains(content, "backup.enabled: false") {
		t.Errorf("adjacent key was lost:\n%s", content)
	}
}

func TestSyncRemoteShape_UnsetsNestedServerModeRemote(t *testing.T) {
	tmpDir := seedRemoteShapeRig(t, configfile.DoltModeServer,
		"sync:\n  remote: \"git+https://github.com/org/repo.git\"\n  branch: main\n")

	if err := SyncRemoteShape(tmpDir); err != nil {
		t.Fatalf("SyncRemoteShape: %v", err)
	}

	beadsDir := filepath.Join(tmpDir, ".beads")
	if got := config.GetStringFromDir(beadsDir, "sync.remote"); got != "" {
		t.Errorf("sync.remote = %q after fix, want it gone", got)
	}
	if got := config.GetStringFromDir(beadsDir, "sync.branch"); got != "main" {
		t.Errorf("sync.branch = %q, want %q preserved", got, "main")
	}
}

// The unparseable-shape finding is diagnostic only: the fixer must refuse it
// rather than rewrite a value the user chose.
func TestSyncRemoteShape_RefusesEmbeddedRig(t *testing.T) {
	tmpDir := seedRemoteShapeRig(t, configfile.DoltModeEmbedded,
		"sync.remote: \"https://github.com/org/repo.git\"\n")

	if err := SyncRemoteShape(tmpDir); err == nil {
		t.Fatalf("SyncRemoteShape succeeded on an embedded rig; want a refusal")
	}

	content := readRigConfigYaml(t, tmpDir)
	if !strings.Contains(content, "sync.remote: \"https://github.com/org/repo.git\"") {
		t.Errorf("config.yaml was modified on the diagnostic-only path:\n%s", content)
	}
}

func TestSyncRemoteShape_RefusesWhenNoRemoteConfigured(t *testing.T) {
	tmpDir := seedRemoteShapeRig(t, configfile.DoltModeServer, "# nothing\n")

	if err := SyncRemoteShape(tmpDir); err == nil {
		t.Fatalf("SyncRemoteShape succeeded with no remote configured; want a refusal")
	}
}
