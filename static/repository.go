package static

import (
	"sort"
	"sync"

	"github.com/guregodevo/mario/workflow"
)

type StaticWorkflowRepository struct {
	mutex              *sync.RWMutex
	emutex             *sync.RWMutex
	DownstreamDeps     map[string]map[string]map[string]bool            // [version][name] list of downstreams instance
	UpstreamsDeps      map[string]map[string]map[string]bool            // [version][name] list of upstreams instance
	InstanceExecutions map[string]map[string]workflow.WorkflowExecution // [instance_id] ordered list of executions
}

func NewWorkflowRepository() *StaticWorkflowRepository {
	return &StaticWorkflowRepository{
		mutex:              &sync.RWMutex{},
		emutex:             &sync.RWMutex{},
		UpstreamsDeps:      make(map[string]map[string]map[string]bool),
		DownstreamDeps:     make(map[string]map[string]map[string]bool),
		InstanceExecutions: make(map[string]map[string]workflow.WorkflowExecution),
	}
}

func (t *StaticWorkflowRepository) Close() {
}

func (t *StaticWorkflowRepository) Fetch(id string) (workflow.WorkflowExecution, bool) {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	instances, oks := t.InstanceExecutions[id]
	if oks && len(instances) > 0 {
		var latestExe workflow.WorkflowExecution
		first := true
		for _, exe := range instances {
			if first || exe.StartDate.After(latestExe.StartDate) {
				latestExe = exe
				first = false
			}
		}
		return latestExe, true
	}
	return workflow.WorkflowExecution{}, false
}

func (t *StaticWorkflowRepository) RevertRequires(e, d string, version string) {
	a, ok := t.UpstreamsDeps[version][e]
	if !ok {
		return
	}
	delete(a, d)
}

func (t *StaticWorkflowRepository) Requires(e, d string, version string) {
	if _, oks := t.UpstreamsDeps[version]; !oks {
		t.UpstreamsDeps[version] = make(map[string]map[string]bool, 0)
	}
	_, ok := t.UpstreamsDeps[version][e]
	if !ok {
		t.UpstreamsDeps[version][e] = make(map[string]bool, 0)
	}
	t.UpstreamsDeps[version][e][d] = true
	t.RequiredBy(d, e, version)
}

func (t *StaticWorkflowRepository) DeepUpstreams(e string, version string) map[string]bool {
	visited := make(map[string]bool)
	queue := make([]string, 0)

	// Trigger with the direct upstreams of 'e'
	if upstreamsMap, ok := t.UpstreamsDeps[version][e]; ok {
		for upstream, _ := range upstreamsMap {
			queue = append(queue, upstream)
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if _, alreadyVisited := visited[current]; alreadyVisited {
			continue
		}

		visited[current] = true

		if upstreamsMap, ok := t.UpstreamsDeps[version][current]; ok {
			for upstream, _ := range upstreamsMap {
				if _, alreadyVisited := visited[upstream]; !alreadyVisited {
					queue = append(queue, upstream)
				}
			}
		}
	}

	return visited
}

func (t *StaticWorkflowRepository) DeepDownstreams(e string, version string) map[string]bool {
	visited := make(map[string]bool)
	queue := make([]string, 0)

	// Trigger with the direct downstreams of 'e'
	if downstreamsMap, ok := t.DownstreamDeps[version][e]; ok {
		for downstream, _ := range downstreamsMap {
			queue = append(queue, downstream)
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if _, alreadyVisited := visited[current]; alreadyVisited {
			continue
		}

		visited[current] = true

		if downstreamsMap, ok := t.DownstreamDeps[version][current]; ok {
			for downstream, _ := range downstreamsMap {
				if _, alreadyVisited := visited[downstream]; !alreadyVisited {
					queue = append(queue, downstream)
				}
			}
		}
	}

	return visited
}

func (t *StaticWorkflowRepository) Downstreams(e string, version string) map[string]bool {
	if _, oks := t.DownstreamDeps[version]; oks {
		deps, ok := t.DownstreamDeps[version][e]
		if ok {
			return deps
		}
	}
	return make(map[string]bool, 0)
}

func (t *StaticWorkflowRepository) RequiredBy(e, d string, version string) {
	if _, oks := t.DownstreamDeps[version]; !oks {
		t.DownstreamDeps[version] = make(map[string]map[string]bool, 1)
	}
	_, ok := t.DownstreamDeps[version][e]
	if !ok {
		t.DownstreamDeps[version][e] = make(map[string]bool, 0)
	}
	t.DownstreamDeps[version][e][d] = true
}

func (t *StaticWorkflowRepository) Upsert(instance workflow.WorkflowExecution) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if _, oks := t.InstanceExecutions[instance.InstanceId()]; !oks {
		t.InstanceExecutions[instance.InstanceId()] = make(map[string]workflow.WorkflowExecution, 0)
	}
	t.InstanceExecutions[instance.InstanceId()][instance.ExecutionId] = instance
	return nil
}

func (t *StaticWorkflowRepository) Upstreams(e string, version string) map[string]bool {
	if _, oks := t.UpstreamsDeps[version]; oks {
		deps, ok := t.UpstreamsDeps[version][e]
		if ok {
			return deps
		}
	}
	return make(map[string]bool, 0)
}

// ExecutionsByName returns a sorted list of WorkflowExecution objects by name, limited by the given limit.
func (t *StaticWorkflowRepository) ExecutionsByName(name string, limit int) []workflow.WorkflowExecution {
	t.emutex.RLock()
	defer t.emutex.RUnlock()

	// Collect all executions with the specified name
	executions := make([]workflow.WorkflowExecution, 0)
	for _, instances := range t.InstanceExecutions {
		for _, exec := range instances {
			if exec.WorkflowName() == name {
				executions = append(executions, exec)
			}
		}
	}

	// Sort executions by StartDate in descending order
	sort.Slice(executions, func(i, j int) bool {
		return executions[i].StartDate.After(executions[j].StartDate)
	})

	// Return up to the specified limit of executions
	if limit > 0 && len(executions) > limit {
		return executions[:limit]
	}
	return executions
}

func (t *StaticWorkflowRepository) Executions(id string) map[string]workflow.WorkflowExecution {
	t.emutex.RLock()
	defer t.emutex.RUnlock()
	_, oks := t.InstanceExecutions[id]
	if !oks {
		return make(map[string]workflow.WorkflowExecution, 0)
	}
	return t.InstanceExecutions[id]
}
