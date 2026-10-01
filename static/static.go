package static

import (
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
	"time"
)

type CommonExecutable struct {
	workflow.ExecutableWorkflowInstance
}

func (t *CommonExecutable) ToExecutable() workflow.ExecutableWorkflowInstance {
	return t.ExecutableWorkflowInstance
}

func (t *CommonExecutable) Execute() error {

	// Check if the task has already been started or completed
	t.Mu.Lock()
	t.StartDate = time.Now().UTC()
	if t.WfOutput.Exists() {
		t.Status = workflow.Done
		t.Mu.Unlock()
		logger.Log.Info("workflow", "Task %s already %s. Skipping execution.\n", t.DName, t.Status)
		return nil

	}
	t.Mu.Unlock()

	t.Mu.Lock()
	t.Status = workflow.Started
	t.Mu.Unlock()
	logger.Log.Info("workflow", "Starting task %s\n", t.DName)
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

	logger.Log.Info("workflow", "Completed task instance %s\n", t.InstanceId())
	return nil
}
