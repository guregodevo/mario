package static

import (
	"errors"
	"github.com/guregodevo/mario/workflow"
)

type ChannelQueue struct {
	taskCh chan workflow.WorkflowInstanceId
}

func NewChannelQueue(size int) *ChannelQueue {
	return &ChannelQueue{
		taskCh: make(chan workflow.WorkflowInstanceId, size),
	}
}

func (q *ChannelQueue) Enqueue(task workflow.WorkflowInstanceId) error {
	q.taskCh <- task
	return nil
}

func (q *ChannelQueue) Dequeue() (workflow.WorkflowInstanceId, error) {
	task, ok := <-q.taskCh
	if !ok {
		return workflow.WorkflowInstanceId{}, errors.New("channel closed")
	}
	return task, nil
}

func (q *ChannelQueue) Close() error {
	close(q.taskCh)
	return nil
}
