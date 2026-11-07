package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	// System is an llm task's system message (a template, like Prompt): the
	// role and the rules, sent apart from the question when the host takes one.
	System string `yaml:"system,omitempty"`
	Model  string `yaml:"model"`
	// Budget is the tokens an agent task may spend running on alone before
	// it hands back (0: the runner's default).
	Budget int64 `yaml:"budget,omitempty"`
	// OutputSchema is the JSON Schema an agent task's answer must match: the
	// answer is kept as that JSON, so the steps after it read typed data.
	OutputSchema       map[string]interface{} `yaml:"output_schema,omitempty"`
	Tools              []string               `yaml:"tools,omitempty"`
	ContentType        string                 `yaml:"content_type,omitempty"`
	Requires           []Requires             `yaml:"requires,omitempty"`
	Args               map[string]string      `yaml:"args,omitempty"`
	StartDate          string                 `yaml:"start_date,omitempty"`
	StopDate           string                 `yaml:"stop_date,omitempty"`
	PartitionOffset    string                 `yaml:"partition_offset,omitempty"`
	MaximumBillingTier string                 `yaml:"maximum_billing_tier,omitempty"`
	UseLegacySQL       string                 `yaml:"use_legacy_sql,omitempty"`
	FlattenResults     string                 `yaml:"flatten_results,omitempty"`
	AllowLargeResults  string                 `yaml:"allow_large_results,omitempty"`
	Timeout            string                 `yaml:"timeout,omitempty"`
	MaxRetries         int32                  `yaml:"max_retries,omitempty"`
	// Optional says a task that fails past its retries is passed over, not
	// the end of the run: what requires it still runs, its output is empty,
	// and the run reports it as failed. Without it a failed requirement
	// holds everything after it (a planning step that ran out of time ended
	// 23 runs of 500 on a tree nothing had checked, 2026-11-06).
	Optional bool `yaml:"optional,omitempty"`
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
	// When is a condition on running at all: a shell line (a template, like
	// the command) run in the project directory just before the task; when it
	// exits non-zero the task is skipped as done with an empty answer and
	// nothing it would have called is called. A DAG has no loop and no
	// branch; this is how a round after "done" costs nothing (2026-10-27: a
	// ten-round harness kept calling its model for eight rounds after the
	// manager had finished, 60% of the bill).
	When string `yaml:"when,omitempty"`
	// Each makes one file several tasks, opt-in: a task with `each: [a, b]`
	// is the same task twice, named <name>-a and <name>-b, with {{.item}} in
	// its templates (prompt, command, when, target). Without it nothing
	// changes. It is read before the type's schema, so no type has to list it.
	Each []string `yaml:"each,omitempty"`
	// Item is the one entry of Each this copy was made for.
	Item string `yaml:"-"`

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

// itemName is what an `each` entry may be: it ends up in a task's name.
var itemName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// withoutEach returns the YAML without its top-level `each` key (for the
// type's schema, which does not list it) and the entries, checked.
func withoutEach(yamlContent []byte) ([]byte, []string, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(yamlContent, &doc); err != nil || doc == nil {
		return yamlContent, nil, nil // not a mapping: the validator reports it
	}
	raw, ok := doc["each"]
	if !ok {
		return yamlContent, nil, nil
	}
	list, ok := raw.([]interface{})
	if !ok || len(list) == 0 {
		return nil, nil, fmt.Errorf("each: a list of names, e.g. each: [a, b]")
	}
	seen := map[string]bool{}
	items := make([]string, 0, len(list))
	for _, v := range list {
		item := fmt.Sprint(v)
		if !itemName.MatchString(item) {
			return nil, nil, fmt.Errorf("each: %q is not a name (letters, digits, - _ . only)", item)
		}
		if seen[item] {
			return nil, nil, fmt.Errorf("each: %q twice", item)
		}
		seen[item] = true
		items = append(items, item)
	}
	delete(doc, "each")
	rest, err := yaml.Marshal(doc)
	if err != nil {
		return nil, nil, err
	}
	return rest, items, nil
}

// Expand returns the task as it is, or, with Each, one copy per entry: Item
// and the arg `item` set. The copies share nothing with the original.
func (d *YamlTaskDefinition) Expand() []*YamlTaskDefinition {
	if len(d.Each) == 0 {
		return []*YamlTaskDefinition{d}
	}
	out := make([]*YamlTaskDefinition, 0, len(d.Each))
	for _, item := range d.Each {
		c := *d
		c.Each = nil
		c.Item = item
		c.Args = make(map[string]string, len(d.Args)+1)
		for k, v := range d.Args {
			c.Args[k] = v
		}
		c.Args["item"] = item
		c.Requires = append([]Requires(nil), d.Requires...)
		out = append(out, &c)
	}
	return out
}

func YamlParse(validator *YAMLValidator, yamlContent []byte) (error, *YamlTaskDefinition) {

	forSchema, _, errEach := withoutEach(yamlContent)
	if errEach != nil {
		return errEach, nil
	}
	err, _ := validator.ValidateYAML(forSchema)

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
