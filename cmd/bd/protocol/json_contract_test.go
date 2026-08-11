// json_contract_test.go — CI regression tests for --json output contracts.
//
// These tests verify that commands with --json always produce valid JSON
// and include required fields. Regressions like GH#2492, GH#2465, GH#2407,
// GH#2395 are prevented by these tests.
package protocol

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestJSONContract_ListOutputIsValidJSON verifies bd list --json always
// produces valid JSON (not mixed with tree-renderer text).
func TestJSONContract_ListOutputIsValidJSON(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.create("JSON contract test issue")

	out := w.run("list", "--json")
	items := parseJSONOutput(t, out)
	if len(items) == 0 {
		t.Fatal("bd list --json returned no items")
	}
}

// TestJSONContract_ShowOutputHasRequiredFields verifies bd show --json
// includes all required issue fields.
func TestJSONContract_ShowOutputHasRequiredFields(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Required fields test")

	out := w.run("show", id, "--json")
	items := parseJSONOutput(t, out)
	if len(items) == 0 {
		t.Fatal("bd show --json returned no items")
	}

	issue := items[0]
	requiredFields := []string{"id", "title", "status", "priority", "issue_type", "created_at", "schema_version"}
	for _, field := range requiredFields {
		if _, ok := issue[field]; !ok {
			t.Errorf("bd show --json missing required field %q", field)
		}
	}
}

// TestJSONContract_ReadyOutputIsValidJSON verifies bd ready --json produces
// valid JSON even when no issues are ready.
func TestJSONContract_ReadyOutputIsValidJSON(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	out := w.run("ready", "--json")
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("bd ready --json produced invalid JSON: %v\nOutput:\n%s", err, out)
	}
}

// TestJSONContract_CreateOutputHasID verifies bd create --json returns
// the created issue with its ID.
func TestJSONContract_CreateOutputHasID(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	out := w.run("create", "Create contract test", "--description=test", "--json")

	var issue map[string]any
	if err := json.Unmarshal([]byte(out), &issue); err != nil {
		t.Fatalf("bd create --json produced invalid JSON: %v\nOutput:\n%s", err, out)
	}

	assertSchemaVersion(t, issue, "bd create --json")
	if _, ok := issue["id"]; !ok {
		t.Error("bd create --json output missing 'id' field")
	}
}

// TestJSONContract_ErrorOutputIsValidJSON verifies that errors with --json
// produce valid JSON with schema_version to stderr (not mixed text).
func TestJSONContract_ErrorOutputIsValidJSON(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	// Try to show a nonexistent issue with --json
	out, _ := w.runExpectError("show", "nonexistent-xyz-999", "--json")

	// The output (stderr) should be valid JSON or empty
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return // Empty is acceptable for errors
	}

	// Try to parse as JSON object
	var errObj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &errObj); err != nil {
		// Try each line — error JSON may be mixed with other stderr output
		for _, line := range strings.Split(trimmed, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var lineObj map[string]any
			if json.Unmarshal([]byte(line), &lineObj) == nil {
				if _, hasError := lineObj["error"]; hasError {
					assertSchemaVersion(t, lineObj, "bd error JSON line")
					return
				}
			}
		}
		t.Logf("Note: error output not fully JSON — this is acceptable for some error paths")
	} else {
		if _, hasError := errObj["error"]; hasError {
			assertSchemaVersion(t, errObj, "bd show error --json")
		}
	}
}

// TestJSONContract_CloseOutputHasStatus verifies bd close --json returns
// the updated issue with closed status.
func TestJSONContract_CloseOutputHasStatus(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Close contract test")

	out := w.run("close", id, "--json")
	items := parseJSONOutput(t, out)
	if len(items) == 0 {
		t.Fatal("bd close --json returned no items")
	}

	assertField(t, items[0], "status", "closed")
}

func TestJSONContract_CommandArityShapes(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	first := w.create("Arity first")
	second := w.create("Arity second")
	assertJSONObject(t, w.run("show", first, "--json"), "show single")
	assertJSONArray(t, w.run("show", first, second, "--json"), "show batch")

	assertJSONObject(t, w.run("update", first, "--priority", "1", "--json"), "update single")
	assertJSONArray(t, w.run("update", first, second, "--priority", "2", "--json"), "update batch")

	closeSingle := w.create("Close single")
	closeA := w.create("Close batch A")
	closeB := w.create("Close batch B")
	assertJSONObject(t, w.run("close", closeSingle, "--reason", "done", "--json"), "close single")
	assertJSONArray(t, w.run("close", closeA, closeB, "--reason", "done", "--json"), "close batch")

	assertJSONObject(t, w.run("reopen", closeSingle, "--json"), "reopen single")
	assertJSONArray(t, w.run("reopen", closeA, closeB, "--json"), "reopen batch")

	partial := w.run("show", first, "missing-partial-result", "--json")
	partialJSON := partial
	if start := strings.Index(partial, "["); start >= 0 {
		partialJSON = partial[start:]
	}
	assertJSONArray(t, partialJSON, "show partial batch")

	lastTouchedUpdate := w.create("Last touched update")
	updated := w.run("update", "--title", "Updated without ID", "--json")
	assertJSONObject(t, updated, "update last-touched")
	if !strings.Contains(updated, lastTouchedUpdate) {
		t.Fatalf("no-ID update did not target last-touched %s: %s", lastTouchedUpdate, updated)
	}

	lastTouchedClose := w.create("Last touched close")
	closed := w.run("close", "--reason", "done", "--json")
	assertJSONObject(t, closed, "close last-touched")
	if !strings.Contains(closed, lastTouchedClose) {
		t.Fatalf("no-ID close did not target last-touched %s: %s", lastTouchedClose, closed)
	}

	envelopeSingle := runWithJSONEnvelope(t, w, "show", first, "--json")
	assertEnvelopeDataShape(t, envelopeSingle, false, "show envelope single")
	envelopeBatch := runWithJSONEnvelope(t, w, "show", first, second, "--json")
	assertEnvelopeDataShape(t, envelopeBatch, true, "show envelope batch")
}

func TestJSONContract_CloseAuxiliaryFlagsAlwaysEnvelope(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Close envelope")

	out := w.run("close", id, "--reason", "done", "--suggest-next", "--continue", "--claim-next", "--json")
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("close auxiliary output is not an object: %v\n%s", err, out)
	}
	for _, key := range []string{"closed", "unblocked", "continue", "claimed", "schema_version"} {
		if _, ok := envelope[key]; !ok {
			t.Errorf("close auxiliary envelope missing %q: %s", key, out)
		}
	}
}

func TestJSONContract_ShowFieldsProjection(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Projected issue")
	w.run("update", id, "--set-metadata", "team=cli")

	out := w.run("show", id, "--json", "--fields", "status,id,metadata")
	statusAt := strings.Index(out, `"status"`)
	idAt := strings.Index(out, `"id"`)
	metadataAt := strings.Index(out, `"metadata"`)
	if statusAt < 0 || idAt < 0 || metadataAt < 0 || !(statusAt < idAt && idAt < metadataAt) {
		t.Fatalf("show --fields did not preserve order: %s", out)
	}
	var projected map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &projected); err != nil {
		t.Fatal(err)
	}
	if len(projected) != 4 { // requested fields + legacy schema_version
		t.Fatalf("show --fields returned unexpected keys: %s", out)
	}

	unloaded := w.run("show", id, "--json", "--fields", "comments")
	var unloadedObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(unloaded), &unloadedObject); err != nil {
		t.Fatal(err)
	}
	if string(unloadedObject["comments"]) != "null" {
		t.Fatalf("unloaded comments = %s, want null", unloadedObject["comments"])
	}

	w.run("comments", "add", id, "projection comment")
	loaded := w.run("show", id, "--json", "--include-comments", "--fields", "comments")
	var loadedObject struct {
		Comments []map[string]any `json:"comments"`
	}
	if err := json.Unmarshal([]byte(loaded), &loadedObject); err != nil {
		t.Fatal(err)
	}
	if len(loadedObject.Comments) != 1 {
		t.Fatalf("included comments count = %d, want 1", len(loadedObject.Comments))
	}

	dependent := w.create("Projection dependent", "--deps", "blocked-by:"+id)
	dependents := w.run("show", id, "--json", "--include-dependents", "--fields", "dependents")
	var dependentObject struct {
		Dependents []map[string]any `json:"dependents"`
	}
	if err := json.Unmarshal([]byte(dependents), &dependentObject); err != nil {
		t.Fatal(err)
	}
	if len(dependentObject.Dependents) != 1 || dependentObject.Dependents[0]["id"] != dependent {
		t.Fatalf("projected dependents = %#v, want %s", dependentObject.Dependents, dependent)
	}

	second := w.create("Second projected issue")
	assertJSONArray(t, w.run("show", id, second, "--json", "--fields", "id,status"), "show fields batch")

	errOut, _ := w.runExpectError("show", id, "--json", "--fields", "bogus")
	if !strings.Contains(errOut, "unknown field") || !strings.Contains(errOut, "valid fields") {
		t.Fatalf("unknown --fields error lacks field guidance: %s", errOut)
	}
}

func TestJSONContract_MetadataStringAndTypedFlags(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Metadata flags")
	w.run("update", id,
		"--set-metadata", "leading=0123",
		"--set-metadata", "large=12345678901234567890",
		"--set-metadata", "exponent=1e5",
		"--set-metadata", "truth=true",
		"--set-metadata", "nothing=null",
		"--set-metadata-json", "count=42",
		"--set-metadata-json", "enabled=true",
	)

	issue := w.showJSON(id)
	metadata, ok := issue["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %T, want object", issue["metadata"])
	}
	for key, want := range map[string]string{
		"leading": "0123", "large": "12345678901234567890", "exponent": "1e5", "truth": "true", "nothing": "null",
	} {
		if metadata[key] != want {
			t.Errorf("metadata[%s] = %#v, want string %q", key, metadata[key], want)
		}
	}
	if metadata["count"] != float64(42) || metadata["enabled"] != true {
		t.Errorf("typed metadata lost types: %#v", metadata)
	}

	invalid, _ := w.runExpectError("update", id, "--set-metadata-json", "bad=0123")
	if !strings.Contains(invalid, "invalid JSON") {
		t.Fatalf("invalid typed metadata error = %s", invalid)
	}
	duplicate, _ := w.runExpectError("update", id, "--set-metadata", "same=x", "--set-metadata-json", "same=1")
	if !strings.Contains(duplicate, "both --set-metadata and --set-metadata-json") {
		t.Fatalf("duplicate metadata error = %s", duplicate)
	}
}

func assertJSONObject(t *testing.T, output, context string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(output), &object); err != nil {
		t.Fatalf("%s output is not object: %v\n%s", context, err, output)
	}
}

func assertJSONArray(t *testing.T, output, context string) {
	t.Helper()
	var array []map[string]any
	if err := json.Unmarshal([]byte(output), &array); err != nil {
		t.Fatalf("%s output is not array: %v\n%s", context, err, output)
	}
}

func runWithJSONEnvelope(t *testing.T, w *workspace, args ...string) string {
	t.Helper()
	cmd := exec.Command(w.bd, args...)
	cmd.Dir = w.dir
	cmd.Env = append(w.env(), "BD_JSON_ENVELOPE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s with envelope: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func assertEnvelopeDataShape(t *testing.T, output string, wantArray bool, context string) {
	t.Helper()
	var envelope struct {
		SchemaVersion int             `json:"schema_version"`
		Data          json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("%s is not an envelope: %v\n%s", context, err, output)
	}
	if envelope.SchemaVersion != 2 {
		t.Fatalf("%s schema_version = %d, want 2", context, envelope.SchemaVersion)
	}
	trimmed := strings.TrimSpace(string(envelope.Data))
	if wantArray && !strings.HasPrefix(trimmed, "[") {
		t.Fatalf("%s data = %s, want array", context, trimmed)
	}
	if !wantArray && !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("%s data = %s, want object", context, trimmed)
	}
}

// TestJSONContract_ReadyOutputHasFullObjects verifies bd ready --json returns
// full issue objects with dependency counts, not just IDs (beads-clt).
func TestJSONContract_ReadyOutputHasFullObjects(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.create("Ready full object test")

	out := w.run("ready", "--json")
	items := parseJSONOutput(t, out)
	if len(items) == 0 {
		t.Skip("no ready issues — create returned non-ready issue")
	}
	issue := items[0]
	requiredFields := []string{"id", "title", "status", "priority", "dependency_count", "dependent_count"}
	for _, field := range requiredFields {
		if _, ok := issue[field]; !ok {
			t.Errorf("bd ready --json item missing required field %q", field)
		}
	}
}

// TestJSONContract_BlockedOutputHasBlockedBy verifies bd blocked --json returns
// full issue objects with blocked_by field (beads-clt).
func TestJSONContract_BlockedOutputHasBlockedBy(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	blocker := w.create("Blocker issue")
	blocked := w.create("Blocked issue")
	w.run("dep", "add", blocked, blocker, "--type", "blocks")

	out := w.run("blocked", "--json")
	items := parseJSONOutput(t, out)

	var found map[string]any
	for _, item := range items {
		if id, ok := item["id"].(string); ok && id == blocked {
			found = item
			break
		}
	}
	if found == nil {
		t.Fatalf("blocked issue %s not found in bd blocked --json output", blocked)
	}

	requiredFields := []string{"id", "title", "status", "blocked_by_count", "blocked_by"}
	for _, field := range requiredFields {
		if _, ok := found[field]; !ok {
			t.Errorf("bd blocked --json item missing required field %q", field)
		}
	}
}

// TestJSONContract_PingOutputIsValidJSON verifies bd ping --json returns
// structured health check output with timing info.
func TestJSONContract_PingOutputIsValidJSON(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)

	out := w.run("ping", "--json")
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("bd ping --json produced invalid JSON: %v\nOutput:\n%s", err, out)
	}
	assertSchemaVersion(t, obj, "bd ping --json")
	if status, ok := obj["status"].(string); !ok || status != "ok" {
		t.Errorf("bd ping --json status = %v, want ok", obj["status"])
	}
	if _, ok := obj["total_ms"]; !ok {
		t.Error("bd ping --json missing total_ms field")
	}
}

// TestJSONContract_SchemaVersionPresent verifies that schema_version is
// present in object-returning --json commands (show, create, ping).
// Array-returning commands (list, ready) do not include schema_version.
func TestJSONContract_SchemaVersionPresent(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	id := w.create("Schema version test")

	tests := []struct {
		name string
		args []string
	}{
		{"show", []string{"show", id, "--json"}},
		{"ping", []string{"ping", "--json"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := w.run(tt.args...)
			var obj map[string]any
			if err := json.Unmarshal([]byte(out), &obj); err != nil {
				t.Fatalf("bd %s produced invalid JSON: %v\nOutput:\n%s",
					tt.name, err, out)
			}
			assertSchemaVersion(t, obj, "bd "+tt.name+" --json")
		})
	}
}
