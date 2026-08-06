package doltserver

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
)

// ServerMode describes who owns and manages the dolt sql-server lifecycle.
type ServerMode int

const (
	// ServerModeOwned means beads auto-starts and manages the server.
	// This is the default for standalone users with no explicit port config.
	ServerModeOwned ServerMode = iota

	// ServerModeExternal means the user manages the server lifecycle
	// (e.g., systemd, Docker, Hosted Dolt, VPS). Beads never starts or
	// stops the server. Determined when metadata.json carries the
	// dolt_server_lifecycle marker or an explicit dolt_server_port, or when
	// BEADS_DOLT_SHARED_SERVER is set.
	ServerModeExternal

	// ServerModeEmbedded is the legacy in-process embedded dolt path.
	// Determined when metadata.json dolt_mode is "embedded".
	ServerModeEmbedded
)

// String returns a human-readable label for the server mode.
func (m ServerMode) String() string {
	switch m {
	case ServerModeOwned:
		return "owned"
	case ServerModeExternal:
		return "external"
	case ServerModeEmbedded:
		return "embedded"
	default:
		return fmt.Sprintf("ServerMode(%d)", int(m))
	}
}

// ResolveServerMode determines the server mode from the given beadsDir.
// This is the single source of truth for how the server lifecycle is managed.
//
// Decision logic (checked in order):
//  1. BEADS_DOLT_SERVER_MODE=1 env var                -> ServerModeExternal
//  2. BEADS_DOLT_SHARED_SERVER env var is set          -> ServerModeExternal
//  3. metadata.json dolt_mode == "embedded"            -> ServerModeEmbedded
//  4. metadata.json has dolt_server_lifecycle set      -> ServerModeExternal
//  5. metadata.json has explicit dolt_server_port      -> ServerModeExternal
//  6. default                                          -> ServerModeOwned
//
// Runtime env vars (1, 2) take precedence over persisted metadata.json
// to prevent stale dolt_mode=embedded from silently overriding an active
// shared-server or server-mode configuration (GH#2949).
//
// The function loads metadata.json only if the file exists, to avoid
// triggering the legacy config.json -> metadata.json migration side effect.
func ResolveServerMode(beadsDir string) ServerMode {
	// 1-2. Runtime env vars, checked before metadata.json is even loaded.
	if mode, ok := serverModeFromEnv(); ok {
		return mode
	}

	var fileCfg *configfile.Config

	// Only load config if metadata.json exists (avoids legacy migration side effect)
	metadataPath := configfile.ConfigPath(beadsDir)
	if _, err := os.Stat(metadataPath); err == nil {
		if cfg, loadErr := configfile.Load(beadsDir); loadErr == nil && cfg != nil {
			fileCfg = cfg
		}
	}

	return serverModeFromFileConfig(fileCfg)
}

// ServerModeForConfig resolves the server mode from an already-loaded
// metadata.json config (nil means "no metadata.json"), applying the same env
// var precedence as ResolveServerMode.
//
// It exists so a caller can evaluate what a prospective metadata.json edit
// would do to the server lifecycle *before* writing it — `bd doctor`'s
// dolt_server_port cleanup uses this to refuse any edit that would flip an
// externally managed rig to beads-owned (which would let bd auto-start a
// shadow server).
func ServerModeForConfig(fileCfg *configfile.Config) ServerMode {
	if mode, ok := serverModeFromEnv(); ok {
		return mode
	}
	return serverModeFromFileConfig(fileCfg)
}

// serverModeFromEnv covers decision steps 1 and 2. ok is false when no env var
// forces a mode.
func serverModeFromEnv() (ServerMode, bool) {
	// 1. BEADS_DOLT_SERVER_MODE=1 env var -> external (explicit server mode)
	if os.Getenv("BEADS_DOLT_SERVER_MODE") == "1" {
		return ServerModeExternal, true
	}

	// 2. Shared server mode (env var or config.yaml) -> external.
	// Must be checked before metadata.json so that a stale
	// dolt_mode=embedded cannot override active shared-server intent.
	if IsSharedServerMode() {
		return ServerModeExternal, true
	}

	return ServerModeOwned, false
}

// serverModeFromFileConfig covers decision steps 3 through 6.
func serverModeFromFileConfig(fileCfg *configfile.Config) ServerMode {
	// 3. Explicit embedded mode in metadata.json. Kept above the lifecycle
	// marker: an embedded rig has no server for anyone to own, so the two
	// together are a contradiction `bd doctor` reports rather than resolves.
	if fileCfg != nil && strings.ToLower(fileCfg.DoltMode) == configfile.DoltModeEmbedded &&
		fileCfg.DoltMode != "" { // empty defaults to embedded in GetDoltMode, but we treat empty as "unset"
		return ServerModeEmbedded
	}

	// 4. Explicit lifecycle marker in metadata.json -> external. This outranks
	// dolt_server_port so an externally managed rig can drop the git-tracked
	// port key without flipping to owned.
	if fileCfg != nil && fileCfg.HasExternalServerLifecycle() {
		return ServerModeExternal
	}

	// 5. Explicit server port in metadata.json -> external. Legacy fallback for
	// rigs that predate the marker and have not run the doctor migration yet.
	if fileCfg != nil && fileCfg.DoltServerPort > 0 {
		return ServerModeExternal
	}

	// 6. Default: beads owns the server
	return ServerModeOwned
}
