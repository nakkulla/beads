package issueops

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/steveyegge/beads/internal/types"
)

// externalDBNameRE is a strict allowlist for discovered external database
// names. The dolt/domain-db identifier validators cannot be reused here because
// both packages import issueops (import cycle); this local allowlist is
// injection-safe on its own and discovered names are always backtick-quoted in
// SQL regardless.
var externalDBNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// prefixMap is the discovered issue-prefix -> database mapping for the shared
// Dolt server. It is built by querying every database's own config table for
// issue_prefix, which bd records at rig creation, so no manual registration
// exists to drift.
type prefixMap struct {
	// byPrefix maps an unambiguously-owned issue prefix to its database name.
	byPrefix map[string]string
	// ambiguous maps a prefix claimed by two or more databases to the claiming
	// database names (sorted). Refs matching such a prefix are unresolvable.
	ambiguous map[string][]string
	// selfPrefix is the issue prefix of the database the connection is on
	// ("" when the local config has none). It participates in classification
	// but never in external resolution.
	selfPrefix string
}

type prefixMatch int

const (
	// prefixMatchNone means no discovered prefix owns the id.
	prefixMatchNone prefixMatch = iota
	// prefixMatchOK means exactly one database owns the matched prefix.
	prefixMatchOK
	// prefixMatchAmbiguous means two or more databases claim the matched prefix.
	prefixMatchAmbiguous
	// prefixMatchSelf means the longest match is the local rig's own prefix.
	prefixMatchSelf
)

// match resolves id against the discovered prefixes plus the local rig's own
// prefix, using longest-prefix wins so a hyphen-bearing prefix (team-alpha)
// beats a shorter one that also matches (team). A prefix owns an id when the
// id starts with prefix + "-"; child ids (<parent>.<n>) and wisp-shaped ids
// carry the same prefix and match unchanged.
func (m *prefixMap) match(id string) (prefix, db string, result prefixMatch) {
	best := ""
	bestResult := prefixMatchNone
	bestDB := ""

	consider := func(p string, r prefixMatch, dbName string) {
		if p == "" || !strings.HasPrefix(id, p+"-") {
			return
		}
		if len(p) <= len(best) {
			return
		}
		best, bestResult, bestDB = p, r, dbName
	}

	consider(m.selfPrefix, prefixMatchSelf, "")
	for p, dbName := range m.byPrefix {
		consider(p, prefixMatchOK, dbName)
	}
	for p := range m.ambiguous {
		consider(p, prefixMatchAmbiguous, "")
	}
	return best, bestDB, bestResult
}

// ambiguityReason renders the diagnostic detail for an ambiguous prefix.
func (m *prefixMap) ambiguityReason(prefix string) string {
	return fmt.Sprintf("ambiguous prefix (%s)", strings.Join(m.ambiguous[prefix], ", "))
}

// discoveryCache memoizes one prefix map for the lifetime of the options value
// that owns it. bd runs one command per process, so a cache created at store
// construction is exactly the "once per command execution" the design calls
// for: bd ready resolves ready work more than once per invocation, and a single
// dep add both classifies and validates its target.
//
// The zero ExternalResolverOptions has no cache, so callers that build options
// as a struct literal (tests, one-off repositories) still discover per call.
type discoveryCache struct {
	mu   sync.Mutex
	done bool
	m    *prefixMap
	err  error
}

func (c *discoveryCache) get(ctx context.Context, tx DBTX) (*prefixMap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return c.m, c.err
	}
	c.m, c.err = discoverPrefixMap(ctx, tx)
	c.done = true
	return c.m, c.err
}

// prefixMap returns the discovered map, reusing this options value's cache when
// it has one.
func (o ExternalResolverOptions) prefixMap(ctx context.Context, tx DBTX) (*prefixMap, error) {
	if o.discovery == nil {
		return discoverPrefixMap(ctx, tx)
	}
	return o.discovery.get(ctx, tx)
}

// discoverPrefixMap enumerates the databases on the shared server and reads
// each one's config.issue_prefix, producing the prefix -> database mapping
// used by both external resolution and cross-prefix classification.
//
// Discovery is server-side only: a database that has no readable config table
// (information_schema, mysql, a non-beads database) is skipped rather than
// failing the whole map, while a failure to list databases at all is an error
// the caller must treat as fail-closed.
func discoverPrefixMap(ctx context.Context, tx DBTX) (*prefixMap, error) {
	selfDB, err := currentDatabase(ctx, tx)
	if err != nil {
		return nil, err
	}
	dbs, err := listDatabases(ctx, tx)
	if err != nil {
		return nil, err
	}

	m := &prefixMap{
		byPrefix:  make(map[string]string),
		ambiguous: make(map[string][]string),
	}
	if selfPrefix, err := GetConfigInTx(ctx, tx, "issue_prefix"); err == nil {
		m.selfPrefix = selfPrefix
	}

	claims := make(map[string][]string)
	for _, dbName := range dbs {
		if dbName == selfDB || !externalDBNameRE.MatchString(dbName) {
			continue
		}
		prefix, ok := readIssuePrefix(ctx, tx, dbName)
		if !ok || prefix == "" || prefix == m.selfPrefix {
			continue
		}
		claims[prefix] = append(claims[prefix], dbName)
	}
	for prefix, owners := range claims {
		if len(owners) == 1 {
			m.byPrefix[prefix] = owners[0]
			continue
		}
		sort.Strings(owners)
		m.ambiguous[prefix] = owners
	}
	return m, nil
}

func currentDatabase(ctx context.Context, tx DBTX) (string, error) {
	var name sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&name); err != nil {
		return "", fmt.Errorf("discover prefixes: current database: %w", err)
	}
	return name.String, nil
}

func listDatabases(ctx context.Context, tx DBTX) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("discover prefixes: list databases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("discover prefixes: scan database: %w", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("discover prefixes: rows: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

// readIssuePrefix reads one database's config.issue_prefix. A database without
// a readable config table is not a beads rig, so the miss is reported as
// "not found" rather than an error.
//
//nolint:gosec // G201: dbName is allowlist-validated by externalDBNameRE and backtick-quoted.
func readIssuePrefix(ctx context.Context, tx DBTX, dbName string) (string, bool) {
	q := fmt.Sprintf("SELECT value FROM `%s`.config WHERE `key` = 'issue_prefix'", dbName)
	var value sql.NullString
	if err := tx.QueryRowContext(ctx, q).Scan(&value); err != nil {
		return "", false
	}
	return value.String, value.Valid
}

// IsCrossPrefixTarget reports whether targetID lives in a different rig's
// database than sourceID.
//
// The comparison is still source-vs-target — a rig whose issue IDs predate or
// diverge from its configured issue_prefix must keep working — but the prefix
// each ID resolves to is now a longest match against the prefixes discovered on
// the shared server plus the local one. That is what fixes the case the old
// types.ExtractPrefix comparison got wrong: with a local prefix of "team" and a
// foreign prefix of "team-alpha", "team-alpha-xyz" is foreign even though its
// first hyphen segment matches.
//
// Two IDs can only share a longest-match prefix when they share their first
// hyphen segment (a matching prefix's own first segment is that segment), so a
// differing ExtractPrefix already settles the question, and discovery is
// consulted only when a longer prefix could possibly apply. Outside server mode
// no discovery is possible, so the first-segment comparison stands alone; the
// write path rejects those targets anyway (ValidateExternalDepTarget).
func IsCrossPrefixTarget(ctx context.Context, tx DBTX, sourceID, targetID string, opts ExternalResolverOptions) bool {
	if types.ExtractPrefix(sourceID) != types.ExtractPrefix(targetID) {
		return true
	}
	if !opts.ServerMode {
		return false
	}
	// A longer prefix can only win when the remainder itself contains a
	// hyphen, so ordinary local ids skip discovery entirely.
	if !hasHyphenAfterFirstSegment(sourceID) && !hasHyphenAfterFirstSegment(targetID) {
		return false
	}
	m, err := opts.prefixMap(ctx, tx)
	if err != nil {
		return false
	}
	srcPrefix, _, _ := m.match(sourceID)
	tgtPrefix, _, _ := m.match(targetID)
	return srcPrefix != tgtPrefix
}

// hasHyphenAfterFirstSegment reports whether id could match a prefix longer
// than its first hyphen segment.
func hasHyphenAfterFirstSegment(id string) bool {
	i := strings.Index(id, "-")
	return i >= 0 && strings.Contains(id[i+1:], "-")
}

// ValidateExternalDepTarget checks, at write time, that a cross-prefix
// dependency target can actually be resolved: the prefix must map to exactly
// one database on the shared server and the issue must exist there. This is
// what keeps unresolvable rows (typos, unmapped prefixes) from ever being
// stored. The target's status is deliberately not checked — depending on an
// open issue is the normal case.
func ValidateExternalDepTarget(ctx context.Context, tx DBTX, targetID string, opts ExternalResolverOptions) error {
	if !opts.ServerMode {
		return fmt.Errorf("cross-prefix dependency %s requires shared-server mode: external targets cannot be resolved from a local database", targetID)
	}
	m, err := opts.prefixMap(ctx, tx)
	if err != nil {
		return fmt.Errorf("cross-prefix dependency %s: %w", targetID, err)
	}
	prefix, dbName, result := m.match(targetID)
	switch result {
	case prefixMatchAmbiguous:
		return fmt.Errorf("cross-prefix dependency %s: %s", targetID, m.ambiguityReason(prefix))
	case prefixMatchOK:
	default:
		return fmt.Errorf("cross-prefix dependency %s: unknown prefix (no database on the shared server declares it)", targetID)
	}
	exists, err := externalIssueExists(ctx, tx, dbName, targetID)
	if err != nil {
		return fmt.Errorf("cross-prefix dependency %s: %w", targetID, err)
	}
	if !exists {
		return fmt.Errorf("cross-prefix dependency %s not found in database %s", targetID, dbName)
	}
	return nil
}

//nolint:gosec // G201: dbName is allowlist-validated by externalDBNameRE and backtick-quoted; the id flows as a ? placeholder.
func externalIssueExists(ctx context.Context, tx DBTX, dbName, id string) (bool, error) {
	q := fmt.Sprintf("SELECT 1 FROM `%s`.issues WHERE id = ? LIMIT 1", dbName)
	var probe int
	err := tx.QueryRowContext(ctx, q, id).Scan(&probe)
	switch {
	case err == nil:
		return true, nil
	case err == sql.ErrNoRows:
		return false, nil
	default:
		return false, fmt.Errorf("external issue lookup failed: %w", err)
	}
}
