package scheduler

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
)

type Scheduler interface {
	Trigger(task workflow.Workflow) error
	Start(task workflow.Workflow)
	Accept(e error)
	DelegateExecution(task workflow.WorkflowInstance) error
}

type LocalScheduler struct {
	backfill    bool
	repository  workflow.WorkflowRepository
	taskFactory factory.TaskFactory
	counter     int32
	taskQueue   workflow.Queue
	errCh       chan error
	retryQueue  workflow.Queue
	shutdownCh  chan struct{}
	wg          sync.WaitGroup
	executor    engine.Executor
	retryTime   time.Duration
	createMu    sync.Mutex
	runningMu   sync.Mutex
	running     map[string]bool
	completed   map[string]bool
	ctx         context.Context // the run's context: cancelled, nothing more starts
	events      chan<- Event    // nil when nobody listens; see Event
}

// Event is one transition a task makes, sent in the order it happened on
// the channel a host installs with SetEvents. The engine owns the channel's
// end: it closes it when the run is complete. A host that keeps its own
// listener interface reads the channel and calls it; the engine knows no
// host.
type Event struct {
	Kind      EventKind
	Name      string // the workflow's name
	Partition string
	Err       error // Failed: why
	WillRetry bool  // Failed: whether the engine will try again
	At        time.Time
}

type EventKind string

const (
	Started EventKind = "started"
	Done    EventKind = "done"
	Failed  EventKind = "failed"
	// Skipped: the task's target was already there, so it was marked done
	// without running (a re-run resumes where the last one stopped).
	Skipped EventKind = "skipped"
)

// SetEvents installs the channel the run's events are sent on, in order.
// The engine closes it when WaitForCompletion is done; a nil channel means
// nobody listens. Give it room (a few dozen) — a send blocks the scheduler
// until the host reads.
func (e *LocalScheduler) SetEvents(ch chan<- Event) { e.events = ch }

func (e *LocalScheduler) emit(kind EventKind, name, partition string, err error, willRetry bool) {
	if e.events == nil {
		return
	}
	e.events <- Event{Kind: kind, Name: name, Partition: partition, Err: err, WillRetry: willRetry, At: time.Now()}
}

// SetContext makes a run cancellable: once ctx is done no further task
// starts, and the executor sees the same ctx for the task it is running.
func (e *LocalScheduler) SetContext(ctx context.Context) {
	if ctx != nil {
		e.ctx = ctx
	}
}

func NewLocalScheduler(backfill, loop bool, taskQueue workflow.Queue, retryQueue workflow.Queue, repository workflow.WorkflowRepository, taskFactory factory.TaskFactory, executor engine.Executor, retryTime time.Duration) *LocalScheduler {
	e := &LocalScheduler{
		backfill:    backfill,
		repository:  repository,
		taskFactory: taskFactory,
		taskQueue:   taskQueue,
		errCh:       make(chan error),
		retryQueue:  retryQueue,
		shutdownCh:  make(chan struct{}),
		executor:    executor,
		retryTime:   retryTime,
		running:     make(map[string]bool),
		completed:   make(map[string]bool),
		ctx:         context.Background(),
	}

	// Require this in your NewLocalScheduler function or any initializer
	go func() {
		for err := range e.errCh {
			logger.Log.Error(fmt.Sprintf("%v", err), "component", "local scheduler")
		}
	}()
	if loop {
		go e.loop()
		go e.retryLoop()
	}
	return e
}

func (e *LocalScheduler) DelegateExecution(t workflow.WorkflowInstance) error {
	return e.executor.Execute(e.ctx, t.ToExecutable())
}

func (e *LocalScheduler) Trigger(id workflow.WorkflowInstanceId) error {
	atomic.AddInt32(&e.counter, 1)
	logger.Log.Info(fmt.Sprintf("SUBMIT %v", id), "component", "local scheduler")
	e.taskQueue.Enqueue(id)
	return nil
}

func (e *LocalScheduler) Accept(err error) {
	e.errCh <- err
}

func (e *LocalScheduler) loop() {
	logger.Log.Debug("Polling Task queue ...")
	for {
		taskId, err := e.taskQueue.Dequeue()
		if err != nil {
			logger.Log.Error(fmt.Sprintf("Error dequeuing task: %v", err), "component", "local scheduler")
			return
		} else {
			logger.Log.Info(fmt.Sprintf("dequeuing task: %v", taskId), "component", "local scheduler")
		}

		e.Start(taskId)
	}
}

func (e *LocalScheduler) fetchOrCreate(name, partition string) *workflow.ExecutableWorkflowInstance {
	id := workflow.InstanceIdOf(name, partition)
	// Fetch and create must be atomic, otherwise concurrent callers create duplicate executions of the same instance.
	e.createMu.Lock()
	defer e.createMu.Unlock()
	exe, ok := e.repository.Fetch(id)
	var t *workflow.ExecutableWorkflowInstance
	if !ok {
		t = e.taskFactory.NewExecutable(name)
		e.repository.Upsert(t.WorkflowExecution)
		logger.Log.Warn(fmt.Sprintf("Creating task id %s name:%s partition:%s --> %s tPartition %s", id, name, partition, t.InstanceId(), t.Partition), "component", "local scheduler")
	} else {
		t = e.taskFactory.ExecutableOf(exe)
		logger.Log.Warn(fmt.Sprintf("Got task id %s name:%s partition:%s --> %s tPartition %s", id, name, partition, t.InstanceId(), t.Partition), "component", "local scheduler")
	}
	return t
}

// claim marks the instance as running in this scheduler. It returns false if it is already running or has already completed in this scheduler.
func (e *LocalScheduler) claim(instanceId string) bool {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()
	if e.running[instanceId] || e.completed[instanceId] {
		return false
	}
	e.running[instanceId] = true
	return true
}

func (e *LocalScheduler) release(instanceId string) {
	e.runningMu.Lock()
	delete(e.running, instanceId)
	e.runningMu.Unlock()
}

// complete releases the instance and records that it ran successfully in this scheduler, so that a backfill runs it only once.
func (e *LocalScheduler) complete(instanceId string) {
	e.runningMu.Lock()
	delete(e.running, instanceId)
	e.completed[instanceId] = true
	e.runningMu.Unlock()
}

func (e *LocalScheduler) isRunning(instanceId string) bool {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()
	return e.running[instanceId]
}

func (e *LocalScheduler) isCompleted(instanceId string) bool {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()
	return e.completed[instanceId]
}

func (e *LocalScheduler) Start(taskId workflow.WorkflowInstanceId) {
	e.wg.Add(1)

	go func(id workflow.WorkflowInstanceId) {
		defer e.wg.Done()
		defer atomic.AddInt32(&e.counter, -1)
		logger.Log.Info(fmt.Sprintf("Starting task %v", id), "component", "local scheduler")

		t := e.fetchOrCreate(id.WorkflowName(), id.Partition)
		logger.Log.Info(fmt.Sprintf("Fetch task %s isExternal: %v", t.InstanceId(), t.IsExternal()), "component", "local scheduler")

		if t.IsExternal() {
			logger.Log.Info(fmt.Sprintf("Skipping task %s. Is External", t.InstanceId()), "component", "local scheduler")
			return
		}

		if !e.backfill && t.GetStatus() == workflow.Done {
			logger.Log.Info(fmt.Sprintf("Skipping task %s. Already done", t.InstanceId()), "component", "local scheduler")
			return
		}

		if e.isCompleted(t.InstanceId()) {
			logger.Log.Info(fmt.Sprintf("Skipping task %s. Already completed in this run", t.InstanceId()), "component", "local scheduler")
			return
		}

		// When backfilling, a task left Started by a previous run is restarted, unless it is running in this scheduler.
		if (!e.backfill && t.GetStatus() == workflow.Started) || e.isRunning(t.InstanceId()) {
			logger.Log.Info(fmt.Sprintf("Skipping task %s. Already started", t.InstanceId()), "component", "local scheduler")
			return
		}

		// Check for dependencies
		logger.Log.Info(fmt.Sprintf("Checking task '%s' dependencies", t.InstanceId()), "component", "local scheduler")
		missingDeps := make([]workflow.WorkflowInstanceId, 0)
		for depName, _ := range e.repository.Upstreams(t.WorkflowName(), id.Version()) {
			logger.Log.Info(fmt.Sprintf("Checking %s ...", workflow.InstanceIdOf(depName, id.Partition)), "component", "local scheduler")

			dep := e.fetchOrCreate(depName, id.Partition)
			if dep.Status != workflow.Done {
				missingdep := fmt.Errorf(" %s Job Status %d", dep.Target().Name(), dep.GetStatus())
				// An EXTERNAL requirement is settled here by its target: nobody
				// runs it. A requirement of this DAG is not — it is triggered, and
				// settles itself after ITS requirements (its own Start skips it
				// when its target is there), so the graph resolves root to sink.
				if dep.IsExternal() && dep.Target().Exists() {
					logger.Log.Info(fmt.Sprintf("Skipping %s ...", missingdep), "component", "local scheduler")
					dep.SetStatus(workflow.Done)
					e.repository.Upsert(dep.WorkflowExecution)
					e.emit(Skipped, dep.WorkflowName(), dep.Partition, nil, false)
				} else {
					logger.Log.Error(fmt.Sprintf("Missing %s ...", missingdep), "component", "local scheduler")
					missingDeps = append(missingDeps, dep.WorkflowInstanceId)
				}
			} else {
				logger.Log.Info(fmt.Sprintf("Found %s ...", dep.InstanceId()), "component", "local scheduler")
			}

		}
		if len(missingDeps) > 0 {
			//t.SetStatus(workflow.MissingDeps)
			//e.repository.Upsert(t)
			if e.backfill {
				for i := range missingDeps {
					// A dependency already running here notifies its downstreams when it completes.
					if !e.isRunning(missingDeps[i].InstanceId()) {
						e.Trigger(missingDeps[i])
					}
				}
			}
			logger.Log.Error(fmt.Sprintf("Missing %s version %s component %s deps: %v ...", t.InstanceId(), t.Version(), t.Component(), missingDeps), "component", "local scheduler")
			return
		}

		instanceId := t.InstanceId()
		if !e.claim(instanceId) {
			logger.Log.Info(fmt.Sprintf("Skipped task %s. Already running or completed", instanceId), "component", "local scheduler")
			return
		}
		// The status may have changed since the first fetch.
		t = e.fetchOrCreate(t.WorkflowName(), t.Partition)
		if !e.backfill && (t.GetStatus() == workflow.Started || t.GetStatus() == workflow.Done) {
			e.release(instanceId)
			logger.Log.Info(fmt.Sprintf("Skipped task %s. Already %v", instanceId, t.GetStatus()), "component", "local scheduler")
			return
		}

		// Its requirements are all done (above): now, a task whose output is
		// already there is complete, as in Luigi — not run again, and what
		// depends on it may go. Checked HERE, after the requirements, so a
		// DAG resolves root to sink: a downstream never settles before what
		// it requires has.
		// Done before (the same partition triggered again: a rerun, a
		// resume) counts too: the target is the proof either way. Skipping
		// only what was not yet Done re-ran a finished sink on every
		// trigger (a Memdoor rerun, 2026-10-04: a one-step workflow rewrote
		// its file on each run of the same partition).
		if t.Target().Exists() {
			if t.GetStatus() != workflow.Done {
				t.SetStatus(workflow.Done)
				e.repository.Upsert(t.WorkflowExecution)
			}
			e.complete(instanceId)
			logger.Log.Info(fmt.Sprintf("Skipping task %s. Its target exists", instanceId), "component", "local scheduler")
			e.emit(Skipped, t.WorkflowName(), t.Partition, nil, false)
			e.notifyDownstreams(t.WorkflowName(), t.Partition, t.Version())
			return
		}

		if err := e.ctx.Err(); err != nil {
			e.release(instanceId)
			logger.Log.Info(fmt.Sprintf("Not starting %s: run cancelled (%v)", instanceId, err), "component", "local scheduler")
			return
		}
		t.SetStatus(workflow.Started)
		t.StartDate = time.Now()
		e.repository.Upsert(t.WorkflowExecution)
		logger.Log.Info(fmt.Sprintf("Started task %s", instanceId), "component", "local scheduler")
		e.emit(Started, t.WorkflowName(), t.Partition, nil, false)

		err := e.executor.Execute(e.ctx, t)
		t.EndDate = time.Now()
		if err != nil {
			t.SetStatus(workflow.Failed)
			t.SetError(err)
			e.repository.Upsert(t.WorkflowExecution)
			e.release(instanceId)
			e.emit(Failed, t.WorkflowName(), t.Partition, err, t.Retries() < t.MaxRetries() && e.ctx.Err() == nil)
			e.Accept(fmt.Errorf("Task failed %s: %v", instanceId, err))
			e.retry(t.WorkflowInstanceId)
		} else {
			t.SetStatus(workflow.Done)
			e.repository.Upsert(t.WorkflowExecution)
			e.complete(instanceId)
			logger.Log.Info(fmt.Sprintf("Completed %s", instanceId), "component", "local scheduler")
			e.emit(Done, t.WorkflowName(), t.Partition, nil, false)
			e.notifyDownstreams(t.WorkflowName(), t.Partition, t.Version())
		}
	}(taskId)
}

func (e *LocalScheduler) retry(t workflow.WorkflowInstanceId) {
	atomic.AddInt32(&e.counter, 1)
	logger.Log.Info(fmt.Sprintf("Retrying %s", t.InstanceId()), "component", "local scheduler")
	e.retryQueue.Enqueue(t)
}

// Require this after a task is completed successfully
func (e *LocalScheduler) notifyDownstreams(taskName, partition, version string) {
	for parentTaskName, _ := range e.repository.Downstreams(taskName, version) {
		parentTask := e.fetchOrCreate(parentTaskName, partition)
		e.triggerIfRequired(parentTask.WorkflowExecution)

	}
}

func (e *LocalScheduler) triggerIfRequired(task workflow.WorkflowExecution) {
	if e.isCompleted(task.InstanceId()) {
		return
	}
	if (e.backfill || task.Status != workflow.Done) && e.allDependenciesCompleted(task.WorkflowName(), task.Partition, task.Version()) {
		e.Trigger(task.WorkflowInstanceId)
	}
}

// Require this for checking whether all dependencies have completed
func (e *LocalScheduler) allDependenciesCompleted(taskName, partition, version string) bool {
	for depId, _ := range e.repository.Upstreams(taskName, version) {
		dep := e.fetchOrCreate(depId, partition)
		if dep.Status != workflow.Done {
			return false
		}
	}
	return true
}

func (e *LocalScheduler) handleRetry(rt *workflow.ExecutableWorkflowInstance) {
	defer atomic.AddInt32(&e.counter, -1)
	<-time.After(e.retryTime)
	rt.IncRetry()
	e.repository.Upsert(rt.WorkflowExecution)
	logger.Log.Error(fmt.Sprintf("Retrying task %s  (retries: %d)", rt.Target().Name(), rt.Retries()), "component", "local scheduler")
	e.triggerIfRequired(rt.WorkflowExecution)
}

func (e *LocalScheduler) retryLoop() {
	for {
		select {
		case <-e.shutdownCh:
			return
		default:
			rTaskId, err := e.retryQueue.Dequeue() // dequeue from retryQueue
			if err != nil {
				logger.Log.Info(fmt.Sprintf("Error dequeuing retry task: %v", err), "component", "local scheduler")
				continue
			}
			rTaskExe := e.fetchOrCreate(rTaskId.WorkflowName(), rTaskId.Partition)

			if rTaskExe.Retries() < rTaskExe.MaxRetries() {
				go e.handleRetry(rTaskExe)
			} else {
				atomic.AddInt32(&e.counter, -1)
				maxError := fmt.Errorf("Max retries (%d/%d) reached for task %s", rTaskExe.Retries(), rTaskExe.MaxRetries(), rTaskExe.Target().Name())
				logger.Log.Warn(fmt.Sprintf("Max retries %v ", maxError), "component", "local scheduler")
			}
		}
	}
}

func (e *LocalScheduler) WaitForCompletion() error {
	// Say the count when it changes, not ten times a second: a host that
	// keeps the engine's log (Memdoor does) got 2,500 identical lines from
	// one four-minute task.
	last := int32(-1)
	for n := atomic.LoadInt32(&e.counter); n > 0; n = atomic.LoadInt32(&e.counter) {
		if n != last {
			logger.Log.Info(fmt.Sprintf("%d tasks running ", n), "component", "local scheduler")
			last = n
		}
		time.Sleep(100 * time.Millisecond)
	}
	logger.Log.Info(fmt.Sprintf("%d tasks running ", atomic.LoadInt32(&e.counter)), "component", "local scheduler")

	e.wg.Wait()

	logger.Log.Info("Preparing to close task channel", "component", "local scheduler")
	e.taskQueue.Close()
	logger.Log.Info("Waiting for all tasks to finish execution", "component", "local scheduler")

	e.retryQueue.Close() // Close the retry channel.
	close(e.errCh)       // Close the error channel.

	logger.Log.Info("All tasks have finished execution", "component", "local scheduler")
	close(e.shutdownCh) // Signal to retryLoop to stop processing.
	if e.events != nil {
		close(e.events) // the run is complete: the host's reader ends
		e.events = nil
	}

	select {
	case err := <-e.errCh:
		return err
	default:
		return nil
	}
}
