package static

import (
	"testing"
)

func TestStaticWorkflowRepository(t *testing.T) {
	// Prepare SQLite repository
	repo := NewWorkflowRepository()

	RepositoryTestcases(t, repo)
}
