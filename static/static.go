package static

import (
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
	"time"
)

type CommonExecutable struct {
	workflow.ExecutableWorkflowInstance
}

// ToExecutable hands out THE instance, not a copy: it carries a mutex, and a
// copied mutex is a different lock (go vet refuses the copy).
func (t *CommonExecutable) ToExecutable() *workflow.ExecutableWorkflowInstance {
	return &t.ExecutableWorkflowInstance
}

func (t *CommonExecutable) Execute() error {

	// Check if the task has already been started or completed
	t.Mu.Lock()
	t.StartDate = time.Now().UTC()
	if t.WfOutput.Exists() {
		t.Status = workflow.Done
		t.Mu.Unlock()
		logger.Log.Info(fmt.Sprintf("Task %s already %s. Skipping execution.", t.DName, t.Status), "component", "workflow")
		return nil

	}
	t.Mu.Unlock()

	t.Mu.Lock()
	t.Status = workflow.Started
	t.Mu.Unlock()
	logger.Log.Info(fmt.Sprintf("Starting task %s", t.DName), "component", "workflow")
	err := t.RunFunc()
	if err != nil {
		t.Mu.Lock()
		t.Status = workflow.Failed
		t.Mu.Unlock()
		return err
	}

	t.Mu.Lock()
	t.Status = workflow.Done
	t.Mu.Unlock()

	logger.Log.Info(fmt.Sprintf("Completed task instance %s", t.InstanceId()), "component", "workflow")
	return nil
}
