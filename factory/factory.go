package factory

import (
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
	"log"
)

type TaskFactory interface {
	Name() []string
	NewWorkflow(name, version, partition, component string) (workflow.WorkflowInstanceId, error)
	NewExecutable(name string) workflow.ExecutableWorkflowInstance
	ExecutableOf(instance workflow.WorkflowExecution) workflow.ExecutableWorkflowInstance
	Fn(name string) func() error
	NewDataEndpoint(name string) workflow.DataEndpoint
	NewYaml() *templates.YAMLValidator
}

type Component interface {
	Add(factory TaskFactory)
	Get(name string) TaskFactory
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

func (r *MarioComponent) Get(name string) TaskFactory {
	factory, exists := r.factories[name]
	if exists {
		return factory
	}
	log.Fatalf("Unable to find factory '%s'", name)
	return nil
}
