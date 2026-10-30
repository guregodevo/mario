package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
)

// sleepyFactory makes tasks that run for a while and fail with the context's
// error when the run is cancelled under them, with retries allowed.
type sleepyFactory struct {
	static.DummyTaskFactory
	sleep time.Duration
}

// run sleeps, or returns the context's error when the run is cancelled first.
func (f *sleepyFactory) run(ctx context.Context) error {
	select {
	case <-time.After(f.sleep):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *sleepyFactory) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	return static.DummyBuilder(0, 0).SetWorkflow(name, 3, false, f.Version, f.Component).SetRuntime(f.Partition, workflow.Scheduled, 0).
		SetConcrete(&static.DummyDataEndpoint{EndpointName: name}, f.run).Instance().ToExecutable()
}

func (f *sleepyFactory) ExecutableOf(instance workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	return static.DummyBuilder(0, 0).SetExecution(instance).SetConcrete(&static.DummyDataEndpoint{EndpointName: instance.DName}, f.run).Instance().ToExecutable()
}

// A cancelled run ends: the task running when the context is cancelled fails
// once and is not retried, nothing upstream is re-triggered, and
// WaitForCompletion returns. It used to retry the cancelled task, whose Start
// re-triggered its requirements through the backfill, which failed on the same
// cancelled context and were retried in turn: a stopped run that never ended
// and submitted tasks by the hundred a minute (2026-10-26).
func TestACancelledRunEndsWithoutRetrying(t *testing.T) {
	repo := static.NewWorkflowRepository()
	version := fmt.Sprintf("cancel-%d", time.Now().UnixNano())
	factory := &sleepyFactory{DummyTaskFactory: static.DummyTaskFactory{Version: version, Partition: "2026-10-26", Component: utils.COMPONENT}, sleep: 300 * time.Millisecond}
	a, b, c := factory.NewExecutable("A"), factory.NewExecutable("B"), factory.NewExecutable("C")
	repo.Requires(b.WorkflowName(), a.WorkflowName(), version)
	repo.Requires(c.WorkflowName(), b.WorkflowName(), version)

	ctx, cancel := context.WithCancel(context.Background())
	s := NewLocalScheduler(true, true, static.NewChannelQueue(32), static.NewChannelQueue(32), repo, factory, engine.NewLocalExecutor(), time.Millisecond)
	s.SetContext(ctx)
	if err := s.Trigger(c.WorkflowInstanceId); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // A is running
	cancel()

	done := make(chan struct{})
	go func() { _ = s.WaitForCompletion(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("the cancelled run did not end: %d tasks still counted", s.counter)
	}
	for _, task := range []*workflow.ExecutableWorkflowInstance{a, b, c} {
		got, _ := repo.Fetch(task.InstanceId())
		t.Logf("%s: status %v retries %d start %v end %v executions %d", task.DName, got.Status, got.DRetries, got.StartDate.Format("15:04:05.000"), got.EndDate.Format("15:04:05.000"), len(repo.Executions(task.InstanceId())))
		if got.Status == workflow.Done {
			t.Errorf("%s completed under a cancelled context", task.DName)
		}
		if n := len(repo.Executions(task.InstanceId())); n > 1 {
			t.Errorf("%s was executed %d times after the cancel; once at most", task.DName, n)
		}
		if got.DRetries > 0 {
			t.Errorf("%s was retried %d times after the cancel", task.DName, got.DRetries)
		}
	}
}
