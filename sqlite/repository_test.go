package sqlite

import (
	"github.com/guregodevo/mario/static"
	"testing"
)

func TestSQLiteWorkflowRepository(t *testing.T) {
	// Prepare SQLite repository
	repo := NewWorkflowRepository("data_test", static.BuilderDummyFn)
	defer repo.Close()

	static.RepositoryTestcases(t, repo)
}
