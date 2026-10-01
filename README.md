# mario

A Directed Acyclic Graph (DAG) execution library in Go.

Inspired by Spotify's [Luigi](https://github.com/spotify/luigi), Mario lets you declare tasks and their dependencies, then trigger a target task for a given partition (for example a date such as `2023-01-01`). The scheduler runs the upstream tasks first, concurrently where possible, retries failures, and records every execution.

Mario is experimental: the API is not stable yet.

## Features

| Feature | Status |
|---|---|
| Task definition | Go, or YAML validated against a JSON schema |
| Parallel execution | Yes, one goroutine per runnable task |
| Retries | Yes, per-task maximum and configurable delay |
| Backfill | Yes |
| Execution history | Yes |
| Cross-DAG dependencies | Yes, through external tasks and data endpoints |
| Calendar scheduling | No, use cron |
| Web dashboard, monitoring, alerting | Not yet |

## Install

```sh
go get github.com/guregodevo/mario
```

Requires Go 1.23 or later. The SQLite repository uses `github.com/mattn/go-sqlite3`, which needs cgo.

## Concepts

* **Workflow**: a named task with a version, a component and a maximum number of retries.
* **Workflow instance**: a workflow for one partition. Its id is `<name>-<partition>`.
* **Execution**: one run of an instance, with a status (`Scheduled`, `Started`, `Failed`, `Done`, ...), start and end dates, and the error if it failed.
* **Data endpoint**: the output of a task. A dependency counts as satisfied when its last execution is `Done` or its endpoint already exists.
* **External task**: a dependency owned by someone else. Mario never runs it; it only waits for it.

## Packages

| Package | Role |
|---|---|
| `workflow` | Core types and interfaces: `WorkflowExecution`, `WorkflowRepository`, `Queue`, `DataEndpoint` |
| `scheduler` | `LocalScheduler`: reads the task queue, checks upstreams, executes, notifies downstreams, retries |
| `engine` | `Executor` interface and a local executor that calls the task's function |
| `factory` | `TaskFactory` interface, and `BuildDAG` to build the dependency graph from YAML task definitions |
| `static` | In-memory repository, channel-based queue, and dummy tasks for tests and examples |
| `sqlite` | SQLite repository |
| `api` | Repository backed by a remote REST state service (set `API_URL`) |
| `templates` | YAML task definitions, JSON schema validation, lazy template rendering |
| `lineage` | Cycle detection, topological sort, random DAG generation |
| `fileio` | File access abstraction with a local implementation |
| `utils` | Partition parsing and date offsets |

To run your own tasks, implement `factory.TaskFactory`. To store state elsewhere, implement `workflow.WorkflowRepository`. To distribute work, implement `workflow.Queue`.

## Usage

This is the example in `main.go`. `D` requires `C`, `A` and an external task; `C` requires `A` and `B`.

```go
package main

import (
	"fmt"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/scheduler"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/utils"
	"github.com/guregodevo/mario/workflow"
)

func main() {
	partition := "2023-01-01"
	version := fmt.Sprintf("%d", time.Now().Unix())

	repo := static.NewWorkflowRepository()
	factory := static.DummyTaskFactory{Version: version, Partition: partition, Component: utils.COMPONENT}
	defer repo.Close()

	// Define tasks
	taskA := factory.NewExecutable("A")
	taskB := factory.NewExecutable("B")
	taskB.Status = workflow.Done
	taskC := factory.NewExecutable("C")
	taskD := factory.NewExecutable("D")
	taskExternalD := factory.NewExecutable("ExternalD")
	taskExternalD.DExternal = true

	// Define dependencies
	repo.Requires(taskC.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskC.WorkflowName(), taskB.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskC.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskA.WorkflowName(), version)
	repo.Requires(taskD.WorkflowName(), taskExternalD.WorkflowName(), version)

	// backfill=true, loop=true, a task queue and a retry queue, 3s between retries
	s := scheduler.NewLocalScheduler(true, true, static.NewChannelQueue(100), static.NewChannelQueue(100), repo, &factory, engine.NewLocalExecutor(), 3*time.Second)
	s.Trigger(taskD.WorkflowInstanceId)
	if err := s.WaitForCompletion(); err != nil {
		logger.Log.Fatal("main", "Execution failed: %v", err)
	}
	fmt.Println("All tasks completed successfully!")
}
```

Run it with:

```sh
make run
```

## Development

```sh
make build   # go build
make tests   # go test ./...
make fmt     # go fmt ./...
```

Tests that need a remote state service are skipped unless the `CI` environment variable is set.

## Contribute

Contributions are welcome. Please open an issue to discuss a change, or submit a pull request.
