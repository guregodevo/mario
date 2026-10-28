package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

// A task with a `when` whose condition does not hold is skipped as done with
// an empty answer: its model is never called, and the task after it reads
// the empty output without failing. One whose condition holds runs as usual.
func TestATaskRunsOnlyWhenItsConditionHolds(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	if err := os.WriteFile(filepath.Join(root, "status.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"worker": "type: llm\nmodel: fast\nprompt: keep working\nwhen: '! grep -qx done status.txt'\n",
		"land":   "type: command\ncommand: |\n  printf 'got [%s]' \"$(cat <<'MARIO_EOF'\n  {{ output \"worker\" }}\n  MARIO_EOF\n  )\"\nrequires:\n  - table_pattern: worker\n",
		"finish": "type: llm\nmodel: fast\nprompt: finish up\nwhen: grep -qx done status.txt\nrequires:\n  - table_pattern: land\n",
	})
	seq := run(t, root, types, defs)
	if got := strings.Join(seq, " | "); got != "started worker | done worker | started land | done land | started finish | done finish" {
		t.Fatalf("events = %q", got)
	}
	out := Outputs{Dir: filepath.Join(root, "out")}
	if got, _ := out.Read("wf.steps.worker", "2026-10-02"); got != "" {
		t.Fatalf("the skipped worker's output = %q, want empty", got)
	}
	if got, _ := out.Read("wf.steps.land", "2026-10-02"); got != "got []" {
		t.Fatalf("the task after the skipped one read %q", got)
	}
	if got, _ := out.Read("wf.steps.finish", "2026-10-02"); got != "answer to finish up" {
		t.Fatalf("the task whose condition holds answered %q", got)
	}
	if len(model.calls) != 1 || !strings.Contains(model.calls[0], "finish up") {
		t.Fatalf("the model was called for %v; only the finish should call it", model.calls)
	}
}

// A model call reads a file a command before it wrote ({{ file }}), and an
// llm task with a file target writes its answer there: the call has no tools,
// and the file is the proof the task after it reads.
func TestAnLLMTaskReadsAFileAndWritesItsTarget(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"diff":   "type: command\ncommand: mkdir -p runs && printf -- '-a\\n+b\\n' > runs/{{.partition}}.diff\n",
		"review": "type: llm\nprompt: 'review {{ file (printf \"runs/%s.diff\" .partition) }}'\nrequires:\n  - table_pattern: diff\ntarget:\n  file: runs/{{.partition}}.md\n",
		"gate":   "type: command\ncommand: grep -q 'review -a' runs/{{.partition}}.md\nrequires:\n  - table_pattern: review\n",
	})
	seq := run(t, root, types, defs)
	if got := strings.Join(seq, " | "); got != "started diff | done diff | started review | done review | started gate | done gate" {
		t.Fatalf("events = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "runs", "2026-10-02.md"))
	if err != nil || string(data) != "answer to review -a\n+b\n\n" {
		t.Fatalf("the target holds %q (%v)", data, err)
	}
	if len(model.calls) != 1 || !strings.Contains(model.calls[0], "review -a\n+b") {
		t.Fatalf("the model was asked %q", model.calls)
	}
}
