package delivery

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// compileSchema compiles schema documents like the official
// schemas/project-delivery.schema.json (draft 2020-12) from their exact bytes.
// Compilation failures are contract violations of the distribution and are
// fail-closed: an uncompilable schema never validates anything.
func compileSchema(schemaBytes []byte) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaBytes))
	if err != nil {
		return nil, fmt.Errorf("official config schema is not valid JSON: %w", err)
	}
	// The official schema carries a "$id" (e.g. urn:oroborus:...). Register the
	// document under its own id when present so internal $refs resolve.
	key := "delivery.schema.json"
	if obj, ok := doc.(map[string]any); ok {
		if id, ok := obj["$id"].(string); ok && id != "" {
			key = id
		}
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(key, doc); err != nil {
		return nil, fmt.Errorf("official config schema could not be registered: %w", err)
	}
	schema, err := compiler.Compile(key)
	if err != nil {
		return nil, fmt.Errorf("official config schema could not be compiled: %w", err)
	}
	return schema, nil
}

// validateDeliveryConfig converts the rendered YAML document to JSON and
// validates it against the official schema. YAML→JSON conversion goes through
// encoding/json so numbers and maps are normalized to the shapes the schema
// library expects.
func validateDeliveryConfig(schemaBytes, deliveryYAML []byte) error {
	schema, err := compileSchema(schemaBytes)
	if err != nil {
		return err
	}
	var document any
	if err := yaml.Unmarshal(deliveryYAML, &document); err != nil {
		return fmt.Errorf("rendered delivery config is not valid YAML: %w", err)
	}
	jsonBytes, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("rendered delivery config could not be converted to JSON: %w", err)
	}
	var instance any
	if err := json.Unmarshal(jsonBytes, &instance); err != nil {
		return fmt.Errorf("rendered delivery config could not be converted to JSON: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("rendered delivery config does not satisfy %s: %w", schemaLocation(schema), err)
	}
	return nil
}

func schemaLocation(schema *jsonschema.Schema) string {
	if schema.ID != "" {
		return schema.ID
	}
	return "official config schema"
}
