package fix

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

func seedDriftRig(t *testing.T, doltMode string, storedPort, portFile int) string {
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

func rawMetadata(t *testing.T, tmpDir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tmpDir, ".beads", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDoltPortDrift_RemovesStaleKey(t *testing.T) {
	tmpDir := seedDriftRig(t, configfile.DoltModeServer, 13307, 3399)
	// External is pinned by env, so the removal cannot flip the lifecycle.
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")
	beadsDir := filepath.Join(tmpDir, ".beads")

	before := doltserver.ResolveServerMode(beadsDir)

	if err := DoltPortDrift(tmpDir); err != nil {
		t.Fatalf("DoltPortDrift: %v", err)
	}

	meta := rawMetadata(t, tmpDir)
	if _, present := meta["dolt_server_port"]; present {
		t.Errorf("dolt_server_port still present after fix: %v", meta["dolt_server_port"])
	}
	// The stale key must be removed, never rewritten with the effective port.
	if got, ok := meta["dolt_mode"]; !ok || got != configfile.DoltModeServer {
		t.Errorf("dolt_mode = %v, want %q preserved", got, configfile.DoltModeServer)
	}
	if after := doltserver.ResolveServerMode(beadsDir); after != before {
		t.Errorf("lifecycle changed: before=%v after=%v", before, after)
	}
}

func TestDoltPortDrift_RefusesWhenRemovalWouldFlipLifecycle(t *testing.T) {
	tmpDir := seedDriftRig(t, configfile.DoltModeServer, 13307, 3399)
	beadsDir := filepath.Join(tmpDir, ".beads")

	before := doltserver.ResolveServerMode(beadsDir)
	if before != doltserver.ServerModeExternal {
		t.Fatalf("precondition: mode = %v, want External", before)
	}

	err := DoltPortDrift(tmpDir)
	if err == nil {
		t.Fatalf("DoltPortDrift succeeded; removing the only External signal must be refused")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "lifecycle") {
		t.Errorf("error = %v, want it to explain the lifecycle refusal", err)
	}

	meta := rawMetadata(t, tmpDir)
	if got, present := meta["dolt_server_port"]; !present || int(got.(float64)) != 13307 {
		t.Errorf("dolt_server_port = %v, want it left at 13307", got)
	}
	if after := doltserver.ResolveServerMode(beadsDir); after != before {
		t.Errorf("lifecycle changed: before=%v after=%v", before, after)
	}
}

// TestDoltPortDrift_LifecycleMarkerUnblocksRemoval is the beads-ode completion
// criterion: with dolt_server_lifecycle pinned, dropping the stale
// dolt_server_port no longer flips the rig to Owned, so the guard that refuses
// removal in TestDoltPortDrift_RefusesWhenRemovalWouldFlipLifecycle stands down.
// No env var is set here — the marker alone carries the External verdict.
func TestDoltPortDrift_LifecycleMarkerUnblocksRemoval(t *testing.T) {
	tmpDir := seedDriftRig(t, configfile.DoltModeServer, 13307, 3399)
	beadsDir := filepath.Join(tmpDir, ".beads")

	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		t.Fatalf("load seeded config: %v", err)
	}
	cfg.DoltServerLifecycle = configfile.DoltServerLifecycleExternal
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}

	report := InspectDoltPortDrift(beadsDir)
	if !report.Removable {
		t.Fatalf("Removable = false with the lifecycle marker pinned: %s", report.BlockedReason)
	}
	if report.ModeBefore != doltserver.ServerModeExternal || report.ModeAfter != doltserver.ServerModeExternal {
		t.Fatalf("simulated lifecycle before=%v after=%v, want External on both sides", report.ModeBefore, report.ModeAfter)
	}

	if err := DoltPortDrift(tmpDir); err != nil {
		t.Fatalf("DoltPortDrift: %v", err)
	}

	meta := rawMetadata(t, tmpDir)
	if _, present := meta["dolt_server_port"]; present {
		t.Errorf("dolt_server_port still present after fix: %v", meta["dolt_server_port"])
	}
	if got := meta["dolt_server_lifecycle"]; got != configfile.DoltServerLifecycleExternal {
		t.Errorf("dolt_server_lifecycle = %v, want it preserved as %q", got, configfile.DoltServerLifecycleExternal)
	}
	if after := doltserver.ResolveServerMode(beadsDir); after != doltserver.ServerModeExternal {
		t.Errorf("mode after removal = %v, want External (auto-start must stay off)", after)
	}
}

func TestDoltPortDrift_NoDrift(t *testing.T) {
	tmpDir := seedDriftRig(t, configfile.DoltModeServer, 13307, 13307)
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")

	if err := DoltPortDrift(tmpDir); err == nil {
		t.Fatalf("DoltPortDrift succeeded with no drift; want an error")
	}

	meta := rawMetadata(t, tmpDir)
	if _, present := meta["dolt_server_port"]; !present {
		t.Errorf("dolt_server_port was removed even though there is no drift")
	}
}

func TestDoltPortDrift_EmbeddedRigIsRefused(t *testing.T) {
	tmpDir := seedDriftRig(t, configfile.DoltModeEmbedded, 13307, 3399)

	if err := DoltPortDrift(tmpDir); err == nil {
		t.Fatalf("DoltPortDrift succeeded on an embedded rig; want an error")
	}

	meta := rawMetadata(t, tmpDir)
	if _, present := meta["dolt_server_port"]; !present {
		t.Errorf("dolt_server_port was removed on an embedded rig")
	}
}

func TestDoltPortDrift_NoHigherAuthorityIsRefused(t *testing.T) {
	// Stored key with no env / port file above it: DefaultConfig falls back to
	// the metadata value, so there is nothing to defer to.
	tmpDir := seedDriftRig(t, configfile.DoltModeServer, 13307, 0)
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")

	if err := DoltPortDrift(tmpDir); err == nil {
		t.Fatalf("DoltPortDrift succeeded with no higher-authority port source; want an error")
	}

	meta := rawMetadata(t, tmpDir)
	if _, present := meta["dolt_server_port"]; !present {
		t.Errorf("dolt_server_port was removed with no higher-authority source")
	}
}
