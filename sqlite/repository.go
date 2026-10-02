package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
	_ "modernc.org/sqlite"
)

type SqliteWorkflowRepository struct {
	Instancemux *sync.Mutex
	Exemux      *sync.Mutex
	db          *sql.DB
	dbFile      string
	builderFn   workflow.GetBuilderFunc
}

func (t *SqliteWorkflowRepository) Close() {
	t.db.Close()
}

func NewWorkflowRepository(dataSourceName string, builderFunc workflow.GetBuilderFunc) *SqliteWorkflowRepository {
	return open(get_db_file(dataSourceName), builderFunc)
}

// OpenWorkflowRepositoryAt is NewWorkflowRepositoryAt for a host that must
// handle the failure: it returns the error instead of exiting the process.
// A host that cannot open its run table — a project that went away, a
// read-only disk — should carry on without it, not die on startup.
func OpenWorkflowRepositoryAt(dbFile string, builderFunc workflow.GetBuilderFunc) (*SqliteWorkflowRepository, error) {
	return openAt(dbFile, builderFunc)
}

// open prepares the database at db_file — creating the tables if they are
// absent and leaving whatever they already hold untouched.
func open(db_file string, builderFunc workflow.GetBuilderFunc) *SqliteWorkflowRepository {
	repo, err := openAt(db_file, builderFunc)
	if err != nil {
		log.Fatal(err)
	}
	return repo
}

// openAt does the work, reporting failure to the caller.
func openAt(db_file string, builderFunc workflow.GetBuilderFunc) (*SqliteWorkflowRepository, error) {
	db, err := sql.Open("sqlite", db_file)
	if err != nil {
		return nil, err
	}

	// Create the tables if they do not exist. Nothing is dropped: a
	// repository that deletes what it holds on every open cannot answer
	// "what did this run do" after a restart, which is the whole reason a
	// host chooses it over the in-memory one.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS workflow_dependencies (
		workflow_id TEXT NOT NULL,
		required_workflow_id TEXT NOT NULL,        
		version  TEXT NOT NULL,
    	PRIMARY KEY (workflow_id, required_workflow_id, version)
	);`)
	if err != nil {
		db.Close()
		return nil, err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS workflow_executions (
		ExecutionId TEXT PRIMARY KEY,
		Id TEXT NOT NULL,
		Name TEXT NOT NULL,
		Partition TEXT NOT NULL,
		StartDate DATETIME NOT NULL,
		EndDate DATETIME,
		Status TEXT NOT NULL,
		Error TEXT,
		DParameters TEXT,
		max_retries INT32 DEFAULT 3,
		Retries INT32 DEFAULT 0,
		Version TEXT NOT NULL,
		component TEXT NOT NULL 
	);`)
	if err != nil {
		db.Close()
		return nil, err
	}

	db.Exec("PRAGMA journal_mode=WAL")

	return &SqliteWorkflowRepository{db: db, builderFn: builderFunc, Instancemux: &sync.Mutex{}, Exemux: &sync.Mutex{}, dbFile: db_file}, nil
}

func get_db_file(dataSourceName string) string {
	return fmt.Sprintf("./%s.db", dataSourceName)
}

func (r *SqliteWorkflowRepository) ExecutionsByName(name string, limit int) []workflow.WorkflowExecution {
	r.Exemux.Lock()
	defer r.Exemux.Unlock()

	// Define the query to select executions by name with a limit
	query := `
    SELECT ExecutionId, Name, Partition, StartDate, EndDate, Status, Error, DParameters, Retries, Component, Version
    FROM workflow_executions
    WHERE Name = ?
    ORDER BY StartDate DESC
    LIMIT ?`

	// Prepare a slice to hold the resulting executions
	executions := make([]workflow.WorkflowExecution, 0)

	// Execute the query with the name and limit as parameters
	rows, err := r.db.Query(query, name, limit)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
		return executions
	}
	defer rows.Close()

	// Iterate over rows and populate the executions slice
	for rows.Next() {
		var execution workflow.WorkflowExecution
		var statusString int
		var errorString string
		var parametersString string

		// Scan each row into the execution structure
		if err := rows.Scan(&execution.ExecutionId, &execution.DName, &execution.Partition, &execution.StartDate, &execution.EndDate, &statusString, &errorString, &parametersString, &execution.DRetries, &execution.DComponent, &execution.DVersion); err != nil {
			log.Printf("sqlite repository: %v", err)
		}

		// Convert the scanned values to appropriate types
		execution.Status = workflow.Status(statusString)

		if errorString != "" {
			execution.Error = errorString
		}

		if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
			log.Printf("sqlite repository: %v", err)
		}

		// Append the execution to the list
		executions = append(executions, execution)
	}

	return executions
}

func (r *SqliteWorkflowRepository) Fetch(id string) (workflow.WorkflowExecution, bool) {
	r.Instancemux.Lock()
	defer r.Instancemux.Unlock()

	query := `SELECT e.ExecutionId, e.Name, e.Partition, e.max_retries, e.StartDate, e.EndDate, e.Status, e.Error, e.DParameters, e.Retries, e.Version, e.Component
              FROM workflow_executions e
              WHERE e.Id = ?
              ORDER BY e.StartDate DESC, e.ExecutionId DESC
              LIMIT 1`

	var execution workflow.WorkflowExecution
	var statusString int
	var errorString string
	var parametersString string

	err := r.db.QueryRow(query, id).Scan(&execution.ExecutionId, &execution.DName, &execution.Partition, &execution.DMaxRetries, &execution.StartDate, &execution.EndDate, &statusString, &errorString, &parametersString, &execution.DRetries, &execution.DVersion, &execution.DComponent)
	if err != nil {
		if err == sql.ErrNoRows {
			// No matching workflow execution was found
			return workflow.WorkflowExecution{}, false
		}
		log.Printf("sqlite repository: %v", err)
	}

	// Convert status string to Status type
	execution.Status = workflow.Status(statusString)
	// Convert error string to error type (assuming it's stored as a string)
	if errorString != "" {
		execution.Error = errorString
	}

	// Convert parameters string to map (assuming it's stored as JSON)
	if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
		log.Printf("sqlite repository: %v", err)
	}

	return execution, true
}

func (r *SqliteWorkflowRepository) RevertRequires(e, required string, version string) {
	r.Instancemux.Lock()
	defer r.Instancemux.Unlock()

	_, err := r.db.Exec("DELETE FROM workflow_dependencies WHERE workflow_id = ? AND required_workflow_id = ? AND version = ?",
		e, required, version)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
	}
}

func (r *SqliteWorkflowRepository) Requires(e string, required string, version string) {
	r.Instancemux.Lock()
	defer r.Instancemux.Unlock()

	_, err := r.db.Exec("INSERT OR IGNORE INTO workflow_dependencies (workflow_id, required_workflow_id, version) VALUES (?, ?, ?)",
		e, required, version)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
	}
}

func (r *SqliteWorkflowRepository) fetchIdsByQuery(query string, instanceID string, version string) map[string]bool {
	workflows := make(map[string]bool, 0)

	rows, err := r.db.Query(query, instanceID, version)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
		return workflows
	}
	defer rows.Close()

	for rows.Next() {
		var workflowID string

		if err := rows.Scan(&workflowID); err != nil {
			log.Printf("sqlite repository: %v", err)
			return workflows
		}
		workflows[workflowID] = true
	}
	return workflows
}

func (r *SqliteWorkflowRepository) fetchWorkflowsByQuery(query string, instanceID string, version string) map[string]workflow.WorkflowExecution {
	workflows := make(map[string]workflow.WorkflowExecution, 0)

	rows, err := r.db.Query(query, instanceID, version)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
		return workflows
	}
	defer rows.Close()

	for rows.Next() {
		var workflowID string
		if err := rows.Scan(&workflowID); err != nil {
			log.Printf("sqlite repository: %v", err)
		}
		if inst, ok := r.Fetch(workflowID); ok {
			workflows[inst.InstanceId()] = inst
		} else {
			logger.Log.Error(fmt.Sprintf("cannot find workflow id %s", workflowID), "component", "sqlite")
		}
	}
	return workflows
}

func (r *SqliteWorkflowRepository) Upstreams(w string, version string) map[string]bool {
	query := `
        SELECT required_workflow_id
        FROM workflow_dependencies
        WHERE workflow_id = ? AND version = ? ;`

	return r.fetchIdsByQuery(query, w, version)
}

func (r *SqliteWorkflowRepository) Downstreams(w string, version string) map[string]bool {
	query := `
        SELECT workflow_id
        FROM workflow_dependencies
        WHERE required_workflow_id = ? AND version = ? ;`

	return r.fetchIdsByQuery(query, w, version)
}

func (r *SqliteWorkflowRepository) DeepUpstreams(w string, version string) map[string]bool {
	query := `
    WITH RECURSIVE upstreams AS (
        SELECT required_workflow_id
        FROM workflow_dependencies
        WHERE workflow_id = ? AND version = ? 
        UNION ALL
        SELECT wd.required_workflow_id
        FROM workflow_dependencies wd
        JOIN upstreams u ON wd.workflow_id = u.required_workflow_id
    )
    SELECT DISTINCT required_workflow_id FROM upstreams;`

	return r.fetchIdsByQuery(query, w, version)
}

func (r *SqliteWorkflowRepository) DeepDownstreams(w string, version string) map[string]bool {
	query := `
    WITH RECURSIVE downstreams AS (
        SELECT workflow_id
        FROM workflow_dependencies
        WHERE required_workflow_id = ? AND version = ? 
        UNION ALL
        SELECT wd.workflow_id
        FROM workflow_dependencies wd
        JOIN downstreams d ON wd.required_workflow_id = d.workflow_id
    )
    SELECT DISTINCT workflow_id FROM downstreams;`

	return r.fetchIdsByQuery(query, w, version)
}

func (t *SqliteWorkflowRepository) RequiredBy(e, d string, version string) {
	t.Requires(d, e, version)
}

func (r *SqliteWorkflowRepository) Upsert(execution workflow.WorkflowExecution) error {
	r.Exemux.Lock()
	defer r.Exemux.Unlock()

	// Marshal DParameters into a JSON string
	parametersBytes, err := json.Marshal(execution.DParameters)
	if err != nil {
		return fmt.Errorf("error marshalling parameters: %w", err)
	}

	query := `INSERT OR REPLACE INTO workflow_executions (ExecutionId, Id, Name, Partition, max_retries, StartDate, EndDate, Status, Error, DParameters, Retries, Version, Component) 
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = r.db.Exec(query, execution.ExecutionId, execution.InstanceId(), execution.DName, execution.Partition, execution.DMaxRetries, execution.StartDate, execution.EndDate, execution.Status, execution.Error, parametersBytes, execution.DRetries, execution.DVersion, execution.DComponent)
	if err != nil {
		return fmt.Errorf("error executing upsert query: %w  Id : %s", err, execution.ExecutionId)
	}

	return nil
}

func (r *SqliteWorkflowRepository) Executions(id string) map[string]workflow.WorkflowExecution {
	executions := make(map[string]workflow.WorkflowExecution, 0)
	query := `
    SELECT ExecutionId, Name, Partition, StartDate, EndDate, Status, Error, DParameters, Retries, Component, Version
    FROM workflow_executions 
    WHERE Id = ?`

	rows, err := r.db.Query(query, id)
	if err != nil {
		log.Printf("sqlite repository: %v", err)
		return executions
	}
	defer rows.Close()

	for rows.Next() {
		var execution workflow.WorkflowExecution
		var statusString int
		var parametersString string

		if err := rows.Scan(&execution.ExecutionId, &execution.DName, &execution.Partition, &execution.StartDate, &execution.EndDate, &statusString, &execution.Error, &parametersString, &execution.DRetries, &execution.DComponent, &execution.DVersion); err != nil {
			log.Printf("sqlite repository: %v", err)
		}

		// Convert status string to Status type
		execution.Status = workflow.Status(statusString)

		// Convert parameters string to map (assuming it's stored as JSON)
		if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
			log.Printf("sqlite repository: %v", err)
		}

		executions[execution.ExecutionId] = execution
	}

	return executions
}

// NewWorkflowRepositoryAt opens a repository whose database is at the given
// path, exactly as given. NewWorkflowRepository keeps the historical
// behaviour of resolving a bare name to ./<name>.db in the working
// directory; a host that keeps its state somewhere of its choosing (a
// project's .memdoor/, a data directory) names the file itself.
func NewWorkflowRepositoryAt(dbFile string, builderFunc workflow.GetBuilderFunc) *SqliteWorkflowRepository {
	return open(dbFile, builderFunc)
}

// RunSummary is one run of a workflow, as a person reads it afterwards: the
// partition it was for, how many of its tasks are done, when it started and
// how it ended. A run is a set of task executions sharing a partition; this
// is their aggregate, which mario keeps no single row for.
type RunSummary struct {
	Name       string
	Version    string
	Partition  string
	Started    time.Time
	Ended      time.Time
	Status     workflow.Status
	Error      string
	Executions int
}

// Runs lists the runs of a workflow, newest first, up to limit. It reads the
// executions a run wrote and groups them by partition — the run's identity —
// so a person can see what a workflow did after the process that ran it is
// gone. A run with no executions is not a run.
func (r *SqliteWorkflowRepository) Runs(name string, limit int) []RunSummary {
	r.Exemux.Lock()
	defer r.Exemux.Unlock()

	if limit <= 0 {
		limit = 20
	}
	query := `
    SELECT Partition, Version, COUNT(*)
    FROM workflow_executions
    WHERE Name = ?
    GROUP BY Partition
    ORDER BY Partition DESC
    LIMIT ?`

	rows, err := r.db.Query(query, name, limit)
	if err != nil {
		log.Printf("Runs(%q): %v", name, err)
		return nil
	}

	runs := make([]RunSummary, 0, limit)
	for rows.Next() {
		var run RunSummary
		run.Name = name
		if err := rows.Scan(&run.Partition, &run.Version, &run.Executions); err != nil {
			rows.Close()
			log.Printf("Runs(%q): %v", name, err)
			return nil
		}
		runs = append(runs, run)
	}
	// Closed before the state pass below: that one queries the same
	// database, and a cursor still open over it would be holding a
	// connection the next query needs.
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("Runs(%q): %v", name, err)
		return nil
	}

	// The state and the span of a run are its tasks': failed if any failed,
	// done when all of them are, running while some have not started.
	// The partition orders the runs: it is the run's own name (the moment
	// it started, or the day, or whatever the host named), and it sorts
	// lexically because it is a timestamp the host formats that way.
	for i := range runs {
		runs[i].Status, runs[i].Error, runs[i].Started, runs[i].Ended = r.runState(name, runs[i].Partition)
	}
	return runs
}

// runState folds a run's task executions into the run's own state, and
// reports when the run began and last moved. Read from the plain columns
// rather than an SQL aggregate: MIN(StartDate) arrives from the driver as
// text (an aggregate has no column type to convert), and this needs a time.
func (r *SqliteWorkflowRepository) runState(name, partition string) (workflow.Status, string, time.Time, time.Time) {
	rows, err := r.db.Query(`SELECT Status, Error, StartDate, EndDate FROM workflow_executions WHERE Name = ? AND Partition = ?`, name, partition)
	if err != nil {
		log.Printf("runState(%q, %q): %v", name, partition, err)
		return workflow.Scheduled, "", time.Time{}, time.Time{}
	}
	defer rows.Close()

	state, errText, pending := workflow.Done, "", 0
	var started, ended time.Time
	for rows.Next() {
		var status int
		var errString string
		var start, end sql.NullTime
		if err := rows.Scan(&status, &errString, &start, &end); err != nil {
			continue
		}
		if start.Valid && (started.IsZero() || start.Time.Before(started)) {
			started = start.Time
		}
		if end.Valid && end.Time.After(ended) {
			ended = end.Time
		}
		switch workflow.Status(status) {
		case workflow.Failed:
			state = workflow.Failed
			if errText == "" {
				errText = errString
			}
		case workflow.Done:
			// keeps the run done unless a failure already said otherwise
		default:
			pending++
		}
	}
	if state == workflow.Failed {
		return workflow.Failed, errText, started, ended
	}
	if pending > 0 {
		return workflow.Started, "", started, ended
	}
	return state, "", started, ended
}
