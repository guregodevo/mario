package factory

import (
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

// BuildDAG Populates DAG dependencies. All tasks are returned in a map  whose key is the Task Name
func BuildDAG(version string, partition, component string, repository workflow.WorkflowRepository, taskDefs map[string]*templates.YamlTaskDefinition, reg Component) map[string]workflow.WorkflowInstanceId {
	tasks := make(map[string]workflow.WorkflowInstanceId, 0)

	for _, taskDef := range taskDefs {
		wf, err := reg.Get(taskDef.Type).NewWorkflow(taskDef.Name, version, partition, component)
		if err != nil {
			logger.Log.Fatal("cmd", "Cannot register Workflow %v \n", taskDef)
		}
		createOrUpdate(false, wf, tasks)
	}

	for _, t := range taskDefs {
		for _, dep := range t.Requires {
			depTask, ok := tasks[dep.Name()]
			if ok {
				exe := tasks[t.Name]
				repository.Requires(exe.WorkflowName(), depTask.WorkflowName(), version)
			} else {
				if dep.External {
					w, e := reg.Get(t.Type).NewWorkflow(dep.Name(), version, partition, component)
					if e != nil {
						logger.Log.Info("dag", "Unexpected error when adding dependency %s : %v \n", dep.Name(), e)
					} else {
						createOrUpdate(true, w, tasks)
						logger.Log.Info("dag", "[LOG] %s --> %s \n", t.Name, dep.Name())
						exe := tasks[t.Name]
						repository.Requires(exe.WorkflowName(), w.WorkflowName(), version)
					}
				} else {
					logger.Log.Fatal("dag", "Cannot find yaml file for required dependency : %s \n", dep.Name())
				}
			}
		}
	}
	return tasks
}

func createOrUpdate(external bool, w workflow.WorkflowInstanceId, tasks map[string]workflow.WorkflowInstanceId) {
	w.DExternal = external
	tasks[w.WorkflowName()] = w
}
