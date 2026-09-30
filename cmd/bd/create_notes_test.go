//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/types"
)

// TestCreateWithNotes verifies that the --notes flag works correctly
// during issue creation in both direct mode and RPC mode.
func TestCreateWithNotes(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("DirectMode_WithNotes", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue with notes",
			Notes:     "These are my test notes",
			Priority:  1,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != "These are my test notes" {
			t.Errorf("expected notes 'These are my test notes', got %q", retrieved.Notes)
		}
	})

	t.Run("DirectMode_WithoutNotes", func(t *testing.T) {
		issue := &types.Issue{
			Title:     "Issue without notes",
			Priority:  2,
			IssueType: types.TypeBug,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != "" {
			t.Errorf("expected empty notes, got %q", retrieved.Notes)
		}
	})

	t.Run("DirectMode_WithNotesAndOtherFields", func(t *testing.T) {
		issue := &types.Issue{
			Title:              "Full issue with notes",
			Description:        "Detailed description",
			Design:             "Design notes here",
			AcceptanceCriteria: "All tests pass",
			Notes:              "Additional implementation notes",
			Priority:           1,
			IssueType:          types.TypeFeature,
			Status:             types.StatusOpen,
			Assignee:           "testuser",
			CreatedAt:          time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify all fields
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Title != "Full issue with notes" {
			t.Errorf("expected title 'Full issue with notes', got %q", retrieved.Title)
		}
		if retrieved.Description != "Detailed description" {
			t.Errorf("expected description, got %q", retrieved.Description)
		}
		if retrieved.Design != "Design notes here" {
			t.Errorf("expected design, got %q", retrieved.Design)
		}
		if retrieved.AcceptanceCriteria != "All tests pass" {
			t.Errorf("expected acceptance criteria, got %q", retrieved.AcceptanceCriteria)
		}
		if retrieved.Notes != "Additional implementation notes" {
			t.Errorf("expected notes 'Additional implementation notes', got %q", retrieved.Notes)
		}
		if retrieved.Assignee != "testuser" {
			t.Errorf("expected assignee 'testuser', got %q", retrieved.Assignee)
		}
	})

	t.Run("DirectMode_NotesWithSpecialCharacters", func(t *testing.T) {
		specialNotes := "Notes with special chars: \n- Bullet point\n- Another one\n\nAnd \"quotes\" and 'apostrophes'"
		issue := &types.Issue{
			Title:     "Issue with special char notes",
			Notes:     specialNotes,
			Priority:  2,
			IssueType: types.TypeTask,
			Status:    types.StatusOpen,
			CreatedAt: time.Now(),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatalf("failed to create issue: %v", err)
		}

		// Retrieve and verify
		retrieved, err := s.GetIssue(ctx, issue.ID)
		if err != nil {
			t.Fatalf("failed to retrieve issue: %v", err)
		}

		if retrieved.Notes != specialNotes {
			t.Errorf("notes mismatch.\nExpected: %q\nGot: %q", specialNotes, retrieved.Notes)
		}
	})
}

func TestEmbeddedCreateAppendNotes(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt create tests")
	}
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "can")
	for _, tc := range []struct {
		name   string
		values []string
		want   string
	}{
		{"single", []string{"a"}, "a"},
		{"repeated", []string{"a", "b"}, "a\nb"},
		{"commas_and_newlines", []string{"a,b", "c\nd"}, "a,b\nc\nd"},
		{"empty_values", []string{"", "b", ""}, "\nb\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"Append notes " + tc.name, "--type", "task"}
			for _, value := range tc.values {
				args = append(args, "--append-notes", value)
			}
			issue := bdCreate(t, bd, dir, args...)
			if got := bdShow(t, bd, dir, issue.ID); got.Notes != tc.want {
				t.Errorf("notes = %q, want %q", got.Notes, tc.want)
			}
		})
	}
	for _, notes := range []string{"x", ""} {
		out := bdCreateFail(t, bd, dir, "Conflicting notes", "--notes", notes, "--append-notes", "y")
		if !strings.Contains(out, "cannot specify both --notes and --append-notes") {
			t.Errorf("expected conflict error, got %s", out)
		}
	}
}

func TestGatherAppendNotes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		set  bool
	}{
		{"absent", nil, "", false},
		{"single", []string{"--append-notes", "a"}, "a", true},
		{"repeated", []string{"--append-notes", "a", "--append-notes", "b"}, "a\nb", true},
		{"literal", []string{"--append-notes", "a,b", "--append-notes", "c\nd"}, "a,b\nc\nd", true},
		{"empty", []string{"--append-notes", ""}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			registerCommonIssueFlags(cmd)
			cmd.Flags().String("priority", "2", "")
			if err := cmd.ParseFlags(tc.args); err != nil {
				t.Fatal(err)
			}
			created, err := gatherCreateInput(cmd, []string{"Test notes input"})
			if err != nil {
				t.Fatal(err)
			}
			if created.notes != tc.want {
				t.Errorf("create notes = %q, want %q", created.notes, tc.want)
			}
			updated := gatherUpdateInput(context.Background(), cmd)
			if updated.appendNotes != tc.want || updated.hasAppendNotes != tc.set {
				t.Errorf("update notes = %q, set = %t; want %q, %t", updated.appendNotes, updated.hasAppendNotes, tc.want, tc.set)
			}
		})
	}
	cmd := &cobra.Command{}
	registerCommonIssueFlags(cmd)
	if err := cmd.ParseFlags([]string{"--notes", "x", "--append-notes", "y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := gatherCreateInput(cmd, []string{"Test conflicting notes"}); err == nil {
		t.Fatalf("expected notes conflict, got %v", err)
	}
}
