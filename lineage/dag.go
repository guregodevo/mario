package lineage

import (
	"fmt"
	"github.com/guregodevo/mario/workflow"
)

func HasCycle(repository workflow.WorkflowRepository, name, version string, visited map[string]bool, recursionStack map[string]bool) bool {
	if recursionStack[name] {
		return true
	}

	if visited[name] {
		return false
	}

	visited[name] = true
	recursionStack[name] = true

	for dep, _ := range repository.Upstreams(name, version) {
		if HasCycle(repository, dep, version, visited, recursionStack) {
			return true
		}
	}

	recursionStack[name] = false
	return false
}

// flattenDependencies recursively adds all unique dependencies of a task to an accumulator map.
func flattenDependencies(repository workflow.WorkflowRepository, task string, version string, accumulator map[string]bool) {
	if _, exists := accumulator[task]; exists {
		return
	}
	accumulator[task] = true
	for dep, _ := range repository.Upstreams(task, version) {
		flattenDependencies(repository, dep, version, accumulator)
	}
}

func SortDAG(repository workflow.WorkflowRepository, tasks []string, version string) ([]string, error) {
	allTasksMap := make(map[string]bool)
	for _, task := range tasks {
		flattenDependencies(repository, task, version, allTasksMap)
	}

	var allTasks []string
	for task, _ := range allTasksMap {
		allTasks = append(allTasks, task)
	}

	sortedTasks, err := topologicalSortMultiple(repository, version, allTasks)
	if err != nil {
		return nil, err
	}
	return sortedTasks, nil
}

// topologicalSortMultiple returns tasks in an order where each task appears before its dependencies.
func topologicalSortMultiple(repository workflow.WorkflowRepository, version string, tasks []string) ([]string, error) {
	var sorted []string
	visited := make(map[string]bool)
	temp := make(map[string]bool)

	var visit func(task string) error
	visit = func(task string) error {
		taskKey := task

		if temp[taskKey] {
			return fmt.Errorf("cycle detected")
		}

		if !visited[taskKey] {
			temp[taskKey] = true
			for dep, _ := range repository.Upstreams(task, version) {
				if err := visit(dep); err != nil {
					return err
				}
			}
			visited[taskKey] = true
			temp[taskKey] = false
			sorted = append([]string{task}, sorted...)
		}
		return nil
	}

	for _, task := range tasks {
		if err := visit(task); err != nil {
			return nil, err
		}
	}
	if len(sorted) != len(tasks) {
		return nil, fmt.Errorf("missing tasks not sorted")
	}
	return sorted, nil
}
