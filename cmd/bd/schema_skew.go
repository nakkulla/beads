package main

import (
	"encoding/json"
	"os"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/schema"
)

func handleSchemaSkewJSON(e *schema.SchemaSkewError) {
	outer := buildJSONClassifiedError(e.Error(), e.EscapeHint(), storage.FailureSchemaMigrationRequired, map[string]interface{}{"operation": "database_open"})
	if m := jsonErrorPayload(outer); m != nil {
		m["schema_skew"] = map[string]interface{}{
			"current_version":  e.DBVersion,
			"required_version": e.BinaryVersion,
			"delta":            e.DBVersion - e.BinaryVersion,
		}
	}
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(outer)
}
