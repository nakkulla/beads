package issueops

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/storage/sqlbuild"
)

// ExternalDiag is a per-project diagnostic for external dependency refs that
// could not be resolved (unresolvable verdicts only). Storage never prints to
// stderr; the CLI layer (U1c) consumes these via
// ExternalResolverOptions.DiagSink and warns with per-project dedup.
type ExternalDiag struct {
	Project string
	Reason  string
	Refs    []string
}

// ExternalResolverOptions configures query-time resolution of
// external:<project>:<capability> dependencies. Mode is always explicit input
// and is NEVER inferred from the connection. The zero value
// (ServerMode=false) is the spec-intended fail-closed default: every external
// ref is unresolvable, so any issue whose only blocker is external stays out
// of ready work.
type ExternalResolverOptions struct {
	// ServerMode gates whether the resolver may query external databases at
	// all. When false, no cross-database query is issued.
	ServerMode bool
	// Databases maps an external project name to the Dolt database name that
	// holds its issues (the external_databases config). Values are database
	// names on the shared server, not filesystem paths.
	Databases map[string]string
	// DiagSink, when non-nil, receives per-project diagnostics for
	// unresolvable refs. It is carried on the options (which live on the
	// store) so the existing store/InTx method signatures need no diagnostics
	// return value and WorkFilter is never used as the carrier.
	DiagSink func([]ExternalDiag)
}

// externalDBNameRE is a strict allowlist for mapped external database names.
// The dolt/domain-db identifier validators cannot be reused here because both
// packages import issueops (import cycle); this local allowlist is
// injection-safe on its own and mapped names are always backtick-quoted in
// SQL regardless.
var externalDBNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// blockingExternalDepTypes are the dependency types whose external targets
// block ready work, matching types.DependencyType.Blocks semantics for the
// stored edge types that carry an external target.
const blockingExternalDepTypesSQL = "'blocks','conditional-blocks'"

// parseExternalRef splits external:<project>:<capability>. It returns a
// best-effort project (parts[1] when present) even when the ref is malformed,
// so a malformed ref can still be attributed to a project in diagnostics.
func parseExternalRef(ref string) (project, capability string, ok bool) {
	parts := strings.SplitN(ref, ":", 3)
	if len(parts) >= 2 {
		project = parts[1]
	}
	if !strings.HasPrefix(ref, "external:") || len(parts) != 3 || parts[1] == "" || parts[2] == "" {
		return project, "", false
	}
	return parts[1], parts[2], true
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
// (b) per-project diagnostics for the unresolvable refs only. It issues at most
// one satisfaction query per project.
func resolveExternalRefs(ctx context.Context, tx DBTX, refs []string, opts ExternalResolverOptions) ([]string, []ExternalDiag) {
	if len(refs) == 0 {
		return nil, nil
	}

	// Non-server mode: never query. Every ref is unresolvable.
	if !opts.ServerMode {
		agg := newDiagAggregator()
		for _, ref := range refs {
			project, _, _ := parseExternalRef(ref)
			agg.add(project, "server mode required", ref)
		}
		return sortedUnique(refs), agg.diags()
	}

	// Group well-formed refs by project; malformed refs are unresolvable.
	type projGroup struct {
		refByCapLabel map[string]string // "provides:<cap>" -> full ref
		refs          []string
	}
	groups := make(map[string]*projGroup)
	var projOrder []string
	var unsatisfied []string
	agg := newDiagAggregator()

	for _, ref := range refs {
		project, capability, ok := parseExternalRef(ref)
		if !ok {
			unsatisfied = append(unsatisfied, ref)
			agg.add(project, "malformed external reference", ref)
			continue
		}
		g := groups[project]
		if g == nil {
			g = &projGroup{refByCapLabel: make(map[string]string)}
			groups[project] = g
			projOrder = append(projOrder, project)
		}
		g.refByCapLabel["provides:"+capability] = ref
		g.refs = append(g.refs, ref)
	}

	markUnresolvable := func(g *projGroup, project, reason string) {
		unsatisfied = append(unsatisfied, g.refs...)
		for _, ref := range g.refs {
			agg.add(project, reason, ref)
		}
	}

	for _, project := range projOrder {
		g := groups[project]
		dbName, mapped := opts.Databases[project]
		switch {
		case !mapped || dbName == "":
			markUnresolvable(g, project, "no external_databases mapping")
		case !externalDBNameRE.MatchString(dbName):
			markUnresolvable(g, project, fmt.Sprintf("invalid external database name %q", dbName))
		default:
			labels := make([]string, 0, len(g.refByCapLabel))
			for label := range g.refByCapLabel {
				labels = append(labels, label)
			}
			satisfied, qErr := queryProvidedLabels(ctx, tx, dbName, labels)
			if qErr != nil {
				markUnresolvable(g, project, fmt.Sprintf("external database query failed: %v", qErr))
				continue
			}
			for label, ref := range g.refByCapLabel {
				if _, ok := satisfied[label]; !ok {
					unsatisfied = append(unsatisfied, ref)
				}
			}
		}
	}

	return sortedUnique(unsatisfied), agg.diags()
}

// queryProvidedLabels runs the single per-project satisfaction query: a
// capability is satisfied iff the target database has a closed issue carrying
// the matching provides:<capability> label. dbName is validated by
// externalDBNameRE and backtick-quoted; only the labels flow as ? args.
//
//nolint:gosec // G201: dbName is allowlist-validated and backtick-quoted; capability labels are ? placeholders.
func queryProvidedLabels(ctx context.Context, tx DBTX, dbName string, labels []string) (map[string]struct{}, error) {
	placeholders, args := sqlbuild.InPlaceholders(labels)
	dbQ := "`" + dbName + "`"
	query := fmt.Sprintf(
		"SELECT DISTINCT l.label FROM %s.labels l JOIN %s.issues i ON i.id = l.issue_id WHERE i.status = 'closed' AND l.label IN (%s)",
		dbQ, dbQ, placeholders)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]struct{})
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			return nil, err
		}
		out[label] = struct{}{}
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
// (project, reason) and emits them deterministically.
type diagAggregator struct {
	order []string
	byKey map[string]*ExternalDiag
}

func newDiagAggregator() *diagAggregator {
	return &diagAggregator{byKey: make(map[string]*ExternalDiag)}
}

func (a *diagAggregator) add(project, reason, ref string) {
	key := project + "\x00" + reason
	d := a.byKey[key]
	if d == nil {
		d = &ExternalDiag{Project: project, Reason: reason}
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
