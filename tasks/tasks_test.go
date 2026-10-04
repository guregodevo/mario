package tasks

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guregodevo/mario/engine"
	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/fileio"
	"github.com/guregodevo/mario/scheduler"
	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/templates"
)

type echoModel struct{ calls []string }

func (m *echoModel) Complete(_ context.Context, model, prompt string) (string, error) {
	m.calls = append(m.calls, model+": "+prompt)
	return "answer to " + prompt, nil
}

// writeDAG lays out <root>/<project>/<group>/<task>.yaml and walks it with the
// validator built from the registered types.
func writeDAG(t *testing.T, root string, reg factory.Component, files map[string]string) map[string]*templates.YamlTaskDefinition {
	t.Helper()
	dir := filepath.Join(root, "wf", "steps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, y := range files {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(y), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err, defs := templates.Walk(&fileio.LocalFileIO{}, nil, factory.NewValidator(reg), filepath.Join(root, "wf"), "2026-10-02", "", "Prompt")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*templates.YamlTaskDefinition{}
	for _, d := range defs {
		byName[d.Name] = d
	}
	return byName
}

// run binds every registered type to the definitions and runs the DAG to
// completion, answering the ordered events.
func run(t *testing.T, root string, types []Base, defs map[string]*templates.YamlTaskDefinition) []string {
	t.Helper()
	reg := factory.NewComponent()
	outputs := Outputs{Dir: filepath.Join(root, "out")}
	for _, b := range types {
		reg.Add(b.Bind(defs, root, outputs, "2026-10-02", "v"))
	}
	repo := static.NewWorkflowRepository()
	ids, err := factory.BuildDAG("v", "2026-10-02", "c", repo, defs, reg)
	if err != nil {
		t.Fatal(err)
	}
	s := scheduler.NewLocalScheduler(true, true, static.NewChannelQueue(32), static.NewChannelQueue(32), repo, Mixed(reg, defs), engine.NewLocalExecutor(), 10*time.Millisecond)
	ch := make(chan scheduler.Event, 64)
	s.SetEvents(ch)
	var seq []string
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			line := string(ev.Kind) + " " + ev.Name[strings.LastIndex(ev.Name, ".")+1:]
			if ev.Err != nil {
				line += " (" + ev.Err.Error() + ")"
			}
			seq = append(seq, line)
		}
		close(done)
	}()
	required := map[string]bool{}
	for _, d := range defs {
		for _, r := range d.Requires {
			required[r.Name()] = true
		}
	}
	for name, id := range ids {
		if !required[name] && !id.DExternal {
			if err := s.Trigger(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.WaitForCompletion()
	<-done
	return seq
}

func TestCommandAndLLMTasksRunFromYAMLInOrder(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"log":     "type: command\ncommand: printf 'a\\nb\\n'\n",
		"summary": "type: llm\nmodel: fast\nprompt: summarize {{.what}} on {{.partition}}\nargs:\n  what: the log\nrequires:\n  - table_pattern: log\n",
	})
	seq := run(t, root, types, defs)
	if got := strings.Join(seq, " | "); got != "started log | done log | started summary | done summary" {
		t.Fatalf("events = %q", got)
	}
	out := Outputs{Dir: filepath.Join(root, "out")}
	if got, _ := out.Read("wf.steps.log", "2026-10-02"); got != "a\nb\n" {
		t.Fatalf("command output = %q", got)
	}
	if got, _ := out.Read("wf.steps.summary", "2026-10-02"); got != "answer to summarize the log on 2026-10-02" {
		t.Fatalf("llm output = %q", got)
	}
	if len(model.calls) != 1 || !strings.HasPrefix(model.calls[0], "fast: ") {
		t.Fatalf("model calls = %v", model.calls)
	}
}

func TestATaskWithATargetIsProvenByIt(t *testing.T) {
	root := t.TempDir()
	types := []Base{Command()}
	reg := factory.NewComponent()
	reg.Add(&types[0])
	defs := writeDAG(t, root, reg, map[string]string{
		"make": "type: command\ncommand: echo made > MADE.txt\ntarget:\n  file: MADE.txt\n",
		"lie":  "type: command\ncommand: echo nothing\ntarget:\n  file: NEVER.txt\n",
	})
	seq := strings.Join(run(t, root, types, defs), " | ")
	if !strings.Contains(seq, "done make") || !strings.Contains(seq, "failed lie") {
		t.Fatalf("events = %q", seq)
	}
	if _, err := os.Stat(filepath.Join(root, "MADE.txt")); err != nil {
		t.Fatal("the target was made")
	}
}

func TestTheValidatorIsBuiltFromTheRegisteredTypes(t *testing.T) {
	reg := factory.NewComponent()
	c := Command()
	reg.Add(&c)
	v := factory.NewValidator(reg)
	if err, _ := templates.YamlParse(v, []byte("type: llm\nprompt: x\n")); err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("an unregistered type must be refused, naming the registered ones: %v", err)
	}
	if err, _ := templates.YamlParse(v, []byte("type: command\ncommand: ls\ncolour: red\n")); err == nil || !strings.Contains(err.Error(), "colour") {
		t.Fatalf("a key the type's schema does not know must be refused: %v", err)
	}
	if err, _ := templates.YamlParse(v, []byte("type: command\ncommand: ls\n")); err != nil {
		t.Fatal(err)
	}
}

// A DAG walked once is run on any day: the prompt renders over the run's
// partition, not the one the walk happened to have.
func TestPromptRendersOverTheBoundPartition(t *testing.T) {
	d := &templates.YamlTaskDefinition{Prompt: "on {{.partition}}", Args: map[string]string{}}
	d.Name = "p.d.t"
	d.Partition = ""
	d.LazyRenderedField = func() (string, error) { return "on ", nil }
	b := Base{}.Bind(nil, "", Outputs{}, "2026-10-02", "v")
	if got, _ := b.Prompt(d); got != "on 2026-10-02" {
		t.Fatalf("prompt = %q", got)
	}
}

// An external that another DAG defines is checked by that definition's
// target, not by the output convention.
func TestAnExternalKnownElsewhereIsCheckedByItsTarget(t *testing.T) {
	root := t.TempDir()
	other := &templates.YamlTaskDefinition{Target: &templates.YamlTarget{File: "CHANGELOG.md"}}
	other.Name = "release.steps.changelog"
	b := Command().Bind(map[string]*templates.YamlTaskDefinition{}, root, Outputs{Dir: filepath.Join(root, "out")}, "2026-10-02", "v")
	b.Externals = map[string]*templates.YamlTaskDefinition{other.Name: other}
	if b.NewDataEndpoint(other.Name).Exists() {
		t.Fatal("no file, no proof")
	}
	os.WriteFile(filepath.Join(root, "CHANGELOG.md"), []byte("x"), 0o644)
	if !b.NewDataEndpoint(other.Name).Exists() {
		t.Fatal("the sibling's file is the proof")
	}
}

// A prompt reads what the task before it produced: {{ output "log" }} is the
// kept output of that task, for this run's partition.
func TestAPromptReadsAnUpstreamOutput(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"log":     "type: command\ncommand: printf 'c1\\nc2\\n'\n",
		"summary": "type: llm\nprompt: \"summarize: {{ output \\\"log\\\" }}\"\nrequires:\n  - table_pattern: log\n",
	})
	seq := strings.Join(run(t, root, types, defs), " | ")
	if !strings.HasSuffix(seq, "done summary") {
		t.Fatalf("events = %q", seq)
	}
	if len(model.calls) != 1 || model.calls[0] != ": summarize: c1\nc2\n" {
		t.Fatalf("the prompt carried the upstream output: %q", model.calls)
	}
}

// A task with a target is proven by the target, and its answer is still
// kept: a later prompt can read it with {{ output "name" }}.
func TestATargetedTasksAnswerIsReadableDownstream(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"fetch":   "type: command\ncommand: echo rows=300 > ROWS.txt && echo fetched 300 rows\ntarget:\n  file: ROWS.txt\n",
		"summary": "type: llm\nprompt: \"report: {{ output \\\"fetch\\\" }}\"\nrequires:\n  - table_pattern: fetch\n",
	})
	seq := strings.Join(run(t, root, types, defs), " | ")
	if !strings.HasSuffix(seq, "done summary") {
		t.Fatalf("events = %q", seq)
	}
	if len(model.calls) != 1 || !strings.Contains(model.calls[0], "fetched 300 rows") {
		t.Fatalf("the targeted task's answer reached the prompt: %q", model.calls)
	}
}

// A target carries the partition when each run must make its own.
func TestATargetRendersThePartition(t *testing.T) {
	root := t.TempDir()
	d := &templates.YamlTaskDefinition{Target: &templates.YamlTarget{File: "DIGEST-{{.partition}}.md"}}
	d.Name = "w.steps.digest"
	b := Command().Bind(map[string]*templates.YamlTaskDefinition{d.Name: d}, root, Outputs{Dir: filepath.Join(root, "out")}, "2026-10-02T1317", "v")
	if b.NewDataEndpoint(d.Name).Exists() {
		t.Fatal("not yet")
	}
	os.WriteFile(filepath.Join(root, "DIGEST-2026-10-02T1317.md"), []byte("x"), 0o644)
	if !b.NewDataEndpoint(d.Name).Exists() {
		t.Fatal("the partition's file is the proof")
	}
}

// Mixed routes each task to the factory of its type; a name with no
// definition (an external) goes to the first registered type, whose output
// convention is shared.
func TestMixedRoutesByTypeAndExternalsToTheFirst(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"log":     "type: command\ncommand: echo hi\n",
		"summary": "type: llm\nprompt: sum\nrequires:\n  - table_pattern: log\n  - table_pattern: gate\n    external: true\n",
	})
	bound := factory.NewComponent()
	outputs := Outputs{Dir: filepath.Join(root, "out")}
	for _, b := range types {
		bound.Add(b.Bind(defs, root, outputs, "2026-10-02", "v"))
	}
	m := Mixed(bound, defs)
	if err := m.Fn("wf.steps.log")(context.Background()); err != nil {
		t.Fatalf("the command type ran log: %v", err)
	}
	if err := m.Fn("wf.steps.summary")(context.Background()); err != nil {
		t.Fatalf("the llm type ran summary: %v", err)
	}
	if len(model.calls) != 1 {
		t.Fatalf("model calls = %v", model.calls)
	}
	if m.NewDataEndpoint("wf.steps.gate").Exists() {
		t.Fatal("an external's proof is the shared output convention: not there yet")
	}
	outputs.Write("wf.steps.gate", "2026-10-02", "yes")
	if !m.NewDataEndpoint("wf.steps.gate").Exists() {
		t.Fatal("… and there once written")
	}
	if err := m.Fn("wf.steps.gate")(context.Background()); err == nil {
		t.Fatal("an external is never run")
	}
}

func TestOutputsRoundTrip(t *testing.T) {
	o := Outputs{Dir: t.TempDir()}
	if o.Path("w.g.t", "p") != filepath.Join(o.Dir, "w", "g", "t", "p") {
		t.Fatalf("path = %q", o.Path("w.g.t", "p"))
	}
	if err := o.Write("w.g.t", "p", "data"); err != nil {
		t.Fatal(err)
	}
	if got, _ := o.Read("w.g.t", "p"); got != "data" || !o.Endpoint("w.g.t", "p").Exists() {
		t.Fatalf("read %q", got)
	}
	if o.Endpoint("w.g.other", "p").Exists() {
		t.Fatal("another task's output is not there")
	}
}
