package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
)

// recorder is a host listening to a run, the way a UI would.
type recorder struct {
	mu  sync.Mutex
	seq []string
}

func (r *recorder) add(s string)                       { r.mu.Lock(); r.seq = append(r.seq, s); r.mu.Unlock() }
func (r *recorder) TaskStarted(name, partition string) { r.add("started " + name) }
func (r *recorder) TaskDone(name, partition string)    { r.add("done " + name) }
func (r *recorder) TaskFailed(name, partition string, err error, willRetry bool) {
	r.add(fmt.Sprintf("failed %s retry=%v", name, willRetry))
}

// A host sees every transition, in the order the graph imposes: B requires A,
// so A starts and finishes before B starts.
func TestEventsFollowTheRun(t *testing.T) {
	version := fmt.Sprintf("%d", time.Now().UnixNano())
	partition := "2026-10-02"
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}
	repo := static.NewWorkflowRepository()
	a := factory.NewExecutable("A")
	b := factory.NewExecutable("B")
	repo.Requires(b.WorkflowName(), a.WorkflowName(), version)

	rec := &recorder{}
	s := NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, factory, engine.NewLocalExecutor(), time.Millisecond)
	s.SetEvents(rec)
	if err := s.Trigger(b.WorkflowInstanceId); err != nil {
		t.Fatal(err)
	}
	s.WaitForCompletion()

	rec.mu.Lock()
	got := strings.Join(rec.seq, " | ")
	rec.mu.Unlock()
	if got != "started A | done A | started B | done B" {
		t.Fatalf("events = %q", got)
	}
}

type countingExecutor struct{ n atomic.Int32 }

func (c *countingExecutor) Execute(ctx context.Context, task *workflow.ExecutableWorkflowInstance) error {
	c.n.Add(1)
	return task.RunFunc(ctx)
}

// Once the run's context is cancelled nothing more starts, and the run still
// completes — a cancelled run must not hang the caller.
func TestACancelledRunStartsNothing(t *testing.T) {
	version := fmt.Sprintf("%d", time.Now().UnixNano())
	partition := "2026-10-02"
	factory := &static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}
	repo := static.NewWorkflowRepository()
	a := factory.NewExecutable("A")

	exec := &countingExecutor{}
	s := NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, factory, exec, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.SetContext(ctx)
	if err := s.Trigger(a.WorkflowInstanceId); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.WaitForCompletion(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled run must still complete")
	}
	if n := exec.n.Load(); n != 0 {
		t.Fatalf("a cancelled run started %d task(s)", n)
	}
}
