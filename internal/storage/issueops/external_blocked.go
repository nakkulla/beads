package issueops

import (
	"context"
	"fmt"
	"sort"

	"github.com/steveyegge/beads/internal/storage/sqlbuild"
	"github.com/steveyegge/beads/internal/types"
)

// externalBlockedSources are the table families scanned for external-blocked
// rows: the same two families ready work draws from.
var externalBlockedSources = []struct {
	tables sqlbuild.FilterTables
	wisp   bool
}{
	{sqlbuild.IssuesFilterTables, false},
	{sqlbuild.WispsFilterTables, true},
}

// CollectExternalBlockedInTx finds the rows blocked by unsatisfied external
// refs, split by whether they already carry a stored blocker.
//
// The scan predicate is the ready-work predicate with two changes: the
// external-exclusion clause is dropped (that is what we are looking for) and
// "is_blocked = 0" is dropped so a row with both a local blocker and an
// external ref is still seen. Everything else — status, pinned, deferred,
// ephemeral, issue type — still applies, so nothing ready work would have
// rejected for another reason can leak in. Stored-blocked rows are then split
// out into StoredBlockedRefs: they are merge material for the entry the stored
// blocked path emits, never a new entry of their own.
//
// unsatisfiedExternalRefs is the union the ready path already computed
// (unsatisfied + unresolvable); callers pass the very same slice so ready and
// blocked cannot disagree. in carries the precomputed ID sets the ready WHERE
// clause folds in; its UnsatisfiedExternalRefs and IncludeStoredBlocked fields
// are set here.
//
// Candidates carry the raw refs in BlockedBy with a matching BlockedByCount.
func CollectExternalBlockedInTx(
	ctx context.Context,
	tx DBTX,
	filter types.WorkFilter,
	unsatisfiedExternalRefs []string,
	in sqlbuild.ReadyWorkWhereInputs,
) (*types.ExternalBlocked, error) {
	if len(unsatisfiedExternalRefs) == 0 {
		return nil, nil
	}
	in.UnsatisfiedExternalRefs = nil
	in.IncludeStoredBlocked = true

	refsByID := make(map[string][]string)
	storedBlocked := make(map[string]bool)
	wispSet := make(map[string]struct{})
	var ids []string

	for _, src := range externalBlockedSources {
		whereSQL, args, err := sqlbuild.BuildReadyWorkWhere(filter, src.tables, in)
		if err != nil {
			return nil, err
		}
		candidates, err := externalBlockedCandidatesInTx(ctx, tx, src.tables.Main, whereSQL, args)
		if err != nil {
			if src.wisp && isTableNotExistError(err) {
				continue
			}
			return nil, err
		}
		if len(candidates) == 0 {
			continue
		}
		candidateIDs := make([]string, 0, len(candidates))
		for _, c := range candidates {
			candidateIDs = append(candidateIDs, c.id)
		}
		hits, err := externalBlockingEdgesInTx(ctx, tx, src.tables.Dependencies, candidateIDs, unsatisfiedExternalRefs)
		if err != nil {
			if src.wisp && isTableNotExistError(err) {
				continue
			}
			return nil, err
		}
		for _, c := range candidates {
			refs := hits[c.id]
			if len(refs) == 0 {
				continue
			}
			if _, seen := refsByID[c.id]; !seen {
				ids = append(ids, c.id)
			}
			refsByID[c.id] = append(refsByID[c.id], refs...)
			if c.isBlocked {
				storedBlocked[c.id] = true
			}
			if src.wisp {
				wispSet[c.id] = struct{}{}
			}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)

	// An ID present in both tables is canonically the wisp record (be-iabdi,
	// the same preference mergeReadyWisps applies). An issues-table hit for
	// such an ID is a stale row, and honoring it would let one ID sit in both
	// halves of a single explain run.
	canonicalWisps, err := WispIDSetInTx(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("external-blocked: wisp routing: %w", err)
	}
	kept := ids[:0]
	for _, id := range ids {
		_, fromWispScan := wispSet[id]
		if _, isWisp := canonicalWisps[id]; isWisp && !fromWispScan {
			delete(refsByID, id)
			delete(storedBlocked, id)
			continue
		}
		kept = append(kept, id)
	}
	ids = kept
	if len(ids) == 0 {
		return nil, nil
	}

	out := &types.ExternalBlocked{}
	var candidateIDs []string
	for _, id := range ids {
		if storedBlocked[id] {
			if out.StoredBlockedRefs == nil {
				out.StoredBlockedRefs = make(map[string][]string)
			}
			out.StoredBlockedRefs[id] = sortedUnique(refsByID[id])
			continue
		}
		candidateIDs = append(candidateIDs, id)
	}
	if len(candidateIDs) == 0 {
		return out, nil
	}

	issues, err := GetIssuesByIDsInTx(ctx, tx, candidateIDs, wispSet)
	if err != nil {
		return nil, fmt.Errorf("external-blocked: fetch issues: %w", err)
	}
	issueMap := make(map[string]*types.Issue, len(issues))
	for _, iss := range issues {
		issueMap[iss.ID] = iss
	}
	// Best-effort, same as the stored blocked path: a failed lookup drops the
	// parent field, never the candidate. The parent-child edge is local even
	// though the unsatisfied blocker lives in another database.
	parentMap, parentErr := loadParentIDsForChildrenInTx(ctx, tx,
		[]string{"dependencies", "wisp_dependencies"}, candidateIDs)
	if parentErr != nil {
		parentMap = nil
	}

	out.Candidates = make([]*types.BlockedIssue, 0, len(candidateIDs))
	for _, id := range candidateIDs {
		issue, ok := issueMap[id]
		if !ok || issue == nil {
			continue
		}
		refs := sortedUnique(refsByID[id])
		candidate := &types.BlockedIssue{
			Issue:          *issue,
			BlockedBy:      refs,
			BlockedByCount: len(refs),
		}
		if parentID, found := parentMap[id]; found {
			parent := parentID
			candidate.Parent = &parent
		}
		out.Candidates = append(out.Candidates, candidate)
	}
	return out, nil
}

// DropExternalBlockedFromReady removes from ready every row the
// external-blocked scan claimed. Both halves come from one resolution, so they
// can only overlap when an ID exists in BOTH the issues and wisps tables
// (be-iabdi): the canonical wisp record is external-blocked while the ready
// path still carries the stale issues row for that ID. Ready and blocked must
// stay mutually exclusive within one `bd ready --explain` run.
func DropExternalBlockedFromReady(ready []*types.Issue, blocked *types.ExternalBlocked) []*types.Issue {
	if blocked == nil || len(ready) == 0 {
		return ready
	}
	if len(blocked.Candidates) == 0 && len(blocked.StoredBlockedRefs) == 0 {
		return ready
	}
	drop := make(map[string]struct{}, len(blocked.Candidates)+len(blocked.StoredBlockedRefs))
	for _, c := range blocked.Candidates {
		if c != nil {
			drop[c.Issue.ID] = struct{}{}
		}
	}
	for id := range blocked.StoredBlockedRefs {
		drop[id] = struct{}{}
	}
	out := make([]*types.Issue, 0, len(ready))
	for _, iss := range ready {
		if iss == nil {
			continue
		}
		if _, skip := drop[iss.ID]; skip {
			continue
		}
		out = append(out, iss)
	}
	return out
}

type externalBlockedCandidate struct {
	id        string
	isBlocked bool
}

//nolint:gosec // G201: mainTable is a hardcoded table name; whereSQL is built by sqlbuild with ? placeholders.
func externalBlockedCandidatesInTx(ctx context.Context, tx DBTX, mainTable, whereSQL string, args []any) ([]externalBlockedCandidate, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT id, is_blocked FROM %s %s", mainTable, whereSQL), args...)
	if err != nil {
		return nil, fmt.Errorf("external-blocked: candidates from %s: %w", mainTable, err)
	}
	defer func() { _ = rows.Close() }()
	var out []externalBlockedCandidate
	for rows.Next() {
		var c externalBlockedCandidate
		if err := rows.Scan(&c.id, &c.isBlocked); err != nil {
			return nil, fmt.Errorf("external-blocked: scan candidate from %s: %w", mainTable, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("external-blocked: candidate rows from %s: %w", mainTable, err)
	}
	return out, nil
}

// externalBlockingEdgesInTx maps candidate ID -> the unsatisfied external refs
// it carries a blocking-type edge on. Both the ID list and the ref list are
// batched under QueryBatchSize.
//
//nolint:gosec // G201: depTable is a hardcoded table name; only IN placeholders are formatted in.
func externalBlockingEdgesInTx(ctx context.Context, tx DBTX, depTable string, candidateIDs, refs []string) (map[string][]string, error) {
	out := make(map[string][]string)
	for iStart := 0; iStart < len(candidateIDs); iStart += queryBatchSize {
		iEnd := iStart + queryBatchSize
		if iEnd > len(candidateIDs) {
			iEnd = len(candidateIDs)
		}
		idPH, idArgs := buildSQLInClause(candidateIDs[iStart:iEnd])
		for rStart := 0; rStart < len(refs); rStart += queryBatchSize {
			rEnd := rStart + queryBatchSize
			if rEnd > len(refs) {
				rEnd = len(refs)
			}
			refPH, refArgs := buildSQLInClause(refs[rStart:rEnd])
			args := make([]interface{}, 0, len(idArgs)+len(refArgs))
			args = append(args, idArgs...)
			args = append(args, refArgs...)
			query := fmt.Sprintf(`
				SELECT DISTINCT issue_id, depends_on_external FROM %s
				WHERE type IN (%s)
				  AND issue_id IN (%s)
				  AND depends_on_external IN (%s)
			`, depTable, blockingExternalDepTypesSQL, idPH, refPH)
			err := func() error {
				rows, err := tx.QueryContext(ctx, query, args...)
				if err != nil {
					return err
				}
				defer func() { _ = rows.Close() }()
				for rows.Next() {
					var id, ref string
					if err := rows.Scan(&id, &ref); err != nil {
						return fmt.Errorf("scan external-blocked edge: %w", err)
					}
					out[id] = append(out[id], ref)
				}
				if err := rows.Err(); err != nil {
					return fmt.Errorf("external-blocked edge rows: %w", err)
				}
				return nil
			}()
			if err != nil {
				return nil, fmt.Errorf("external-blocked: edges from %s: %w", depTable, err)
			}
		}
	}
	return out, nil
}
