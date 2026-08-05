package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

// seedPortDriftRig writes a server-mode metadata.json with storedPort and,
// when portFile > 0, a higher-authority dolt-server.port file.
func seedPortDriftRig(t *testing.T, doltMode string, storedPort, portFile int) string {
	t.Helper()
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	tmpDir := t.TempDir()
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &configfile.Config{
		Database:       "beads.db",
		Backend:        configfile.BackendDolt,
		DoltMode:       doltMode,
		DoltDatabase:   "beads",
		DoltServerHost: "127.0.0.1",
		DoltServerPort: storedPort,
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if portFile > 0 {
		if err := os.WriteFile(filepath.Join(beadsDir, doltserver.PortFileName), []byte(strconv.Itoa(portFile)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return tmpDir
}

func TestCheckDoltPortDrift(t *testing.T) {
	t.Run("no beads dir", func(t *testing.T) {
		check := CheckDoltPortDrift(t.TempDir())
		if check.Name != DoltPortDriftCheckName {
			t.Errorf("Name = %q, want %q", check.Name, DoltPortDriftCheckName)
		}
		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
		}
	})

	t.Run("drift detected", func(t *testing.T) {
		tmpDir := seedPortDriftRig(t, configfile.DoltModeServer, 13307, 3399)

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusWarning {
			t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
		}
		if !strings.Contains(check.Message+check.Detail, "13307") || !strings.Contains(check.Message+check.Detail, "3399") {
			t.Errorf("message/detail must name both ports: %q / %q", check.Message, check.Detail)
		}
		if !strings.Contains(check.Detail, doltserver.PortFileName) {
			t.Errorf("Detail = %q, want the authority source (%s) named", check.Detail, doltserver.PortFileName)
		}
		// dolt_server_port is the only persisted signal that keeps this rig
		// External, so removing it would flip the lifecycle to Owned. The
		// check must stay diagnostic-only (empty Fix keeps it out of the
		// doctor --fix worklist) and say why.
		if check.Fix != "" {
			t.Errorf("Fix = %q, want empty: removing the key here would flip the lifecycle", check.Fix)
		}
		if !strings.Contains(strings.ToLower(check.Detail), "lifecycle") {
			t.Errorf("Detail = %q, want the lifecycle refusal explained", check.Detail)
		}
	})

	t.Run("fixable when external lifecycle is pinned independently", func(t *testing.T) {
		tmpDir := seedPortDriftRig(t, configfile.DoltModeServer, 13307, 3399)
		// BEADS_DOLT_SERVER_MODE=1 pins ServerModeExternal independently of
		// metadata.json, so dropping the stale key cannot flip the lifecycle.
		t.Setenv("BEADS_DOLT_SERVER_MODE", "1")

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusWarning {
			t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
		}
		if check.Fix == "" {
			t.Errorf("Fix is empty; the stale key is safe to remove when External is pinned")
		}
	})

	t.Run("no drift when stored equals effective", func(t *testing.T) {
		tmpDir := seedPortDriftRig(t, configfile.DoltModeServer, 13307, 13307)

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
		}
	})

	t.Run("no stored key is not drift", func(t *testing.T) {
		tmpDir := seedPortDriftRig(t, configfile.DoltModeServer, 0, 3399)

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q (%s)", check.Status, StatusOK, check.Message)
		}
	})

	t.Run("embedded mode rigs are skipped", func(t *testing.T) {
		// Gate is configfile.Config.IsDoltServerMode(): an embedded rig with a
		// leftover port must not be reported.
		tmpDir := seedPortDriftRig(t, configfile.DoltModeEmbedded, 13307, 3399)

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusOK {
			t.Errorf("Status = %q, want %q for embedded mode (%s)", check.Status, StatusOK, check.Message)
		}
	})

	t.Run("env port is reported as the authority", func(t *testing.T) {
		tmpDir := seedPortDriftRig(t, configfile.DoltModeServer, 13307, 0)
		t.Setenv("BEADS_DOLT_SERVER_PORT", "3401")

		check := CheckDoltPortDrift(tmpDir)

		if check.Status != StatusWarning {
			t.Fatalf("Status = %q, want %q (%s)", check.Status, StatusWarning, check.Message)
		}
		if !strings.Contains(check.Detail, "BEADS_DOLT_SERVER_PORT") {
			t.Errorf("Detail = %q, want the env authority named", check.Detail)
		}
	})
}
