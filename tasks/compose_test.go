package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/fileio"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/templates"
)

// composed writes workflows (name -> task -> YAML) under root, walks the one called top and composes it
// with the others as its siblings.
func composed(t *testing.T, root string, reg factory.Component, top string, workflows map[string]map[string]string) (map[string]*templates.YamlTaskDefinition, error) {
	t.Helper()
	for wf, files := range workflows {
		dir := filepath.Join(root, wf, "steps")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, y := range files {
			if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(y), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	walk := func(wf string) (map[string]*templates.YamlTaskDefinition, error) {
		if _, err := os.Stat(filepath.Join(root, wf)); err != nil {
			return nil, nil
		}
		err, defs := templates.Walk(&fileio.LocalFileIO{}, nil, factory.NewValidator(reg), filepath.Join(root, wf), "2026-10-02", "", "Prompt")
		return defs, err
	}
	found, err := walk(top)
	if err != nil {
		t.Fatal(err)
	}
	defs := map[string]*templates.YamlTaskDefinition{}
	for _, d := range found {
		defs[d.Name] = d
	}
	return defs, templates.YamlTaskDefinitions(defs).Compose(walk)
}

func commandTypes() ([]Base, factory.Component) {
	types := []Base{Command()}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	return types, reg
}

// The short form: dataset_id defaults to the requiring task's group.
const requireLowerMake = "  - project_id: lower\n    table_pattern: make\n"

// A requirement on another workflow's task pulls it, and what it requires, into the run as upstream. The
// other workflow's unrequired tasks stay out.
func TestComposePullsARequiredWorkflowInUpstream(t *testing.T) {
	root := t.TempDir()
	types, reg := commandTypes()
	defs, err := composed(t, root, reg, "upper", map[string]map[string]string{
		"lower": {
			"base":   "type: command\ncommand: echo base\n",
			"make":   "type: command\ncommand: echo made\nrequires:\n  - table_pattern: base\n",
			"unused": "type: command\ncommand: echo unused\n",
		},
		"upper": {"use": "type: command\ncommand: echo used\nrequires:\n" + requireLowerMake},
	})
	if err != nil {
		t.Fatal(err)
	}
	if defs["lower.steps.make"] == nil || defs["lower.steps.base"] == nil || defs["lower.steps.unused"] != nil {
		t.Fatalf("pulled %v", names(defs))
	}
	seq := strings.Join(runOn(t, root, types, defs, static.NewWorkflowRepository()), " | ")
	if !before(seq, "done base", "started make") || !before(seq, "done make", "started use") {
		t.Fatalf("not in dependency order: %s", seq)
	}
}

// The pulled workflow runs after what the requiring task requires in its own workflow, and with: binds the
// pulled task's arg to that task's output. The value is written the short way, {{ output "hand" }}, as in
// any task of the requiring workflow, and still reads that workflow's hand when the pulled task renders it.
func TestComposeOrdersThePulledWorkflowAndBindsWith(t *testing.T) {
	root := t.TempDir()
	types, reg := commandTypes()
	defs, err := composed(t, root, reg, "upper", map[string]map[string]string{
		"lower": {"make": "type: command\nargs:\n  dir: .\ncommand: echo made in {{.dir}}\n"},
		"upper": {
			"hand": "type: command\ncommand: printf /tmp/wt-7\n",
			"use":  "type: command\ncommand: echo used\nrequires:\n  - table_pattern: hand\n" + requireLowerMake + "    with:\n      dir: '{{ output \"hand\" }}'\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	seq := strings.Join(runOn(t, root, types, defs, static.NewWorkflowRepository()), " | ")
	if !before(seq, "done hand", "started make") {
		t.Fatalf("the pulled workflow did not wait for hand: %s", seq)
	}
	out, err := Outputs{Dir: filepath.Join(root, "out")}.Read("lower.steps.make", "2026-10-02")
	if err != nil || strings.TrimSpace(out) != "made in /tmp/wt-7" {
		t.Fatalf("lower.make ran with %q, %v", out, err)
	}
}

// One task that pulls two tasks of a workflow, one of them already upstream of its own requirement, is
// not a cycle: what its requirement needs stays ahead, the rest waits.
func TestComposeDoesNotMakeACycleOfASharedUpstream(t *testing.T) {
	root := t.TempDir()
	types, reg := commandTypes()
	defs, err := composed(t, root, reg, "upper", map[string]map[string]string{
		"lower": {
			"first":  "type: command\ncommand: echo first\n",
			"second": "type: command\ncommand: echo second\nrequires:\n  - table_pattern: first\n",
			"side":   "type: command\ncommand: echo side\n",
		},
		"upper": {
			"hand": "type: command\ncommand: echo hand\nrequires:\n  - project_id: lower\n    dataset_id: steps\n    table_pattern: second\n",
			"end":  "type: command\ncommand: echo end\nrequires:\n  - table_pattern: hand\n  - project_id: lower\n    dataset_id: steps\n    table_pattern: side\n",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	seq := strings.Join(runOn(t, root, types, defs, static.NewWorkflowRepository()), " | ")
	if !before(seq, "done second", "started hand") || !before(seq, "done hand", "started side") || !strings.Contains(seq, "done end") {
		t.Fatalf("order: %s", seq)
	}
}

// What Compose refuses or leaves alone: a with: key nobody declares, a task bound to two values, an
// external requirement, a workflow that is not there.
func TestComposeRefusesAndLeavesAlone(t *testing.T) {
	_, reg := commandTypes()
	if _, err := composed(t, t.TempDir(), reg, "upper", map[string]map[string]string{
		"lower": {"make": "type: command\nargs:\n  dir: .\ncommand: echo made in {{.dir}}\n"},
		"upper": {"use": "type: command\ncommand: echo used\nrequires:\n" + requireLowerMake + "    with:\n      dri: x\n"},
	}); err == nil || !strings.Contains(err.Error(), "binds dri, which no task of lower.steps.make declares as an arg (declared: dir)") {
		t.Fatalf("undeclared with: err %v", err)
	}
	if _, err := composed(t, t.TempDir(), reg, "upper", map[string]map[string]string{
		"lower": {"make": "type: command\nargs:\n  dir: .\ncommand: echo {{.dir}}\n"},
		"upper": {
			"a": "type: command\ncommand: echo a\nrequires:\n" + requireLowerMake + "    with:\n      dir: one\n",
			"b": "type: command\ncommand: echo b\nrequires:\n" + requireLowerMake + "    with:\n      dir: two\n",
		},
	}); err == nil || !strings.Contains(err.Error(), "lower.steps.make.dir is bound twice") {
		t.Fatalf("bound twice: err %v", err)
	}
	defs, err := composed(t, t.TempDir(), reg, "upper", map[string]map[string]string{
		"lower": {"make": "type: command\ncommand: echo made\n"},
		"upper": {"use": "type: command\ncommand: echo used\nrequires:\n" + requireLowerMake + "    external: true\n"},
	})
	if err != nil || defs["lower.steps.make"] != nil {
		t.Fatalf("an external requirement was pulled in: %v, %v", names(defs), err)
	}
	defs, err = composed(t, t.TempDir(), reg, "upper", map[string]map[string]string{
		"upper": {"use": "type: command\ncommand: echo used\nrequires:\n" + requireLowerMake},
	})
	if err != nil || len(defs) != 1 {
		t.Fatalf("a missing workflow: %v, %v", names(defs), err)
	}
}

func names(defs map[string]*templates.YamlTaskDefinition) []string {
	var out []string
	for k := range defs {
		out = append(out, k)
	}
	return out
}

func before(seq, a, b string) bool {
	i, j := strings.Index(seq, a), strings.Index(seq, b)
	return i >= 0 && j >= 0 && i < j
}

// Backward compatible: a workflow that names no other workflow, or only an external task of one, comes out
// of Compose as it went in, and no other workflow is even loaded.
func TestComposeLeavesAWorkflowThatNamesNoOtherAsItWas(t *testing.T) {
	root := t.TempDir()
	_, reg := commandTypes()
	dir := filepath.Join(root, "solo", "steps")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("type: command\nargs:\n  x: one\ncommand: echo {{.x}}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.yaml"), []byte("type: command\ncommand: echo b\nrequires:\n  - table_pattern: a\n  - project_id: other\n    table_pattern: gate\n    external: true\n"), 0o644)
	err, found := templates.Walk(&fileio.LocalFileIO{}, nil, factory.NewValidator(reg), filepath.Join(root, "solo"), "2026-10-02", "", "Prompt")
	if err != nil {
		t.Fatal(err)
	}
	defs := map[string]*templates.YamlTaskDefinition{}
	before := map[string]string{}
	for _, d := range found {
		defs[d.Name] = d
		b, _ := templates.YamlSerialize(d)
		before[d.Name] = string(b)
	}
	loaded := 0
	if err := templates.YamlTaskDefinitions(defs).Compose(func(string) (map[string]*templates.YamlTaskDefinition, error) {
		loaded++
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if loaded != 0 || len(defs) != len(before) {
		t.Fatalf("loaded %d other workflows, %d tasks (was %d)", loaded, len(defs), len(before))
	}
	for name, d := range defs {
		if b, _ := templates.YamlSerialize(d); string(b) != before[name] {
			t.Errorf("%s changed:\n%s\nwas\n%s", name, b, before[name])
		}
	}
}

// Good defaults: a requirement on another workflow with nothing else written works. No with:, no
// dataset_id, and the pulled task runs on its own arg default.
func TestComposeWorksWithNothingButTheRequirement(t *testing.T) {
	root := t.TempDir()
	types, reg := commandTypes()
	defs, err := composed(t, root, reg, "upper", map[string]map[string]string{
		"lower": {"make": "type: command\nargs:\n  dir: its-default\ncommand: echo made in {{.dir}}\n"},
		"upper": {"use": "type: command\ncommand: echo used\nrequires:\n  - project_id: lower\n    table_pattern: make\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seq := strings.Join(runOn(t, root, types, defs, static.NewWorkflowRepository()), " | "); !before(seq, "done make", "started use") {
		t.Fatalf("order: %s", seq)
	}
	if out, _ := (Outputs{Dir: filepath.Join(root, "out")}).Read("lower.steps.make", "2026-10-02"); strings.TrimSpace(out) != "made in its-default" {
		t.Fatalf("lower.make ran with %q", out)
	}
}
