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

// `each` makes one file several tasks, opt-in: named <name>-<entry>, with the
// entry as {{.item}} in the prompt and the target; a file without it is as before.
func TestWalkEachMakesOneTaskPerEntry(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "team", "steps")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plan.yaml"), []byte("type: test_task\nprompt: plan\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "ideate.yaml"), []byte("type: test_task\neach: [a, b]\nargs:\n  who: team\nprompt: ideator {{.item}} of {{.who}} on {{.partition}}\ntarget:\n  command: test -f proposal-{{.item}}.md\nrequires:\n  - table_pattern: plan\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "solve.yaml"), []byte("type: test_task\nprompt: solve\nrequires:\n  - table_pattern: ideate-a\n  - table_pattern: ideate-b\n"), 0o644)

	err, defs := Walk(&fileio.LocalFileIO{}, nil, NewYAMLValidator(&MockSchemaLoader{}), filepath.Join(root, "team"), "p1", "", "Prompt")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*YamlTaskDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	if len(defs) != 4 || byName["team.steps.ideate-a"] == nil || byName["team.steps.ideate-b"] == nil || byName["team.steps.ideate"] != nil {
		t.Fatalf("names = %v", byName)
	}
	for _, item := range []string{"a", "b"} {
		d := byName["team.steps.ideate-"+item]
		got, err := d.LazyRenderedField()
		if want := "ideator " + item + " of team on p1"; err != nil || got != want {
			t.Fatalf("prompt = %q, %v; want %q", got, err, want)
		}
		if d.Args["item"] != item || d.Item != item || len(d.Each) != 0 {
			t.Fatalf("item not set: %+v", d)
		}
		if len(d.Requires) != 1 || d.Requires[0].Name() != "team.steps.plan" {
			t.Fatalf("requires = %+v", d.Requires)
		}
	}
	if byName["team.steps.ideate-a"].Target.Command != "test -f proposal-{{.item}}.md" {
		t.Fatalf("target = %q", byName["team.steps.ideate-a"].Target.Command)
	}
}

func TestEachRefusesWhatIsNotAListOfNames(t *testing.T) {
	for _, bad := range []string{"each: a", "each: []", "each: [a, a]", "each: ['a b']", "each: ['../x']"} {
		root := t.TempDir()
		dir := filepath.Join(root, "p", "steps")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "x.yaml"), []byte("type: test_task\nprompt: x\n"+bad+"\n"), 0o644)
		if err, _ := Walk(&fileio.LocalFileIO{}, nil, NewYAMLValidator(&MockSchemaLoader{}), filepath.Join(root, "p"), "p1", "", "Prompt"); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
