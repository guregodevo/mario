package factory

import (
	"strings"
	"testing"

	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/templates"
)

func defs(pairs ...string) map[string]*templates.YamlTaskDefinition {
	out := map[string]*templates.YamlTaskDefinition{}
	for i := 0; i < len(pairs); i += 2 {
		d := &templates.YamlTaskDefinition{Type: "dummy"}
		d.Name = pairs[i]
		if pairs[i+1] != "" {
			for _, r := range strings.Split(pairs[i+1], ",") {
				external := strings.HasSuffix(r, "!")
				d.Requires = append(d.Requires, templates.Requires{ProjectID: "p", DatasetID: "d", TablePattern: strings.TrimSuffix(r, "!"), External: external})
			}
		}
		out[pairs[i]] = d
	}
	return out
}

func component() Component {
	reg := NewComponent()
	reg.Add(&static.DummyTaskFactory{Version: "v", Partition: "2026-10-02", Component: "c"})
	return reg
}

// An external requirement with no definition is registered as an external
// task: waited for, never run.
func TestBuildDAGRegistersAnUndefinedExternal(t *testing.T) {
	tasks, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.a", "", "p.d.b", "a,gate!"), component())
	if err != nil {
		t.Fatal(err)
	}
	gate, ok := tasks["p.d.gate"]
	if !ok || !gate.DExternal {
		t.Fatalf("gate = %+v %v", gate, ok)
	}
}

// A requirement marked external whose task IS defined in the DAG is a
// contradiction: the task would be run, and "external" says it must not be.
// Refused, with both names, rather than running a gate nobody opened.
func TestBuildDAGRefusesExternalOnADefinedTask(t *testing.T) {
	_, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.approve", "", "p.d.ship", "approve!"), component())
	if err == nil || !strings.Contains(err.Error(), "p.d.approve") || !strings.Contains(err.Error(), "external") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildDAGRefusesAMissingRequirementThatIsNotExternal(t *testing.T) {
	_, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.b", "ghost"), component())
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v", err)
	}
}

// A cycle is refused by name when the DAG is built: it would never run and
// never fail (seen live: a run that ended "done" with nothing done).
func TestBuildDAGRefusesACycle(t *testing.T) {
	_, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.a", "b", "p.d.b", "c", "p.d.c", "a"), component())
	if err == nil || !strings.Contains(err.Error(), "cycle") || !strings.Contains(err.Error(), "p.d.a") {
		t.Fatalf("err = %v", err)
	}
	if _, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.a", "", "p.d.b", "a", "p.d.c", "a,b"), component()); err != nil {
		t.Fatalf("a diamond is not a cycle: %v", err)
	}
}
