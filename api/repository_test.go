package api

import (
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"testing"
)

func TestRestWorkflowRepository(t *testing.T) {
	utils.SkipCI(t)
	// Prepare SQLite repository
	repo, err := NewWorkflowRepository(static.BuilderDummyFn)
	if err != nil {
		t.Skip(err) // no API_URL here
	}

	static.RepositoryTestcases(t, repo)
}
