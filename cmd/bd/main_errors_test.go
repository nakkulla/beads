package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func TestHandleFreshCloneError_UsesBootstrapFirstGuidance(t *testing.T) {
	err := errors.New("post-migration validation failed: required config key missing: issue_prefix")

	origStderr := os.Stderr
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stderr = w

	handled := handleFreshCloneError(err)
	_ = w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	if _, copyErr := io.Copy(&buf, r); copyErr != nil {
		t.Fatal(copyErr)
	}
	_ = r.Close()

	if !handled {
		t.Fatal("expected fresh clone error to be handled")
	}
	msg := buf.String()
	if !strings.Contains(msg, "bd bootstrap") {
		t.Fatalf("expected bootstrap guidance, got:\n%s", msg)
	}
	if strings.Contains(msg, "To initialize a new database: bd init") {
		t.Fatalf("did not expect init-first guidance for fresh clone recovery, got:\n%s", msg)
	}
	if !strings.Contains(msg, "brand-new database from scratch") {
		t.Fatalf("expected brand-new project fallback note, got:\n%s", msg)
	}
}

func TestHandleFreshCloneDatabaseOpenErrorUsesStructuredJSON(t *testing.T) {
	err := errors.New("post-migration validation failed: required config key missing: issue_prefix")
	oldJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = oldJSON })
	jsonOutput = true

	for _, envelope := range []bool{false, true} {
		t.Run(fmt.Sprintf("envelope=%t", envelope), func(t *testing.T) {
			if envelope {
				t.Setenv("BD_JSON_ENVELOPE", "1")
			} else {
				t.Setenv("BD_JSON_ENVELOPE", "")
			}
			var handled bool
			var runErr error
			stderr := captureStderr(t, func() {
				handled, runErr = handleFreshCloneDatabaseOpenError(err)
			})
			if !handled {
				t.Fatal("fresh-clone error was not handled")
			}
			if code, ok := exitCodeFromError(runErr); !ok || code != 1 {
				t.Fatalf("run error = %v, want exit code 1", runErr)
			}
			payload := decodeSingleJSONPayload(t, stderr)
			if payload["failure_code"] != string(storage.FailureDatabaseOpenFailed) {
				t.Fatalf("payload = %#v", payload)
			}
			evidence := payload["evidence"].(map[string]interface{})
			if evidence["operation"] != "database_open" {
				t.Fatalf("evidence = %#v", evidence)
			}
			if strings.Contains(stderr, "To diagnose") || strings.Contains(stderr, "bd bootstrap") {
				t.Fatalf("human guidance was appended to JSON stderr: %q", stderr)
			}
		})
	}
}
