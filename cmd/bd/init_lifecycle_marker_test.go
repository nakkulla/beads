// Decision seam for the bd init external-lifecycle marker
// (docs/superpowers/specs/2026-08-06-external-lifecycle-marker-design.md §3).
//
// The seam is a pure predicate plus the config builder it feeds, so the
// recording rule is pinned without a live dolt sql-server. The command-level
// acceptance cases in init_server_mode_acceptance_test.go (//go:build cgo) are
// supplementary integration coverage, not the authority for this rule.

package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
)

func TestShouldRecordExternalLifecycle(t *testing.T) {
	cases := []struct {
		name            string
		portFlagChanged bool
		sharedServer    bool
		want            bool
	}{
		// The marker records user-managed lifecycle intent, and an explicitly
		// passed --server-port is the only init-time signal for it.
		{"explicit --server-port on a non-shared rig", true, false, true},
		// `bd init --server` alone is an owned auto-start rig, and every
		// CGO_ENABLED=0 build writes dolt_mode=server regardless — neither is
		// an external signal.
		{"no --server-port", false, false, false},
		// Shared-server rigs resolve External at runtime from env/config.yaml.
		// A git-tracked marker would wrongly propagate to clones that have no
		// shared-server configuration.
		{"shared server with --server-port", true, true, false},
		{"shared server without --server-port", false, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRecordExternalLifecycle(tc.portFlagChanged, tc.sharedServer); got != tc.want {
				t.Errorf("shouldRecordExternalLifecycle(%v, %v) = %v, want %v",
					tc.portFlagChanged, tc.sharedServer, got, tc.want)
			}
		})
	}
}

// TestInitTimeCloneConfigLifecycleMarker covers the remote-bootstrap init path,
// which builds its metadata.json separately from the local one.
func TestInitTimeCloneConfigLifecycleMarker(t *testing.T) {
	withMarker := initTimeCloneConfig(true, "", 3312, "", "", "beads_proj", true)
	if got := withMarker.DoltServerLifecycle; got != configfile.DoltServerLifecycleExternal {
		t.Errorf("dolt_server_lifecycle = %q, want %q", got, configfile.DoltServerLifecycleExternal)
	}
	if !withMarker.HasExternalServerLifecycle() {
		t.Error("HasExternalServerLifecycle() = false on a rig initialized with an explicit port")
	}

	withoutMarker := initTimeCloneConfig(true, "", 3312, "", "", "beads_proj", false)
	if got := withoutMarker.DoltServerLifecycle; got != "" {
		t.Errorf("dolt_server_lifecycle = %q, want it unset", got)
	}
	if withoutMarker.HasExternalServerLifecycle() {
		t.Error("HasExternalServerLifecycle() = true without an explicit --server-port")
	}
}
