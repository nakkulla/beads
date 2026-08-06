package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// syntheticExternalDep mirrors what the storage layer returns for a
// cross-prefix dependency edge: an entry whose ID is the foreign issue ID,
// whose External marker is set, and whose other Issue fields are zero-valued.
func syntheticExternalDep(ref string, depType types.DependencyType) *types.IssueWithDependencyMetadata {
	return &types.IssueWithDependencyMetadata{
		Issue:          types.Issue{ID: ref},
		DependencyType: depType,
		External:       true,
	}
}

// TestFormatDependencyLineExternalRef verifies bd show renders a synthesized
// external dependency edge with an (external) marker rather than crashing or
// printing fabricated status/priority fields.
func TestFormatDependencyLineExternalRef(t *testing.T) {
	const ref = "beads-mol0a11"
	line := formatDependencyLine("→", syntheticExternalDep(ref, types.DepBlocks))

	if !strings.Contains(line, ref) {
		t.Errorf("dependency line %q does not contain external target %q", line, ref)
	}
	if !strings.Contains(line, "(external)") {
		t.Errorf("dependency line %q missing (external) marker", line)
	}
	if strings.Contains(line, "P0") {
		t.Errorf("dependency line %q shows a fabricated priority for an external ref", line)
	}
}

// TestExternalDepListLine verifies the shared dep-list line renderer (used by
// both the direct and proxied-server single-ID list paths) shows the ref, the
// (external) marker, and the edge type — never fabricated status/priority.
func TestExternalDepListLine(t *testing.T) {
	const ref = "UI-kfl4"
	line := externalDepListLine(syntheticExternalDep(ref, types.DepBlocks))

	if !strings.Contains(line, ref) {
		t.Errorf("dep list line %q does not contain external target %q", line, ref)
	}
	if !strings.Contains(line, "(external)") {
		t.Errorf("dep list line %q missing (external) marker", line)
	}
	if !strings.Contains(line, "via blocks") {
		t.Errorf("dep list line %q missing dependency type", line)
	}
	if strings.Contains(line, "[P") || strings.Contains(line, "(open)") {
		t.Errorf("dep list line %q shows fabricated priority/status for an external ref", line)
	}
}

// TestShowJSONIncludesExternalDependency verifies the bd show --json
// dependencies array carries the external edge additively with its id and type,
// with no nil-pointer serialization issues.
func TestShowJSONIncludesExternalDependency(t *testing.T) {
	const ref = "dotfiles-1tif"
	details := &types.IssueDetails{
		Issue: types.Issue{ID: "test-1", Title: "Root", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask},
		Dependencies: []*types.IssueWithDependencyMetadata{
			{Issue: types.Issue{ID: "test-2", Title: "Local Blocker", Status: types.StatusOpen}, DependencyType: types.DepBlocks},
			syntheticExternalDep(ref, types.DepBlocks),
		},
	}

	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal IssueDetails: %v", err)
	}

	var decoded struct {
		Dependencies []struct {
			ID             string `json:"id"`
			DependencyType string `json:"dependency_type"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal IssueDetails: %v\n%s", err, raw)
	}

	if len(decoded.Dependencies) != 2 {
		t.Fatalf("dependencies length = %d, want 2: %s", len(decoded.Dependencies), raw)
	}
	var found bool
	for _, d := range decoded.Dependencies {
		if d.ID == ref {
			found = true
			if d.DependencyType != string(types.DepBlocks) {
				t.Errorf("external dependency_type = %q, want %q", d.DependencyType, types.DepBlocks)
			}
		}
	}
	if !found {
		t.Errorf("external dependency %q not present in JSON dependencies array: %s", ref, raw)
	}
}
