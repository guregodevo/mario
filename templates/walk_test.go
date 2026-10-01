package templates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/fileio"
)

// A directory of <project>/<dataset>/<task>.yaml, no template directory and
// no FuncMap: the prompt renders from its own text and args, a requirement
// without project or dataset takes the task's, and agent/target are read.
func TestWalkWithoutTemplatesFillsRequireDefaults(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "release", "steps")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "tests.yaml"), []byte("type: test_task\nagent: verifier\ntarget:\n  command: go test ./...\nprompt: run them\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ship.yaml"), []byte("type: test_task\nprompt: ship {{.version}} on {{.partition}}\nargs:\n  version: v1\nrequires:\n  - table_pattern: tests\n  - table_pattern: approve\n    external: true\n  - project_id: other\n    dataset_id: data\n    table_pattern: thing\n"), 0o644)

	err, defs := Walk(&fileio.LocalFileIO{}, nil, NewYAMLValidator(&MockSchemaLoader{}), filepath.Join(root, "release"), "2026-10-02", "", "Prompt")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*YamlTaskDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	tests, ship := byName["release.steps.tests"], byName["release.steps.ship"]
	if tests == nil || ship == nil {
		t.Fatalf("names = %v", byName)
	}
	if tests.Agent != "verifier" || tests.Target == nil || tests.Target.Command != "go test ./..." {
		t.Fatalf("agent/target not read: %+v %+v", tests.Agent, tests.Target)
	}
	var names []string
	for _, r := range ship.Requires {
		names = append(names, r.Name())
	}
	if got := strings.Join(names, ","); got != "release.steps.tests,release.steps.approve,other.data.thing" {
		t.Fatalf("requires = %q", got)
	}
	if !ship.Requires[1].External {
		t.Fatal("external flag lost")
	}
	rendered, err := ship.LazyRenderedField()
	if err != nil || rendered != "ship v1 on 2026-10-02" {
		t.Fatalf("rendered = %q, %v", rendered, err)
	}
}
