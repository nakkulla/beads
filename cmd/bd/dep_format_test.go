package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestDepListFormatFlag(t *testing.T) {
	flag := depListCmd.Flags().Lookup("format")
	if flag == nil {
		t.Fatal("dep list --format flag is not registered")
	}
	if got, want := flag.DefValue, "issues"; got != want {
		t.Fatalf("dep list --format default = %q, want %q", got, want)
	}
}

func TestDepListEdgesForIssues(t *testing.T) {
	t.Parallel()

	issues := []*types.IssueWithDependencyMetadata{{
		Issue:          types.Issue{ID: "beads-target"},
		DependencyType: types.DepBlocks,
	}}
	for _, tt := range []struct {
		name      string
		direction string
		wantFrom  string
		wantTo    string
	}{
		{name: "down", direction: "down", wantFrom: "beads-source", wantTo: "beads-target"},
		{name: "up", direction: "up", wantFrom: "beads-target", wantTo: "beads-source"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			edges := depListEdgesForIssues("beads-source", tt.direction, issues)
			if len(edges) != 1 {
				t.Fatalf("len(edges) = %d, want 1", len(edges))
			}
			if edges[0].IssueID != tt.wantFrom || edges[0].DependsOnID != tt.wantTo || edges[0].Type != types.DepBlocks {
				t.Fatalf("edge = %#v, want %s -> %s via blocks", edges[0], tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestValidateDepListFormat(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"issues", "edges"} {
		if err := validateDepListFormat(format); err != nil {
			t.Errorf("validateDepListFormat(%q): %v", format, err)
		}
	}
	if err := validateDepListFormat("bogus"); err == nil {
		t.Fatal("validateDepListFormat(bogus) succeeded, want error")
	}
}
