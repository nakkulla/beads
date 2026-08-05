package doltserver

import (
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
)

// TestServerModeForConfig_MatchesResolveServerMode pins the refactor that split
// ResolveServerMode into an env stage and a metadata stage: for every metadata
// shape, deciding from an in-memory config must agree with deciding from disk.
func TestServerModeForConfig_MatchesResolveServerMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *configfile.Config
		want ServerMode
	}{
		{"no metadata", nil, ServerModeOwned},
		{"empty metadata", &configfile.Config{}, ServerModeOwned},
		{"embedded", &configfile.Config{DoltMode: configfile.DoltModeEmbedded}, ServerModeEmbedded},
		{"embedded with port", &configfile.Config{DoltMode: configfile.DoltModeEmbedded, DoltServerPort: 3307}, ServerModeEmbedded},
		{"explicit port", &configfile.Config{DoltServerPort: 3307}, ServerModeExternal},
		{"server mode with port", &configfile.Config{DoltMode: configfile.DoltModeServer, DoltServerPort: 13307}, ServerModeExternal},
		// Regression anchor: server mode WITHOUT an explicit port stays Owned.
		// bd init writes dolt_mode=server for every CGO_ENABLED=0 build and for
		// `bd init --server`, so classifying it External here would disable
		// auto-start (dolt.resolveAutoStart) for those rigs.
		{"server mode without port", &configfile.Config{DoltMode: configfile.DoltModeServer}, ServerModeOwned},
		{"proxied server", &configfile.Config{DoltMode: configfile.DoltModeProxiedServer}, ServerModeOwned},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
			t.Setenv("BEADS_DOLT_SERVER_MODE", "")
			config.ResetForTesting()

			dir := t.TempDir()
			if tc.cfg != nil {
				if err := tc.cfg.Save(dir); err != nil {
					t.Fatal(err)
				}
			}

			fromDisk := ResolveServerMode(dir)
			fromConfig := ServerModeForConfig(tc.cfg)

			if fromDisk != tc.want {
				t.Errorf("ResolveServerMode = %v, want %v", fromDisk, tc.want)
			}
			if fromConfig != fromDisk {
				t.Errorf("ServerModeForConfig = %v, ResolveServerMode = %v; must agree", fromConfig, fromDisk)
			}
		})
	}
}

func TestServerModeForConfig_EnvOverridesMetadata(t *testing.T) {
	t.Run("BEADS_DOLT_SERVER_MODE=1 beats stale embedded", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
		t.Setenv("BEADS_DOLT_SERVER_MODE", "1")
		config.ResetForTesting()

		got := ServerModeForConfig(&configfile.Config{DoltMode: configfile.DoltModeEmbedded})
		if got != ServerModeExternal {
			t.Errorf("ServerModeForConfig = %v, want ServerModeExternal", got)
		}
	})

	t.Run("shared server beats stale embedded", func(t *testing.T) {
		t.Setenv("BEADS_DOLT_SERVER_MODE", "")
		t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
		config.ResetForTesting()

		got := ServerModeForConfig(&configfile.Config{DoltMode: configfile.DoltModeEmbedded})
		if got != ServerModeExternal {
			t.Errorf("ServerModeForConfig = %v, want ServerModeExternal", got)
		}
	})
}
