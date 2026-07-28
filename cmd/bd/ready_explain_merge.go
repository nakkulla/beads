package main

import (
	"sort"

	"github.com/steveyegge/beads/internal/types"
)

// mergeExternalBlocked folds external-dependency blocking into the blocked
// list `bd ready --explain` renders. Stored is_blocked is never set for
// external targets (satisfaction lives in another database and would go
// stale), so these rows arrive separately from GetBlockedIssues.
//
// Candidates carry no stored blocker: they join the array as new entries, or
// merge into an existing entry if one somehow exists. StoredBlockedRefs only
// completes the blocker list of an entry the stored path already emitted —
// never a new entry, so a stored-blocked row the stored path deliberately
// dropped cannot leak back in here.
//
// BlockedByCount is reset from BlockedBy on every touched entry:
// BuildReadyExplanation copies that field instead of recomputing it, so an
// unreset count renders 0 for external-only rows and the local-only count for
// mixed ones.
func mergeExternalBlocked(blocked []*types.BlockedIssue, external *types.ExternalBlocked) []*types.BlockedIssue {
	if external == nil || (len(external.Candidates) == 0 && len(external.StoredBlockedRefs) == 0) {
		return blocked
	}
	byID := make(map[string]*types.BlockedIssue, len(blocked)+len(external.Candidates))
	for _, bi := range blocked {
		if bi != nil {
			byID[bi.Issue.ID] = bi
		}
	}

	out := blocked
	for _, ext := range external.Candidates {
		if ext == nil {
			continue
		}
		if existing, ok := byID[ext.Issue.ID]; ok {
			appendBlockers(existing, ext.BlockedBy)
			continue
		}
		entry := *ext
		entry.BlockedBy = append([]string(nil), ext.BlockedBy...)
		entry.BlockedByCount = len(entry.BlockedBy)
		out = append(out, &entry)
		byID[entry.Issue.ID] = &entry
	}

	// Map iteration order is random; walk sorted IDs so a mixed entry's
	// blocker list is stable across runs.
	storedIDs := make([]string, 0, len(external.StoredBlockedRefs))
	for id := range external.StoredBlockedRefs {
		storedIDs = append(storedIDs, id)
	}
	sort.Strings(storedIDs)
	for _, id := range storedIDs {
		existing, ok := byID[id]
		if !ok {
			continue
		}
		appendBlockers(existing, external.StoredBlockedRefs[id])
	}
	return out
}

// appendBlockers adds refs not already listed and resets the count.
func appendBlockers(entry *types.BlockedIssue, refs []string) {
	seen := make(map[string]struct{}, len(entry.BlockedBy))
	for _, id := range entry.BlockedBy {
		seen[id] = struct{}{}
	}
	for _, ref := range refs {
		if _, dup := seen[ref]; dup {
			continue
		}
		seen[ref] = struct{}{}
		entry.BlockedBy = append(entry.BlockedBy, ref)
	}
	entry.BlockedByCount = len(entry.BlockedBy)
}
