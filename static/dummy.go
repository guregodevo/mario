package static

import (
	"context"
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
	"time"
)

func BuilderDummyFn() workflow.WorflowBuilder {
	return DummyBuilder(0, 0*time.Hour)
}

type DummyDataEndpoint struct {
	EndpointName     string
	EndDate          time.Time
	ExpectedFailures int
	Failures         int
	Complete         bool
}

func (t *DummyDataEndpoint) Name() string {
	return t.EndpointName
}

func (t *DummyDataEndpoint) Exists() bool {
	return t.Complete
}

type DummyTaskFactory struct {
	Version   string
	Partition string
	Component string
}

func (f *DummyTaskFactory) Name() []string {
	return []string{"dummy"}
}

func (f *DummyTaskFactory) Fn(name string) func() error {
	return func() error {
		logger.Log.Info(fmt.Sprintf("Running %v", f.Name()), "component", "workflow")
		return nil
	}
}

func (f *DummyTaskFactory) NewYaml() *templates.YAMLValidator {
	return nil
}

func (f *DummyTaskFactory) NewDataEndpoint(name string) workflow.DataEndpoint {
	return &DummyDataEndpoint{EndpointName: name, EndDate: time.Now(), Failures: 0, ExpectedFailures: 0, Complete: false}
}

func (f *DummyTaskFactory) NewWorkflow(name, version, partition, component string) (workflow.WorkflowInstanceId, error) {
	return DummyBuilder(0, 0*time.Millisecond).SetWorkflow(name, 0, false, version, component).OfInstance(), nil
}

func (f *DummyTaskFactory) ExecutableOf(instance workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	return DummyBuilder(0, 0*time.Millisecond).SetExecution(instance).SetDefaultConcrete().Instance().ToExecutable()
}

func (f *DummyTaskFactory) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	return DummyBuilder(0, 0*time.Millisecond).SetWorkflow(name, 0, false, f.Version, f.Component).SetRuntime(f.Partition, workflow.Scheduled, 0).SetDefaultConcrete().Instance().ToExecutable()
}

// DummyWorflowFactory creates and returns a pointer to a StaticWorkflow object with the specified properties.
// The function takes the following parameters:
// - nbFailures: The number of expected failures before the workflow completes successfully.
// - sleepDuration: The time duration for which the workflow will sleep during each run.
//
// The returned StaticWorkflow object has a RunFunc that simulates the running of the workflow.
// This RunFunc will sleep for the specified duration, then increment the failure count,
// and finally check if the number of failures has reached or exceeded the expected count.
// If it has, RunFunc will return nil, indicating success; otherwise, it will return a "fake error".
type DummyWorflowFactory struct {
	StaticWorflowBuilder
	nbFailures    int
	sleepDuration time.Duration
}

func DummyBuilder(nbFailures int, sleepDuration time.Duration) workflow.WorflowBuilder {
	return &DummyWorflowFactory{
		StaticWorflowBuilder: StaticWorflowBuilder{&CommonExecutable{}},
		nbFailures:           nbFailures,
		sleepDuration:        sleepDuration,
	}
}

func (f *DummyWorflowFactory) SetConcrete(wf workflow.DataEndpoint, fn func(ctx context.Context) error) workflow.WorflowBuilder {
	f.Inst.WfOutput = wf
	f.Inst.RunFunc = fn
	return f
}

func (f *DummyWorflowFactory) SetDefaultConcrete() workflow.WorflowBuilder {
	e := &DummyDataEndpoint{EndpointName: f.Inst.DName, EndDate: time.Now(), Failures: 0, ExpectedFailures: f.nbFailures, Complete: false}
	f.Inst.WfOutput = e
	f.Inst.RunFunc = func(ctx context.Context) error {
		select {
		case <-time.After(f.sleepDuration):
		case <-ctx.Done():
			return ctx.Err()
		}
		logger.Log.Info(fmt.Sprintf("Running %v", f.Inst.DName), "component", "workflow")
		e.Failures++
		e.Complete = e.ExpectedFailures <= e.Failures
		if !e.Complete {
			logger.Log.Info(fmt.Sprintf("fake error %v", f.Inst.DName), "component", "workflow")
			return fmt.Errorf("fake error")
		} else {
			return nil
		}
	}
	return f
}
