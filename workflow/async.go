package workflow

type Queue interface {
	Enqueue(id WorkflowInstanceId) error
	Dequeue() (WorkflowInstanceId, error)
	Close() error
}
