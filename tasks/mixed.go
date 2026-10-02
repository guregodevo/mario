package tasks

import (
	"context"
	"fmt"

	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

// Mixed is the factory a DAG of several types runs on: each task goes to the
// registered factory of its definition's `type`; a name with no definition
// (an external) goes to the first registered one, whose output convention
// every bound Base shares. It is what the scheduler is handed.
func Mixed(reg factory.Component, defs map[string]*templates.YamlTaskDefinition) factory.TaskFactory {
	return &mixed{reg: reg, defs: defs}
}

type mixed struct {
	reg  factory.Component
	defs map[string]*templates.YamlTaskDefinition
}

func (m *mixed) of(name string) factory.TaskFactory {
	if d, ok := m.defs[name]; ok {
		if f, ok := m.reg.Get(d.Type); ok {
			return f
		}
	}
	names := m.reg.Names()
	if len(names) == 0 {
		panic(fmt.Sprintf("tasks.Mixed: no task type registered (task %s)", name))
	}
	f, _ := m.reg.Get(names[0])
	return f
}

func (m *mixed) Name() []string { return m.reg.Names() }
func (m *mixed) NewWorkflow(name, version, partition, component string) (workflow.WorkflowInstanceId, error) {
	return m.of(name).NewWorkflow(name, version, partition, component)
}
func (m *mixed) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	return m.of(name).NewExecutable(name)
}
func (m *mixed) ExecutableOf(e workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	return m.of(e.WorkflowName()).ExecutableOf(e)
}
func (m *mixed) Fn(name string) func(ctx context.Context) error { return m.of(name).Fn(name) }
func (m *mixed) NewDataEndpoint(name string) workflow.DataEndpoint {
	return m.of(name).NewDataEndpoint(name)
}
