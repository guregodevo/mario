package scheduler

import (
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
	}

	// Require this in your NewLocalScheduler function or any initializer
	go func() {
		for err := range e.errCh {
			logger.Log.Error("local scheduler", "%v\n", err)
		}
	}()
	if loop {
		go e.loop()
		go e.retryLoop()
	}
	return e
}

func (e *LocalScheduler) DelegateExecution(t workflow.WorkflowInstance) error {
	return e.executor.Execute(t.ToExecutable())
}

func (e *LocalScheduler) Trigger(id workflow.WorkflowInstanceId) error {
	atomic.AddInt32(&e.counter, 1)
	logger.Log.Info("local scheduler", "SUBMIT %v\n", id)
	e.taskQueue.Enqueue(id)
	return nil
}

func (e *LocalScheduler) Accept(err error) {
	e.errCh <- err
}

func (e *LocalScheduler) loop() {
	logger.Log.Debugf("Polling Task queue ...")
	for {
		taskId, err := e.taskQueue.Dequeue()
		if err != nil {
			logger.Log.Error("local scheduler", "Error dequeuing task: %v", err)
			return
		} else {
			logger.Log.Info("local scheduler", "dequeuing task: %v", taskId)
		}

		e.Start(taskId)
	}
}

func (e *LocalScheduler) fetchOrCreate(name, partition string) workflow.ExecutableWorkflowInstance {
	id := workflow.InstanceIdOf(name, partition)
	exe, ok := e.repository.Fetch(id)
	var t workflow.ExecutableWorkflowInstance
	if !ok {
		t = e.taskFactory.NewExecutable(name)
		e.repository.Upsert(t.WorkflowExecution)
		logger.Log.Warn("local scheduler", "Creating task id %s name:%s partition:%s --> %s tPartition %s \n", id, name, partition, t.InstanceId(), t.Partition)
	} else {
		logger.Log.Warn("local scheduler", "Got task id %s name:%s partition:%s --> %s tPartition %s \n", id, name, partition, t.InstanceId(), t.Partition)
		t = e.taskFactory.ExecutableOf(exe)
	}
	return t
}

func (e *LocalScheduler) Start(taskId workflow.WorkflowInstanceId) {
	e.wg.Add(1)

	go func(id workflow.WorkflowInstanceId) {
		defer e.wg.Done()
		defer atomic.AddInt32(&e.counter, -1)
		logger.Log.Info("local scheduler", "Starting task %v \n", id)

		t := e.fetchOrCreate(id.WorkflowName(), id.Partition)
		logger.Log.Info("local scheduler", "Fetch task %s isExternal: %v\n", t.InstanceId(), t.IsExternal())

		if t.IsExternal() {
			logger.Log.Info("local scheduler", "Skipping task %s. Is External\n", t.InstanceId())
			return
		}

		if !e.backfill && t.GetStatus() == workflow.Done {
			logger.Log.Info("local scheduler", "Skipping task %s. Already done\n", t.InstanceId())
			return
		}

		if !e.backfill && t.GetStatus() == workflow.Started {
			logger.Log.Info("local scheduler", "Skipping task %s. Already started\n", t.InstanceId())
			return
		}

		// Check for dependencies
		logger.Log.Info("local scheduler", "Checking task '%s' dependencies\n", t.InstanceId())
		missingDeps := make([]workflow.WorkflowInstanceId, 0)
		for depName, _ := range e.repository.Upstreams(t.WorkflowName(), id.Version()) {
			logger.Log.Info("local scheduler", "Checking %s ...\n", workflow.InstanceIdOf(depName, id.Partition))

			dep := e.fetchOrCreate(depName, id.Partition)
			if dep.Status != workflow.Done {
				missingdep := fmt.Errorf(" %s Job Status %d", dep.Target().Name(), dep.GetStatus())
				if dep.Target().Exists() {
					logger.Log.Info("local scheduler", "Skipping %s ...\n", missingdep)
					dep.SetStatus(workflow.Done)
					e.repository.Upsert(dep.WorkflowExecution)
				} else {
					logger.Log.Error("local scheduler", "Missing %s ...\n", missingdep)
					missingDeps = append(missingDeps, dep.WorkflowInstanceId)
				}
			} else {
				logger.Log.Info("local scheduler", "Found %s ...\n", dep.InstanceId())
			}

		}
		if len(missingDeps) > 0 {
			//t.SetStatus(workflow.MissingDeps)
			//e.repository.Upsert(t)
			if e.backfill {
				for i := range missingDeps {
					e.Trigger(missingDeps[i])
				}
			}
			logger.Log.Error("local scheduler", "Missing %s version %s component %s deps: %v ...\n", t.InstanceId(), t.Version(), t.Component(), missingDeps)
			e.repository.Upsert(t.WorkflowExecution)
			return
		}
		t = e.fetchOrCreate(t.WorkflowName(), t.Partition)
		if t.GetStatus() != workflow.Started {
			t.SetStatus(workflow.Started)
			t.StartDate = time.Now()
			e.repository.Upsert(t.WorkflowExecution)
			logger.Log.Info("local scheduler", "Started task %s\n", t.InstanceId())

			if err := e.executor.Execute(t); err != nil {
				t.EndDate = time.Now()
				t.SetStatus(workflow.Failed)
				t.SetError(err)
				e.repository.Upsert(t.WorkflowExecution)
				e.Accept(fmt.Errorf("Task failed %s: %v", t.InstanceId(), err))
				e.retry(t.WorkflowInstanceId)
			} else {
				t.EndDate = time.Now()
				t.SetStatus(workflow.Done)
				e.repository.Upsert(t.WorkflowExecution)
				logger.Log.Info("local scheduler", "Completed %s\n", t.InstanceId())
				e.notifyDownstreams(t.WorkflowName(), t.Partition, t.Version())
			}
		} else {
			logger.Log.Info("local scheduler", "Skipped task %s. Already Started \n", t.InstanceId())
		}
	}(taskId)
}

func (e *LocalScheduler) retry(t workflow.WorkflowInstanceId) {
	atomic.AddInt32(&e.counter, 1)
	logger.Log.Info("local scheduler", "Retrying %s\n", t.InstanceId())
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

func (e *LocalScheduler) handleRetry(rt workflow.ExecutableWorkflowInstance) {
	defer atomic.AddInt32(&e.counter, -1)
	<-time.After(e.retryTime)
	rt.IncRetry()
	e.repository.Upsert(rt.WorkflowExecution)
	logger.Log.Error("local scheduler", "Retrying task %s  (retries: %d) \n", rt.Target().Name(), rt.Retries())
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
				logger.Log.Info("local scheduler", "Error dequeuing retry task: %v", err)
				continue
			}
			rTaskExe := e.fetchOrCreate(rTaskId.WorkflowName(), rTaskId.Partition)

			if rTaskExe.Retries() < rTaskExe.MaxRetries() {
				go e.handleRetry(rTaskExe)
			} else {
				atomic.AddInt32(&e.counter, -1)
				maxError := fmt.Errorf("Max retries (%d/%d) reached for task %s", rTaskExe.Retries(), rTaskExe.MaxRetries(), rTaskExe.Target().Name())
				logger.Log.Warn("local scheduler", "Max retries %v ", maxError)
			}
		}
	}
}

func (e *LocalScheduler) WaitForCompletion() error {
	for atomic.LoadInt32(&e.counter) > 0 {
		logger.Log.Info("local scheduler", "%d tasks running ", e.counter)
		time.Sleep(3 * time.Second) // Sleep for 100 milliseconds
	}
	logger.Log.Info("local scheduler", "%d tasks running ", e.counter)

	e.wg.Wait()

	logger.Log.Info("local scheduler", "Preparing to close task channel")
	e.taskQueue.Close()
	logger.Log.Info("local scheduler", "Waiting for all tasks to finish execution")

	e.retryQueue.Close() // Close the retry channel.
	close(e.errCh)       // Close the error channel.

	logger.Log.Info("local scheduler", "All tasks have finished execution")
	close(e.shutdownCh) // Signal to retryLoop to stop processing.

	select {
	case err := <-e.errCh:
		return err
	default:
		return nil
	}
}
