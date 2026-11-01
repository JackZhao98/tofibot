package extensions

import (
	"bytes"
	"encoding/json"
	"sort"
)

// A remote input schema can be larger than a search page or an error hint.
// Dropping it, or cutting its JSON at a byte limit, hid exactly what the model
// needs: encoding/json sorts keys, so "$defs" comes before "properties" and
// "required", and a byte cut removed the top-level fields first. These helpers
// shrink a schema in steps that keep the top level whole, and write the top
// level first.

// Keys written first at the top level, in this order; "$defs" and
// "definitions" are written last.
var schemaLeadKeys = []string{"type", "required", "properties", "additionalProperties", "description"}

const (
	schemaFull = iota
	schemaNoDefDescriptions
	schemaTopDescriptionsOnly
	schemaNoDescriptions
	schemaDefNamesOnly
)

// compactSchema returns the schema at the given level of detail (a copy; the
// input is not changed). Each level keeps every top-level property name, type,
// enum and the required list: first descriptions inside $defs go, then all but
// the top-level property descriptions, then every description, and last the
// bodies of $defs.
func compactSchema(params map[string]any, level int) map[string]any {
	out := map[string]any{}
	for key, value := range params {
		out[key] = value
	}
	if level == schemaFull {
		return out
	}
	for _, defsKey := range []string{"$defs", "definitions"} {
		defs, ok := params[defsKey].(map[string]any)
		if !ok {
			continue
		}
		if level >= schemaDefNamesOnly {
			names := map[string]any{}
			for name := range defs {
				names[name] = map[string]any{}
			}
			out[defsKey] = names
			out["x-tofi-note"] = "The bodies of " + defsKey + " are omitted for size. The top-level properties and required list are complete."
			continue
		}
		kept := map[string]any{}
		for name, def := range defs {
			kept[name] = stripSubschema(def)
		}
		out[defsKey] = kept
	}
	if level >= schemaNoDescriptions {
		// Keep only structure: an over-long description must not hide it.
		stripped := stripDescriptions(out)
		if note, ok := out["x-tofi-note"]; ok {
			stripped["x-tofi-note"] = note
		}
		return stripped
	}
	if level >= schemaTopDescriptionsOnly {
		if props, ok := params["properties"].(map[string]any); ok {
			kept := map[string]any{}
			for name, prop := range props {
				if m, ok := prop.(map[string]any); ok {
					inner := stripDescriptions(m)
					if d, ok := m["description"]; ok {
						inner["description"] = d
					}
					kept[name] = inner
				} else {
					kept[name] = prop
				}
			}
			out["properties"] = kept
		}
	}
	return out
}

// Keywords whose value maps names to subschemas: the names are data (a
// property may be called "title" or "description") and always stay.
var schemaNameMapKeywords = map[string]bool{"properties": true, "patternProperties": true, "$defs": true, "definitions": true, "dependentSchemas": true}

// Keywords whose value is one subschema, or a list of subschemas.
var schemaSubschemaKeywords = map[string]bool{"items": true, "additionalProperties": true, "additionalItems": true, "unevaluatedProperties": true,
	"unevaluatedItems": true, "not": true, "if": true, "then": true, "else": true, "contains": true, "propertyNames": true,
	"anyOf": true, "allOf": true, "oneOf": true, "prefixItems": true}

// stripDescriptions removes prose keywords from a schema node and its
// subschemas; structure stays. It walks schema keywords only: property names
// and data values (enum, const, default) are never touched.
func stripDescriptions(node map[string]any) map[string]any {
	out := map[string]any{}
	for key, v := range node {
		switch {
		case key == "description" || key == "title" || key == "examples" || key == "$comment":
			continue
		case schemaNameMapKeywords[key]:
			if names, ok := v.(map[string]any); ok {
				kept := map[string]any{}
				for name, sub := range names {
					kept[name] = stripSubschema(sub)
				}
				out[key] = kept
				continue
			}
		case schemaSubschemaKeywords[key]:
			out[key] = stripSubschema(v)
			continue
		}
		out[key] = v
	}
	return out
}

func stripSubschema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return stripDescriptions(t)
	case []any:
		items := make([]any, len(t))
		for i, item := range t {
			items[i] = stripSubschema(item)
		}
		return items
	}
	return v
}

// marshalSchemaTopFirst writes the top level in schemaLeadKeys order, then the
// other keys sorted, then "$defs"/"definitions". Nested values use the
// standard encoding.
func marshalSchemaTopFirst(schema map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	written := map[string]bool{}
	first := true
	write := func(key string) error {
		value, ok := schema[key]
		if !ok || written[key] {
			return nil
		}
		written[key] = true
		k, err := json.Marshal(key)
		if err != nil {
			return err
		}
		v, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		buf.Write(k)
		buf.WriteByte(':')
		buf.Write(v)
		return nil
	}
	for _, key := range schemaLeadKeys {
		if err := write(key); err != nil {
			return nil, err
		}
	}
	var rest []string
	for key := range schema {
		if key != "$defs" && key != "definitions" {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	for _, key := range append(rest, "$defs", "definitions") {
		if err := write(key); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// schemaWithin returns the most detailed level of the schema whose top-first
// encoding fits in limit bytes, and whether it had to be compacted. ok is
// false when even the smallest level does not fit.
func schemaWithin(params map[string]any, limit int) (schema map[string]any, encoded []byte, compacted, ok bool) {
	for level := schemaFull; level <= schemaDefNamesOnly; level++ {
		candidate := compactSchema(params, level)
		data, err := marshalSchemaTopFirst(candidate)
		if err != nil {
			return nil, nil, false, false
		}
		if len(data) <= limit {
			return candidate, data, level != schemaFull, true
		}
		schema, encoded = candidate, data
	}
	return schema, encoded, true, false
}

// compactMatch returns the most detailed compacted level of schema that
// fits(level) accepts, trying from the first compacted level down.
func compactMatch(schema map[string]any, fits func(map[string]any) bool) (map[string]any, bool) {
	for level := schemaNoDefDescriptions; level <= schemaDefNamesOnly; level++ {
		candidate := compactSchema(schema, level)
		if fits(candidate) {
			return candidate, true
		}
	}
	return nil, false
}
