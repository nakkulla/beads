package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestFlattenChildrenByResolvedIDOrder(t *testing.T) {
	first := &types.IssueWithDependencyMetadata{Issue: types.Issue{ID: "beads-child-a"}}
	second := &types.IssueWithDependencyMetadata{Issue: types.Issue{ID: "beads-child-b"}}
	allChildren := map[string][]*types.IssueWithDependencyMetadata{
		"beads-parent-b": {second},
		"beads-parent-a": {first},
	}

	got := flattenChildrenByResolvedIDOrder(
		[]string{"beads-parent-a", "beads-parent-b"},
		allChildren,
	)
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("flattened children = %#v, want [%s %s]", got, first.ID, second.ID)
	}
}
