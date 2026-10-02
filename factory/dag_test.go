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
	tasks, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.a", "", "p.d.b", "p.d.a,p.d.gate!"), component())
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
	_, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.approve", "", "p.d.ship", "p.d.approve!"), component())
	if err == nil || !strings.Contains(err.Error(), "p.d.approve") || !strings.Contains(err.Error(), "external") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildDAGRefusesAMissingRequirementThatIsNotExternal(t *testing.T) {
	_, err := BuildDAG("v", "2026-10-02", "c", static.NewWorkflowRepository(), defs("p.d.b", "p.d.ghost"), component())
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("err = %v", err)
	}
}
