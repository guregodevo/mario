package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
)

// countingFactory counts every fetch of a task, the cost of a visit by the
// scheduler's walk.
type countingFactory struct {
	sleepyFactory
	fetches int64
}

func (f *countingFactory) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	atomic.AddInt64(&f.fetches, 1)
	return f.sleepyFactory.NewExecutable(name)
}

func (f *countingFactory) ExecutableOf(instance workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	atomic.AddInt64(&f.fetches, 1)
	return f.sleepyFactory.ExecutableOf(instance)
}

// The backfill walk expands each task once. A layered DAG, two tasks a layer
// each requiring both of the layer below, has 2^depth paths from its sinks
// to its roots; a walk that re-expanded a task on every visit walked them
// all while the roots ran. A 92-task harness of ten rounds submitted tens
// of thousands of tasks a minute that way (Memdoor, 2026-10-27).
func TestTheBackfillWalkExpandsEachTaskOnce(t *testing.T) {
	repo := static.NewWorkflowRepository()
	version := fmt.Sprintf("walk-%d", time.Now().UnixNano())
	factory := &countingFactory{sleepyFactory: sleepyFactory{DummyTaskFactory: static.DummyTaskFactory{Version: version, Partition: "2026-10-27", Component: utils.COMPONENT}, sleep: 20 * time.Millisecond}}
	const depth = 12
	var layer []string
	var all []string
	ids := map[string]workflow.WorkflowInstanceId{}
	for d := 0; d < depth; d++ {
		var next []string
		for i := 0; i < 2; i++ {
			name := fmt.Sprintf("L%dN%d", d, i)
			ids[name] = factory.NewExecutable(name).WorkflowInstanceId
			for _, below := range layer {
				repo.Requires(name, below, version)
			}
			next = append(next, name)
		}
		layer = next
		all = append(all, next...)
	}
	atomic.StoreInt64(&factory.fetches, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := NewLocalScheduler(true, true, static.NewChannelQueue(4096), static.NewChannelQueue(64), repo, factory, engine.NewLocalExecutor(), time.Millisecond)
	s.SetContext(ctx)
	for _, sink := range layer {
		if err := s.Trigger(ids[sink]); err != nil {
			t.Fatal(err)
		}
	}
	finished := make(chan struct{})
	go func() { _ = s.WaitForCompletion(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not complete in 20 s")
	}
	for _, name := range all {
		id := ids[name]
		exe, ok := repo.Fetch(id.InstanceId())
		if !ok || exe.Status != workflow.Done {
			t.Fatalf("%s is not done", name)
		}
	}
	fetches := atomic.LoadInt64(&factory.fetches)
	if limit := int64(len(all) * 40); fetches > limit {
		t.Fatalf("%d fetches for %d tasks: the walk re-expanded tasks (limit %d)", fetches, len(all), limit)
	}
	t.Logf("%d tasks, %d fetches", len(all), fetches)
}
