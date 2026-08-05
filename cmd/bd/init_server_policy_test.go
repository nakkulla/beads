package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

// A1: server-mode rigs must not derive sync.remote from the git origin URL.
func TestInitShouldDeriveSyncRemoteFromGitOrigin(t *testing.T) {
	tests := []struct {
		name           string
		source         initSyncRemoteSource
		initServerMode bool
		stealth        bool
		inGitRepo      bool
		bareGitRepo    bool
		want           bool
		wantGitProbes  bool // whether the git-repo probes may run at all
	}{
		{
			name:          "embedded mode in git repo derives from origin",
			source:        initSyncRemoteNone,
			inGitRepo:     true,
			want:          true,
			wantGitProbes: true,
		},
		{
			name:           "server mode skips origin derivation",
			source:         initSyncRemoteNone,
			initServerMode: true,
			inGitRepo:      true,
			want:           false,
		},
		{
			name:           "server mode skips even in bare repo",
			source:         initSyncRemoteNone,
			initServerMode: true,
			inGitRepo:      true,
			bareGitRepo:    true,
			want:           false,
		},
		{
			name:      "stealth skips origin derivation",
			source:    initSyncRemoteNone,
			stealth:   true,
			inGitRepo: true,
			want:      false,
		},
		{
			name:          "bare git repo skips origin derivation",
			source:        initSyncRemoteNone,
			inGitRepo:     true,
			bareGitRepo:   true,
			want:          false,
			wantGitProbes: true,
		},
		{
			name:          "not a git repo skips origin derivation",
			source:        initSyncRemoteNone,
			want:          false,
			wantGitProbes: true,
		},
		{
			name:      "explicit --remote is not the origin path",
			source:    initSyncRemoteExplicit,
			inGitRepo: true,
			want:      false,
		},
		{
			name:      "configured sync.remote is not the origin path",
			source:    initSyncRemoteConfigured,
			inGitRepo: true,
			want:      false,
		},
		{
			name:           "explicit --remote in server mode is still not the origin path",
			source:         initSyncRemoteExplicit,
			initServerMode: true,
			inGitRepo:      true,
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probed := false
			inGitRepo := func() bool { probed = true; return tt.inGitRepo }
			bareGitRepo := func() bool { probed = true; return tt.bareGitRepo }

			got := shouldDeriveSyncRemoteFromGitOrigin(tt.source, tt.initServerMode, tt.stealth, inGitRepo, bareGitRepo)
			if got != tt.want {
				t.Fatalf("shouldDeriveSyncRemoteFromGitOrigin = %v, want %v", got, tt.want)
			}
			// The git probes shell out; keep them short-circuited exactly like
			// the original inline condition did.
			if !tt.wantGitProbes && probed {
				t.Fatal("git repo probes ran despite a cheap short-circuit condition")
			}
		})
	}
}

// A4: server-mode rigs default to skipping agent instruction files, but any
// explicit agents flag (including --skip-agents=false) wins.
func TestInitShouldDefaultSkipAgentsForServerMode(t *testing.T) {
	tests := []struct {
		name                  string
		initServerMode        bool
		skipAgentsChanged     bool
		agentsFileChanged     bool
		agentsTemplateChanged bool
		agentsProfileChanged  bool
		want                  bool
	}{
		{
			name:           "server mode with no agents flags defaults to skip",
			initServerMode: true,
			want:           true,
		},
		{
			name: "embedded mode never defaults to skip",
			want: false,
		},
		{
			name:                  "embedded mode with agents flags never defaults to skip",
			agentsFileChanged:     true,
			agentsTemplateChanged: true,
			want:                  false,
		},
		{
			name:              "explicit --skip-agents wins",
			initServerMode:    true,
			skipAgentsChanged: true,
			want:              false,
		},
		{
			name:              "explicit --agents-file wins",
			initServerMode:    true,
			agentsFileChanged: true,
			want:              false,
		},
		{
			name:                  "explicit --agents-template wins",
			initServerMode:        true,
			agentsTemplateChanged: true,
			want:                  false,
		},
		{
			name:                 "explicit --agents-profile wins",
			initServerMode:       true,
			agentsProfileChanged: true,
			want:                 false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDefaultSkipAgentsForServerMode(tt.initServerMode, tt.skipAgentsChanged, tt.agentsFileChanged, tt.agentsTemplateChanged, tt.agentsProfileChanged)
			if got != tt.want {
				t.Fatalf("shouldDefaultSkipAgentsForServerMode = %v, want %v", got, tt.want)
			}
		})
	}
}

// A3: server-mode init seeds backup.enabled=false only when the key is absent.
func TestInitShouldSeedServerModeBackupDisabled(t *testing.T) {
	tests := []struct {
		name           string
		initServerMode bool
		existing       string
		want           bool
	}{
		{
			name:           "server mode with absent key seeds the default",
			initServerMode: true,
			want:           true,
		},
		{
			name:           "server mode preserves explicit true",
			initServerMode: true,
			existing:       "true",
			want:           false,
		},
		{
			name:           "server mode preserves explicit false",
			initServerMode: true,
			existing:       "false",
			want:           false,
		},
		{
			name:     "embedded mode never seeds",
			existing: "",
			want:     false,
		},
		{
			name:     "embedded mode with explicit true never seeds",
			existing: "true",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSeedServerModeBackupDisabled(tt.initServerMode, tt.existing)
			if got != tt.want {
				t.Fatalf("shouldSeedServerModeBackupDisabled = %v, want %v", got, tt.want)
			}
		})
	}
}

// A3 wiring: exercise the seeding helper against a real config.yaml. Covers both
// key shapes that config.SetYamlConfigInDir can produce — nested (when the file
// already has an uncommented mapping) and flat (comment-only init template).
func TestInitApplyServerModeBackupDefault(t *testing.T) {
	tests := []struct {
		name           string
		initServerMode bool
		configYaml     string
		wantSeeded     bool
		wantValue      string
	}{
		{
			name:           "absent key is seeded to false",
			initServerMode: true,
			configYaml:     "# Beads Config\n",
			wantSeeded:     true,
			wantValue:      "false",
		},
		{
			name:           "commented template key counts as absent",
			initServerMode: true,
			configYaml:     "# Beads Config\n# backup:\n#   enabled: false\n",
			wantSeeded:     true,
			wantValue:      "false",
		},
		{
			name:           "explicit nested true is preserved",
			initServerMode: true,
			configYaml:     "backup:\n  enabled: true\n",
			wantSeeded:     false,
			wantValue:      "true",
		},
		{
			name:           "explicit nested false is left alone",
			initServerMode: true,
			configYaml:     "backup:\n  enabled: false\n",
			wantSeeded:     false,
			wantValue:      "false",
		},
		{
			name:           "explicit flat true is preserved",
			initServerMode: true,
			configYaml:     "# Beads Config\nbackup.enabled: true\n",
			wantSeeded:     false,
			wantValue:      "true",
		},
		{
			name:           "explicit flat false is left alone",
			initServerMode: true,
			configYaml:     "# Beads Config\nbackup.enabled: false\n",
			wantSeeded:     false,
			wantValue:      "false",
		},
		{
			name:           "embedded mode writes nothing",
			initServerMode: false,
			configYaml:     "# Beads Config\n",
			wantSeeded:     false,
			wantValue:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			if err := os.MkdirAll(beadsDir, 0o750); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(beadsDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(tt.configYaml), 0o600); err != nil {
				t.Fatal(err)
			}

			seeded, err := applyServerModeBackupDefault(beadsDir, tt.initServerMode)
			if err != nil {
				t.Fatalf("applyServerModeBackupDefault failed: %v", err)
			}
			if seeded != tt.wantSeeded {
				t.Fatalf("seeded = %v, want %v", seeded, tt.wantSeeded)
			}
			if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != tt.wantValue {
				t.Fatalf("backup.enabled = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

// A3: the absent-key probe must see a value written by config.SetYamlConfigInDir
// regardless of which key shape that write produced — a second server-mode init
// must not re-seed over an existing value.
func TestInitBackupDefaultIsIdempotent(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The real init template is comment-only, which is what forces the flat
	// key shape on the first write.
	if err := createConfigYaml(beadsDir, false, ""); err != nil {
		t.Fatal(err)
	}

	seeded, err := applyServerModeBackupDefault(beadsDir, true)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if !seeded {
		t.Fatal("first server-mode apply did not seed backup.enabled")
	}
	if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != "false" {
		t.Fatalf("after seeding, backup.enabled = %q, want %q", got, "false")
	}

	// A user flips it on, then re-inits.
	if err := config.SetYamlConfigInDir(beadsDir, "backup.enabled", "true"); err != nil {
		t.Fatal(err)
	}
	seeded, err = applyServerModeBackupDefault(beadsDir, true)
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	if seeded {
		t.Fatal("second apply overwrote an existing backup.enabled value")
	}
	if got := existingConfigYamlValue(beadsDir, "backup.enabled"); got != "true" {
		t.Fatalf("after re-init, backup.enabled = %q, want %q", got, "true")
	}
}
