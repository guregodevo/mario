package factory

import (
	"context"
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

type Component interface {
	Add(factory TaskFactory)
	Get(name string) (TaskFactory, bool)
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
