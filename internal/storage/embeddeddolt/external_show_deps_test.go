//go:build cgo

package embeddeddolt_test

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestAddDependency_CrossPrefixRejectedOnEmbeddedStore locks the write-time
// guard on the real EmbeddedDoltStore: an embedded rig is a single local
// database, so no cross-prefix target can be resolved and the edge must be
// refused rather than stored as a permanently unresolvable blocker.
func TestAddDependency_CrossPrefixRejectedOnEmbeddedStore(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "sx")
	ctx := t.Context()

	a := &types.Issue{ID: "sx-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
	if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
		t.Fatalf("CreateIssue A: %v", err)
	}

	err := te.store.AddDependency(ctx, &types.Dependency{
		IssueID: "sx-a", DependsOnID: "dotfiles-1tif", Type: types.DepBlocks,
	}, "tester")
	if err == nil {
		t.Fatal("AddDependency must reject a cross-prefix target on an embedded store")
	}
	if !strings.Contains(err.Error(), "shared-server mode") {
		t.Errorf("err = %v, want a shared-server-mode rejection", err)
	}

	deps, err := te.store.GetDependenciesWithMetadata(ctx, "sx-a")
	if err != nil {
		t.Fatalf("GetDependenciesWithMetadata: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("rejected edge must not be stored, got %+v", deps)
	}
}

// TestGetDependenciesWithMetadataIncludesExternalRefs exercises the real
// EmbeddedDoltStore (issueops.GetDependenciesWithMetadataInTx) end to end
// against a pre-existing external edge: an issue with one local + one external
// blocking dependency must list both, the external entry must carry the target
// ID, the dependency type and the External marker, the dependency count must
// equal the listed length, and the dependents direction must be unaffected (an
// external ref is never a dependency source).
//
// The external row is inserted directly because bd no longer writes one from
// an embedded store — the listing contract still has to hold for rows written
// while the rig was attached to the shared server.
func TestGetDependenciesWithMetadataIncludesExternalRefs(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	te := newTestEnv(t, "sx")
	ctx := t.Context()

	const extRef = "dotfiles-1tif"

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
	te.exec(t, ctx,
		"INSERT INTO dependencies (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_by) VALUES ('d-ext', 'sx-a', NULL, NULL, ?, 'blocks', 'tester')",
		extRef)

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
	if !ext.External {
		t.Error("external entry must be marked External")
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
