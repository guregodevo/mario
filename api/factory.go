package api

import (
	"github.com/guregodevo/mario/workflow"
)

func GrpcExecutionOf(execution workflow.WorkflowExecution) *WorkflowExecution {
	grpcExecution := WorkflowExecution{
		// Populate fields based on execution
		ExecutionId: execution.ExecutionId,
		Id:          execution.DName,
		Retries:     execution.DRetries,
		MaxRetries:  execution.DMaxRetries,
		External:    execution.DExternal,
		StartDate:   execution.StartDate,
		EndDate:     execution.EndDate,
		Partition:   execution.Partition,
		Error:       execution.Error,
		Status:      int32(execution.Status),
		Parameters:  execution.DParameters,
		Version:     execution.Version(),
		Component:   execution.Component(),
	}
	return &grpcExecution
}

func FromGrpcExecution(e *WorkflowExecution) workflow.WorkflowExecution {

	return workflow.WorkflowExecution{
		// Populate fields based on execution
		ExecutionId: e.ExecutionId,
		StartDate:   e.StartDate,
		EndDate:     e.EndDate,
		Error:       e.Error,
		DRetries:    e.Retries,
		Status:      workflow.Status(e.Status),
		DParameters: e.Parameters,
		WorkflowInstanceId: workflow.WorkflowInstanceId{
			Partition: e.Partition,
			WorkflowId: workflow.WorkflowId{
				DVersion:    e.Version,
				DComponent:  e.Component,
				DName:       e.Id,
				DMaxRetries: e.MaxRetries,
				DExternal:   e.External,
			},
		},
	}
}
