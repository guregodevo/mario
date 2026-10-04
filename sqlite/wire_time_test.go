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
	// The text in the table is RFC 3339 UTC, whatever zone the time arrived
	// in: the one form the driver reads back on any machine. A time in any
	// other zone is written as its String() — "+0200 +0200" for the nameless
	// zone JSON gives on a UTC box, "+0200 CEST" on this laptop — and the
	// first of those was not read back (live 2026-10-05).
	var stored string
	if err := repo.db.QueryRow(`SELECT StartDate FROM workflow_executions WHERE Id = ?`, exe.InstanceId()).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, stored); err != nil {
		t.Fatalf("StartDate is stored as %q, want RFC 3339 UTC", stored)
	}
	got, ok := repo.Fetch(exe.InstanceId())
	if !ok {
		t.Fatal("not found")
	}
	if got.Status != workflow.Done || !got.StartDate.Equal(exe.StartDate) || !got.EndDate.Equal(exe.EndDate) || got.DParameters["k"] != "v" {
		t.Fatalf("read back changed: status=%v start=%v end=%v params=%v", got.Status, got.StartDate, got.EndDate, got.DParameters)
	}
}
