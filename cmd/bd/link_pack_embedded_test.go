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
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "lnk")
	issue := bdCreate(t, bd, dir, "linked worktree", "--description", "historic body")

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
	store := openStore(t, beadsDir, "lnk")
	linkCommit, err := store.GetCurrentCommit(t.Context())
	if err != nil {
		t.Fatalf("GetCurrentCommit: %v", err)
	}
	store.Close()
	const prURL = "https://github.com/example/beads/pull/59"
	bdUpdate(t, bd, dir, issue.ID, "--set-metadata", "pr_url="+prURL)

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
	var links map[string]any
	if err := json.Unmarshal([]byte(linksOut), &links); err != nil {
		t.Fatalf("parse show --links JSON: %v\n%s", err, linksOut)
	}
	linkData, ok := links["links"].(map[string]any)
	if !ok || linkData["branch"] != issue.ID || linkData["pr_url"] != prURL {
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

	asOfOut := bdShowRaw(t, bd, dir, issue.ID, "--json", "--links", "--as-of", linkCommit)
	var asOfItems []map[string]any
	if err := json.Unmarshal([]byte(asOfOut), &asOfItems); err != nil {
		t.Fatalf("parse show --as-of --links JSON: %v\n%s", err, asOfOut)
	}
	asOfLinks, _ := asOfItems[0]["links"].(map[string]any)
	if asOfLinks["pr_url"] != prURL {
		t.Fatalf("--as-of links are not current: %v", asOfLinks)
	}
	asOfHuman := bdShowRaw(t, bd, dir, issue.ID, "--links", "--as-of", linkCommit)
	if !strings.Contains(asOfHuman, "historic body") || !strings.Contains(asOfHuman, "LINKS") {
		t.Fatalf("human --as-of --links lost existing sections: %s", asOfHuman)
	}

	unmatched := bdCreate(t, bd, dir, "unmatched issue")
	unmatchedOut := bdShowRaw(t, bd, dir, unmatched.ID, "--json", "--links")
	var unmatchedItem map[string]any
	if err := json.Unmarshal([]byte(unmatchedOut), &unmatchedItem); err != nil {
		t.Fatalf("parse unmatched links JSON: %v\n%s", err, unmatchedOut)
	}
	unmatchedLinks, _ := unmatchedItem["links"].(map[string]any)
	if unmatchedLinks["worktree"] != nil {
		t.Fatalf("unmatched worktree = %v, want null", unmatchedLinks["worktree"])
	}

	fallback := bdCreate(t, bd, dir, "branch-name fallback")
	if out, err := bdRunWithFlockRetry(t, bd, dir, "worktree", "create", fallback.ID); err != nil {
		t.Fatalf("create fallback worktree: %v\n%s", err, out)
	}
	fallbackOut := bdShowRaw(t, bd, dir, fallback.ID, "--json", "--links")
	var fallbackItem map[string]any
	if err := json.Unmarshal([]byte(fallbackOut), &fallbackItem); err != nil {
		t.Fatalf("parse fallback links JSON: %v\n%s", err, fallbackOut)
	}
	fallbackLinks, _ := fallbackItem["links"].(map[string]any)
	if _, ok := fallbackLinks["worktree"].(map[string]any); !ok {
		t.Fatalf("branch-name fallback did not find worktree: %v", fallbackLinks)
	}

	if out := bdShowFail2(t, bd, dir, issue.ID, "--children", "--links"); !strings.Contains(out, "cannot combine") {
		t.Fatalf("children/links conflict: %s", out)
	}
	child := bdCreate(t, bd, dir, "child", "--parent", issue.ID)
	childrenOut := bdShowRaw(t, bd, dir, issue.ID, "--children", "--json")
	var children []map[string]any
	if err := json.Unmarshal([]byte(childrenOut), &children); err != nil {
		t.Fatalf("parse children-only JSON: %v\n%s", err, childrenOut)
	}
	if len(children) != 1 || children[0]["id"] != child.ID {
		t.Fatalf("children-only shape changed: %v", children)
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
