package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/workflow"
)

// The client sends its token on every request, a nil builder is the dummy
// one, and an upsert the server refused is an error the caller sees.
func TestRESTClientTokenAndErrors(t *testing.T) {
	var auth []string
	factory := &static.DummyTaskFactory{Version: "1", Partition: "2026-10-05", Component: "t"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workflow":
			http.Error(w, "no room", http.StatusInsufficientStorage)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/workflow/"):
			_ = json.NewEncoder(w).Encode(GrpcExecutionOf(factory.NewExecutable("A").WorkflowExecution))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	repo := NewRESTWorkflowRepository(srv.URL+"/", nil).WithToken("tok-1")
	exe := factory.NewExecutable("A").WorkflowExecution
	if err := repo.Upsert(exe); err == nil || !strings.Contains(err.Error(), "507") {
		t.Fatalf("a refused upsert must be an error naming the status, got %v", err)
	}
	got, ok := repo.Fetch(exe.InstanceId())
	if !ok || got.WorkflowName() != "A" {
		t.Fatalf("fetch with the dummy builder: ok=%v name=%q", ok, got.WorkflowName())
	}
	for i, a := range auth {
		if a != "Bearer tok-1" {
			t.Errorf("request %d carried %q, want the bearer token", i, a)
		}
	}
	if len(auth) != 2 {
		t.Fatalf("expected 2 requests, saw %d", len(auth))
	}
}

// Runs asks the server for the workflow's runs and reads them whole.
func TestRESTClientListsRuns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workflow/hello/runs" || r.URL.Query().Get("limit") != "5" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]workflow.RunSummary{{Name: "hello", Partition: "2026-10-05", Version: "hello@2026-10-05", Status: workflow.Done, Executions: 2}})
	}))
	defer srv.Close()
	runs := NewRESTWorkflowRepository(srv.URL, nil).Runs("hello", 5)
	if len(runs) != 1 || runs[0].Partition != "2026-10-05" || runs[0].Status != workflow.Done || runs[0].Executions != 2 {
		t.Fatalf("runs: %+v", runs)
	}
}
