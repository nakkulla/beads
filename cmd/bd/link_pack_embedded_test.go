//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedWorktreeIssueLinkAndShowLinks(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lnk")
	issue := bdCreate(t, bd, dir, "linked worktree")

	created, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "create", "--issue", issue.ID, "--json")
	if err != nil {
		t.Fatalf("worktree create --issue: %v\n%s", err, created)
	}
	var createResult map[string]any
	if err := json.Unmarshal(created, &createResult); err != nil {
		t.Fatalf("parse create JSON: %v\n%s", err, created)
	}
	if createResult["issue_id"] != issue.ID || createResult["branch"] != issue.ID {
		t.Fatalf("create link output: %v", createResult)
	}

	got := bdShow(t, bd, dir, issue.ID)
	if metadataValue(got.Metadata, "branch") != issue.ID {
		t.Fatalf("metadata.branch = %q, want %q", metadataValue(got.Metadata, "branch"), issue.ID)
	}

	listed, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "list", "--json")
	if err != nil {
		t.Fatalf("worktree list: %v\n%s", err, listed)
	}
	if !strings.Contains(string(listed), `"issue_id": "`+issue.ID+`"`) || !strings.Contains(string(listed), `"issue_source": "metadata"`) {
		t.Fatalf("worktree list did not expose link: %s", listed)
	}

	shown := bdShowDetails(t, bd, dir, issue.ID)
	_ = shown // retain the established default-shape helper as a regression check.
	linksOut := bdShowRaw(t, bd, dir, issue.ID, "--json", "--links")
	var linkItems []map[string]any
	if err := json.Unmarshal([]byte(linksOut), &linkItems); err != nil {
		t.Fatalf("parse show --links JSON: %v\n%s", err, linksOut)
	}
	if len(linkItems) != 1 {
		t.Fatalf("show --links item count = %d", len(linkItems))
	}
	links := linkItems[0]
	linkData, ok := links["links"].(map[string]any)
	if !ok || linkData["branch"] != issue.ID {
		t.Fatalf("show links = %v", links)
	}
	worktree, ok := linkData["worktree"].(map[string]any)
	if !ok {
		t.Fatalf("show worktree link = %v", linkData)
	}
	expectedPath, _ := filepath.EvalSymlinks(filepath.Join(dir, issue.ID))
	actualPath, _ := filepath.EvalSymlinks(worktree["path"].(string))
	if actualPath != expectedPath {
		t.Fatalf("show worktree link = %v", linkData)
	}

	if out := bdShowFail2(t, bd, dir, issue.ID, "--children", "--links"); !strings.Contains(out, "cannot combine") {
		t.Fatalf("children/links conflict: %s", out)
	}

	occupied := filepath.Join(dir, "occupied")
	if err := os.Mkdir(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "create", "occupied", "--issue", "lnk-missing"); err == nil || !strings.Contains(string(out), "not found") {
		t.Fatalf("missing issue must win over occupied path: err=%v out=%s", err, out)
	}
	conflict := bdCreate(t, bd, dir, "conflicting link")
	bdUpdate(t, bd, dir, conflict.ID, "--set-metadata", "branch=other")
	if out, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "create", "conflict-target", "--issue", conflict.ID); err == nil || !strings.Contains(string(out), "already linked") {
		t.Fatalf("different branch conflict: err=%v out=%s", err, out)
	}
	if out, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "create", issue.ID, "--issue", issue.ID); err == nil || !strings.Contains(string(out), "path already exists") {
		t.Fatalf("same branch must proceed to existing path check: err=%v out=%s", err, out)
	}
}
