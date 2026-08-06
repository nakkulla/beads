package issueops

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

// ExternalDiag is a per-prefix diagnostic for external dependency refs that
// could not be resolved (unresolvable verdicts only). Storage never prints to
// stderr; the CLI layer (U1c) consumes these via
// ExternalResolverOptions.DiagSink and warns with per-prefix dedup.
type ExternalDiag struct {
	Prefix string
	Reason string
	Refs   []string
}

// ExternalResolverOptions configures query-time resolution of cross-prefix
// issue-ID dependencies. Mode is always explicit input and is NEVER inferred
// from the connection. The zero value (ServerMode=false) is the spec-intended
// fail-closed default: every external ref is unresolvable, so any issue whose
// only blocker is external stays out of ready work.
type ExternalResolverOptions struct {
	// ServerMode gates whether the resolver may discover and query other
	// databases at all. When false, no cross-database query is issued.
	ServerMode bool
	// DiagSink, when non-nil, receives per-prefix diagnostics for
	// unresolvable refs. It is carried on the options (which live on the
	// store) so the existing store/InTx method signatures need no diagnostics
	// return value and WorkFilter is never used as the carrier.
	DiagSink func([]ExternalDiag)
}

// blockingExternalDepTypes are the dependency types whose external targets
// block ready work, matching types.DependencyType.Blocks semantics for the
// stored edge types that carry an external target.
const blockingExternalDepTypesSQL = "'blocks','conditional-blocks'"

// refPrefixGuess is a best-effort prefix for diagnostics only, used when the
// ref could not be attributed to a discovered prefix. It takes everything
// before the first hyphen, which is right for the common single-token prefix
// and harmless when it is not — no resolution decision depends on it.
func refPrefixGuess(ref string) string {
	if i := strings.Index(ref, "-"); i > 0 {
		return ref[:i]
	}
	return ref
}

// collectBlockingExternalRefs returns the DISTINCT depends_on_external values
// of blocking-type edges across dependencies and wisp_dependencies. An empty
// result means the caller skips the resolver entirely (zero overhead: no
// cross-database queries are issued).
//
//nolint:gosec // G201: depTable is hardcoded to "dependencies"/"wisp_dependencies".
func collectBlockingExternalRefs(ctx context.Context, tx DBTX) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	for _, depTable := range []string{"dependencies", "wisp_dependencies"} {
		err := func() error {
			query := fmt.Sprintf(
				"SELECT DISTINCT depends_on_external FROM %s WHERE depends_on_external IS NOT NULL AND type IN (%s)",
				depTable, blockingExternalDepTypesSQL)
			rows, err := tx.QueryContext(ctx, query)
			if err != nil {
				if depTable == "wisp_dependencies" && isTableNotExistError(err) {
					return nil
				}
				return fmt.Errorf("collect external refs from %s: %w", depTable, err)
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var ref string
				if err := rows.Scan(&ref); err != nil {
					return fmt.Errorf("collect external refs from %s: scan: %w", depTable, err)
				}
				if _, dup := seen[ref]; !dup {
					seen[ref] = struct{}{}
					out = append(out, ref)
				}
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("collect external refs from %s: rows: %w", depTable, err)
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// resolveExternalRefs classifies each distinct external ref as satisfied,
// unsatisfied, or unresolvable and returns (a) the refs that must block ready
// work — unsatisfied and unresolvable are equally blocking (fail-closed) — and
// (b) per-prefix diagnostics for the unresolvable refs only.
//
// A ref is a bare cross-prefix issue ID; it is satisfied exactly when the
// database owning its prefix holds that issue with status='closed'. resolved
// is deliberately not satisfying: a PR-delivered issue is not merged work.
// The resolver issues one discovery pass plus at most one satisfaction query
// per target database.
func resolveExternalRefs(ctx context.Context, tx DBTX, refs []string, opts ExternalResolverOptions) ([]string, []ExternalDiag) {
	if len(refs) == 0 {
		return nil, nil
	}

	// Non-server mode: never query. Every ref is unresolvable.
	if !opts.ServerMode {
		agg := newDiagAggregator()
		for _, ref := range refs {
			agg.add(refPrefixGuess(ref), "server mode required", ref)
		}
		return sortedUnique(refs), agg.diags()
	}

	agg := newDiagAggregator()
	m, err := discoverPrefixMap(ctx, tx)
	if err != nil {
		reason := fmt.Sprintf("prefix discovery failed: %v", err)
		for _, ref := range refs {
			agg.add(refPrefixGuess(ref), reason, ref)
		}
		return sortedUnique(refs), agg.diags()
	}

	// Group resolvable refs by the database that owns their prefix.
	type dbGroup struct {
		prefix string
		refs   []string
	}
	groups := make(map[string]*dbGroup)
	var dbOrder []string
	var blocking []string

	for _, ref := range refs {
		prefix, dbName, result := m.match(ref)
		switch result {
		case prefixMatchOK:
			g := groups[dbName]
			if g == nil {
				g = &dbGroup{prefix: prefix}
				groups[dbName] = g
				dbOrder = append(dbOrder, dbName)
			}
			g.refs = append(g.refs, ref)
		case prefixMatchAmbiguous:
			blocking = append(blocking, ref)
			agg.add(prefix, m.ambiguityReason(prefix), ref)
		case prefixMatchSelf:
			// A stored external row whose target is in fact local cannot be
			// resolved as external work; treat it as unresolvable rather than
			// silently satisfying it from the local table.
			blocking = append(blocking, ref)
			agg.add(prefix, "local prefix stored as external reference", ref)
		default:
			blocking = append(blocking, ref)
			agg.add(refPrefixGuess(ref), "unknown prefix", ref)
		}
	}

	for _, dbName := range dbOrder {
		g := groups[dbName]
		closed, qErr := queryClosedExternalIssues(ctx, tx, dbName, g.refs)
		if qErr != nil {
			blocking = append(blocking, g.refs...)
			reason := fmt.Sprintf("external database query failed: %v", qErr)
			for _, ref := range g.refs {
				agg.add(g.prefix, reason, ref)
			}
			continue
		}
		for _, ref := range g.refs {
			if _, ok := closed[ref]; !ok {
				// Unsatisfied (target open, or absent) — blocking, but not a
				// diagnostic: this is ordinary "still waiting" state.
				blocking = append(blocking, ref)
			}
		}
	}

	return sortedUnique(blocking), agg.diags()
}

// queryClosedExternalIssues runs the single per-database satisfaction query:
// which of the requested issue IDs are closed in the target database. dbName is
// validated by externalDBNameRE and backtick-quoted; only the IDs flow as ? args.
//
//nolint:gosec // G201: dbName is allowlist-validated and backtick-quoted; issue IDs are ? placeholders.
func queryClosedExternalIssues(ctx context.Context, tx DBTX, dbName string, ids []string) (map[string]struct{}, error) {
	placeholders, args := sqlbuild.InPlaceholders(ids)
	query := fmt.Sprintf(
		"SELECT id FROM `%s`.issues WHERE status = 'closed' AND id IN (%s)",
		dbName, placeholders)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveReadyExternalBlocksInTx collects blocking external refs and resolves
// them once per top-level ready call. It returns the refs that must block
// ready work (unsatisfied + unresolvable) and dispatches diagnostics to the
// sink. When no external deps exist the resolver is never invoked. Exported so
// the domain/db (proxied-server) stack runs the identical collect+resolve step
// and stays in ready-semantics parity with the classic stack.
func ResolveReadyExternalBlocksInTx(ctx context.Context, tx DBTX, opts ExternalResolverOptions) ([]string, error) {
	refs, err := collectBlockingExternalRefs(ctx, tx)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	unsatisfied, diags := resolveExternalRefs(ctx, tx, refs, opts)
	if len(diags) > 0 && opts.DiagSink != nil {
		opts.DiagSink(diags)
	}
	return unsatisfied, nil
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// diagAggregator groups unresolvable refs into ExternalDiag rows keyed by
// (prefix, reason) and emits them deterministically.
type diagAggregator struct {
	order []string
	byKey map[string]*ExternalDiag
}

func newDiagAggregator() *diagAggregator {
	return &diagAggregator{byKey: make(map[string]*ExternalDiag)}
}

func (a *diagAggregator) add(prefix, reason, ref string) {
	key := prefix + "\x00" + reason
	d := a.byKey[key]
	if d == nil {
		d = &ExternalDiag{Prefix: prefix, Reason: reason}
		a.byKey[key] = d
		a.order = append(a.order, key)
	}
	d.Refs = append(d.Refs, ref)
}

func (a *diagAggregator) diags() []ExternalDiag {
	if len(a.order) == 0 {
		return nil
	}
	keys := append([]string(nil), a.order...)
	sort.Strings(keys)
	out := make([]ExternalDiag, 0, len(keys))
	for _, k := range keys {
		d := a.byKey[k]
		sort.Strings(d.Refs)
		out = append(out, *d)
	}
	return out
}
