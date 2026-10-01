package static

import (
	"fmt"
	"testing"
	"time"

	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
	"github.com/stretchr/testify/assert"
)

func RepositoryTestcases(t *testing.T, repo workflow.WorkflowRepository) {
	partition := "2023-01-01"
	version := fmt.Sprintf("%d", time.Now().Unix())

	builderFn := DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}

	// Define tasks
	taskAA := builderFn.NewExecutable("A").WorkflowExecution

	assert.NotEmptyf(t, taskAA.InstanceId(), "instance id should not be empty")
	assert.Equalf(t, taskAA.WorkflowName(), "A", "name should not be empty")
	assert.NotEmptyf(t, taskAA.Partition, "partition should not be empty")
	assert.Equalf(t, taskAA.Status, workflow.Scheduled, "partition should not be empty")
	assert.Equalf(t, taskAA.IsExternal(), false, "external should not be empty")
	assert.Equalf(t, taskAA.DRetries, int32(0), "retries should not be empty")
	assert.Equalf(t, taskAA.MaxRetries(), int32(0), "max retries should not be empty")

	err := repo.Upsert(taskAA)
	assert.Nilf(t, err, "instance id could not be upserted . Error %v", err)

	taskA, okTaskA := repo.Fetch(taskAA.InstanceId())

	assert.Equalf(t, true, okTaskA, "instance A could not be fetched")

	assert.NotEmptyf(t, taskA.InstanceId(), "instance id should not be empty")
	assert.Equalf(t, taskA.WorkflowName(), "A", "name should not be empty")
	assert.Equalf(t, taskA.Status, workflow.Scheduled, "status should match")
	assert.NotEmptyf(t, taskA.Partition, "partition should not be empty")
	assert.Equalf(t, taskA.IsExternal(), false, "external should not be empty")
	assert.Equalf(t, taskA.DRetries, int32(0), "retries should not be empty")
	assert.Equalf(t, taskA.MaxRetries(), int32(0), "max retries should not be empty")

	taskB := builderFn.NewExecutable("B").WorkflowExecution
	taskB.Status = workflow.Done
	repo.Upsert(taskB)
	taskC := builderFn.NewExecutable("C").WorkflowExecution
	repo.Upsert(taskC)
	taskD := builderFn.NewExecutable("D").WorkflowExecution
	repo.Upsert(taskD)
	repo.Upsert(taskD) // check idempotency

	repo.Requires(taskC.InstanceId(), taskA.InstanceId(), version)
	repo.Requires(taskC.InstanceId(), taskB.InstanceId(), version)
	repo.Requires(taskD.InstanceId(), taskC.InstanceId(), version)
	repo.Requires(taskD.InstanceId(), taskA.InstanceId(), version)
	repo.Requires(taskD.InstanceId(), taskA.InstanceId(), version) // check idempotency

	// Test Upstreams function
	expectedUpstreamsForA := []string{}
	expectedUpstreamsForB := []string{}
	expectedUpstreamsForC := []string{"A", "B"}
	expectedUpstreamsForD := []string{"A", "C"}

	actualUpstreamsForA := repo.Upstreams(taskA.InstanceId(), version)
	actualUpstreamsForB := repo.Upstreams(taskB.InstanceId(), version)
	actualUpstreamsForC := repo.Upstreams(taskC.InstanceId(), version)
	actualUpstreamsForD := repo.Upstreams(taskD.InstanceId(), version)

	// Verify the upstreams
	assert.ElementsMatch(t, expectedUpstreamsForA, Names(repo, actualUpstreamsForA), "Upstreams for A should match")
	assert.ElementsMatch(t, expectedUpstreamsForB, Names(repo, actualUpstreamsForB), "Upstreams for B should match")
	assert.ElementsMatch(t, expectedUpstreamsForC, Names(repo, actualUpstreamsForC), "Upstreams for C should match")
	assert.ElementsMatch(t, expectedUpstreamsForD, Names(repo, actualUpstreamsForD), "Upstreams for D should match")

	// Test Deep Upstreams function
	expectedDeepUpstreamsForA := []string{}
	expectedDeepUpstreamsForB := []string{}
	expectedDeepUpstreamsForC := []string{"A", "B"}
	expectedDeepUpstreamsForD := []string{"A", "C", "B"}

	actualDeepUpstreamsForA := repo.DeepUpstreams(taskA.InstanceId(), version)
	actualDeepUpstreamsForB := repo.DeepUpstreams(taskB.InstanceId(), version)
	actualDeepUpstreamsForC := repo.DeepUpstreams(taskC.InstanceId(), version)
	actualDeepUpstreamsForD := repo.DeepUpstreams(taskD.InstanceId(), version)

	// Verify the Deep upstreams
	assert.ElementsMatch(t, expectedDeepUpstreamsForA, Names(repo, actualDeepUpstreamsForA), "Deep Upstreams for A should match")
	assert.ElementsMatch(t, expectedDeepUpstreamsForB, Names(repo, actualDeepUpstreamsForB), "Deep Upstreams for B should match")
	assert.ElementsMatch(t, expectedDeepUpstreamsForC, Names(repo, actualDeepUpstreamsForC), "Deep Upstreams for C should match")
	assert.ElementsMatch(t, expectedDeepUpstreamsForD, Names(repo, actualDeepUpstreamsForD), "Deep Upstreams for D should match")

	// Test Downstreams function
	expectedDownstreamsForA := []string{"C", "D"}
	expectedDownstreamsForB := []string{"C"}
	expectedDownstreamsForC := []string{"D"}
	expectedDownstreamsForD := []string{}

	actualDownstreamsForA := repo.Downstreams(taskA.InstanceId(), version)
	actualDownstreamsForB := repo.Downstreams(taskB.InstanceId(), version)
	actualDownstreamsForC := repo.Downstreams(taskC.InstanceId(), version)
	actualDownstreamsForD := repo.Downstreams(taskD.InstanceId(), version)

	// Verify the Downstreams
	assert.ElementsMatch(t, expectedDownstreamsForA, Names(repo, actualDownstreamsForA), "Downstreams for A should match")
	assert.ElementsMatch(t, expectedDownstreamsForB, Names(repo, actualDownstreamsForB), "Downstreams for B should match")
	assert.ElementsMatch(t, expectedDownstreamsForC, Names(repo, actualDownstreamsForC), "Downstreams for C should match")
	assert.ElementsMatch(t, expectedDownstreamsForD, Names(repo, actualDownstreamsForD), "Downstreams for D should match")

	// Test Deep Downstreams function
	expectedDeepDownstreamsForA := []string{"C", "D"}
	expectedDeepDownstreamsForB := []string{"C", "D"}
	expectedDeepDownstreamsForC := []string{"D"}
	expectedDeepDownstreamsForD := []string{}

	actualDeepDownstreamsForA := repo.DeepDownstreams(taskA.InstanceId(), version)
	actualDeepDownstreamsForB := repo.DeepDownstreams(taskB.InstanceId(), version)
	actualDeepDownstreamsForC := repo.DeepDownstreams(taskC.InstanceId(), version)
	actualDeepDownstreamsForD := repo.DeepDownstreams(taskD.InstanceId(), version)

	// Verify the Downstreams
	assert.ElementsMatch(t, expectedDeepDownstreamsForA, Names(repo, actualDeepDownstreamsForA), "Deep Downstreams for A should match")
	assert.ElementsMatch(t, expectedDeepDownstreamsForB, Names(repo, actualDeepDownstreamsForB), "Deep Downstreams for B should match")
	assert.ElementsMatch(t, expectedDeepDownstreamsForC, Names(repo, actualDeepDownstreamsForC), "Deep Downstreams for C should match")
	assert.ElementsMatch(t, expectedDeepDownstreamsForD, Names(repo, actualDeepDownstreamsForD), "Deep Downstreams for D should match")

	// Verify the executions
	e := workflow.WorkflowExecution{
		ExecutionId:        taskA.ExecutionId,
		StartDate:          time.Now().UTC(),
		EndDate:            time.Now().UTC(),
		Status:             workflow.Scheduled,
		Error:              "",
		DParameters:        make(map[string]string, 0),
		DRetries:           10,
		WorkflowInstanceId: taskA.WorkflowInstanceId,
	}
	assert.NotEmptyf(t, "execution id should not be empty", taskA.InstanceId())
	repo.Upsert(e)
	actualExecutionsForA := repo.Executions(taskA.InstanceId())
	assert.ElementsMatch(t, []string{e.ExecutionId}, ExecutionIds(actualExecutionsForA), "Executions for A should match")
}

func MapKeysToSlice(m map[string]workflow.WorkflowInstance) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func ExecutionIds(l map[string]workflow.WorkflowExecution) []string {
	r := make([]string, 0)
	for _, e := range l {
		println(e.ExecutionId)
		r = append(r, e.ExecutionId)
	}
	return r
}

func Ids(repo workflow.WorkflowRepository, l map[string]bool, version string) []string {
	r := make([]string, 0)
	for i := range l {
		w, _ := repo.Fetch(i)
		r = append(r, w.WorkflowName())
	}
	return r
}

func Names(repo workflow.WorkflowRepository, l map[string]bool) []string {
	r := make([]string, 0)
	for i := range l {
		w, _ := repo.Fetch(i)
		r = append(r, w.WorkflowName())
	}
	return r
}
