package scheduler

import (
	"fmt"
	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/lineage"
	"github.com/guregodevo/mario/sqlite"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
	"os"
	"testing"
	"time"
)

func allTasksDone(repo workflow.WorkflowRepository, tasks []workflow.WorkflowExecution, version string) bool {
	for _, task := range tasks {
		if task.Status != workflow.Done {
			return false
		}
		if len(repo.Executions(task.InstanceId())) == 0 {
			return false
		}
	}
	return true
}

func TestNoCycles(t *testing.T) {
	partition := "2023-01-01"
	version := fmt.Sprintf("%d", time.Now().Unix())
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}
	repo := static.NewWorkflowRepository()
	taskA := factory.NewExecutable("A")
	taskB := factory.NewExecutable("B")
	taskC := factory.NewExecutable("C")
	taskD := factory.NewExecutable("D")
	taskE := factory.NewExecutable("E")
	taskF := factory.NewExecutable("F")

	repo.Requires(taskA.WorkflowName(), taskF.WorkflowName(), version)
	repo.Requires(taskB.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskC.WorkflowName(), taskB.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskC.WorkflowName(), version)
	repo.Requires(taskE.WorkflowName(), taskD.WorkflowName(), version)
	repo.Requires(taskF.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskF.WorkflowName(), taskE.WorkflowName(), version)
	if !lineage.HasCycle(repo, taskA.WorkflowName(), version, make(map[string]bool), make(map[string]bool)) {
		t.Fatalf("Cycle NOT detected in the DAG!")
	}
	for i := 0; i < 10; i++ {
		r := static.NewWorkflowRepository()
		tasks := lineage.GenerateRandomDAG(factory, r, version, partition, 0*time.Millisecond, 0, 100, 100)
		hasCycle(t, repo, tasks, version)
	}
}

func hasCycle(t *testing.T, repo workflow.WorkflowRepository, tasks []workflow.WorkflowExecution, version string) {
	for _, task := range tasks {
		if lineage.HasCycle(repo, task.InstanceId(), version, make(map[string]bool), make(map[string]bool)) {
			t.Fatalf("Cycle detected in the DAG!")
		}
	}
}

func TestNoDeadlocks(t *testing.T) {
	version := fmt.Sprintf("%d", time.Now().Unix())

	partition := "2023-02-01"
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	for i := 0; i < 10; i++ {
		repo := static.NewWorkflowRepository()

		tasks := lineage.GenerateRandomDAG(factory, repo, version, partition, 0*time.Millisecond, 0, 10, 10)
		rootTask := tasks[len(tasks)-1]

		doneCh := make(chan error)
		fn := &static.DummyTaskFactory{}

		executor := NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, fn, engine.NewLocalExecutor(), 1*time.Millisecond)

		go func() {
			if err := executor.Trigger(rootTask.WorkflowInstanceId); err != nil {
				doneCh <- fmt.Errorf("Execution failed at Trigger: %v", err)
				return
			}

			executor.WaitForCompletion()

			doneCh <- nil
		}()

		select {
		case err := <-doneCh:
			if err != nil {
				t.Fatalf("Execution failed: %v", err)
			}
		case <-time.After(11 * time.Second):
			t.Fatalf("Potential deadlock detected, tasks didn't complete!")
		}
	}
}

func TestRandomDAGSqlite(t *testing.T) {

	partition := "2023-02-01"
	version := fmt.Sprintf("%d", time.Now().Unix())
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	for i := 0; i < 2; i++ {
		removeDB("test_randomdag")
		repo := sqlite.NewWorkflowRepository("test_randomdag", static.BuilderDummyFn)

		executor := NewLocalScheduler(true, true, static.NewChannelQueue(10), static.NewChannelQueue(10), repo, factory, engine.NewLocalExecutor(), 1*time.Millisecond)

		tasks := lineage.GenerateRandomDAG(factory, repo, version, partition, 0*time.Millisecond, 1, 10, 10)

		for _, task := range tasks {
			executor.Trigger(task.WorkflowInstanceId)
		}

		if err := executor.WaitForCompletion(); err != nil {
			t.Fatalf("Execution failed: %v", err)
		}
		validate(t, repo, tasks, false, version)
		time.Sleep(100 * time.Millisecond)
		repo.Close()
		removeDB("test_randomdag")
		time.Sleep(100 * time.Millisecond)

	}
}

func TestRandomDAGStatic(t *testing.T) {
	version := fmt.Sprintf("%d", time.Now().Unix())
	partition := "2023-01-01"
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	for i := 0; i < 10; i++ {
		repo := static.NewWorkflowRepository()
		executor := NewLocalScheduler(true, true, static.NewChannelQueue(10), static.NewChannelQueue(10), repo, factory, engine.NewLocalExecutor(), 1*time.Millisecond)
		tasks := lineage.GenerateRandomDAG(factory, repo, version, partition, 0*time.Millisecond, 1, 10, 10)

		for _, task := range tasks {
			executor.Trigger(task.WorkflowInstanceId)
		}

		if err := executor.WaitForCompletion(); err != nil {
			t.Fatalf("Execution failed: %v", err)
		}
		validate(t, repo, tasks, false, version)
	}
}

func TestDeterministicDAGTriadSqlite(t *testing.T) {
	removeDB("test_dag_triad")
	defer removeDB("test_dag_triad")
	repo := sqlite.NewWorkflowRepository("test_dag_triad", static.BuilderDummyFn)
	defer repo.Close()
	taskQueue := static.NewChannelQueue(10)

	_TestDeterministicDAGTriad(t, taskQueue, static.NewChannelQueue(10), repo)
}

func TestDeterministicDAGTriadStatic(t *testing.T) {
	repo := static.NewWorkflowRepository()
	_TestDeterministicDAGTriad(t, static.NewChannelQueue(10), static.NewChannelQueue(10), repo)
}

func _TestDeterministicDAGTriad(t *testing.T, taskQueue workflow.Queue, retryQueue workflow.Queue, repo workflow.WorkflowRepository) {
	partition := "2023-01-01"
	version := fmt.Sprintf("%d", time.Now().Unix())

	factory := static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	// Define tasks
	taskA := factory.NewExecutable("A")
	taskAs := factory.NewExecutable("As")
	taskB := factory.NewExecutable("B")
	taskBs := factory.NewExecutable("Bs")

	repo.Requires(taskA.WorkflowName(), taskAs.WorkflowName(), version)
	repo.Requires(taskB.WorkflowName(), taskBs.WorkflowName(), version)
	taskC := factory.NewExecutable("C")
	repo.Requires(taskC.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskC.WorkflowName(), taskB.WorkflowName(), version)

	executor := NewLocalScheduler(true, true, taskQueue, retryQueue, repo, &factory, engine.NewLocalExecutor(), 1*time.Millisecond)

	err := executor.Trigger(taskC.WorkflowInstanceId)
	if err != nil {
		t.Fatalf("Execution failed: %v. Task %v", err, taskC)
		return
	}

	executor.WaitForCompletion()

	tasks := []workflow.WorkflowExecution{taskA.WorkflowExecution, taskB.WorkflowExecution, taskC.WorkflowExecution}
	validate(t, repo, tasks, true, version)
}

func validate(t *testing.T, repo workflow.WorkflowRepository, tasks []workflow.WorkflowExecution, checkExecutions bool, version string) {
	for i, _ := range tasks {
		tasks[i], _ = repo.Fetch(tasks[i].InstanceId())
	}
	if !allTasksDone(repo, tasks, version) {
		for _, task := range tasks {
			if task.Status != workflow.Done {
				for _, task := range tasks {
					t.Log(task.String())
				}
				t.Fatalf("Task %s did not complete!", task.DName)
			}
			if checkExecutions && task.Status != workflow.Done {
				//verify that task has 1 execution done.
				if executions := repo.Executions(task.InstanceId()); len(executions) == 0 {
					t.Fatalf("Task %s missing executions (0)", task.InstanceId())
				} else {
					doneCount := 0
					for i := range executions {
						exe := executions[i]
						if exe.Status == workflow.Done {
							doneCount++
						}
					}
					if doneCount == 0 {
						t.Fatalf("Task %s missing 1 done execution", task.InstanceId())
					}
					if doneCount > 1 {
						t.Fatalf("Task %s has %d done executions. Expected 1 . Actual executions : %v", task.InstanceId(), doneCount, executions)
					}
				}
			}
		}
	}
}

func TestDeterministicDAGStatic(t *testing.T) {
	repo := static.NewWorkflowRepository()
	_TestDeterministicDAG(t, static.NewChannelQueue(10), static.NewChannelQueue(10), repo)
}

func TestDeterministicDAGSqlite(t *testing.T) {
	removeDB("test_dag")
	defer removeDB("test_dag")
	repo := sqlite.NewWorkflowRepository("test_dag", static.BuilderDummyFn)
	defer repo.Close()
	taskQueue := static.NewChannelQueue(10)

	_TestDeterministicDAG(t, taskQueue, static.NewChannelQueue(10), repo)

}

func _TestDeterministicDAG(t *testing.T, taskQueue workflow.Queue, retryQueue workflow.Queue, repo workflow.WorkflowRepository) {
	partition := "2023-02-01"
	version := fmt.Sprintf("%d", time.Now().Unix())
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	// Define tasks
	taskA := factory.NewExecutable("A")
	taskB := factory.NewExecutable("B")
	taskC := factory.NewExecutable("C")
	taskD := factory.NewExecutable("D")

	repo.Requires(taskC.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskC.WorkflowName(), taskB.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskC.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskA.WorkflowName(), version)

	executor := NewLocalScheduler(true, true, taskQueue, retryQueue, repo, factory, engine.NewLocalExecutor(), 1*time.Millisecond)

	err := executor.Trigger(taskD.WorkflowInstanceId)
	if err != nil {
		t.Fatalf("Execution failed: %v. Task %v", err, taskD)
		return
	}

	executor.WaitForCompletion()
	tasks := []workflow.WorkflowExecution{taskA.WorkflowExecution, taskB.WorkflowExecution, taskC.WorkflowExecution, taskD.WorkflowExecution}
	validate(t, repo, tasks, true, version)
}
// removeDB clears what a sqlite-backed test wrote. A persistence repository
// no longer deletes its file on Close (that is what choosing it is for), so
// the test owns its own litter: before it opens, and when it lets go.
func removeDB(name string) {
	for _, f := range []string{"./" + name + ".db", "./" + name + ".db-shm", "./" + name + ".db-wal"} {
		os.Remove(f)
	}
}
