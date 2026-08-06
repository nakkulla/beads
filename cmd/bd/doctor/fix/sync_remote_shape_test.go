package fix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
)

// doltModeExternalRig is a test-only marker meaning "a rig whose Dolt server
// lifecycle is external". The helpers below translate it into what actually
// makes doltserver.ResolveServerMode say External: dolt_mode=server *plus* a
// persisted dolt_server_port. Passing plain configfile.DoltModeServer therefore
// builds a bd-owned local server rig, which is the negative case — dolt_mode
// alone is not an external signal (CGO_ENABLED=0 builds write it for every rig).
const doltModeExternalRig = "external-rig"

// unroutableExternalPort is the persisted dolt_server_port an "external rig"
// fixture carries. It must not be a port a real dolt server could be listening
// on: a reachable port makes the fixture's store open for real, which turns a
// refusal test into a false pass.
const unroutableExternalPort = 1

const externalRigServerPort = unroutableExternalPort

// rigConfigFor builds the metadata.json config for a seeded rig.
func rigConfigFor(doltMode string) *configfile.Config {
	cfg := &configfile.Config{
		Database:     "beads.db",
		Backend:      configfile.BackendDolt,
		DoltMode:     doltMode,
		DoltDatabase: "beads",
	}
	if doltMode == doltModeExternalRig {
		cfg.DoltMode = configfile.DoltModeServer
		cfg.DoltServerPort = externalRigServerPort
	}
	return cfg
}

func seedRemoteShapeRig(t *testing.T, doltMode, yaml string) string {
	t.Helper()
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := rigConfigFor(doltMode)
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
		"external rig with a canonical remote": {
			doltMode:         doltModeExternalRig,
			yaml:             "sync.remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote:       "git+https://github.com/org/repo.git",
			wantServerRemote: true,
			wantUnsettable:   true,
		},
		"external rig with a plain git URL trips both": {
			doltMode:         doltModeExternalRig,
			yaml:             "sync.remote: \"https://github.com/org/repo.git\"\n",
			wantRemote:       "https://github.com/org/repo.git",
			wantNotDolt:      true,
			wantServerRemote: true,
			wantUnsettable:   true,
		},
		"external rig with no remote": {
			doltMode: doltModeExternalRig,
			yaml:     "# nothing\n",
		},
		// dolt_mode=server without an external lifecycle signal is a rig whose
		// sql-server bd starts and owns. It syncs through its remote like any
		// local rig, so (b) must stay silent — otherwise the fixer would unset
		// the remote it depends on.
		"owned local server rig is not a policy violation": {
			doltMode:   configfile.DoltModeServer,
			yaml:       "sync.remote: \"git+https://github.com/org/repo.git\"\n",
			wantRemote: "git+https://github.com/org/repo.git",
		},
		"deprecated sync.git-remote is not unsettable via sync.remote": {
			doltMode:         doltModeExternalRig,
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
	tmpDir := seedRemoteShapeRig(t, doltModeExternalRig,
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
	tmpDir := seedRemoteShapeRig(t, doltModeExternalRig,
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
	tmpDir := seedRemoteShapeRig(t, doltModeExternalRig, "# nothing\n")

	if err := SyncRemoteShape(tmpDir); err == nil {
		t.Fatalf("SyncRemoteShape succeeded with no remote configured; want a refusal")
	}
}

// An owned local server rig keeps its sync remote: unsetting it would remove
// the rig's only sync path. dolt_mode=server alone must never reach the fixer.
func TestSyncRemoteShape_RefusesOwnedServerRig(t *testing.T) {
	tmpDir := seedRemoteShapeRig(t, configfile.DoltModeServer,
		"sync.remote: \"git+https://github.com/org/repo.git\"\n")

	if err := SyncRemoteShape(tmpDir); err == nil {
		t.Fatalf("SyncRemoteShape succeeded on a bd-owned server rig; want a refusal")
	}

	content := readRigConfigYaml(t, tmpDir)
	if !strings.Contains(content, "sync.remote: \"git+https://github.com/org/repo.git\"") {
		t.Errorf("config.yaml was modified for an owned rig:\n%s", content)
	}
}
