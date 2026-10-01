package lineage

import (
	"fmt"
	"github.com/guregodevo/mario/factory"
	"math/rand"
	"time"

	"github.com/guregodevo/mario/workflow"
)

func IndexOf(Workflow workflow.WorkflowExecution, Workflows []workflow.WorkflowExecution) int {
	for i, t := range Workflows {
		if t.ExecutionId == Workflow.ExecutionId {
			return i
		}
	}
	return -1
}

func GenerateRandomDAG(factory factory.TaskFactory, repository workflow.WorkflowRepository, version, partition string, duration time.Duration, nbFailures int32, maxTasks, maxDependencies int) []workflow.WorkflowExecution {
	tasks := make([]workflow.WorkflowExecution, rand.Intn(maxTasks)+1) // +1 to ensure at least 1 task is created
	// Create tasks

	for i := range tasks {
		tasks[i] = factory.NewExecutable(fmt.Sprintf("Task_%d", i)).WorkflowExecution

	}

	// Randomly assign dependencies ensuring no cycles
	for _, task := range tasks {
		numDeps := rand.Intn(maxDependencies)
		for j := 0; j < numDeps; j++ {
			depIndex := rand.Intn(len(tasks))
			if depIndex == IndexOf(task, tasks) {
				continue // Ensure task does not depend on itself
			}
			potentialDep := tasks[depIndex]
			repository.Requires(task.WorkflowName(), potentialDep.WorkflowName(), version)
			if HasCycle(repository, task.WorkflowName(), version, make(map[string]bool), make(map[string]bool)) {
				// Dependency creates a cycle. Remove it.
				repository.RevertRequires(task.WorkflowName(), potentialDep.WorkflowName(), version)
				j-- // retry the same dependency slot
			}
		}
	}
	return tasks // Return the first task, just for reference. You might want to return the entire slice based on your needs.
}
