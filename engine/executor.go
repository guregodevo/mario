package engine

import (
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
)

type Executor interface {
	Execute(task workflow.ExecutableWorkflowInstance) error
}

// LocalDockerExecutor executes tasks in local Docker containers
type LocalExecutor struct {
}

func (e *LocalExecutor) Execute(task workflow.ExecutableWorkflowInstance) error {
	logger.Log.Info("local executor", "Executing task %s \n", task.Target().Name())
	return task.RunFunc()
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}
