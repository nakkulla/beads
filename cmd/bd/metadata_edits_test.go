package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyMetadataEdits_SetNewKey(t *testing.T) {
	t.Parallel()
	result, err := applyMetadataEdits(nil, []string{"team=platform"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"platform"` {
		t.Errorf("expected \"platform\", got %s", data["team"])
	}
}

func TestApplyMetadataEdits_SetOverwritesExisting(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"team":"old","sprint":"Q1"}`)
	result, err := applyMetadataEdits(existing, []string{"team=new"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"new"` {
		t.Errorf("expected \"new\", got %s", data["team"])
	}
	// sprint should be preserved
	if string(data["sprint"]) != `"Q1"` {
		t.Errorf("expected \"Q1\", got %s", data["sprint"])
	}
}

func TestApplyMetadataEdits_UnsetKey(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"team":"platform","sprint":"Q1"}`)
	result, err := applyMetadataEdits(existing, nil, []string{"team"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := data["team"]; ok {
		t.Error("expected team key to be removed")
	}
	if string(data["sprint"]) != `"Q1"` {
		t.Errorf("expected \"Q1\", got %s", data["sprint"])
	}
}

func TestApplyMetadataEdits_SetAndUnset(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"team":"platform","sprint":"Q1"}`)
	result, err := applyMetadataEdits(existing, []string{"env=prod"}, []string{"sprint"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"platform"` {
		t.Errorf("expected \"platform\", got %s", data["team"])
	}
	if string(data["env"]) != `"prod"` {
		t.Errorf("expected \"prod\", got %s", data["env"])
	}
	if _, ok := data["sprint"]; ok {
		t.Error("expected sprint key to be removed")
	}
}

func TestApplyMetadataEdits_NumericValue(t *testing.T) {
	t.Parallel()
	result, err := applyMetadataEdits(nil, []string{"story_points=5"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["story_points"]) != `"5"` {
		t.Errorf("expected string \"5\", got %s", data["story_points"])
	}
}

func TestApplyMetadataEdits_BoolValue(t *testing.T) {
	t.Parallel()
	result, err := applyMetadataEdits(nil, []string{"urgent=true"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["urgent"]) != `"true"` {
		t.Errorf("expected string \"true\", got %s", data["urgent"])
	}
}

func TestApplyMetadataEdits_NullValue(t *testing.T) {
	t.Parallel()
	result, err := applyMetadataEdits(nil, []string{"cleared=null"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["cleared"]) != `"null"` {
		t.Errorf("expected string \"null\", got %s", data["cleared"])
	}
}

func TestApplyMetadataEdits_StringPreservesNumericLookingValues(t *testing.T) {
	t.Parallel()

	values := []string{"0123", "12345678901234567890", "1e5", "true", "null"}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			result, err := applyMetadataEdits(nil, []string{"value=" + value}, nil)
			if err != nil {
				t.Fatalf("applyMetadataEdits: %v", err)
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(result, &data); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			want, _ := json.Marshal(value)
			if string(data["value"]) != string(want) {
				t.Errorf("value %q encoded as %s, want JSON string %s", value, data["value"], want)
			}
		})
	}
}

func TestApplyMetadataEditsWithJSON_StoresTypedValues(t *testing.T) {
	t.Parallel()

	result, err := applyMetadataEditsWithJSON(nil, nil, []string{
		"count=42",
		"enabled=true",
		"cleared=null",
		`nested={"key":"value"}`,
	}, nil)
	if err != nil {
		t.Fatalf("applyMetadataEditsWithJSON: %v", err)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for key, want := range map[string]string{
		"count": "42", "enabled": "true", "cleared": "null", "nested": `{"key":"value"}`,
	} {
		if got := string(data[key]); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
}

func TestApplyMetadataEditsWithJSON_RejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := applyMetadataEditsWithJSON(nil, nil, []string{"count=0123"}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("error = %v, want invalid JSON error", err)
	}
}

func TestApplyMetadataEditsWithJSON_RejectsDuplicateKeyAcrossFlags(t *testing.T) {
	t.Parallel()

	_, err := applyMetadataEditsWithJSON(nil, []string{"count=42"}, []string{"count=42"}, nil)
	if err == nil || !strings.Contains(err.Error(), "both --set-metadata and --set-metadata-json") {
		t.Fatalf("error = %v, want cross-flag duplicate error", err)
	}
}

func TestApplyMetadataEdits_EmptyExisting(t *testing.T) {
	t.Parallel()
	// Empty metadata (nil)
	result, err := applyMetadataEdits(nil, []string{"team=platform"}, nil)
	if err != nil {
		t.Fatalf("nil metadata: %v", err)
	}
	if !json.Valid(result) {
		t.Errorf("result is not valid JSON: %s", result)
	}

	// Empty JSON object
	result, err = applyMetadataEdits(json.RawMessage(`{}`), []string{"team=platform"}, nil)
	if err != nil {
		t.Fatalf("empty object: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"platform"` {
		t.Errorf("expected \"platform\", got %s", data["team"])
	}
}

func TestApplyMetadataEdits_InvalidKey(t *testing.T) {
	t.Parallel()
	_, err := applyMetadataEdits(nil, []string{"bad key=val"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestApplyMetadataEdits_InvalidUnsetKey(t *testing.T) {
	t.Parallel()
	_, err := applyMetadataEdits(nil, nil, []string{"bad key"})
	if err == nil {
		t.Fatal("expected error for invalid unset key")
	}
}

func TestApplyMetadataEdits_InvalidFormat(t *testing.T) {
	t.Parallel()
	_, err := applyMetadataEdits(nil, []string{"noequalssign"}, nil)
	if err == nil {
		t.Fatal("expected error for missing =")
	}
}

func TestApplyMetadataEdits_NonObjectExisting(t *testing.T) {
	t.Parallel()
	_, err := applyMetadataEdits(json.RawMessage(`"just a string"`), []string{"team=platform"}, nil)
	if err == nil {
		t.Fatal("expected error for non-object metadata")
	}
}

func TestApplyMetadataEdits_MultipleSetFlags(t *testing.T) {
	t.Parallel()
	result, err := applyMetadataEdits(nil, []string{"team=platform", "sprint=Q1", "priority=2"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"platform"` {
		t.Errorf("expected \"platform\", got %s", data["team"])
	}
	if string(data["sprint"]) != `"Q1"` {
		t.Errorf("expected \"Q1\", got %s", data["sprint"])
	}
	if string(data["priority"]) != `"2"` {
		t.Errorf("expected string \"2\", got %s", data["priority"])
	}
}

func TestApplyMetadataEdits_UnsetNonexistentKey(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"team":"platform"}`)
	result, err := applyMetadataEdits(existing, nil, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["team"]) != `"platform"` {
		t.Errorf("expected \"platform\", got %s", data["team"])
	}
}

func TestMergeMetadata_MergesKeys(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"key1":"value1","key2":"value2"}`)
	incoming := json.RawMessage(`{"key3":"value3"}`)
	result, err := mergeMetadata(existing, incoming)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["key1"]) != `"value1"` {
		t.Errorf("expected key1=value1, got %s", data["key1"])
	}
	if string(data["key2"]) != `"value2"` {
		t.Errorf("expected key2=value2, got %s", data["key2"])
	}
	if string(data["key3"]) != `"value3"` {
		t.Errorf("expected key3=value3, got %s", data["key3"])
	}
}

func TestMergeMetadata_OverwritesExistingKeys(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"key1":"old","key2":"keep"}`)
	incoming := json.RawMessage(`{"key1":"new"}`)
	result, err := mergeMetadata(existing, incoming)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["key1"]) != `"new"` {
		t.Errorf("expected key1=new, got %s", data["key1"])
	}
	if string(data["key2"]) != `"keep"` {
		t.Errorf("expected key2=keep, got %s", data["key2"])
	}
}

func TestMergeMetadata_NilExisting(t *testing.T) {
	t.Parallel()
	incoming := json.RawMessage(`{"key1":"value1"}`)
	result, err := mergeMetadata(nil, incoming)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(result, &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(data["key1"]) != `"value1"` {
		t.Errorf("expected key1=value1, got %s", data["key1"])
	}
}

func TestMergeMetadata_NonObjectExisting(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`"just a string"`)
	incoming := json.RawMessage(`{"key1":"value1"}`)
	_, err := mergeMetadata(existing, incoming)
	if err == nil {
		t.Fatal("expected error for non-object existing metadata")
	}
}

func TestMergeMetadata_NonObjectIncoming(t *testing.T) {
	t.Parallel()
	existing := json.RawMessage(`{"key1":"value1"}`)
	incoming := json.RawMessage(`"just a string"`)
	_, err := mergeMetadata(existing, incoming)
	if err == nil {
		t.Fatal("expected error for non-object incoming metadata")
	}
}
