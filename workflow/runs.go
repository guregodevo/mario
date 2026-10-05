package workflow

import "time"

// RunSummary is one run of a workflow as a run table lists it: its
// partition (the run's identity), the version its rows share
// ("<workflow>@<partition>"), when it started and ended, how it ended, and
// how many of its tasks' executions it holds.
type RunSummary struct {
	Name       string
	Version    string
	Partition  string
	Started    time.Time
	Ended      time.Time
	Status     Status
	Error      string
	Executions int
}

// RunLister is a repository that can list a workflow's runs, newest first,
// up to limit: the sqlite table does, and the REST repository asks its
// server to. A host lists history through this, whatever the table is.
type RunLister interface {
	Runs(name string, limit int) []RunSummary
}
