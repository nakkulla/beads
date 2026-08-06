package fix

import (
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

// ExternalLifecyclePinReport describes whether a rig still relies on
// dolt_server_port alone to be classified as externally managed, and can
// therefore be migrated to the explicit dolt_server_lifecycle marker.
type ExternalLifecyclePinReport struct {
	// Applicable is false for rigs the check does not apply to; SkipReason
	// then says why.
	Applicable bool
	SkipReason string

	// StoredPort is the metadata.json dolt_server_port that is currently the
	// rig's only persisted External signal.
	StoredPort int

	// UnrecognizedMarker holds the raw dolt_server_lifecycle value when it is
	// non-empty but not a value this bd knows. Diagnostic only: the rig is
	// already running External by the fail-safe in
	// configfile.Config.HasExternalServerLifecycle, so there is nothing to fix.
	UnrecognizedMarker string

	// EmbeddedWithMarker is true when dolt_mode is embedded and the lifecycle
	// marker is set. Diagnostic only: embedded wins in ResolveServerMode, and
	// picking a side automatically could either start a shadow server or take a
	// working rig offline.
	EmbeddedWithMarker bool
}

// InspectExternalLifecyclePin decides whether `bd doctor --fix` should write the
// explicit dolt_server_lifecycle marker into metadata.json.
//
// Gate: configfile.Config.IsDoltServerMode() (dolt_mode based), matching the
// port-drift check. doltserver.ResolveServerMode is not the gate — it is what
// this fix exists to make independent of dolt_server_port.
func InspectExternalLifecyclePin(beadsDir string) ExternalLifecyclePinReport {
	report := ExternalLifecyclePinReport{}

	if doltserver.IsSharedServerMode() {
		// Shared-server rigs resolve External from runtime env/config.yaml, and
		// metadata.json is git-tracked, so pinning here would propagate the
		// verdict to clones that have no shared server.
		report.SkipReason = "shared-server mode (the lifecycle verdict is not owned by this rig)"
		return report
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		report.SkipReason = "no metadata.json"
		return report
	}

	if cfg.HasExternalServerLifecycle() {
		if normalized := strings.ToLower(strings.TrimSpace(cfg.DoltServerLifecycle)); normalized != configfile.DoltServerLifecycleExternal {
			report.UnrecognizedMarker = cfg.DoltServerLifecycle
		}
		if strings.ToLower(cfg.DoltMode) == configfile.DoltModeEmbedded && cfg.DoltMode != "" {
			report.EmbeddedWithMarker = true
		}
	}

	if !cfg.IsDoltServerMode() {
		report.SkipReason = fmt.Sprintf("dolt_mode is %q, not server", cfg.GetDoltMode())
		return report
	}
	if cfg.HasExternalServerLifecycle() {
		report.SkipReason = "metadata.json already pins dolt_server_lifecycle"
		return report
	}

	report.StoredPort = cfg.DoltServerPort
	if report.StoredPort <= 0 {
		report.SkipReason = "metadata.json has no dolt_server_port (nothing classifies this rig as external)"
		return report
	}

	report.Applicable = true
	return report
}

// ExternalLifecyclePin records the explicit dolt_server_lifecycle marker on a
// rig whose only persisted External signal is dolt_server_port.
//
// This is the migration path for rigs created before the marker existed. It
// only adds a signal — the stale port key is left alone, because removing it is
// the port-drift fixer's decision and it needs a higher-authority port source
// to defer to. Both run from `bd doctor --fix`, but the worklist is fixed when
// the checks are evaluated, so the port removal becomes available on the next
// run, once this pin is already on disk.
func ExternalLifecyclePin(path string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	report := InspectExternalLifecyclePin(beadsDir)
	if !report.Applicable {
		return fmt.Errorf("external lifecycle pin not applicable: %s", report.SkipReason)
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no metadata.json found")
	}

	fmt.Printf("  Pinning the external dolt sql-server lifecycle in metadata.json (dolt_server_lifecycle=%s)\n",
		configfile.DoltServerLifecycleExternal)
	fmt.Printf("  This rig was classified external by dolt_server_port %d alone; the marker makes that explicit.\n",
		report.StoredPort)

	cfg.DoltServerLifecycle = configfile.DoltServerLifecycleExternal
	if err := cfg.Save(beadsDir); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("  A following 'bd doctor --fix' run can now drop a stale dolt_server_port key.\n")
	return nil
}
