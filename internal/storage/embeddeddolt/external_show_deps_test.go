//go:build cgo

package embeddeddolt_test

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestGetDependenciesWithMetadataIncludesExternalRefs exercises the real
// EmbeddedDoltStore (issueops.GetDependenciesWithMetadataInTx) end to end: an
// issue with one local + one external blocking dependency must list both, the
// external entry must carry ID=ref and the dependency type, the dependency
// count must equal the listed length, and the dependents direction must be
// unaffected (an external ref is never a dependency source).
func TestGetDependenciesWithMetadataIncludesExternalRefs(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "sx")
	ctx := t.Context()

	const extRef = "external:beads:cap-a"

	a := &types.Issue{ID: "sx-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	b := &types.Issue{ID: "sx-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
		t.Fatalf("CreateIssue A: %v", err)
	}
	if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
		t.Fatalf("CreateIssue B: %v", err)
	}

	if err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "sx-a", DependsOnID: "sx-b", Type: types.DepBlocks}, "tester"); err != nil {
		t.Fatalf("AddDependency local: %v", err)
	}
	if err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "sx-a", DependsOnID: extRef, Type: types.DepBlocks}, "tester"); err != nil {
		t.Fatalf("AddDependency external: %v", err)
	}

	deps, err := te.store.GetDependenciesWithMetadata(ctx, "sx-a")
	if err != nil {
		t.Fatalf("GetDependenciesWithMetadata: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("len(deps) = %d, want 2 (local + external must not be dropped): %+v", len(deps), deps)
	}

	var ext *types.IssueWithDependencyMetadata
	var haveLocal bool
	for _, d := range deps {
		switch d.ID {
		case extRef:
			ext = d
		case "sx-b":
			haveLocal = true
		}
	}
	if !haveLocal {
		t.Errorf("local dependency sx-b missing from results: %+v", deps)
	}
	if ext == nil {
		t.Fatalf("external dependency %s dropped from results: %+v", extRef, deps)
	}
	if ext.DependencyType != types.DepBlocks {
		t.Errorf("external dep type = %q, want %q", ext.DependencyType, types.DepBlocks)
	}
	if ext.Title != "" || ext.Status != "" || ext.Priority != 0 {
		t.Errorf("external entry has fabricated fields: title=%q status=%q priority=%d", ext.Title, ext.Status, ext.Priority)
	}

	// Counts consistency: DependencyCount (all edges) equals the listed length.
	count, err := te.store.CountDependencies(ctx, "sx-a")
	if err != nil {
		t.Fatalf("CountDependencies: %v", err)
	}
	if int(count) != len(deps) {
		t.Errorf("CountDependencies = %d, len(listed) = %d; counts and listed edges must agree", count, len(deps))
	}

	// Dependents direction is unaffected: B is depended on by A (a local issue),
	// and A itself has no dependents. An external ref never appears as a source.
	bDependents, err := te.store.GetDependentsWithMetadata(ctx, "sx-b")
	if err != nil {
		t.Fatalf("GetDependentsWithMetadata(sx-b): %v", err)
	}
	if len(bDependents) != 1 || bDependents[0].ID != "sx-a" {
		t.Errorf("sx-b dependents = %+v, want exactly [sx-a]", bDependents)
	}
	aDependents, err := te.store.GetDependentsWithMetadata(ctx, "sx-a")
	if err != nil {
		t.Fatalf("GetDependentsWithMetadata(sx-a): %v", err)
	}
	if len(aDependents) != 0 {
		t.Errorf("sx-a dependents = %+v, want none", aDependents)
	}
}
