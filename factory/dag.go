package factory

import (
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

// BuildDAG populates the repository's dependencies from the task definitions
// and returns every task by name. It answers an error — an unknown task type,
// a workflow that cannot be registered, a required dependency with no
// definition — rather than exiting the process: a library reports, the
// program decides.
func BuildDAG(version string, partition, component string, repository workflow.WorkflowRepository, taskDefs map[string]*templates.YamlTaskDefinition, reg Component) (map[string]workflow.WorkflowInstanceId, error) {
	tasks := make(map[string]workflow.WorkflowInstanceId, 0)
	for _, taskDef := range taskDefs {
		f, ok := reg.Get(taskDef.Type)
		if !ok {
			return nil, fmt.Errorf("task %s: no factory registered for type %q", taskDef.Name, taskDef.Type)
		}
		wf, err := f.NewWorkflow(taskDef.Name, version, partition, component)
		if err != nil {
			return nil, fmt.Errorf("task %s: cannot register workflow: %w", taskDef.Name, err)
		}
		createOrUpdate(false, wf, tasks)
	}
	for _, t := range taskDefs {
		for _, dep := range t.Requires {
			depTask, ok := tasks[dep.Name()]
			if ok {
				if dep.External {
					// A defined task would be run; "external" says it must not be.
					// Silently running it would open a gate nobody opened.
					return nil, fmt.Errorf("task %s requires %s as external, but %s has a definition in this DAG: an external task is made outside it — delete the definition, or drop external", t.Name, dep.Name(), dep.Name())
				}
				exe := tasks[t.Name]
				repository.Requires(exe.WorkflowName(), depTask.WorkflowName(), version)
				continue
			}
			if !dep.External {
				return nil, fmt.Errorf("task %s requires %s, which has no definition and is not external", t.Name, dep.Name())
			}
			f, ok := reg.Get(t.Type)
			if !ok {
				return nil, fmt.Errorf("task %s: no factory registered for type %q", t.Name, t.Type)
			}
			w, err := f.NewWorkflow(dep.Name(), version, partition, component)
			if err != nil {
				return nil, fmt.Errorf("task %s: cannot register external dependency %s: %w", t.Name, dep.Name(), err)
			}
			createOrUpdate(true, w, tasks)
			logger.Log.Info(fmt.Sprintf("%s --> %s (external)", t.Name, dep.Name()), "component", "dag")
			exe := tasks[t.Name]
			repository.Requires(exe.WorkflowName(), w.WorkflowName(), version)
		}
	}
	return tasks, nil
}

func createOrUpdate(external bool, w workflow.WorkflowInstanceId, tasks map[string]workflow.WorkflowInstanceId) {
	w.DExternal = external
	tasks[w.WorkflowName()] = w
}
