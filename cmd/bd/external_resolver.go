package main

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// externalDiagCollector turns the storage resolver's per-prefix diagnostics
// (issueops.ExternalResolverOptions.DiagSink) into at most one stderr warning
// per prefix across a single CLI invocation.
//
// bd ready resolves ready work more than once per command — GetReadyWork /
// GetReadyWorkWithCounts run twice for the truncation probe (ready.go), and the
// --claim path resolves again — so without per-prefix dedup the same
// unresolvable prefix would warn repeatedly. Storage never prints; it only
// hands unresolvable diagnostics to this sink.
type externalDiagCollector struct {
	mu   sync.Mutex
	seen map[string]struct{}
	w    io.Writer
}

func newExternalDiagCollector(w io.Writer) *externalDiagCollector {
	return &externalDiagCollector{seen: make(map[string]struct{}), w: w}
}

// sink implements issueops.ExternalResolverOptions.DiagSink.
//
// Dedup key is the prefix; the first diagnostic seen for a prefix wins (its
// reason and ref count are printed, later deliveries for the same prefix are
// dropped). Only unresolvable refs reach this sink by design — refs that are
// merely unsatisfied (normally blocked) produce no diagnostic and no output.
//
// Output goes to c.w (os.Stderr in production) only, keeping --json stdout
// byte-for-byte pure. The mutex guards the shared singleton: the bd ready flow
// invokes the resolver sequentially, but the collector is process-wide and a
// mutex is cheap insurance against any future concurrent ready path.
func (c *externalDiagCollector) sink(diags []issueops.ExternalDiag) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, d := range diags {
		if _, dup := c.seen[d.Prefix]; dup {
			continue
		}
		c.seen[d.Prefix] = struct{}{}
		fmt.Fprintf(c.w, "warning: external dependency unresolvable (prefix %s): %s [%d refs]\n",
			d.Prefix, d.Reason, len(d.Refs))
	}
}

// externalDiag is the process-wide diagnostic collector. bd runs one command
// per process, so a package singleton naturally scopes "one warning per
// project" to a single invocation and dedups across every store the command
// opens (e.g. the main store plus a routed read store).
var externalDiag = newExternalDiagCollector(os.Stderr)

// externalResolverConfigurable is implemented by the concrete stores
// (*dolt.DoltStore, *embeddeddolt.EmbeddedDoltStore) that carry query-time
// external dependency resolution. The storage.DoltStorage interface does not
// expose the setter, so the cmd layer type-asserts to this to populate options
// at store construction.
type externalResolverConfigurable interface {
	SetExternalResolverOptions(issueops.ExternalResolverOptions)
}

// buildExternalResolverOptions assembles the query-time external resolver
// options for a store rooted at beadsDir. The zero value is fail-closed, and
// so is this for embedded / owned stores (ServerMode=false): every external ref
// stays blocking and the resolver never issues a cross-database query.
func buildExternalResolverOptions(beadsDir string) issueops.ExternalResolverOptions {
	return issueops.NewExternalResolverOptions(externalResolverServerMode(beadsDir), externalDiag.sink)
}

// externalResolverServerMode reports whether cross-database external dependency
// resolution may run for a store rooted at beadsDir.
//
// Source of truth: doltserver.ResolveServerMode (the documented single source
// of truth for how the server lifecycle is managed). Only ServerModeExternal —
// a user-managed / shared dolt sql-server — hosts other projects' databases and
// can satisfy a cross-database `<db>.issues` query. Embedded (in-process,
// single database) and Owned (a beads-auto-started single-project server)
// cannot reach another project's database, so they stay fail-closed.
//
// This intentionally does NOT reuse *dolt.DoltStore's private serverMode field:
// that flag is true for any TCP server connection, including an owned
// single-project auto-started server, which would over-report ServerMode and
// spuriously attempt (then fail) a cross-database query. ResolveServerMode
// distinguishes owned from external and matches the fail-closed spec.
func externalResolverServerMode(beadsDir string) bool {
	return doltserver.ResolveServerMode(beadsDir) == doltserver.ServerModeExternal
}

// applyExternalResolverOptions populates query-time external resolver options
// on a freshly constructed store, if the store carries the resolver. Stores
// that do not implement the setter (or a nil store) are returned unchanged.
func applyExternalResolverOptions(s storage.DoltStorage, beadsDir string) storage.DoltStorage {
	if c, ok := s.(externalResolverConfigurable); ok {
		c.SetExternalResolverOptions(buildExternalResolverOptions(beadsDir))
	}
	return s
}
