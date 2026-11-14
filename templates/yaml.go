package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/xeipuuv/gojsonschema"
	yaml "gopkg.in/yaml.v2"
	yaml3 "gopkg.in/yaml.v3"
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
	// Text is what a template task renders: its answer, and with target.file, that file.
	Text string `yaml:"text,omitempty"`
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
	// Cut is the one-line shell values YAML cut short: the key (command, when, target.command) and the
	// comment YAML took off its end. A space then # starts a comment in a plain value, so
	// `command: jq '"(issue #\(.n))"'` reaches the shell as `jq '"(issue`. Compile refuses them.
	Cut map[string]string `yaml:"-"`
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
	task.Cut = cutByComment(yamlContent)
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

// YamlTaskDefinitions is a workflow's tasks by name, <workflow>.<group>.<task>: what Walk returns.
type YamlTaskDefinitions map[string]*YamlTaskDefinition

// Compose makes one graph of this workflow and the workflows it requires. A requirement that names a task
// of another workflow (project_id) and is not external pulls that task, and what it requires, into the
// definitions, and the scheduler runs them upstream in the same run. sibling loads another workflow by
// its name; nil when there is none, and the requirement is left for the DAG builder to refuse.
//
//   - Only what is required comes in.
//   - The pulled workflow's first tasks wait for what the requiring task requires in its own workflow,
//     except what that already needs: a task that requires `hand` and another workflow's `make` runs hand,
//     then the other workflow, then itself.
//   - with: on the requirement binds the args of the pulled tasks that declare them. A value is written as
//     anywhere else in the requiring task: {{ output "hand" }} is this workflow's hand, though the value is
//     rendered when the pulled task runs. A key no pulled task declares is an error that lists the args
//     that are declared, and so is one task bound to two values.
//
// An external requirement is untouched: its target is read, nothing of it runs.
func (defs YamlTaskDefinitions) Compose(sibling func(project string) (map[string]*YamlTaskDefinition, error)) error {
	own := make(YamlTaskDefinitions, len(defs))
	for name, d := range defs {
		own[name] = d
	}
	if err := defs.pull(sibling); err != nil {
		return err
	}
	bound := map[binding]string{} // the value each arg of each pulled task was bound to
	for name, d := range own {
		var here []Requires
		pulled := map[string]bool{}
		for _, r := range d.Requires {
			switch {
			case own[r.Name()] != nil:
				here = append(here, r)
			case !r.External:
				defs.reach(own, r.Name(), pulled)
			}
		}
		// What d's own requirements already need stays ahead of them: waiting on d there is a cycle.
		ahead := map[string]bool{}
		for _, r := range here {
			defs.reach(nil, r.Name(), ahead)
		}
		for n := range pulled {
			if p := defs[n]; !ahead[n] && !p.requiresAny(pulled) {
				p.After(here)
			}
		}
		for _, r := range d.Requires {
			if r.External || len(r.With) == 0 {
				continue
			}
			reached := map[string]bool{}
			defs.reach(own, r.Name(), reached)
			for arg, value := range r.With {
				value = d.qualify(value)
				used := false
				for n := range reached {
					if !defs[n].Bind(arg, value) {
						continue
					}
					used = true
					b := binding{Task: n, Arg: arg}
					if prev, ok := bound[b]; ok && prev != value {
						return fmt.Errorf("task %s: %s is bound twice, to %q and %q", name, b, prev, value)
					}
					bound[b] = value
				}
				if !used {
					return fmt.Errorf("task %s binds %s, which no task of %s declares as an arg (declared: %s)", name, arg, r.Name(), defs.declared(reached))
				}
			}
		}
	}
	return nil
}

// binding is one arg of one pulled task: what a with: sets.
type binding struct {
	Task string
	Arg  string
}

func (b binding) String() string { return b.Task + "." + b.Arg }

// pull adds every task of another workflow that is required, transitively.
func (defs YamlTaskDefinitions) pull(sibling func(string) (map[string]*YamlTaskDefinition, error)) error {
	loaded := map[string]map[string]*YamlTaskDefinition{}
	var queue []string
	for _, d := range defs {
		queue = append(queue, d.missing(defs)...)
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if defs[name] != nil {
			continue
		}
		project := strings.SplitN(name, ".", 2)[0]
		if _, seen := loaded[project]; !seen {
			found, err := sibling(project)
			if err != nil {
				return fmt.Errorf("workflow %s: %w", project, err)
			}
			byName := make(map[string]*YamlTaskDefinition, len(found))
			for _, d := range found {
				byName[d.Name] = d
			}
			loaded[project] = byName
		}
		if d := loaded[project][name]; d != nil {
			defs[name] = d
			queue = append(queue, d.missing(defs)...)
		}
	}
	return nil
}

// reach marks the task and what it requires, transitively, leaving out the tasks of except.
func (defs YamlTaskDefinitions) reach(except YamlTaskDefinitions, name string, into map[string]bool) {
	d := defs[name]
	if d == nil || into[name] || except[name] != nil {
		return
	}
	into[name] = true
	for _, r := range d.Requires {
		defs.reach(except, r.Name(), into)
	}
}

// missing is the tasks d requires that defs does not hold and that are not external.
func (d *YamlTaskDefinition) missing(defs YamlTaskDefinitions) []string {
	var out []string
	for _, r := range d.Requires {
		if defs[r.Name()] == nil && !r.External {
			out = append(out, r.Name())
		}
	}
	return out
}

// shortOutput is {{ output "name" }} with a short name: a task of the group of the task that wrote it.
var shortOutput = regexp.MustCompile(`(\boutput\s+")([^".]+)(")`)

// qualify gives the short task names in text d's own workflow and group, so text written in d reads the
// same when another workflow's task renders it.
func (d *YamlTaskDefinition) qualify(text string) string {
	seg := strings.SplitN(d.Name, ".", 3)
	if len(seg) < 3 {
		return text
	}
	return shortOutput.ReplaceAllString(text, "${1}"+seg[0]+"."+seg[1]+".${2}${3}")
}

// declared is the args the named tasks declare, sorted: what a with: may bind.
func (defs YamlTaskDefinitions) declared(names map[string]bool) string {
	seen := map[string]bool{}
	var args []string
	for n := range names {
		for arg := range defs[n].Args {
			if !seen[arg] {
				seen[arg] = true
				args = append(args, arg)
			}
		}
	}
	if len(args) == 0 {
		return "none"
	}
	sort.Strings(args)
	return strings.Join(args, ", ")
}

// requiresAny says whether d requires one of the named tasks.
func (d *YamlTaskDefinition) requiresAny(names map[string]bool) bool {
	for _, r := range d.Requires {
		if names[r.Name()] {
			return true
		}
	}
	return false
}

// After makes d run after the given requirements, adding those it does not have.
func (d *YamlTaskDefinition) After(reqs []Requires) {
	have := map[string]bool{}
	for _, r := range d.Requires {
		have[r.Name()] = true
	}
	for _, r := range reqs {
		if !have[r.Name()] {
			d.Requires = append(d.Requires, r)
			have[r.Name()] = true
		}
	}
}

// Bind sets an arg d declares to value and answers true; an arg d does not declare is left alone.
func (d *YamlTaskDefinition) Bind(arg, value string) bool {
	if _, declared := d.Args[arg]; !declared {
		return false
	}
	d.Args[arg] = value
	return true
}

// CompileError is one thing wrong with a task that can be told before a run: where it is, what is wrong,
// and what to write instead. Its text is meant for whoever wrote the file, a person or a model.
type CompileError struct {
	Task    string // <workflow>.<group>.<task>
	File    string // <group>/<task>.yaml, under the workflow's directory
	Field   string // the YAML key the problem is in: command, when, target.file, args.dir ...
	Problem string
	Fix     string
}

func (e CompileError) Error() string {
	return fmt.Sprintf("%s: %s: %s. %s", e.File, e.Field, e.Problem, e.Fix)
}

// Compile checks every task before a run and answers everything it found, joined. factory.BuildDAG calls
// it, so a graph is never built, and nothing runs, from a workflow that does not compile. A workflow's author
// fixes them in one pass instead of one failed run each. Nothing is checked that needs the run: an
// output's content, a file's existence.
//
// Checked now: every template parses, and a template uses only args its task declares. An undeclared
// {{.name}} does not fail at run time, it renders "<no value>" into the command.
func (defs YamlTaskDefinitions) Compile() error {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	var found []error
	for _, name := range names {
		d := defs[name]
		for _, e := range d.compile() {
			found = append(found, e)
		}
		for _, e := range defs.outputsOf(d) {
			found = append(found, e)
		}
	}
	return errors.Join(found...)
}

// outputsOf checks every {{ output "name" }} d reads: the task named must run before d, so it must be one d
// requires, directly or through what it requires. Otherwise the output is not there when d renders, or is
// there by luck of the order. A name built at run time ({{ output (printf ...) }}) cannot be told here.
func (defs YamlTaskDefinitions) outputsOf(d *YamlTaskDefinition) []CompileError {
	seg := strings.SplitN(d.Name, ".", 3)
	for len(seg) < 3 {
		seg = append(seg, "")
	}
	_, funcs, err := ArgsOnly(d, "", seg[0], seg[1], seg[2])
	if err != nil {
		return nil
	}
	upstream := map[string]bool{}
	defs.before(d, upstream)
	at := CompileError{Task: d.Name, File: seg[1] + "/" + strings.TrimSuffix(seg[2], "-"+d.Item) + ".yaml"}
	var found []CompileError
	for _, f := range d.fields() {
		if !strings.Contains(f.Text, "{{") {
			continue
		}
		tree, err := template.New(d.Name).Funcs(funcs).Parse(f.Text)
		if err != nil {
			continue // compile reported it
		}
		seen := map[string]bool{}
		for _, name := range usedOutputs(tree.Tree.Root) {
			full := name
			if !strings.Contains(name, ".") {
				full = seg[0] + "." + seg[1] + "." + name
			}
			if upstream[full] || seen[full] {
				continue
			}
			seen[full] = true
			e := at
			e.Field = f.Key
			switch {
			case full == d.Name:
				e.Problem = fmt.Sprintf("reads {{ output %q }}, its own output, which is not written until it ends", name)
				e.Fix = "Read the output of a task this one requires"
			case defs[full] != nil:
				e.Problem = fmt.Sprintf("reads {{ output %q }}, but this task does not require %s, so it may run first and find nothing", name, name)
				e.Fix = fmt.Sprintf("Add it in this file under requires: (- table_pattern: %s)", lastName(full))
			default:
				e.Problem = fmt.Sprintf("reads {{ output %q }}, but no task is named %s", name, name)
				e.Fix = "Name a task this one requires: " + defs.requiredNames(d, seg[0]+"."+seg[1]+".")
			}
			found = append(found, e)
		}
	}
	return found
}

// before marks every task that runs before d: what it requires, and what those require.
func (defs YamlTaskDefinitions) before(d *YamlTaskDefinition, into map[string]bool) {
	for _, r := range d.Requires {
		name := r.Name()
		if into[name] {
			continue
		}
		into[name] = true
		if up := defs[name]; up != nil {
			defs.before(up, into)
		}
	}
}

// requiredNames is the tasks d may read an output of, short where they are in d's own group, sorted.
func (defs YamlTaskDefinitions) requiredNames(d *YamlTaskDefinition, group string) string {
	upstream := map[string]bool{}
	defs.before(d, upstream)
	if len(upstream) == 0 {
		return "none yet: this task requires nothing"
	}
	names := make([]string, 0, len(upstream))
	for name := range upstream {
		names = append(names, strings.TrimPrefix(name, group))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func lastName(full string) string {
	return full[strings.LastIndex(full, ".")+1:]
}

// usedOutputs is the task names a template reads with {{ output "name" }}, where the name is written out.
func usedOutputs(node parse.Node) []string {
	var out []string
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c)
			}
		case *parse.ActionNode:
			walk(n.Pipe)
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, c := range n.Cmds {
				walk(c)
			}
		case *parse.CommandNode:
			if len(n.Args) >= 2 {
				if id, ok := n.Args[0].(*parse.IdentifierNode); ok && id.Ident == "output" {
					if lit, ok := n.Args[1].(*parse.StringNode); ok {
						out = append(out, lit.Text)
					}
				}
			}
			for _, a := range n.Args {
				walk(a)
			}
		case *parse.IfNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.RangeNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		case *parse.WithNode:
			walk(n.Pipe)
			walk(n.List)
			walk(n.ElseList)
		}
	}
	walk(node)
	return out
}

// cutByComment finds the one-line shell values of a task file that YAML ended at a comment while a quote
// was still open: the value was meant to go on. A comment after a whole command (`go test ./... # checks`)
// is a comment, and is left alone.
func cutByComment(yamlContent []byte) map[string]string {
	var doc yaml3.Node
	if err := yaml3.Unmarshal(yamlContent, &doc); err != nil || len(doc.Content) == 0 {
		return nil
	}
	cut := map[string]string{}
	var read func(m *yaml3.Node, prefix string)
	read = func(m *yaml3.Node, prefix string) {
		if m.Kind != yaml3.MappingNode {
			return
		}
		for i := 0; i+1 < len(m.Content); i += 2 {
			key, value := m.Content[i].Value, m.Content[i+1]
			if key == "target" {
				read(value, "target.")
				continue
			}
			if key != "command" && key != "when" {
				continue
			}
			if value.Kind == yaml3.ScalarNode && value.Style == 0 && value.LineComment != "" && openQuote(value.Value) {
				cut[prefix+key] = value.LineComment
			}
		}
	}
	read(doc.Content[0], "")
	if len(cut) == 0 {
		return nil
	}
	return cut
}

// openQuote says a shell line ends inside a ' or " quote.
func openQuote(line string) bool {
	var in rune
	for i, c := range line {
		switch {
		case in == 0 && (c == '\'' || c == '"'):
			in = c
		case in == '"' && c == '"' && (i == 0 || line[i-1] != '\\'):
			in = 0
		case in == '\'' && c == '\'':
			in = 0
		}
	}
	return in != 0
}

// field is one templated field of a task, by the YAML key a person reads.
type field struct {
	Key  string
	Text string
}

// fields is the task's Go templates, in the order they read in the file.
func (d *YamlTaskDefinition) fields() []field {
	out := []field{
		{"prompt", d.Prompt}, {"system", d.System}, {"command", d.Command}, {"text", d.Text},
		{"condition", d.Condition}, {"state", d.State}, {"when", d.When},
	}
	if d.Target != nil {
		out = append(out, field{"target.file", d.Target.File}, field{"target.command", d.Target.Command})
	}
	if d.Fanout != nil {
		out = append(out, field{"fanout.between", d.Fanout.Between}, field{"fanout.until", d.Fanout.Until})
	}
	args := make([]string, 0, len(d.Args))
	for arg := range d.Args {
		args = append(args, arg)
	}
	sort.Strings(args)
	for _, arg := range args {
		out = append(out, field{"args." + arg, d.Args[arg]})
	}
	return out
}

// compile is what Compile finds in one task.
func (d *YamlTaskDefinition) compile() []CompileError {
	seg := strings.SplitN(d.Name, ".", 3)
	for len(seg) < 3 {
		seg = append(seg, "")
	}
	vars, funcs, err := ArgsOnly(d, "", seg[0], seg[1], seg[2])
	if err != nil {
		return nil
	}
	if d.Fanout != nil {
		vars["n"] = "" // a fan-out's attempts are numbered: mario gives each its {{.n}}
	}
	declared := make([]string, 0, len(vars))
	for name := range vars {
		declared = append(declared, name)
	}
	sort.Strings(declared)
	at := CompileError{Task: d.Name, File: seg[1] + "/" + strings.TrimSuffix(seg[2], "-"+d.Item) + ".yaml"}
	var found []CompileError
	cutKeys := make([]string, 0, len(d.Cut))
	for key := range d.Cut {
		cutKeys = append(cutKeys, key)
	}
	sort.Strings(cutKeys)
	for _, key := range cutKeys {
		e := at
		e.Field = key
		e.Problem = fmt.Sprintf("ends inside a quote: YAML read the rest of the line as a comment (%s)", d.Cut[key])
		e.Fix = fmt.Sprintf("Write it as a block, where # is text: %s: | and the line under it, indented", key)
		found = append(found, e)
	}
	for _, f := range d.fields() {
		if !strings.Contains(f.Text, "{{") {
			continue
		}
		e := at
		e.Field = f.Key
		tree, err := template.New(d.Name).Funcs(funcs).Parse(f.Text)
		if err != nil {
			e.Problem = "the template does not parse (" + strings.TrimPrefix(err.Error(), "template: "+d.Name+":") + ")"
			e.Fix = "Close every {{ with }}, quote strings with \", and call a function as {{ name arg }}"
			found = append(found, e)
			continue
		}
		seen := map[string]bool{}
		for _, arg := range usedArgs(tree.Tree.Root) {
			if _, ok := vars[arg]; ok || seen[arg] {
				continue
			}
			seen[arg] = true
			e.Problem = fmt.Sprintf("uses {{.%s}}, an arg this task does not declare; it would render <no value>", arg)
			e.Fix = fmt.Sprintf("Declare it in this file under args: (%s: \"\"), or use one that is declared: %s", arg, strings.Join(declared, ", "))
			found = append(found, e)
		}
	}
	return found
}

// usedArgs is the top-level names a template reads from its data: {{.name}} and {{$.name}}. Inside
// {{range}} and {{with}} the dot is something else, so their bodies are read for {{$.name}} only.
func usedArgs(node parse.Node) []string {
	var out []string
	var walk func(n parse.Node, dot bool)
	walk = func(n parse.Node, dot bool) {
		switch n := n.(type) {
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c, dot)
			}
		case *parse.ActionNode:
			walk(n.Pipe, dot)
		case *parse.PipeNode:
			if n == nil {
				return
			}
			for _, c := range n.Cmds {
				walk(c, dot)
			}
		case *parse.CommandNode:
			for _, a := range n.Args {
				walk(a, dot)
			}
		case *parse.FieldNode:
			if dot && len(n.Ident) > 0 {
				out = append(out, n.Ident[0])
			}
		case *parse.VariableNode:
			if len(n.Ident) > 1 && n.Ident[0] == "$" {
				out = append(out, n.Ident[1])
			}
		case *parse.ChainNode:
			walk(n.Node, dot)
		case *parse.IfNode:
			walk(n.Pipe, dot)
			walk(n.List, dot)
			walk(n.ElseList, dot)
		case *parse.RangeNode:
			walk(n.Pipe, dot)
			walk(n.List, false)
			walk(n.ElseList, dot)
		case *parse.WithNode:
			walk(n.Pipe, dot)
			walk(n.List, false)
			walk(n.ElseList, dot)
		}
	}
	walk(node, true)
	return out
}
