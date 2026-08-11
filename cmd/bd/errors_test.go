package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/schema"
)

func marshalJSONForTest(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(data)
}

func TestBuildJSONClassifiedErrorAddsFailureCodeWithoutLeakingURLCredentials(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "")
	payload := buildJSONClassifiedError(
		"push https://alice:secret@example.test/repo?token=abc failed",
		"pull first",
		storage.FailureSyncRemoteAhead,
		map[string]interface{}{"operation": "dolt_push", "remote": map[string]string{"name": "origin", "transport": "https"}},
	)
	encoded := marshalJSONForTest(t, payload)
	if strings.Contains(encoded, "alice:secret") || strings.Contains(encoded, "token=abc") {
		t.Fatalf("classified JSON leaked credentials: %s", encoded)
	}
	if !strings.Contains(encoded, `"failure_code":"sync_remote_ahead"`) {
		t.Fatalf("classified JSON omitted failure_code: %s", encoded)
	}
}

func TestBuildJSONClassifiedErrorKeepsFieldsInsideEnvelopeData(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "1")
	payload := buildJSONClassifiedError("failed", "", storage.FailureOperationFailedUnknown, map[string]interface{}{"operation": "dolt_pull"})
	outer, ok := payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload type = %T, want map", payload)
	}
	data := outer["data"].(map[string]interface{})
	if data["failure_code"] != storage.FailureOperationFailedUnknown {
		t.Fatalf("data failure_code = %#v", data["failure_code"])
	}
	if _, ok := outer["failure_code"]; ok {
		t.Fatal("failure_code must stay inside envelope data")
	}
}

func TestBuildJSONWarningKeepsNonFatalWarningMachineReadable(t *testing.T) {
	t.Setenv("BD_JSON_ENVELOPE", "")
	payload := buildJSONWarning("dolt auto-push failed", storage.FailureRemoteUnreachable, map[string]interface{}{"operation": "auto_push"})
	encoded := marshalJSONForTest(t, payload)
	if !strings.Contains(encoded, `"warning":"dolt auto-push failed"`) || !strings.Contains(encoded, `"failure_code":"remote_unreachable"`) {
		t.Fatalf("warning payload = %s", encoded)
	}
}

func TestDatabaseOpenFailureCodeUsesTypedAndNarrowEvidence(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want storage.FailureCode
	}{
		{"typed database missing", &storage.ClassifiedError{Code: storage.FailureDatabaseNotFound, Cause: errors.New("display text changed")}, storage.FailureDatabaseNotFound},
		{"schema skew", &schema.SchemaSkewError{DBVersion: 45, BinaryVersion: 42}, storage.FailureSchemaMigrationRequired},
		{"remote migration gate", &schema.RemoteMigrateGateError{CurrentVersion: 48, LatestVersion: 50, Pending: 2}, storage.FailureSchemaMigrationRequired},
		{"positive corruption", errors.New("corrupt manifest with no recoverable data"), storage.FailureLocalStoreCorrupt},
		{"generic unknown database", errors.New("unknown database"), storage.FailureDatabaseOpenFailed},
		{"unclassified", errors.New("open failed for an undocumented reason"), storage.FailureDatabaseOpenFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := databaseOpenFailureCode(tt.err); got != tt.want {
				t.Fatalf("databaseOpenFailureCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReportDatabaseOpenFailureWritesSingleJSONToStderr(t *testing.T) {
	for _, envelope := range []bool{false, true} {
		t.Run(fmt.Sprintf("envelope=%t", envelope), func(t *testing.T) {
			if envelope {
				t.Setenv("BD_JSON_ENVELOPE", "1")
			} else {
				t.Setenv("BD_JSON_ENVELOPE", "")
			}
			stderr := captureStderr(t, func() {
				reportDatabaseOpenFailure(&storage.ClassifiedError{
					Code:  storage.FailureDatabaseNotFound,
					Cause: errors.New("database display text changed"),
				})
			})
			payload := decodeSingleJSONPayload(t, stderr)
			if payload["failure_code"] != string(storage.FailureDatabaseNotFound) {
				t.Fatalf("payload = %#v", payload)
			}
			evidence := payload["evidence"].(map[string]interface{})
			if evidence["operation"] != "database_open" {
				t.Fatalf("evidence = %#v", evidence)
			}
		})
	}
}

func TestJsonStderrError_StructuredOutput(t *testing.T) {
	tests := []struct {
		name    string
		message string
		hint    string
	}{
		{"message_only", "database not found", ""},
		{"message_with_hint", "database not found", "Run 'bd init' to create one"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := map[string]interface{}{
				"schema_version": JSONSchemaVersion,
				"error":          tt.message,
			}
			if tt.hint != "" {
				obj["hint"] = tt.hint
			}

			data, err := json.Marshal(obj)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if parsed["schema_version"] != float64(JSONSchemaVersion) {
				t.Errorf("schema_version = %v, want %d", parsed["schema_version"], JSONSchemaVersion)
			}
			if parsed["error"] != tt.message {
				t.Errorf("error = %v, want %s", parsed["error"], tt.message)
			}
			if tt.hint != "" {
				if parsed["hint"] != tt.hint {
					t.Errorf("hint = %v, want %s", parsed["hint"], tt.hint)
				}
			} else {
				if _, ok := parsed["hint"]; ok {
					t.Errorf("hint should not be present when empty")
				}
			}
		})
	}
}
