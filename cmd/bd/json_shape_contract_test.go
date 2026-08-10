package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

func TestOutputJSONForRequestShapeContract(t *testing.T) {
	if JSONSchemaVersion != 2 {
		t.Fatalf("JSONSchemaVersion = %d, want 2 for arity shape change", JSONSchemaVersion)
	}

	items := []map[string]any{{"id": "beads-1"}}
	for _, tt := range []struct {
		name           string
		envelope       bool
		requestedCount int
		wantObject     bool
	}{
		{name: "legacy single", requestedCount: 1, wantObject: true},
		{name: "legacy partial batch", requestedCount: 2, wantObject: false},
		{name: "envelope single", envelope: true, requestedCount: 1, wantObject: true},
		{name: "envelope partial batch", envelope: true, requestedCount: 2, wantObject: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envelope {
				t.Setenv("BD_JSON_ENVELOPE", "1")
			} else {
				t.Setenv("BD_JSON_ENVELOPE", "0")
			}

			out := captureJSONShapeStdout(t, func() error {
				return outputJSONForRequest(tt.requestedCount, items)
			})
			var top any
			if err := json.Unmarshal(out, &top); err != nil {
				t.Fatalf("unmarshal output: %v\n%s", err, out)
			}

			payload := top
			if tt.envelope {
				envelope, ok := top.(map[string]any)
				if !ok {
					t.Fatalf("top-level = %T, want envelope object", top)
				}
				if envelope["schema_version"] != float64(2) {
					t.Fatalf("envelope schema_version = %v, want 2", envelope["schema_version"])
				}
				payload = envelope["data"]
			}

			if tt.wantObject {
				object, ok := payload.(map[string]any)
				if !ok {
					t.Fatalf("payload = %T, want object", payload)
				}
				if !tt.envelope && object["schema_version"] != float64(2) {
					t.Fatalf("legacy object schema_version = %v, want 2", object["schema_version"])
				}
			} else if _, ok := payload.([]any); !ok {
				t.Fatalf("payload = %T, want array", payload)
			}
		})
	}
}

func TestBuildCloseJSONEnvelopeKeepsRequestedEmptyKeys(t *testing.T) {
	payload := buildCloseJSONEnvelope(nil, true, true, true, nil, nil, nil)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"closed", "unblocked", "continue", "claimed"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("close envelope missing requested key %q: %s", key, encoded)
		}
	}
	if string(got["closed"]) != "[]" || string(got["unblocked"]) != "[]" {
		t.Fatalf("empty array keys encoded incorrectly: %s", encoded)
	}
	if string(got["continue"]) != "null" || string(got["claimed"]) != "null" {
		t.Fatalf("empty object keys encoded incorrectly: %s", encoded)
	}
}

func captureJSONShapeStdout(t *testing.T, fn func() error) []byte {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = fn()
	_ = w.Close()
	if err != nil {
		t.Fatalf("output: %v", err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return buf.Bytes()
}
