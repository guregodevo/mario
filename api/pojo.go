package api

import (
	"time"
)

type WorkflowInstance struct {
	Id         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Partition  string            `json:"partition,omitempty"`
	StartDate  time.Time         `json:"start_date,omitempty"`
	EndDate    time.Time         `json:"end_date,omitempty"`
	Status     int32             `json:"status,omitempty"`
	Error      string            `json:"error,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty"`
	Retries    int32             `json:"retries,omitempty"`
	MaxRetries int32             `json:"max_retries,omitempty"`
	External   bool              `json:"external,omitempty"`
	Version    string            `json:"version,omitempty"`
	Component  string            `json:"component,omitempty"`
}

type WorkflowExecution struct {
	ExecutionId string            `json:"execution_id,omitempty"`
	Id          string            `json:"id,omitempty"`
	Partition   string            `json:"partition,omitempty"`
	StartDate   time.Time         `json:"start_date,omitempty"`
	EndDate     time.Time         `json:"end_date,omitempty"`
	Status      int32             `json:"status,omitempty"`
	Error       string            `json:"error,omitempty"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	Retries     int32             `json:"retries,omitempty"`
	Version     string            `json:"version,omitempty"`
	Component   string            `json:"component,omitempty"`
	MaxRetries  int32             `json:"max_retries,omitempty"`
	External    bool              `json:"external,omitempty"`
}

type ExecutionsResponse struct {
	Executions []*WorkflowExecution `json:"executions,omitempty"`
}

type WorkflowExecutionRequest struct {
	WorkflowId string             `json:"workflow_id,omitempty"`
	Executions *WorkflowExecution `json:"executions,omitempty"`
}
