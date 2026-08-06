package fix

import (
	"fmt"
	"os"
	"strconv"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
)

// Authority source labels, in doltserver.DefaultConfig priority order.
const (
	PortAuthorityEnv        = "BEADS_DOLT_SERVER_PORT env var"
	PortAuthorityPortFile   = doltserver.PortFileName
	PortAuthorityConfigYaml = "config.yaml dolt.port"
	PortAuthorityMetadata   = "metadata.json dolt_server_port"
	PortAuthorityUnknown    = "unknown"
)

// DoltPortDriftReport describes the relationship between the git-tracked
// metadata.json dolt_server_port and the port bd actually connects on.
type DoltPortDriftReport struct {
	// Applicable is false for rigs the check does not apply to; SkipReason
	// then says why.
	Applicable bool
	SkipReason string

	StoredPort      int
	EffectivePort   int
	AuthoritySource string

	// Drift is true when the stored key disagrees with the effective port.
	Drift bool

	// Removable is true when the stale key can be dropped safely. When it is
	// false, BlockedReason explains the refusal.
	Removable     bool
	BlockedReason string

	ModeBefore doltserver.ServerMode
	ModeAfter  doltserver.ServerMode
}

// InspectDoltPortDrift compares the stored metadata.json dolt_server_port with
// the effective port from doltserver.DefaultConfig's authority chain
// (env -> dolt-server.port -> config.yaml -> metadata.json).
//
// Gate: configfile.Config.IsDoltServerMode() (dolt_mode based).
// doltserver.ResolveServerMode is deliberately NOT used as the gate — it
// classifies any rig carrying an explicit dolt_server_port as External, which
// is exactly the stale key under investigation, so it would be both circular
// and over-broad here.
func InspectDoltPortDrift(beadsDir string) DoltPortDriftReport {
	report := DoltPortDriftReport{}

	if doltserver.IsSharedServerMode() {
		// In shared-server mode the effective port belongs to the shared
		// server directory, not this rig; comparing the two is meaningless.
		report.SkipReason = "shared-server mode (port is owned by the shared server directory)"
		return report
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil {
		report.SkipReason = "no metadata.json"
		return report
	}
	if !cfg.IsDoltServerMode() {
		report.SkipReason = fmt.Sprintf("dolt_mode is %q, not server", cfg.GetDoltMode())
		return report
	}

	report.Applicable = true
	report.StoredPort = cfg.DoltServerPort
	if report.StoredPort == 0 {
		report.SkipReason = "metadata.json has no dolt_server_port"
		return report
	}

	report.EffectivePort = doltserver.DefaultConfig(beadsDir).Port
	report.AuthoritySource = resolvePortAuthority(beadsDir, report.EffectivePort)

	if report.EffectivePort == report.StoredPort {
		return report
	}
	report.Drift = true

	// Only defer to a source that outranks metadata.json. Without one there is
	// nothing to fall back to and dropping the key would lose the port.
	switch report.AuthoritySource {
	case PortAuthorityEnv, PortAuthorityPortFile, PortAuthorityConfigYaml:
	default:
		report.BlockedReason = fmt.Sprintf("no higher-authority port source resolves to %d", report.EffectivePort)
		return report
	}

	// Lifecycle guard. dolt_server_port is also the persisted signal that makes
	// ResolveServerMode classify a rig as External, so removing it can flip an
	// externally managed rig to Owned — after which bd would auto-start its own
	// server from .beads/dolt (the shadow database failure mode). Simulate the
	// edit and refuse when the verdict changes.
	report.ModeBefore = doltserver.ServerModeForConfig(cfg)
	after := *cfg
	after.DoltServerPort = 0
	report.ModeAfter = doltserver.ServerModeForConfig(&after)
	if report.ModeAfter != report.ModeBefore {
		report.BlockedReason = fmt.Sprintf(
			"removing dolt_server_port would change the server lifecycle from %s to %s; "+
				"pin the external lifecycle first (e.g. BEADS_DOLT_SERVER_MODE=1) or update the port manually",
			report.ModeBefore, report.ModeAfter)
		return report
	}

	report.Removable = true
	return report
}

// resolvePortAuthority labels which source in doltserver.DefaultConfig's chain
// produced effectivePort. Returns PortAuthorityUnknown when no source matches
// (which keeps the caller from acting on a guess).
func resolvePortAuthority(beadsDir string, effectivePort int) string {
	if v := os.Getenv("BEADS_DOLT_SERVER_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port == effectivePort {
			return PortAuthorityEnv
		}
		// The env var short-circuits DefaultConfig even when malformed, so no
		// lower source can be the authority.
		return PortAuthorityUnknown
	}
	if port := doltserver.ReadPortFile(beadsDir); port > 0 && port == effectivePort {
		return PortAuthorityPortFile
	}
	// Read the target's own config.yaml, not the caller's. DefaultConfig
	// consults the process-wide viper view, so under `bd doctor <other-repo>`
	// the effective port can come from the caller's config — and this fixer
	// deletes a key from the *target*. Scoping the lookup to beadsDir means a
	// foreign value simply fails to match, leaving PortAuthorityUnknown, which
	// the caller treats as "no higher-authority source" and refuses to act on.
	if v := config.GetStringFromDirAnyShape(beadsDir, "dolt.port"); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 && port == effectivePort {
			return PortAuthorityConfigYaml
		}
	}
	if cfg, err := configfile.Load(beadsDir); err == nil && cfg != nil && cfg.DoltServerPort == effectivePort {
		return PortAuthorityMetadata
	}
	return PortAuthorityUnknown
}

// DoltPortDrift removes a stale metadata.json dolt_server_port when a
// higher-authority source already supplies the effective port.
//
// The key is removed, never rewritten with the effective value: metadata.json
// is git-tracked, so re-persisting a runtime port re-publishes a deprecated
// source and leaks one project's port to every clone (GH#2372).
func DoltPortDrift(path string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	report := InspectDoltPortDrift(beadsDir)
	if !report.Applicable {
		return fmt.Errorf("port drift fix not applicable: %s", report.SkipReason)
	}
	if !report.Drift {
		if report.SkipReason != "" {
			return fmt.Errorf("port drift fix not applicable: %s", report.SkipReason)
		}
		return fmt.Errorf("no port drift detected (metadata.json and the effective port both say %d)", report.StoredPort)
	}
	if !report.Removable {
		return fmt.Errorf("refusing to remove dolt_server_port: %s", report.BlockedReason)
	}

	cfg, err := configfile.Load(beadsDir)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no metadata.json found")
	}

	fmt.Printf("  Removing stale dolt_server_port %d from metadata.json (effective port %d comes from %s)\n",
		report.StoredPort, report.EffectivePort, report.AuthoritySource)

	cfg.DoltServerPort = 0
	if err := cfg.Save(beadsDir); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}
