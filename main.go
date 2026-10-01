package main

import (
	"fmt"
	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/scheduler"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
	"os"
	"time"
)

func main() {
	partition := "2023-01-01"
	version := fmt.Sprintf("%d", time.Now().Unix())

	repo := static.NewWorkflowRepository()
	factory := static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}
	defer repo.Close()
	// Define tasks
	taskA := factory.NewExecutable("A")
	taskB := factory.NewExecutable("B")
	taskB.Status = workflow.Done
	taskC := factory.NewExecutable("C")
	taskD := factory.NewExecutable("D")
	taskExternalD := factory.NewExecutable("ExternalD")
	taskExternalD.DExternal = true
	repo.Requires(taskC.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskC.WorkflowName(), taskB.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskC.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskExternalD.WorkflowName(), version)

	executor := scheduler.NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, &factory, engine.NewLocalExecutor(), 3*time.Second)
	executor.Trigger(taskD.WorkflowInstanceId)
	if err := executor.WaitForCompletion(); err != nil {
		fmt.Fprintf(os.Stderr, "execution failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("All tasks completed successfully!")
}
