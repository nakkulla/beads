//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

func TestEmbeddedWriteReadbackAndAutoExportConsistency(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd, "--prefix", "wr")
	runBDForReadback(t, bd, dir, "config", "set", "export.auto", "true")
	runBDForReadback(t, bd, dir, "config", "set", "export.interval", "1ms")

	issue := bdCreate(t, bd, dir, "initial readback title", "--type", "task", "--priority", "2")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "initial readback title", types.StatusOpen, "")

	time.Sleep(10 * time.Millisecond)
	bdUpdate(t, bd, dir, issue.ID, "--title", "updated readback title", "--set-metadata", "phase=updated")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "updated readback title", types.StatusOpen, "updated")

	time.Sleep(10 * time.Millisecond)
	bdClose(t, bd, dir, issue.ID, "--reason", "readback consistency test")
	assertReadbackIssue(t, bd, dir, beadsDir, issue.ID, "updated readback title", types.StatusClosed, "updated")
}

func runBDForReadback(t *testing.T, bd, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func assertReadbackIssue(t *testing.T, bd, dir, beadsDir, id, wantTitle string, wantStatus types.Status, wantPhase string) {
	t.Helper()

	shown := bdShow(t, bd, dir, id)
	if shown.Title != wantTitle {
		t.Fatalf("bd show title = %q, want %q", shown.Title, wantTitle)
	}
	if shown.Status != wantStatus {
		t.Fatalf("bd show status = %q, want %q", shown.Status, wantStatus)
	}
	if wantPhase != "" && metadataStringValue(t, shown.Metadata, "phase") != wantPhase {
		t.Fatalf("bd show metadata phase = %q, want %q", metadataStringValue(t, shown.Metadata, "phase"), wantPhase)
	}

	listed := bdListJSON(t, bd, dir, "--all")
	found := false
	for _, got := range listed {
		if got.ID == id {
			found = true
			if got.Title != wantTitle {
				t.Fatalf("bd list title = %q, want %q", got.Title, wantTitle)
			}
			if got.Status != wantStatus {
				t.Fatalf("bd list status = %q, want %q", got.Status, wantStatus)
			}
		}
	}
	if !found {
		t.Fatalf("bd list did not include %s", id)
	}

	exported := readExportedIssue(t, filepath.Join(beadsDir, "issues.jsonl"), id)
	if exported.Title != wantTitle {
		t.Fatalf("export title = %q, want %q", exported.Title, wantTitle)
	}
	if exported.Status != wantStatus {
		t.Fatalf("export status = %q, want %q", exported.Status, wantStatus)
	}
	if wantPhase != "" && metadataStringValue(t, exported.Metadata, "phase") != wantPhase {
		t.Fatalf("export metadata phase = %q, want %q", metadataStringValue(t, exported.Metadata, "phase"), wantPhase)
	}
}

func metadataStringValue(t *testing.T, raw json.RawMessage, key string) string {
	t.Helper()
	if len(raw) == 0 {
		return ""
	}
	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	return values[key]
}

func readExportedIssue(t *testing.T, path, id string) *types.Issue {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export %s: %v", path, err)
	}
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			continue
		}
		if issue.ID == id {
			return &issue
		}
		_ = lineNo
	}
	t.Fatalf("export %s did not contain issue %s", path, id)
	return nil
}
