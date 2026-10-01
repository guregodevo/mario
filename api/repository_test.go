package api

import (
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"testing"
)

func TestRestWorkflowRepository(t *testing.T) {
	utils.SkipCI(t)
	// Prepare SQLite repository
	repo := NewWorkflowRepository(static.BuilderDummyFn)

	static.RepositoryTestcases(t, repo)
}
