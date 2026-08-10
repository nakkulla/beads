package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

type orderedJSONField struct {
	name  string
	value json.RawMessage
}

type orderedJSONObject struct {
	fields []orderedJSONField
}

func (o orderedJSONObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, field := range o.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, err := json.Marshal(field.name)
		if err != nil {
			return nil, err
		}
		buf.Write(name)
		buf.WriteByte(':')
		if len(field.value) == 0 {
			buf.WriteString("null")
		} else {
			buf.Write(field.value)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func (o orderedJSONObject) withSchemaVersion(version int) interface{} {
	fields := make([]orderedJSONField, 0, len(o.fields)+1)
	fields = append(fields, o.fields...)
	fields = append(fields, orderedJSONField{name: "schema_version", value: json.RawMessage(fmt.Sprintf("%d", version))})
	return orderedJSONObject{fields: fields}
}

func parseShowFields(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	validNames := issueDetailsJSONFieldNames()
	valid := make(map[string]struct{}, len(validNames))
	for _, name := range validNames {
		valid[name] = struct{}{}
	}

	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if _, ok := valid[name]; !ok {
			return nil, fmt.Errorf("unknown field %q; valid fields: %s", name, strings.Join(validNames, ", "))
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = struct{}{}
		fields = append(fields, name)
	}
	return fields, nil
}

func issueDetailsJSONFieldNames() []string {
	names := make(map[string]struct{})
	collectJSONFieldNames(reflect.TypeOf(types.IssueDetails{}), names)
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func collectJSONFieldNames(t reflect.Type, names map[string]struct{}) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			collectJSONFieldNames(field.Type, names)
			continue
		}
		if name == "" {
			name = field.Name
		}
		names[name] = struct{}{}
	}
}

func projectIssueDetails(details *types.IssueDetails, fields []string) (orderedJSONObject, error) {
	encoded, err := json.Marshal(details)
	if err != nil {
		return orderedJSONObject{}, fmt.Errorf("marshal issue details: %w", err)
	}
	values := make(map[string]json.RawMessage)
	if err := json.Unmarshal(encoded, &values); err != nil {
		return orderedJSONObject{}, fmt.Errorf("decode issue details: %w", err)
	}

	projected := orderedJSONObject{fields: make([]orderedJSONField, 0, len(fields))}
	for _, name := range fields {
		value := values[name]
		if len(value) == 0 {
			value = json.RawMessage("null")
		}
		projected.fields = append(projected.fields, orderedJSONField{name: name, value: value})
	}
	return projected, nil
}
