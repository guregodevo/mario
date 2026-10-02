package factory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

type TaskFactory interface {
	Name() []string
	NewWorkflow(name, version, partition, component string) (workflow.WorkflowInstanceId, error)
	NewExecutable(name string) *workflow.ExecutableWorkflowInstance
	ExecutableOf(instance workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance
	Fn(name string) func(ctx context.Context) error
	NewDataEndpoint(name string) workflow.DataEndpoint
}

// YAMLValidated is the optional half of a factory that reads mario's own
// per-table YAML; a factory for another kind of task does not carry it.
type YAMLValidated interface {
	NewYaml() *templates.YAMLValidator
}

// Schematic is the optional half of a factory that validates its own YAML:
// the JSON schema for the task type it builds. NewValidator gathers them.
type Schematic interface {
	Schema() []byte
}

type Component interface {
	Add(factory TaskFactory)
	Get(name string) (TaskFactory, bool)
	Names() []string
}

type MarioComponent struct {
	factories map[string]TaskFactory
}

func NewComponent() Component {
	return &MarioComponent{factories: make(map[string]TaskFactory, 0)}
}

func (r *MarioComponent) Add(factory TaskFactory) {
	for _, name := range factory.Name() {
		r.factories[name] = factory
	}
}

// Get answers the factory registered under name, and whether there is one. A
// library does not exit the process over a missing registration; the caller
// decides what a DAG with an unknown task type means.
func (r *MarioComponent) Get(name string) (TaskFactory, bool) {
	factory, exists := r.factories[name]
	return factory, exists
}

// Names lists the registered task types, sorted.
func (r *MarioComponent) Names() []string {
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// NewValidator validates task YAML against the registered types: the root
// schema admits exactly the registered `type` names, and each type's own
// schema is the one its factory carries (Schematic). A type without a
// schema accepts any file whose type names it. The YAML says the type; the
// factory behind it builds the task.
func NewValidator(reg Component) *templates.YAMLValidator {
	return templates.NewYAMLValidator(registryLoader{reg: reg})
}

type registryLoader struct{ reg Component }

func (l registryLoader) LoadSchema(name string) ([]byte, error) {
	if name == "root" {
		names, _ := json.Marshal(l.reg.Names())
		return []byte(fmt.Sprintf(`{"type":"object","properties":{"type":{"enum":%s}},"required":["type"]}`, names)), nil
	}
	f, ok := l.reg.Get(name)
	if !ok {
		return nil, fmt.Errorf("no task type %q is registered (registered: %v)", name, l.reg.Names())
	}
	if s, ok := f.(Schematic); ok {
		return s.Schema(), nil
	}
	return []byte(`{"type":"object"}`), nil
}
