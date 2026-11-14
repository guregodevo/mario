package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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

// YamlFanout is a task's fan-out: how many isolated attempts, and what
// settles them.
type YamlFanout struct {
	Min     int    `yaml:"min"`
	Max     int    `yaml:"max,omitempty"`
	Between string `yaml:"between,omitempty"`
	Until   string `yaml:"until,omitempty"`
	// Checks are executables (glob patterns, templates) the engine runs in
	// each attempt's directory after every wave: one that passes on the run's
	// directory as it stands proves nothing and is not counted; an attempt
	// scores a point per other check it passes. Own names an attempt's own
	// check ({{.n}} in it), which never scores for it. Pick applies the
	// attempt that fails the fewest of the checks it is scored on to the
	// run's directory (a tie: the smaller change).
	Checks []string `yaml:"checks,omitempty"`
	Own    string   `yaml:"own,omitempty"`
	Pick   bool     `yaml:"pick,omitempty"`
}

// MaxFanout bounds a fan-out: attempts are model turns, and a typo in max
// must not run a hundred of them.
const MaxFanout = 10

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
	// Condition, State, Threshold and Else are a decision task's: the
	// statement a decision model scores, the evidence it scores it on (a
	// template over outputs and files), the probability at or above which
	// the task passes, and the shell line whose exit status answers instead
	// when no decision model does (tasks.Decision).
	Condition string  `yaml:"condition,omitempty"`
	State     string  `yaml:"state,omitempty"`
	Threshold float64 `yaml:"threshold,omitempty"`
	Else      string  `yaml:"else,omitempty"`
	// From, By, Tie, Prefer and Skip are a pick task's (tasks.Pick): the
	// fan-out whose attempts it chooses from, the tasks it asks in order on
	// a tie (each names an attempt), the rule it applies to a tie they leave
	// (consensus), the attempt that wins a tie none of them settles, and the
	// pick tasks whose winners it leaves out.
	From   string   `yaml:"from,omitempty"`
	By     []string `yaml:"by,omitempty"`
	Tie    string   `yaml:"tie,omitempty"`
	Prefer int      `yaml:"prefer,omitempty"`
	Skip   []string `yaml:"skip,omitempty"`
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
	// Fanout runs the task several times, each in a working directory of its
	// own the host makes (tasks.Isolator): Min attempts at once, then one
	// more at a time until the attempts are settled (their checks, Between's
	// exit status, or the host's decision model settling sooner), or Max is
	// reached. Each attempt's change is in the task's output.
	Fanout *YamlFanout `yaml:"fanout,omitempty"`

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
	// With binds the required task's args when its workflow is pulled into this one: the values are Go
	// templates, rendered when that task runs (`dir: '{{ output "ship.steps.handoff" }}'`). A host that
	// composes workflows applies them; mario carries them.
	With map[string]string `yaml:"with,omitempty"`
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

	forSchema, items, errEach := withoutEach(yamlContent)
	if errEach != nil {
		return errEach, nil
	}
	forSchema, errRun := withoutRunShape(forSchema, len(items) > 0)
	if errRun != nil {
		return errRun, nil
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

// withoutRunShape returns the YAML without its `fanout` key (how a task is
// run, read by every type, listed by none of their schemas), checked: a
// fan-out is bounded and not an each.
func withoutRunShape(yamlContent []byte, hasEach bool) ([]byte, error) {
	var doc map[string]interface{}
	if err := yaml.Unmarshal(yamlContent, &doc); err != nil || doc == nil {
		return yamlContent, nil
	}
	raw, fanned := doc["fanout"]
	if !fanned {
		return yamlContent, nil
	}
	{
		if hasEach {
			return nil, fmt.Errorf("fanout and each: one or the other (each names its copies, fanout counts them)")
		}
		m := map[string]interface{}{}
		switch raw := raw.(type) {
		case map[string]interface{}:
			m = raw
		case map[interface{}]interface{}:
			for k, v := range raw {
				m[fmt.Sprint(k)] = v
			}
		default:
			return nil, fmt.Errorf("fanout: an object with min, and max, between, until, checks, own, pick")
		}
		for k := range m {
			switch k {
			case "min", "max", "between", "until", "checks", "own", "pick":
			default:
				return nil, fmt.Errorf("fanout: %q is not a field (min, max, between, until, checks, own, pick)", k)
			}
		}
		min, okMin := m["min"].(int)
		if !okMin || min < 1 || min > MaxFanout {
			return nil, fmt.Errorf("fanout: min is a number from 1 to %d", MaxFanout)
		}
		if v, has := m["max"]; has {
			max, okMax := v.(int)
			if !okMax || max < min || max > MaxFanout {
				return nil, fmt.Errorf("fanout: max is a number from min to %d", MaxFanout)
			}
		}
		_, hasChecks := m["checks"]
		if until, has := m["until"]; has && fmt.Sprint(until) != "" {
			if b, _ := m["between"].(string); strings.TrimSpace(b) == "" && !hasChecks {
				return nil, fmt.Errorf("fanout: until needs checks or between, the evidence it is judged on")
			}
		}
		if hasChecks {
			if _, ok := m["checks"].([]interface{}); !ok {
				return nil, fmt.Errorf("fanout: checks is a list of executables (glob patterns)")
			}
		}
		if _, has := m["own"]; has && !hasChecks {
			return nil, fmt.Errorf("fanout: own names an attempt's own check among checks; there are none")
		}
	}
	delete(doc, "fanout")
	return yaml.Marshal(doc)
}
