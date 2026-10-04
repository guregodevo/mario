package sqlite

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/workflow"
)

// An execution that came through JSON (mario-state) carries times in a
// nameless fixed zone. Stored and read back, it is the same execution: the
// dates, and everything the Scan reads after them — status above all.
func TestAnExecutionFromTheWireReadsBackWhole(t *testing.T) {
	repo, err := OpenWorkflowRepositoryAt(filepath.Join(t.TempDir(), "runs.db"), static.BuilderDummyFn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	factory := &static.DummyTaskFactory{Version: "1", Partition: "2026-10-05", Component: "t"}
	exe := factory.NewExecutable("hello.steps.write").WorkflowExecution
	exe.StartDate = time.Date(2026, 10, 5, 1, 47, 33, 123456000, time.FixedZone("", 2*3600))
	exe.EndDate = exe.StartDate.Add(6 * time.Second)
	exe.Status = workflow.Done
	exe.DParameters = map[string]string{"k": "v"}
	// Through JSON and back, as the HTTP server receives it.
	b, _ := json.Marshal(exe)
	var wire workflow.WorkflowExecution
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if err := repo.Upsert(wire); err != nil {
		t.Fatal(err)
	}
	got, ok := repo.Fetch(exe.InstanceId())
	if !ok {
		t.Fatal("not found")
	}
	if got.Status != workflow.Done || !got.StartDate.Equal(exe.StartDate) || !got.EndDate.Equal(exe.EndDate) || got.DParameters["k"] != "v" {
		t.Fatalf("read back changed: status=%v start=%v end=%v params=%v", got.Status, got.StartDate, got.EndDate, got.DParameters)
	}
}
