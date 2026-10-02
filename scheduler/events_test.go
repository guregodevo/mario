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

// recorder is a host listening to a run, the way a UI would: it reads the
// engine's channel and keeps the order.
type recorder struct {
	mu  sync.Mutex
	seq []string
	ch  chan Event
	wg  sync.WaitGroup
}

func newRecorder() *recorder {
	r := &recorder{ch: make(chan Event, 64)}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for ev := range r.ch {
			line := string(ev.Kind) + " " + ev.Name
			if ev.Kind == Failed {
				line = fmt.Sprintf("failed %s retry=%v", ev.Name, ev.WillRetry)
			}
			r.mu.Lock()
			r.seq = append(r.seq, line)
			r.mu.Unlock()
		}
	}()
	return r
}

// joined waits for the channel to close (the run is complete) and answers the order.
func (r *recorder) joined() string {
	r.wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.seq, " | ")
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

	rec := newRecorder()
	s := NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, factory, engine.NewLocalExecutor(), time.Millisecond)
	s.SetEvents(rec.ch)
	if err := s.Trigger(b.WorkflowInstanceId); err != nil {
		t.Fatal(err)
	}
	s.WaitForCompletion()

	if got := rec.joined(); got != "started A | done A | started B | done B" {
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

// presentFactory is a dummy factory whose endpoints can already exist.
type presentFactory struct {
	static.DummyTaskFactory
	present map[string]bool
}

func (f *presentFactory) NewDataEndpoint(name string) workflow.DataEndpoint {
	return &static.DummyDataEndpoint{EndpointName: name, Complete: f.present[name]}
}

func (f *presentFactory) ExecutableOf(instance workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	return static.DummyBuilder(0, 0).SetExecution(instance).SetConcrete(f.NewDataEndpoint(instance.WorkflowName()), f.Fn(instance.WorkflowName())).Instance().ToExecutable()
}

func (f *presentFactory) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	return static.DummyBuilder(0, 0).SetWorkflow(name, 0, false, f.Version, f.Component).SetRuntime(f.Partition, workflow.Scheduled, 0).SetConcrete(f.NewDataEndpoint(name), f.Fn(name)).Instance().ToExecutable()
}

// A task whose output already exists is complete, as in Luigi: it is not
// run, the host hears it was skipped, and what depends on it goes on.
func TestATaskWhoseTargetExistsIsSkipped(t *testing.T) {
	version := fmt.Sprintf("%d", time.Now().UnixNano())
	partition := "2026-10-02"
	factory := &presentFactory{DummyTaskFactory: static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT},
		present: map[string]bool{"A": true}}
	repo := static.NewWorkflowRepository()
	a := factory.NewExecutable("A")
	b := factory.NewExecutable("B")
	repo.Requires(b.WorkflowName(), a.WorkflowName(), version)

	rec := newRecorder()
	s := NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, factory, engine.NewLocalExecutor(), time.Millisecond)
	s.SetEvents(rec.ch)
	if err := s.Trigger(a.WorkflowInstanceId); err != nil {
		t.Fatal(err)
	}
	s.WaitForCompletion()
	if got := rec.joined(); got != "skipped A | started B | done B" {
		t.Fatalf("events = %q", got)
	}
	ra, _ := repo.Fetch(a.InstanceId())
	rb, _ := repo.Fetch(b.InstanceId())
	if !ra.StartDate.IsZero() || rb.StartDate.IsZero() || rb.EndDate.IsZero() {
		t.Fatalf("a skipped task has no start date, a run one has both: A=%v B=%v/%v", ra.StartDate, rb.StartDate, rb.EndDate)
	}
}
