package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/xeipuuv/gojsonschema"
	yaml "gopkg.in/yaml.v2"
)

type SchemaLoader interface {
	LoadSchema(name string) ([]byte, error)
}

type RuntimeContext struct {
	Name      string
	Partition string
}

// A struct to hold the YAML content, based on your YAML structure.
type YamlTaskDefinition struct {
	RuntimeContext     `yaml:"-"`
	Type               string            `yaml:"type"`
	FieldDescriptions  map[string]string `yaml:"field_descriptions,omitempty"`
	DatasetDescription string            `yaml:"dataset_description,omitempty"`
	TableDescription   string            `yaml:"table_description,omitempty"`
	ClusteringFields   []string          `yaml:"clustering_fields,omitempty"`
	Query              string            `yaml:"query,omitempty"`
	Prompt             string            `yaml:"prompt"`
	Model              string            `yaml:"model"`
	// Budget is the tokens an agent task may spend running on alone before
	// it hands back (0: the runner's default).
	Budget             int64             `yaml:"budget,omitempty"`
	Tools              []string          `yaml:"tools,omitempty"`
	ContentType        string            `yaml:"content_type,omitempty"`
	Requires           []Requires        `yaml:"requires,omitempty"`
	Args               map[string]string `yaml:"args,omitempty"`
	StartDate          string            `yaml:"start_date,omitempty"`
	StopDate           string            `yaml:"stop_date,omitempty"`
	PartitionOffset    string            `yaml:"partition_offset,omitempty"`
	MaximumBillingTier string            `yaml:"maximum_billing_tier,omitempty"`
	UseLegacySQL       string            `yaml:"use_legacy_sql,omitempty"`
	FlattenResults     string            `yaml:"flatten_results,omitempty"`
	AllowLargeResults  string            `yaml:"allow_large_results,omitempty"`
	Timeout            string            `yaml:"timeout,omitempty"`
	MaxRetries         int32             `yaml:"max_retries,omitempty"`
	// Agent names who runs the task when the type is run by an agent (a
	// coding agent, a reviewer): the factory for that type reads it.
	Agent string `yaml:"agent,omitempty"`
	// Command is what a command task runs (a shell line, a Go template over
	// args and partition like the prompt).
	Command string `yaml:"command,omitempty"`
	// Target says what proves the task done, when the factory cannot tell
	// from the name alone: a file that must exist, or a command that must
	// exit 0. A task is done when its target exists, never when its run says so.
	Target *YamlTarget `yaml:"target,omitempty"`

	// Lazy renderer for the field (e.g., Prompt)
	LazyRenderedField func() (string, error) `yaml:"-"`
	// Templated says a template directory shaped the rendered field: the
	// lazy renderer is the one to use. Without one, a task type renders the
	// field itself, with what it knows at run time (tasks.Base).
	Templated bool `yaml:"-"`
}

// YamlTarget is a task's proof: one of the two.
type YamlTarget struct {
	File    string `yaml:"file,omitempty"`
	Command string `yaml:"command,omitempty"`
}

type Requires struct {
	DatasetID        string   `yaml:"dataset_id"`
	ProjectID        string   `yaml:"project_id"`
	TablePattern     string   `yaml:"table_pattern"`
	PartitionOffsets []string `yaml:"partition_offsets,omitempty"`
	StartDate        string   `yaml:"start_date,omitempty"`
	StopDate         string   `yaml:"stop_date,omitempty"`
	External         bool     `yaml:"external"`
}

func NewRequires(ProjectID, DatasetId, TablePattern string) Requires {
	return Requires{
		ProjectID:    ProjectID,
		DatasetID:    DatasetId,
		TablePattern: TablePattern,
		External:     false,
	}

}
func (dep Requires) Name() string {
	return fmt.Sprintf("%s.%s.%s", dep.ProjectID, dep.DatasetID, dep.TablePattern)
}

// WithDefaults fills a requirement's project and dataset from the task that
// declares it, as the schema promises ("default value is the … of the task").
func (dep Requires) WithDefaults(projectID, datasetID string) Requires {
	if dep.ProjectID == "" {
		dep.ProjectID = projectID
	}
	if dep.DatasetID == "" {
		dep.DatasetID = datasetID
	}
	return dep
}

// convertInterface converts map[interface{}]interface{} to map[string]interface{}
func convertInterface(i interface{}) interface{} {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m2 := map[string]interface{}{}
		for k, v := range x {
			// Convert key to string for allowed types: string, bool, int
			var strKey string
			switch key := k.(type) {
			case string:
				strKey = key
			case bool:
				strKey = strconv.FormatBool(key) // Convert bool to string
			case int:
				strKey = strconv.Itoa(key) // Convert int to string
			default:
				// Handle unsupported key types by logging and skipping
				fmt.Printf("Skipping unsupported key type: %T\n", k)
				continue
			}

			// Recursively convert the value
			m2[strKey] = convertInterface(v)
		}
		return m2
	case []interface{}:
		for i, v := range x {
			x[i] = convertInterface(v)
		}
	}
	return i
}

// YAMLValidator validates YAML files according to a JSON schema
type YAMLValidator struct {
	SchemaLoader SchemaLoader
}

// NewYAMLValidator creates a new YAMLValidator with the given schema
func NewYAMLValidator(loader SchemaLoader) *YAMLValidator {
	return &YAMLValidator{loader}
}

// Serialize a YamlTaskDefinition struct to YAML
func YamlSerialize(task *YamlTaskDefinition) ([]byte, error) {
	yamlContent, err := yaml.Marshal(task)
	if err != nil {
		return nil, fmt.Errorf("Error marshaling YAML: %v", err)
	}
	return yamlContent, nil
}

func YamlParse(validator *YAMLValidator, yamlContent []byte) (error, *YamlTaskDefinition) {

	err, _ := validator.ValidateYAML(yamlContent)

	if err != nil {
		return err, nil
	}

	var task YamlTaskDefinition
	err = yaml.Unmarshal(yamlContent, &task)
	if err != nil {
		return fmt.Errorf("Error unmarshaling YAML: %v \n", err), nil
	}
	return nil, &task

}

// ValidateYAML validates the given YAML content against the stored schema
func (v *YAMLValidator) ValidateYAML(yamlContent []byte) (error, string) {
	// Step 1: Unmarshal YAML content into a generic interface
	var yamlData interface{}
	if err := yaml.Unmarshal(yamlContent, &yamlData); err != nil {
		return fmt.Errorf("error unmarshaling YAML: %v", err), ""
	}

	// Step 2: Convert the YAML data to JSON-compatible structure
	jsonData := convertInterface(yamlData)

	// Step 3: Marshal the JSON data for schema validation
	jsonBytes, err := json.Marshal(jsonData)
	if err != nil {
		return fmt.Errorf("error converting YAML to JSON: %v", err), ""
	}

	// Step 4: Load the JSON data into a gojsonschema loader for validation
	documentLoader := gojsonschema.NewStringLoader(string(jsonBytes))

	// Step 5: Validate the JSON data against the 'root' schema
	if err := v.validateSchema("root", documentLoader); err != nil {
		return fmt.Errorf("invalid YAML schema at root level: %v", err), ""
	}

	// Step 6: Unmarshal the YAML content directly into the YamlTaskDefinition struct
	var task YamlTaskDefinition
	if err := yaml.Unmarshal(yamlContent, &task); err != nil {
		return fmt.Errorf("error unmarshalling YAML into YamlTaskDefinition: %v", err), ""
	}

	// Step 7: Validate the JSON data against the schema specified by task.Type
	if err := v.validateSchema(task.Type, documentLoader); err != nil {
		return fmt.Errorf("error validating YAML for task type '%s': %v", task.Type, err), ""
	}

	return nil, task.Type
}

func (v *YAMLValidator) validateSchema(taskType string, documentLoader gojsonschema.JSONLoader) error {
	schema, err := v.SchemaLoader.LoadSchema(taskType)
	if err != nil {
		return fmt.Errorf("cannot find schema '%s'", taskType)
	}

	schemaLoader := gojsonschema.NewStringLoader(string(schema))
	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return err
	}

	if !result.Valid() {
		errorFmt := fmt.Sprintf("YAML is not a valid %s. See errors:", taskType)
		for _, desc := range result.Errors() {
			errorFmt = fmt.Sprintf("- %s\n", desc)
		}
		return errors.New(errorFmt)
	}
	return nil
}
