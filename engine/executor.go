package engine

import (
	"context"
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
)

type Executor interface {
	Execute(ctx context.Context, task *workflow.ExecutableWorkflowInstance) error
}

// LocalDockerExecutor executes tasks in local Docker containers
type LocalExecutor struct {
}

func (e *LocalExecutor) Execute(ctx context.Context, task *workflow.ExecutableWorkflowInstance) error {
	logger.Log.Info(fmt.Sprintf("Executing task %s", task.Target().Name()), "component", "local executor")
	return task.RunFunc(ctx)
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}
