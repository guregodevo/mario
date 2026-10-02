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
