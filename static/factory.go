package static

import (
	"context"
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
	"time"
)

type StaticWorflowBuilder struct {
	Inst *CommonExecutable
}

func (f *StaticWorflowBuilder) SetWorkflow(name string, maxRetries int32, external bool, version, component string) workflow.WorflowBuilder {
	f.Inst.WorkflowId = workflow.WorkflowId{
		DName:       name,
		DMaxRetries: maxRetries,
		DExternal:   external,
		DVersion:    version,
		DComponent:  component,
	}
	f.setExecutionId()
	return f
}

func (f *StaticWorflowBuilder) setExecutionId() workflow.WorflowBuilder {
	f.Inst.ExecutionId = fmt.Sprintf("%s-%d", f.Inst.InstanceId(), time.Now().UnixMilli())
	return f
}

func (f *StaticWorflowBuilder) SetExecution(execution workflow.WorkflowExecution) workflow.WorflowBuilder {
	f.Inst.WorkflowInstanceId = execution.WorkflowInstanceId
	f.Inst.StartDate = execution.StartDate
	f.Inst.EndDate = execution.EndDate
	f.Inst.DVersion = execution.DVersion
	f.Inst.DName = execution.DName
	f.Inst.DRetries = execution.DRetries
	f.Inst.Partition = execution.Partition
	f.Inst.SetStatus(execution.Status)
	f.Inst.DParameters = execution.DParameters
	f.Inst.ExecutionId = execution.ExecutionId
	f.Inst.Error = execution.Error
	return f
}

func (f *StaticWorflowBuilder) SetRuntime(partition string, status workflow.Status, retries int32) workflow.WorflowBuilder {
	f.Inst.Partition = partition
	f.Inst.SetStatus(status)
	f.Inst.DRetries = retries
	f.Inst.DParameters = make(map[string]string, 0)
	f.Inst.StartDate = time.Now().UTC()
	f.Inst.EndDate = time.Now().UTC()
	return f
}

func (f *StaticWorflowBuilder) OfInstance() workflow.WorkflowInstanceId {
	return f.Inst.WorkflowInstanceId
}

func (f *StaticWorflowBuilder) OfWorkflow() workflow.WorkflowId {
	return f.Inst.WorkflowId
}

func (f *StaticWorflowBuilder) Of() workflow.WorkflowExecution {
	return f.Inst.WorkflowExecution
}

func (f *StaticWorflowBuilder) Instance() workflow.WorkflowInstance {
	return f.Inst
}

func (f *StaticWorflowBuilder) SetConcrete(e workflow.DataEndpoint, fn func(ctx context.Context) error) workflow.WorflowBuilder {
	f.Inst.WfOutput = e
	f.Inst.RunFunc = fn
	return f
}

func (f *StaticWorflowBuilder) SetDefaultConcrete() workflow.WorflowBuilder {
	e := &DummyDataEndpoint{EndpointName: f.Inst.DName, EndDate: time.Now(), Failures: 0, ExpectedFailures: 0, Complete: false}
	f.Inst.WfOutput = e

	f.Inst.RunFunc = func(ctx context.Context) error {
		logger.Log.Info(fmt.Sprintf("Running %v", f.Inst.DName), "component", "workflow")
		return nil
	}
	return f
}
