package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDepListFormatContract(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	a := w.create("Dependency source A")
	b := w.create("Dependency target B")
	c := w.create("Dependency source C")
	d := w.create("Dependency target D")
	w.run("dep", "add", a, b, "--type", "blocks")
	w.run("dep", "add", c, d, "--type", "blocks")

	for _, args := range [][]string{
		{"dep", "list", a, "--json"},
		{"dep", "list", a, c, "--json"},
		{"dep", "list", b, "--direction", "up", "--json"},
		{"dep", "list", b, d, "--direction", "up", "--json"},
	} {
		out := w.run(args...)
		var items []map[string]any
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("bd %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		for _, item := range items {
			if _, ok := item["id"]; !ok {
				t.Errorf("default dep list item lacks issue id: %#v", item)
			}
			if _, ok := item["dependency_type"]; !ok {
				t.Errorf("default dep list item lacks dependency_type: %#v", item)
			}
			if _, edge := item["issue_id"]; edge {
				t.Errorf("default dep list emitted edge record: %#v", item)
			}
		}
	}

	down := parseDepEdges(t, w.run("dep", "list", a, "--format", "edges", "--json"))
	if len(down) != 1 || down[0].IssueID != a || down[0].DependsOnID != b || down[0].Type != "blocks" {
		t.Fatalf("down edge = %#v", down)
	}
	up := parseDepEdges(t, w.run("dep", "list", b, "--direction", "up", "--format", "edges", "--json"))
	if len(up) != 1 || up[0].IssueID != a || up[0].DependsOnID != b || up[0].Type != "blocks" {
		t.Fatalf("up edge = %#v", up)
	}

	errOut, _ := w.runExpectError("dep", "list", a, "--format", "bogus", "--json")
	if !strings.Contains(errOut, "must be 'issues' or 'edges'") {
		t.Fatalf("unknown dep format error = %s", errOut)
	}
}

type depListWireEdge struct {
	IssueID     string `json:"issue_id"`
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
}

func parseDepEdges(t *testing.T, output string) []depListWireEdge {
	t.Helper()
	var edges []depListWireEdge
	if err := json.Unmarshal([]byte(output), &edges); err != nil {
		t.Fatalf("parse dep edges: %v\n%s", err, output)
	}
	return edges
}
