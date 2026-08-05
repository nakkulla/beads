package fix

import (
	"fmt"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltremote"
)

// Config keys resolveSyncRemote consults, in precedence order.
const (
	SyncRemoteKey       = "sync.remote"
	SyncRemoteLegacyKey = "sync.git-remote"
)

// SyncRemoteShapeReport describes what is wrong with a rig's configured sync
// remote, if anything.
type SyncRemoteShapeReport struct {
	// Remote is the effective sync remote, "" when none is configured.
	Remote string
	// SourceKey is the config.yaml key Remote came from.
	SourceKey string
	// Normalized is the Dolt-compatible form of Remote.
	Normalized string

	// ServerMode reports whether the rig talks to an external Dolt server.
	ServerMode bool

	// NotDoltRemote is finding (a): the configured value is not already a
	// Dolt remote, so Dolt's remote parser would have to rewrite it.
	NotDoltRemote bool
	// ServerModeRemote is finding (b): a server-mode rig carries a routine
	// sync remote at all. True even when the value itself is canonical.
	ServerModeRemote bool

	// Unsettable reports whether finding (b) can be repaired by unsetting
	// SyncRemoteKey. It is false when the value lives in the deprecated
	// fallback key, which this fixer does not own.
	Unsettable bool
}

// InspectSyncRemoteShape reports both sync-remote findings for a rig.
//
// (a) NotDoltRemote is decided by doltremote.Normalize: a value that Normalize
// leaves untouched is already one of Dolt's supported transports (git+https,
// git+ssh, git+http, git+file, dolthub, file, aws, gs) and is silent — GitHub
// hosts included. A value Normalize rewrites (plain https://github.com/...,
// scp-style git@host:path) is not a Dolt remote as written, and bootstrap
// passes the configured value to dolt verbatim (bootstrap.go trusts the URL
// as-is so remotesapi endpoints survive, GH#3339). That same GH#3339 exception
// is why (a) is diagnostic only: a plain http(s) URL can legitimately be a
// self-hosted remotesapi endpoint, so bd reports the suspicious shape and
// leaves the value alone.
//
// (b) ServerModeRemote gates on configfile.Config.IsDoltServerMode(): a rig
// backed by an external Dolt server syncs through that server, so a routine
// sync remote is a policy violation regardless of the value's shape.
func InspectSyncRemoteShape(beadsDir string) SyncRemoteShapeReport {
	report := SyncRemoteShapeReport{}

	if cfg, err := configfile.Load(beadsDir); err == nil && cfg != nil {
		report.ServerMode = cfg.IsDoltServerMode()
	}

	report.Remote, report.SourceKey = resolveSyncRemoteInDir(beadsDir)
	if report.Remote == "" {
		return report
	}

	report.Normalized = doltremote.Normalize(report.Remote)
	report.NotDoltRemote = report.Normalized != report.Remote
	report.ServerModeRemote = report.ServerMode
	report.Unsettable = report.ServerModeRemote && report.SourceKey == SyncRemoteKey

	return report
}

// resolveSyncRemoteInDir mirrors the CLI's resolveSyncRemoteFromDir key order
// (sync.remote, then the deprecated sync.git-remote) and returns the key the
// value came from. Reads are shape-tolerant because `bd init` persists the flat
// root key form.
func resolveSyncRemoteInDir(beadsDir string) (string, string) {
	for _, key := range []string{SyncRemoteKey, SyncRemoteLegacyKey} {
		if value := config.GetStringFromDirAnyShape(beadsDir, key); value != "" {
			return value, key
		}
	}
	return "", ""
}

// SyncRemoteShape removes the routine sync.remote from a server-mode rig.
//
// Only finding (b) is repaired. An unparseable remote is never rewritten: bd
// does not know which remote the user meant, and the value could be a valid
// remotesapi endpoint (GH#3339).
func SyncRemoteShape(path string) error {
	beadsDir, err := resolvedWorkspaceBeadsDir(path)
	if err != nil {
		return err
	}

	report := InspectSyncRemoteShape(beadsDir)
	switch {
	case report.Remote == "":
		return fmt.Errorf("no sync remote is configured; nothing to unset")
	case !report.ServerModeRemote:
		return fmt.Errorf("refusing to unset %s: this rig is not in Dolt server mode, so its sync remote is not a policy violation "+
			"(an unparseable remote is reported for review, never rewritten)", report.SourceKey)
	case !report.Unsettable:
		return fmt.Errorf("refusing to unset %s: the remote is configured under the deprecated %s key; remove it manually",
			SyncRemoteKey, report.SourceKey)
	}

	fmt.Printf("  Removing routine %s (%s) — a server-mode rig syncs through its Dolt server\n", SyncRemoteKey, report.Remote)

	if err := config.UnsetYamlConfigInDir(beadsDir, SyncRemoteKey); err != nil {
		return fmt.Errorf("failed to unset %s: %w", SyncRemoteKey, err)
	}

	if remaining := config.GetStringFromDirAnyShape(beadsDir, SyncRemoteKey); remaining != "" {
		return fmt.Errorf("%s is still set to %q after the unset", SyncRemoteKey, remaining)
	}

	return nil
}
