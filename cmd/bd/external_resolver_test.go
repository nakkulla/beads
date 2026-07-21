package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

// TestExternalDiagCollectorDedup verifies that repeated diagnostic deliveries
// for the same project produce exactly one stderr warning line, first-reason /
// first-refcount wins, and distinct projects each warn once. This mirrors bd
// ready resolving ready work multiple times per invocation.
func TestExternalDiagCollectorDedup(t *testing.T) {
	var buf bytes.Buffer
	c := newExternalDiagCollector(&buf)

	// First delivery (e.g. GetReadyWork): two projects unresolvable.
	c.sink([]issueops.ExternalDiag{
		{Project: "beads", Reason: "no external_databases mapping", Refs: []string{"external:beads:cap-a"}},
		{Project: "gt", Reason: "server mode required", Refs: []string{"external:gt:cap-b", "external:gt:cap-c"}},
	})
	// Second delivery (e.g. the truncation-probe GetReadyWork): same projects,
	// a different reason and ref count for beads. First-wins must hold.
	c.sink([]issueops.ExternalDiag{
		{Project: "beads", Reason: "external database query failed: boom", Refs: []string{"external:beads:cap-a", "external:beads:cap-d"}},
		{Project: "gt", Reason: "server mode required", Refs: []string{"external:gt:cap-b"}},
	})
	// Third delivery (e.g. the --claim path): still no new lines.
	c.sink([]issueops.ExternalDiag{
		{Project: "beads", Reason: "no external_databases mapping", Refs: []string{"external:beads:cap-a"}},
	})

	got := buf.String()
	lines := nonEmptyLines(got)
	if len(lines) != 2 {
		t.Fatalf("expected exactly 2 warning lines (one per project), got %d:\n%s", len(lines), got)
	}

	// beads: first reason and first ref count (1) win, not the later query-failed/2.
	wantBeads := "warning: external dependency unresolvable (project beads): no external_databases mapping [1 refs]"
	// gt: first delivery had 2 refs.
	wantGT := "warning: external dependency unresolvable (project gt): server mode required [2 refs]"
	if !containsLine(lines, wantBeads) {
		t.Errorf("missing/incorrect beads line.\n got: %v\nwant: %q", lines, wantBeads)
	}
	if !containsLine(lines, wantGT) {
		t.Errorf("missing/incorrect gt line.\n got: %v\nwant: %q", lines, wantGT)
	}
}

// TestExternalDiagCollectorNoDiags verifies that an empty or nil delivery
// writes nothing — refs that are merely unsatisfied never reach the sink.
func TestExternalDiagCollectorNoDiags(t *testing.T) {
	var buf bytes.Buffer
	c := newExternalDiagCollector(&buf)
	c.sink(nil)
	c.sink([]issueops.ExternalDiag{})
	if buf.Len() != 0 {
		t.Fatalf("expected no output for empty diagnostics, got %q", buf.String())
	}
}

// fakeConfigurableStore records the options handed to SetExternalResolverOptions.
// It embeds storage.DoltStorage (nil) so it satisfies the interface for the
// applyExternalResolverOptions type assertion without implementing every method.
type fakeConfigurableStore struct {
	storage.DoltStorage
	got    issueops.ExternalResolverOptions
	called bool
}

func (f *fakeConfigurableStore) SetExternalResolverOptions(opts issueops.ExternalResolverOptions) {
	f.called = true
	f.got = opts
}

// TestApplyExternalResolverOptionsWiring verifies the cmd-layer wiring:
// applyExternalResolverOptions populates the DiagSink (the process singleton),
// the Databases map from config, and ServerMode from the effective mode.
func TestApplyExternalResolverOptionsWiring(t *testing.T) {
	// BEADS_DOLT_SERVER_MODE=1 forces doltserver.ResolveServerMode to External
	// (its highest-priority check), so the resolver ServerMode gate is true.
	t.Setenv("BEADS_DOLT_SERVER_MODE", "1")

	fake := &fakeConfigurableStore{}
	out := applyExternalResolverOptions(fake, t.TempDir())

	if out != storage.DoltStorage(fake) {
		t.Fatalf("applyExternalResolverOptions should return the same store instance")
	}
	if !fake.called {
		t.Fatal("SetExternalResolverOptions was not called on a configurable store")
	}
	if !fake.got.ServerMode {
		t.Error("ServerMode should be true under BEADS_DOLT_SERVER_MODE=1 (external server)")
	}
	if fake.got.DiagSink == nil {
		t.Error("DiagSink should be populated so unresolvable diagnostics reach the CLI collector")
	}
	// Databases mirrors config.GetExternalDatabases() (typically empty in tests);
	// nil vs empty is acceptable, we only assert no panic and a usable value.
	_ = fake.got.Databases
}

// TestExternalResolverServerModeEmbedded verifies the fail-closed default: with
// no server env/config and a bare temp dir (no metadata.json), the effective
// mode is not External, so cross-database resolution stays off.
func TestExternalResolverServerModeEmbedded(t *testing.T) {
	// Neutralize any ambient server-mode env that could leak from the harness.
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	if doltserver.IsSharedServerMode() {
		t.Skip("shared-server mode enabled in this environment; embedded fail-closed assertion N/A")
	}
	if externalResolverServerMode(t.TempDir()) {
		t.Error("expected ServerMode=false for a bare directory with no server configuration")
	}
}

// TestApplyExternalResolverOptionsNonConfigurable verifies a store that does
// not carry the resolver is returned unchanged without panicking.
func TestApplyExternalResolverOptionsNonConfigurable(t *testing.T) {
	var s storage.DoltStorage // nil, non-configurable
	if got := applyExternalResolverOptions(s, t.TempDir()); got != nil {
		t.Fatalf("expected nil store passthrough, got %v", got)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

func containsLine(lines []string, want string) bool {
	for _, ln := range lines {
		if ln == want {
			return true
		}
	}
	return false
}
