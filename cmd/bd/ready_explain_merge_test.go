package main

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func blockedEntry(id string, blockers ...string) *types.BlockedIssue {
	return &types.BlockedIssue{
		Issue:          types.Issue{ID: id},
		BlockedBy:      blockers,
		BlockedByCount: len(blockers),
	}
}

func findEntry(t *testing.T, list []*types.BlockedIssue, id string) *types.BlockedIssue {
	t.Helper()
	for _, bi := range list {
		if bi != nil && bi.Issue.ID == id {
			return bi
		}
	}
	t.Fatalf("entry %s missing from %v", id, idsOfBlocked(list))
	return nil
}

func idsOfBlocked(list []*types.BlockedIssue) []string {
	ids := make([]string, 0, len(list))
	for _, bi := range list {
		if bi != nil {
			ids = append(ids, bi.Issue.ID)
		}
	}
	return ids
}

func TestMergeExternalBlocked_NoExternalIsIdentity(t *testing.T) {
	blocked := []*types.BlockedIssue{blockedEntry("a", "b")}
	if got := mergeExternalBlocked(blocked, nil); len(got) != 1 || got[0].Issue.ID != "a" {
		t.Fatalf("nil external must pass the list through, got %v", idsOfBlocked(got))
	}
	empty := &types.ExternalBlocked{}
	if got := mergeExternalBlocked(blocked, empty); len(got) != 1 {
		t.Fatalf("empty external must pass the list through, got %v", idsOfBlocked(got))
	}
}

// Acceptance 1 (CLI half): an external-only candidate becomes a new entry with
// a count that matches its blocker list.
func TestMergeExternalBlocked_CandidateAppendsNewEntry(t *testing.T) {
	blocked := []*types.BlockedIssue{blockedEntry("local", "blk")}
	ext := &types.ExternalBlocked{
		Candidates: []*types.BlockedIssue{blockedEntry("ext-only", "external:p:cap")},
	}

	got := mergeExternalBlocked(blocked, ext)
	entry := findEntry(t, got, "ext-only")
	if len(entry.BlockedBy) != 1 || entry.BlockedBy[0] != "external:p:cap" {
		t.Fatalf("blocked_by = %v", entry.BlockedBy)
	}
	if entry.BlockedByCount != 1 {
		t.Fatalf("blocked_by_count = %d, want 1", entry.BlockedByCount)
	}
	if findEntry(t, got, "local").BlockedByCount != 1 {
		t.Fatal("untouched entry must keep its count")
	}
}

// Acceptance 2 (CLI half): a stored-blocked row's external refs merge into the
// entry the stored path already emitted, and the count is recomputed from the
// merged list — BuildReadyExplanation copies the field verbatim.
func TestMergeExternalBlocked_StoredRefsMergeIntoExistingEntry(t *testing.T) {
	blocked := []*types.BlockedIssue{blockedEntry("mixed", "local-blk")}
	ext := &types.ExternalBlocked{
		StoredBlockedRefs: map[string][]string{
			"mixed": {"external:p:cap", "external:q:cap"},
		},
	}

	got := mergeExternalBlocked(blocked, ext)
	if len(got) != 1 {
		t.Fatalf("merge must not add an entry, got %v", idsOfBlocked(got))
	}
	entry := findEntry(t, got, "mixed")
	want := []string{"local-blk", "external:p:cap", "external:q:cap"}
	if len(entry.BlockedBy) != len(want) {
		t.Fatalf("blocked_by = %v, want %v", entry.BlockedBy, want)
	}
	for i, id := range want {
		if entry.BlockedBy[i] != id {
			t.Fatalf("blocked_by[%d] = %q, want %q", i, entry.BlockedBy[i], id)
		}
	}
	if entry.BlockedByCount != len(want) {
		t.Fatalf("blocked_by_count = %d, want %d", entry.BlockedByCount, len(want))
	}
}

// Acceptance 7 (CLI half): stored-blocked refs never create an entry the
// stored path chose not to emit.
func TestMergeExternalBlocked_StoredRefsNeverCreateEntries(t *testing.T) {
	ext := &types.ExternalBlocked{
		StoredBlockedRefs: map[string][]string{"absent": {"external:p:cap"}},
	}
	if got := mergeExternalBlocked(nil, ext); len(got) != 0 {
		t.Fatalf("stored-blocked refs must not leak in as entries, got %v", idsOfBlocked(got))
	}
}

func TestMergeExternalBlocked_CandidateMergeDedupes(t *testing.T) {
	blocked := []*types.BlockedIssue{blockedEntry("dup", "external:p:cap", "local-blk")}
	ext := &types.ExternalBlocked{
		Candidates: []*types.BlockedIssue{blockedEntry("dup", "external:p:cap", "external:q:cap")},
	}

	got := mergeExternalBlocked(blocked, ext)
	entry := findEntry(t, got, "dup")
	want := []string{"external:p:cap", "local-blk", "external:q:cap"}
	if len(entry.BlockedBy) != len(want) {
		t.Fatalf("blocked_by = %v, want %v", entry.BlockedBy, want)
	}
	for i, id := range want {
		if entry.BlockedBy[i] != id {
			t.Fatalf("blocked_by[%d] = %q, want %q", i, entry.BlockedBy[i], id)
		}
	}
	if entry.BlockedByCount != len(want) {
		t.Fatalf("blocked_by_count = %d, want %d", entry.BlockedByCount, len(want))
	}
}

// The merged list is what BuildReadyExplanation renders: external refs miss
// the blocker map and must come out as ID-only BlockerInfo, with the summary
// counting the new entry.
func TestMergeExternalBlocked_RendersIDOnlyBlockerInfo(t *testing.T) {
	ext := &types.ExternalBlocked{
		Candidates: []*types.BlockedIssue{blockedEntry("ext-only", "external:p:cap")},
	}
	merged := mergeExternalBlocked(nil, ext)

	explanation := types.BuildReadyExplanation(nil, merged, nil, nil, nil, nil)
	if len(explanation.Blocked) != 1 {
		t.Fatalf("blocked items = %d, want 1", len(explanation.Blocked))
	}
	item := explanation.Blocked[0]
	if item.BlockedByCount != 1 || len(item.BlockedBy) != 1 {
		t.Fatalf("blocked_by_count = %d, blocked_by = %+v", item.BlockedByCount, item.BlockedBy)
	}
	blocker := item.BlockedBy[0]
	if blocker.ID != "external:p:cap" {
		t.Fatalf("blocker id = %q", blocker.ID)
	}
	if blocker.Title != "" || blocker.Status != "" || blocker.Priority != 0 {
		t.Fatalf("external blocker must stay ID-only, got %+v", blocker)
	}
	if explanation.Summary.TotalBlocked != 1 {
		t.Fatalf("summary total_blocked = %d, want 1", explanation.Summary.TotalBlocked)
	}
}
