package templates

import (
	"encoding/json"
	"gopkg.in/yaml.v2"
	"testing"
)

// MockSchemaLoader to simulate schema loading for testing
type MockSchemaLoader struct{}

func (m *MockSchemaLoader) LoadSchema(name string) ([]byte, error) {
	// Mock schema, replace with a realistic schema for testing
	mockSchema := `{
		"type": "object",
		"properties": {
			"type": { "type": "string" },
			"field_descriptions": { "type": "object" },
			"dataset_description": { "type": "string" },
			"table_description": { "type": "string" }
		},
		"required": ["type"]
	}`
	return []byte(mockSchema), nil
}

// Sample YAML content for testing
var sampleYAML = `
type: test_task
field_descriptions:
  description1: "Description of field 1"
dataset_description: "Sample dataset description"
table_description: "Sample table description"
clustering_fields: ["field1", "field2"]
query: "SELECT * FROM table"
prompt: "Generate a task"
model: "model_1"
tools: ["tool_1"]
content_type: "json"
requires:
  - dataset_id: "dataset1"
    project_id: "project1"
    table_pattern: "pattern1"
    partition_offsets: ["offset1"]
start_date: "2024-01-01"
stop_date: "2024-12-31"
`

// TestYamlParse validates the YAML parsing and conversion into YamlTaskDefinition
func TestYamlParse(t *testing.T) {
	validator := NewYAMLValidator(&MockSchemaLoader{})

	// Parse the YAML content into a YamlTaskDefinition
	err, taskDef := YamlParse(validator, []byte(sampleYAML))
	if err != nil {
		t.Fatalf("YamlParse failed: %v", err)
	}

	// Assert the parsed values are correct
	if taskDef.Type != "test_task" {
		t.Errorf("Expected 'test_task', got '%s'", taskDef.Type)
	}

	if taskDef.DatasetDescription != "Sample dataset description" {
		t.Errorf("Expected 'Sample dataset description', got '%s'", taskDef.DatasetDescription)
	}

	if len(taskDef.ClusteringFields) != 2 || taskDef.ClusteringFields[0] != "field1" {
		t.Errorf("Expected 'field1' as the first clustering field, got '%v'", taskDef.ClusteringFields)
	}

	// Check Requires field
	if len(taskDef.Requires) != 1 {
		t.Errorf("Expected 1 'Requires' entry, got %d", len(taskDef.Requires))
	}
}

// TestConvertInterface validates the conversion of map[interface{}]interface{} to map[string]interface{}
func TestConvertInterface(t *testing.T) {
	// Define a map with mixed-type keys
	input := map[interface{}]interface{}{
		"string_key": "value1",
		123:          "value2",
		true:         "value3",
	}

	// Convert it using convertInterface
	output := convertInterface(input).(map[string]interface{})

	// Check the results
	if output["string_key"] != "value1" {
		t.Errorf("Expected 'value1' for 'string_key', got '%s'", output["string_key"])
	}

	if output["123"] != "value2" {
		t.Errorf("Expected 'value2' for '123', got '%s'", output["123"])
	}

	if output["true"] != "value3" {
		t.Errorf("Expected 'value3' for 'true', got '%s'", output["true"])
	}
}

// TestValidateYAML tests the YAML validation against a mock schema
func TestValidateYAML(t *testing.T) {
	validator := NewYAMLValidator(&MockSchemaLoader{})

	// Perform the validation
	err, _ := validator.ValidateYAML([]byte(sampleYAML))
	if err != nil {
		t.Fatalf("ValidateYAML failed: %v", err)
	}
}

// TestJsonConversion ensures that the YAML is correctly converted to JSON-compatible structures
func TestJsonConversion(t *testing.T) {
	// Parse YAML content to generic interface
	var yamlData interface{}
	err := yaml.Unmarshal([]byte(sampleYAML), &yamlData)
	if err != nil {
		t.Fatalf("Error unmarshaling YAML: %v", err)
	}

	// Convert the YAML data to JSON-compatible structure
	jsonData := convertInterface(yamlData)

	// Marshal to JSON to check if it converts successfully
	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		t.Fatalf("Error converting YAML to JSON: %v", err)
	}

	// Check if JSON bytes are non-empty
	if len(jsonBytes) == 0 {
		t.Errorf("Expected non-empty JSON bytes, got empty")
	}
}
