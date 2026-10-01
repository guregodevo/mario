package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/workflow"
)

type RESTWorkflowRepository struct {
	httpClient *http.Client
	baseURL    string
	builderFn  workflow.GetBuilderFunc
}

// NewRESTWorkflowRepository creates a new instance of RESTWorkflowRepository.
func NewRESTWorkflowRepository(apiURL string, builderFn workflow.GetBuilderFunc) *RESTWorkflowRepository {
	return &RESTWorkflowRepository{
		httpClient: &http.Client{},
		baseURL:    apiURL,
		builderFn:  builderFn,
	}
}

func NewWorkflowRepository(builderFn workflow.GetBuilderFunc) *RESTWorkflowRepository {
	apiUrl := os.Getenv("API_URL")
	if apiUrl == "" {
		logger.Log.Fatalf("Missing API_URL env variable")
	}
	return NewRESTWorkflowRepository(apiUrl, builderFn)
}

func (r *RESTWorkflowRepository) ExecutionsByName(name string, limit int) []workflow.WorkflowExecution {
	// Construct the URL with query parameters for name and limit
	url := fmt.Sprintf("%s/workflow/%s/latest_executions?limit=%d", r.baseURL, name, limit)

	// Send the HTTP GET request
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return nil
	}
	defer resp.Body.Close()

	// Check the response status code
	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return nil
	}

	// Parse the JSON response into a slice of WorkflowExecution
	var executions []workflow.WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return nil
	}

	return executions
}

func (r *RESTWorkflowRepository) Executions(id string) map[string]workflow.WorkflowExecution {
	// Send an HTTP GET request to retrieve executions.
	url := r.baseURL + "/workflow/" + id + "/executions"
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return make(map[string]workflow.WorkflowExecution, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return make(map[string]workflow.WorkflowExecution, 0)
	}

	// Deserialize the JSON response into a slice of WorkflowExecution.
	var executions map[string]WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&executions); err != nil {
		logger.Log.Errorf("Failed to decode JSON response: %v", err)
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
	url := r.baseURL + "/workflow/" + e + "/version/" + version + "/" + prefix + "/" + target
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
	}
}

func (r *RESTWorkflowRepository) Upstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about upstream dependencies.
	url := r.baseURL + "/workflow/" + id + "/version/" + version + "/upstreams"
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return make(map[string]bool, 0)
	}

	// Parse the response to extract upstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return make(map[string]bool, 0)
	}
	return response
}

func (r *RESTWorkflowRepository) Downstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about downstream dependencies.
	url := r.baseURL + "/workflow/" + id + "/version/" + version + "/downstreams"
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return make(map[string]bool, 0)
	}

	// Parse the response to extract downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) DeepDownstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about deep downstream dependencies.
	url := r.baseURL + "/workflow/" + id + "/version/" + version + "/deep-downstreams"
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return make(map[string]bool, 0)
	}

	// Parse the response to extract deep downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) DeepUpstreams(id string, version string) map[string]bool {
	// Send an HTTP GET request to retrieve information about deep downstream dependencies.
	url := r.baseURL + "/workflow/" + id + "/version/" + version + "/deep-upstreams"
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return make(map[string]bool, 0)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return make(map[string]bool, 0)
	}

	// Parse the response to extract deep downstream dependencies.
	var response map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return make(map[string]bool, 0)
	}

	return response
}

func (r *RESTWorkflowRepository) Fetch(id string) (workflow.WorkflowExecution, bool) {
	// Send an HTTP GET request to retrieve information about a workflow instance by its ID.
	url := r.baseURL + "/workflow/" + id
	resp, err := r.httpClient.Get(url)
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return workflow.WorkflowExecution{}, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// If the workflow instance is not found, return false.
		return workflow.WorkflowExecution{}, false
	} else if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
		return workflow.WorkflowExecution{}, false
	}

	// Parse the JSON response to extract the workflow instance data.
	var response WorkflowExecution
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		logger.Log.Errorf("Failed to parse JSON response: %v", err)
		return workflow.WorkflowExecution{}, false
	}

	wf := FromGrpcExecution(&response)
	// Convert the response to a WorkflowInstance.
	instance := r.builderFn().SetExecution(wf).
		SetDefaultConcrete().
		Of()
	logger.Log.Info("api", "fetch %s - %s", id, instance.String())

	return instance, true
}

func (r *RESTWorkflowRepository) Upsert(instance workflow.WorkflowExecution) error {
	data := GrpcExecutionOf(instance)
	// Marshal the instance data to JSON.
	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Log.Errorf("Failed to marshal JSON data: %v", err)
		return nil
	}

	// Send an HTTP POST request to create or update the workflow instance.
	url := r.baseURL + "/workflow"
	resp, err := r.httpClient.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		logger.Log.Errorf("Failed to send HTTP request: %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log.Errorf("HTTP request failed with status code: %d", resp.StatusCode)
	} else {
		logger.Log.Info("api", "Upserted %s  : ", instance.InstanceId())
	}
	return nil
}
