package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestShowFieldsFlag(t *testing.T) {
	if showCmd.Flags().Lookup("fields") == nil {
		t.Fatal("show --fields flag is not registered")
	}
}

func TestProjectIssueDetailsPreservesRequestedOrder(t *testing.T) {
	fields, err := parseShowFields("status,id,metadata")
	if err != nil {
		t.Fatal(err)
	}
	details := &types.IssueDetails{Issue: types.Issue{
		ID:       "beads-1",
		Status:   types.StatusInProgress,
		Metadata: json.RawMessage(`{"team":"cli"}`),
	}}
	projected, err := projectIssueDetails(details, fields)
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("BD_JSON_ENVELOPE", "0")
	out := string(captureJSONShapeStdout(t, func() error {
		return outputJSONForRequest(1, []orderedJSONObject{projected})
	}))
	statusAt := strings.Index(out, `"status"`)
	idAt := strings.Index(out, `"id"`)
	metadataAt := strings.Index(out, `"metadata"`)
	if statusAt < 0 || idAt < 0 || metadataAt < 0 || !(statusAt < idAt && idAt < metadataAt) {
		t.Fatalf("requested field order not preserved: %s", out)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	for key := range got {
		if key != "status" && key != "id" && key != "metadata" && key != "schema_version" {
			t.Fatalf("unexpected projected key %q in %s", key, out)
		}
	}
}

func TestProjectIssueDetailsIncludesNullForUnloadedField(t *testing.T) {
	fields, err := parseShowFields("comments")
	if err != nil {
		t.Fatal(err)
	}
	projected, err := projectIssueDetails(&types.IssueDetails{}, fields)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), `{"comments":null}`; got != want {
		t.Fatalf("projection = %s, want %s", got, want)
	}
}

func TestParseShowFieldsRejectsUnknownField(t *testing.T) {
	_, err := parseShowFields("id,bogus")
	if err == nil {
		t.Fatal("unknown field accepted")
	}
	if !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "valid fields:") || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("unknown field error lacks valid names: %v", err)
	}
}
