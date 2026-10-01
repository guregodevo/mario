package workflow

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type Status int

const (
	//Missing Dependencies
	MissingDeps Status = iota
	//Scheduled
	Scheduled
	//Submitted by executor
	Submitted
	//Has failed
	Failed
	//Started
	Started
	//Done
	Done
)

func (s Status) String() string {
	return [...]string{"MissingDeps", "Scheduled", "Submitted", "Failed", "Started", "Done"}[s]
}

type WorkflowId struct {
	DName       string `json:"name"`
	DVersion    string `json:"version"`
	DComponent  string `json:"component"`
	DMaxRetries int32  `json:"max_retries"`
	DExternal   bool   `json:"external"`
}

type WorkflowInstanceId struct {
	WorkflowId
	Partition string `json:"partition"`
}

func (w *WorkflowInstanceId) InstanceId() string {
	return InstanceIdOf(w.DName, w.Partition)
}

func InstanceIdOf(name, partition string) string {
	return fmt.Sprintf("%s-%s", name, partition)
}

type ExecutableWorkflowInstance struct {
	WorkflowExecution
	WfOutput DataEndpoint
	RunFunc  func() error
	Mu       sync.Mutex
}

func (w *ExecutableWorkflowInstance) CreationDate() time.Time {
	return w.StartDate
}

func (w *ExecutableWorkflowInstance) GetPartition() string {
	return w.Partition
}

func (w *WorkflowId) IsExternal() bool {
	return w.DExternal
}

// WorkflowExecution is a value object containing all the data about a specific execution of a workflow instance.
// Being a value object fits well with its role to represent a historical record that should not be changed once created.
type WorkflowExecution struct {
	ExecutionId string
	WorkflowInstanceId
	DRetries    int32
	StartDate   time.Time
	EndDate     time.Time
	Status      Status
	Error       string
	DParameters map[string]string
}

func (w *WorkflowId) Version() string {
	return w.DVersion
}

func (w *WorkflowId) WorkflowName() string {
	return w.DName
}

func (w *ExecutableWorkflowInstance) IncRetry() {
	atomic.AddInt32(&w.DRetries, 1)
}

func (w *ExecutableWorkflowInstance) Retries() int32 {
	return w.DRetries
}

func (w *ExecutableWorkflowInstance) DecRetry() {
	atomic.AddInt32(&w.DRetries, -1)
}

func (w *WorkflowId) MaxRetries() int32 {
	return w.DMaxRetries
}

func (w *WorkflowId) String() string {
	return w.DName
}

func (w *WorkflowId) Component() string {
	return w.DComponent
}

func (w *ExecutableWorkflowInstance) String() string {
	w.Mu.Lock()
	defer w.Mu.Unlock()

	//depNames := make([]string, len(w.Deps))
	//for i, dep := range w.Deps {
	//	depNames[i] = dep.Fetch().Target().Name()
	//}

	return fmt.Sprintf(
		"[LOG WORKFLOW] Workflow{Name: %s, Status: %v \n",
		w.DName,
		w.Status,
	)
}

func (t *ExecutableWorkflowInstance) GetStatus() Status {
	t.Mu.Lock()
	defer t.Mu.Unlock()
	return t.Status
}

func (t *ExecutableWorkflowInstance) SetStatus(s Status) {
	t.Mu.Lock()
	t.Status = s
	t.Mu.Unlock()
}

func (t *ExecutableWorkflowInstance) GetError() string {
	return t.Error
}

func (t *ExecutableWorkflowInstance) SetError(err error) {
	if err == nil {
		return
	}
	t.Mu.Lock()
	t.Error = err.Error()
	t.Mu.Unlock()
}

func (t *ExecutableWorkflowInstance) Target() DataEndpoint {
	return t.WfOutput
}

func (t *ExecutableWorkflowInstance) Parameters() map[string]string {
	return t.DParameters
}

// Workflow Describes the generic characteristics and behaviors of a workflow.
type Workflow interface {
	WorkflowName() string
	String() string
	Version() string
	Component() string
	//Maximum of retries configured
	MaxRetries() int32
	//Workflow is External it cannot be executed. The owner is the only user who has permissions to run it.
	IsExternal() bool
}

// WorkflowRepository Manages workflow lineage and keeps track of execution history. The repository is also responsible for creating new workflow executions.
type WorkflowRepository interface {
	Requires(e string, required string, version string)
	RevertRequires(e string, required string, version string)
	RequiredBy(e string, required string, version string)
	Upstreams(id string, version string) map[string]bool
	Downstreams(id string, version string) map[string]bool
	DeepUpstreams(id string, version string) map[string]bool
	DeepDownstreams(id string, version string) map[string]bool
	Fetch(id string) (WorkflowExecution, bool)
	Upsert(instance WorkflowExecution) error
	Executions(id string) map[string]WorkflowExecution
	ExecutionsByName(name string, limit int) []WorkflowExecution
	Close()
}

// WorkflowInstance Represents a specific instance of a workflow for a particular partition.
// This carries over properties from the general Workflow interface but adds instance-specific methods and properties.
type WorkflowInstance interface {
	Workflow
	InstanceId() string
	GetPartition() string
	Parameters() map[string]string
	IncRetry()
	DecRetry()
	GetStatus() Status
	SetStatus(Status)
	CreationDate() time.Time
	Target() DataEndpoint
	String() string
	Retries() int32
	ToExecutable() *ExecutableWorkflowInstance
}

type DataEndpoint interface {
	Name() string
	Exists() bool
}

type WorflowBuilder interface {
	SetExecution(WorkflowExecution) WorflowBuilder
	SetWorkflow(name string, maxRetries int32, external bool, version, component string) WorflowBuilder
	SetRuntime(partition string, status Status, retries int32) WorflowBuilder
	SetDefaultConcrete() WorflowBuilder
	SetConcrete(
		WfOutput DataEndpoint,
		RunFunc func() error) WorflowBuilder
	Of() WorkflowExecution
	OfInstance() WorkflowInstanceId
	OfWorkflow() WorkflowId
	Instance() WorkflowInstance
}

type GetBuilderFunc func() WorflowBuilder
