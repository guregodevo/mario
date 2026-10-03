package sqlite

import (
	"fmt"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/workflow"
	"os"
	"path/filepath"
	"sync"
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

// A host that keeps its state of its choosing names the file itself: the
// database lands exactly where it asked, not in the working directory.
func TestSQLiteWorkflowRepositoryAtHonoursThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.db")

	repo := NewWorkflowRepositoryAt(path, static.BuilderDummyFn)
	repo.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the database is not at the path it was given (%s): %v", path, err)
	}
}

// A run table is what a person reads after the fact: the runs of a workflow,
// newest first, each with the state of its tasks folded in. The test records
// two runs of one workflow — one finished, one failed — and reads them back
// after a reopen, which is the whole point of the repository.
func TestSQLiteWorkflowRepositoryListsRuns(t *testing.T) {
	name := "data_runs_test"
	removeDB(name)
	defer removeDB(name)

	execution := func(partition, task string, status workflow.Status, start time.Time) workflow.WorkflowExecution {
		return workflow.WorkflowExecution{
			ExecutionId: task + "-" + partition,
			WorkflowInstanceId: workflow.WorkflowInstanceId{
				WorkflowId: workflow.WorkflowId{DName: name, DVersion: name + "@" + partition, DComponent: "memdoor"},
				Partition:  partition,
			},
			StartDate: start,
			Status:    status,
		}
	}

	older := time.Now().Add(-time.Hour)
	first := NewWorkflowRepository(name, static.BuilderDummyFn)
	for _, e := range []workflow.WorkflowExecution{
		execution("2026-10-02T090000", "tests", workflow.Done, older),
		execution("2026-10-02T090000", "ship", workflow.Done, older),
		execution("2026-10-03T090000", "tests", workflow.Done, time.Now()),
		execution("2026-10-03T090000", "ship", workflow.Failed, time.Now()),
	} {
		if err := first.Upsert(e); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	first.Close()

	second := NewWorkflowRepository(name, static.BuilderDummyFn)
	defer second.Close()

	runs := second.Runs(name, 10)
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2 (one per partition): %+v", len(runs), runs)
	}
	// Newest first.
	if runs[0].Partition != "2026-10-03T090000" {
		t.Fatalf("first run is %q, want the newest (2026-10-03T090000)", runs[0].Partition)
	}
	if runs[0].Status != workflow.Failed {
		t.Fatalf("the run whose ship task failed is %v, want Failed", runs[0].Status)
	}
	if runs[1].Status != workflow.Done {
		t.Fatalf("the run whose tasks all finished is %v, want Done", runs[1].Status)
	}
	if runs[0].Executions != 2 {
		t.Fatalf("the newest run has %d executions, want 2", runs[0].Executions)
	}
}

// A host that cannot open its run table must be able to carry on without
// it. OpenWorkflowRepositoryAt reports the failure; the older constructors
// exit the process, which in a gateway means a dead gateway.
func TestOpenWorkflowRepositoryAtReportsFailure(t *testing.T) {
	// A path whose parent is a file, not a directory: it cannot be opened.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenWorkflowRepositoryAt(filepath.Join(file, "runs.db"), static.BuilderDummyFn)
	if err == nil {
		repo.Close()
		t.Fatal("opening a database under a regular file must fail, and be reported")
	}
}

// A run finishes while another reads: many goroutines against one
// repository, the way a scheduler writes a task's state as a run table is
// read. database/sql pools connections, and two of them on one sqlite file
// is what sqlite refuses — "unable to open database file: out of memory
// (14)", a bare statement and not a memory condition. One connection, and a
// lock per table, is what this proves.
//
// Run with -race: the unlocked reads this guards against were a genuine
// data race, not only a lost write.
func TestSQLiteWorkflowRepositoryUnderConcurrentUse(t *testing.T) {
	name := "data_concurrent_test"
	removeDB(name)
	defer removeDB(name)

	repo := NewWorkflowRepository(name, static.BuilderDummyFn)
	defer repo.Close()

	execution := func(i int) workflow.WorkflowExecution {
		partition := fmt.Sprintf("2026-10-03T09%02d00", i)
		return workflow.WorkflowExecution{
			ExecutionId: fmt.Sprintf("tests-%s", partition),
			WorkflowInstanceId: workflow.WorkflowInstanceId{
				WorkflowId: workflow.WorkflowId{DName: name, DVersion: name + "@" + partition, DComponent: "memdoor"},
				Partition:  partition,
			},
			StartDate: time.Now(),
			Status:    workflow.Done,
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := execution(i)
			if err := repo.Upsert(e); err != nil {
				t.Errorf("Upsert: %v", err)
			}
			// A read of the same table, at the same time as the writes.
			repo.Fetch(e.InstanceId())
			repo.Executions(e.InstanceId())
			repo.Runs(name, 10)
			repo.Upstreams(e.InstanceId(), e.DVersion)
		}(i)
	}
	wg.Wait()

	// And everything written is there.
	if got := len(repo.ExecutionsByName(name, 100)); got != 24 {
		t.Fatalf("after concurrent use, %d executions are stored, want 24", got)
	}
}
