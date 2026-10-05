package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/workflow"
)

// RESTWorkflowRepository is a WorkflowRepository over HTTP: the state lives on
// a mario-state server, and every call here is a request to it. A bearer
// token (WithToken) names the caller to a server that keeps one table per
// account.
type RESTWorkflowRepository struct {
	httpClient *http.Client
	baseURL    string
	token      string
	builderFn  workflow.GetBuilderFunc
}

const restTimeout = 30 * time.Second

// NewRESTWorkflowRepository creates a new instance of RESTWorkflowRepository.
// A nil builderFn builds executions with the dummy builder, as a host that
// only reads and writes state needs nothing more.
func NewRESTWorkflowRepository(apiURL string, builderFn workflow.GetBuilderFunc) *RESTWorkflowRepository {
	if builderFn == nil {
		builderFn = static.BuilderDummyFn
	}
	return &RESTWorkflowRepository{
		httpClient: &http.Client{Timeout: restTimeout},
		baseURL:    strings.TrimRight(apiURL, "/"),
		builderFn:  builderFn,
	}
}

// WithToken sends this bearer token with every request.
func (r *RESTWorkflowRepository) WithToken(token string) *RESTWorkflowRepository {
	r.token = token
	return r
}

// seg is a name, id, version or target as one path segment: a task is
// named <group>/<task> in Memdoor, and a bare slash in the path is a route,
// not a name (found 2026-10-05: Fetch of kpis/leader answered 404 while its
// Upsert, a body, succeeded).
func seg(v string) string { return url.PathEscape(v) }

// do sends one request with the token, so no call is made without it.
func (r *RESTWorkflowRepository) do(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	return r.httpClient.Do(req)
}

// NewWorkflowRepository builds a REST repository on the API_URL environment
// variable, and says so when it is missing — the program decides what to do.
func NewWorkflowRepository(builderFn workflow.GetBuilderFunc) (*RESTWorkflowRepository, error) {
	apiUrl := os.Getenv("API_URL")
	if apiUrl == "" {
		return nil, fmt.Errorf("missing API_URL env variable")
	}
	return NewRESTWorkflowRepository(apiUrl, builderFn), nil
}

func (r *RESTWorkflowRepository) ExecutionsByName(name string, limit int) []workflow.WorkflowExecution {
	// Construct the URL with query parameters for name and limit
	url := fmt.Sprintf("%s/workflow/%s/latest_executions?limit=%d", r.baseURL, seg(name), limit)

	// Send the HTTP GET request
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return nil
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return nil
	}

	// Parse the JSON response into a slice of WorkflowExecution
	var executions []workflow.WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return nil
	}

	return executions
}

func (r *RESTWorkflowRepository) Executions(id string) map[string]workflow.WorkflowExecution {
	// Send an HTTP GET request to retrieve executions.
	url := r.baseURL + "/workflow/" + seg(id) + "/executions"
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return make(map[string]workflow.WorkflowExecution, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return make(map[string]workflow.WorkflowExecution, 0)
	}

	// Deserialize the JSON response into a slice of WorkflowExecution.
	var executions map[string]WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to decode JSON response: %v", err))
		return make(map[string]workflow.WorkflowExecution, 0)
	}

	// Convert the slice of WorkflowExecution to a map.
	m := make(map[string]workflow.WorkflowExecution, len(executions))
	for _, exe := range executions {
		m[exe.Id] = FromGrpcExecution(&exe)
	}
	return m
}

func (r *RESTWorkflowRepository) Close() {
	r.httpClient.CloseIdleConnections()
}

func (r *RESTWorkflowRepository) Requires(e string, required string, version string) {
	r.changeDeps(e, required, "requires", version)
}

func (r *RESTWorkflowRepository) RequiredBy(e string, required string, version string) {
	r.changeDeps(e, required, "required-by", version)
}

func (r *RESTWorkflowRepository) RevertRequires(e string, required string, version string) {
	r.changeDeps(e, required, "revert-requires", version)
}

func (r *RESTWorkflowRepository) changeDeps(e string, target string, prefix string, version string) {
	var jsonData []byte
	// Send an HTTP POST request to create or update the workflow instance.
	url := r.baseURL + "/workflow/" + seg(e) + "/version/" + seg(version) + "/" + prefix + "/" + seg(target)
	resp, err := r.do(http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
	}
}

func (r *RESTWorkflowRepository) Upstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about upstream dependencies.
	url := r.baseURL + "/workflow/" + seg(id) + "/version/" + seg(version) + "/upstreams"
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return make(map[string]bool, 0)
	}

	// Parse the response to extract upstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return make(map[string]bool, 0)
	}
	return response
}

func (r *RESTWorkflowRepository) Downstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about downstream dependencies.
	url := r.baseURL + "/workflow/" + seg(id) + "/version/" + seg(version) + "/downstreams"
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return make(map[string]bool, 0)
	}

	// Parse the response to extract downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) DeepDownstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about deep downstream dependencies.
	url := r.baseURL + "/workflow/" + seg(id) + "/version/" + seg(version) + "/deep-downstreams"
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return make(map[string]bool, 0)
	}

	// Parse the response to extract deep downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) DeepUpstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about deep downstream dependencies.
	url := r.baseURL + "/workflow/" + seg(id) + "/version/" + seg(version) + "/deep-upstreams"
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return make(map[string]bool, 0)
	}

	// Parse the response to extract deep downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) Fetch(id string) (workflow.WorkflowExecution, bool) {
	// Send an HTTP GET request to retrieve information about a workflow instance by its ID.
	url := r.baseURL + "/workflow/" + seg(id)
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return workflow.WorkflowExecution{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// If the workflow instance is not found, return false.
		return workflow.WorkflowExecution{}, false
	} else if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return workflow.WorkflowExecution{}, false
	}

	// Parse the JSON response to extract the workflow instance data.
	var response WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return workflow.WorkflowExecution{}, false
	}

	wf := FromGrpcExecution(&response)
	// Convert the response to a WorkflowInstance.
	instance := r.builderFn().SetExecution(wf).
		SetDefaultConcrete().
		Of()
	logger.Log.Info(fmt.Sprintf("fetch %s - %s", id, instance.String()), "component", "api")

	return instance, true
}

func (r *RESTWorkflowRepository) Upsert(instance workflow.WorkflowExecution) error {
	data := GrpcExecutionOf(instance)
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", instance.InstanceId(), err)
	}
	// AN UPSERT THAT DID NOT HAPPEN IS AN ERROR, not a log line: the run
	// that wrote it believes the state is kept. This returned nil on every
	// failure until 2026-10-05.
	resp, err := r.do(http.MethodPost, r.baseURL+"/workflow", bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("upsert %s: %w", instance.InstanceId(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upsert %s: %s %s", instance.InstanceId(), resp.Status, strings.TrimSpace(string(msg)))
	}
	logger.Log.Info(fmt.Sprintf("Upserted %s  : ", instance.InstanceId()), "component", "api")
	return nil
}

// Runs lists the workflow's runs as the server's table does
// (GET /workflow/<name>/runs?limit=), newest first; nil when the server
// cannot list them or does not answer.
func (r *RESTWorkflowRepository) Runs(name string, limit int) []workflow.RunSummary {
	url := fmt.Sprintf("%s/workflow/%s/runs?limit=%d", r.baseURL, seg(name), limit)
	resp, err := r.do(http.MethodGet, url, nil)
	if err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to send HTTP request: %v", err))
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Log.Error(fmt.Sprintf("HTTP request failed with status code: %d", resp.StatusCode))
		return nil
	}
	var runs []workflow.RunSummary
	if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
		logger.Log.Error(fmt.Sprintf("Failed to parse JSON response: %v", err))
		return nil
	}
	return runs
}

var _ workflow.RunLister = (*RESTWorkflowRepository)(nil)
