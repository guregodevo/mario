package static

import (
	"fmt"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
	"os"
	"testing"
	"time"
)

func TestChannelQueue(t *testing.T) {
	// Setup: Prepare a test database file
	testDBPath := "./test.db"
	os.Remove(testDBPath) // Ensure old test file is deleted

	cq := NewChannelQueue(10)
	expectedVersion := fmt.Sprintf("%d", time.Now().Unix())

	// Enqueue a task
	expectedTask := workflow.WorkflowInstanceId{Partition: "2023-10-11", WorkflowId: workflow.WorkflowId{DVersion: expectedVersion, DName: "TestTask"}}
	if err := cq.Enqueue(expectedTask); err != nil {
		t.Fatalf("Failed to enqueue task: %v", err)
	}

	logger.Log.Info("Dequeueing", "component", "static_queue")
	receivedTask, _ := cq.Dequeue()

	if expectedTask.InstanceId() != receivedTask.InstanceId() {
		t.Errorf("Expected task %v, but got %v", expectedTask, receivedTask)
	}

	// Clean up
	cq.Close()
}
