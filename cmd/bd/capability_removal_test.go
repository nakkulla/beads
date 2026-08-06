package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The capability model (bd ship, provides: labels, external:<project>:<cap>
// refs, the external_databases config) was removed in favour of cross-prefix
// issue IDs. These are the reversal assertions for that removal: deleting the
// old tests alone would have turned the suite green without proving anything.

// TestShipCommandRemoved asserts bd exposes no ship command under any name or
// alias, so `bd ship <cap>` fails as an unknown command.
func TestShipCommandRemoved(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Name() == "ship" {
				t.Errorf("command %q still registered under %q", sub.Name(), c.Name())
			}
			for _, alias := range sub.Aliases {
				if alias == "ship" {
					t.Errorf("command %q still carries the alias \"ship\"", sub.CommandPath())
				}
			}
			walk(sub)
		}
	}
	walk(rootCmd)
}

// TestExternalDatabasesConfigKeyRejected asserts the retired config namespace
// is no longer a recognized CLI surface, so `bd config set
// external_databases.<name> <db>` is refused instead of silently writing a key
// nothing reads. Prefix -> database resolution is discovered from the server.
func TestExternalDatabasesConfigKeyRejected(t *testing.T) {
	for _, key := range []string{
		"external_databases.beads",
		"external_databases.other-project",
		"external_databases",
	} {
		if isRecognizedConfigKey(key) {
			t.Errorf("config key %q must not be recognized after the capability model was removed", key)
		}
	}
	// The neighbouring namespace that survives must keep working, so the
	// assertion above is about the removal and not a blanket rejection.
	if !isRecognizedConfigKey("external_projects.beads") {
		t.Error("external_projects.* must still be a recognized config namespace")
	}
}

// TestExternalDatabasesNotAContainerKey asserts `bd config show` no longer
// treats external_databases as a container key to skip — the namespace is gone
// entirely rather than hidden.
func TestExternalDatabasesNotAContainerKey(t *testing.T) {
	if isContainerKey("external_databases") {
		t.Error("external_databases must not be listed as a config container key")
	}
	if !isContainerKey("external_projects") {
		t.Error("external_projects must still be a config container key")
	}
}

// TestRejectCapabilityRef asserts the retired external:<project>:<capability>
// syntax is refused at the CLI boundary with a message pointing at issue IDs,
// while ordinary issue IDs pass through untouched.
func TestRejectCapabilityRef(t *testing.T) {
	for _, ref := range []string{
		"external:beads:mol-run-assignee",
		"external:beads",
		"external:",
	} {
		err := rejectCapabilityRef(ref)
		if err == nil {
			t.Errorf("rejectCapabilityRef(%q) = nil, want a rejection", ref)
			continue
		}
		if !strings.Contains(err.Error(), "no longer supported") {
			t.Errorf("rejectCapabilityRef(%q) error = %v, want a retired-syntax message", ref, err)
		}
	}
	for _, ref := range []string{"dotfiles-1tif", "UI-kfl4", "beads-o53.1", "bd-abc123"} {
		if err := rejectCapabilityRef(ref); err != nil {
			t.Errorf("rejectCapabilityRef(%q) = %v, want nil", ref, err)
		}
	}
}
