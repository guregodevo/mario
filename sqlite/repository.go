package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"

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
	db_file := get_db_file(dataSourceName)
	db, err := sql.Open("sqlite", db_file)
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
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
		log.Fatal(err)
	}

	db.Exec("PRAGMA journal_mode=WAL")

	return &SqliteWorkflowRepository{db: db, builderFn: builderFunc, Instancemux: &sync.Mutex{}, Exemux: &sync.Mutex{}, dbFile: db_file}
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
		log.Fatal(err) // Consider better error handling
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
			log.Fatal(err) // Consider better error handling
		}

		// Convert the scanned values to appropriate types
		execution.Status = workflow.Status(statusString)

		if errorString != "" {
			execution.Error = errorString
		}

		if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
			log.Fatal(err) // Consider better error handling
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
		log.Fatal(err) // Or handle the error as appropriate for your application
	}

	// Convert status string to Status type
	execution.Status = workflow.Status(statusString)
	// Convert error string to error type (assuming it's stored as a string)
	if errorString != "" {
		execution.Error = errorString
	}

	// Convert parameters string to map (assuming it's stored as JSON)
	if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
		log.Fatal(err)
	}

	return execution, true
}

func (r *SqliteWorkflowRepository) RevertRequires(e, required string, version string) {
	r.Instancemux.Lock()
	defer r.Instancemux.Unlock()

	_, err := r.db.Exec("DELETE FROM workflow_dependencies WHERE workflow_id = ? AND required_workflow_id = ? AND version = ?",
		e, required, version)
	if err != nil {
		log.Fatal(err)
	}
}

func (r *SqliteWorkflowRepository) Requires(e string, required string, version string) {
	r.Instancemux.Lock()
	defer r.Instancemux.Unlock()

	_, err := r.db.Exec("INSERT OR IGNORE INTO workflow_dependencies (workflow_id, required_workflow_id, version) VALUES (?, ?, ?)",
		e, required, version)
	if err != nil {
		log.Fatal(err)
	}
}

func (r *SqliteWorkflowRepository) fetchIdsByQuery(query string, instanceID string, version string) map[string]bool {
	workflows := make(map[string]bool, 0)

	rows, err := r.db.Query(query, instanceID, version)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var workflowID string

		if err := rows.Scan(&workflowID); err != nil {
			log.Fatal(err)
		}
		workflows[workflowID] = true
	}
	return workflows
}

func (r *SqliteWorkflowRepository) fetchWorkflowsByQuery(query string, instanceID string, version string) map[string]workflow.WorkflowExecution {
	workflows := make(map[string]workflow.WorkflowExecution, 0)

	rows, err := r.db.Query(query, instanceID, version)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	for rows.Next() {
		var workflowID string
		if err := rows.Scan(&workflowID); err != nil {
			log.Fatal(err)
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
		log.Fatal(err) // Consider handling this error more gracefully
	}
	defer rows.Close()

	for rows.Next() {
		var execution workflow.WorkflowExecution
		var statusString int
		var parametersString string

		if err := rows.Scan(&execution.ExecutionId, &execution.DName, &execution.Partition, &execution.StartDate, &execution.EndDate, &statusString, &execution.Error, &parametersString, &execution.DRetries, &execution.DComponent, &execution.DVersion); err != nil {
			log.Fatal(err) // Consider handling this error more gracefully
		}

		// Convert status string to Status type
		execution.Status = workflow.Status(statusString)

		// Convert parameters string to map (assuming it's stored as JSON)
		if err := json.Unmarshal([]byte(parametersString), &execution.DParameters); err != nil {
			log.Fatal(err) // Consider handling this error more gracefully
		}

		executions[execution.ExecutionId] = execution
	}

	return executions
}
