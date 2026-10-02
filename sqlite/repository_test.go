package sqlite

import (
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/workflow"
	"os"
	"testing"
	"time"
)

func TestSQLiteWorkflowRepository(t *testing.T) {
	// Prepare SQLite repository
	// The testcases expect a repository that starts empty. It no longer
	// empties itself (a persistence layer must not), so the test clears
	// what an earlier run left — the same thing a host does when it wants
	// to begin from nothing.
	removeDB("data_test")
	repo := NewWorkflowRepository("data_test", static.BuilderDummyFn)
	defer func() {
		repo.Close()
		removeDB("data_test")
	}()

	static.RepositoryTestcases(t, repo)
}

// removeDB deletes what a repository wrote, so a test leaves nothing behind.
// Close no longer does it: a repository a host chose for persistence must
// not delete the record when it lets go of it.
func removeDB(name string) {
	dbfile := get_db_file(name)
	for _, f := range []string{dbfile, dbfile + "-shm", dbfile + "-wal"} {
		os.Remove(f)
	}
}

// A run a host wrote must still be there when it comes back: that is the
// only reason to choose this repository over the in-memory one. The test
// opens it, records an execution, closes it, opens it again and reads the
// execution back — the restart, in miniature.
func TestSQLiteWorkflowRepositorySurvivesAReopen(t *testing.T) {
	name := "data_reopen_test"
	removeDB(name)
	defer removeDB(name)

	execution := workflow.WorkflowExecution{
		ExecutionId: "release-2026-10-03T090000",
		WorkflowInstanceId: workflow.WorkflowInstanceId{
			WorkflowId: workflow.WorkflowId{
				DName:       "release",
				DVersion:    "release@2026-10-03T090000",
				DComponent:  "memdoor",
				DMaxRetries: 3,
			},
			Partition: "2026-10-03T090000",
		},
		StartDate: time.Now(),
		Status:    workflow.Status(0),
	}

	first := NewWorkflowRepository(name, static.BuilderDummyFn)
	if err := first.Upsert(execution); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	first.Close()

	second := NewWorkflowRepository(name, static.BuilderDummyFn)
	defer second.Close()

	got, found := second.Fetch(execution.InstanceId())
	if !found {
		t.Fatalf("after reopening, execution %q is gone — a run would not survive a restart", execution.InstanceId())
	}
	if got.DName != "release" {
		t.Fatalf("reopened execution has name %q, want %q", got.DName, "release")
	}

	// ExecutionsByName is what a run table is listed from.
	byName := second.ExecutionsByName("release", 10)
	if len(byName) == 0 {
		t.Fatalf("ExecutionsByName(%q) found nothing after a reopen", "release")
	}
}
