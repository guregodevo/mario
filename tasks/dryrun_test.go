package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

// A dry run walks every task and runs none: the command writes no file, the
// model is never called, a target nothing made does not fail its task, the
// gate is not waited at, and each answer is the task's rendered text. A text
// that reads what a run would have made says it does not render yet.
// Mutation check: drop the DryRun branch in Fn and SIDE.txt is written and
// the model called.
func TestADryRunWalksEveryTaskAndRunsNone(t *testing.T) {
	root := t.TempDir()
	model := &echoModel{}
	types := []Base{Command(), LLM(model), Template()}
	reg := factory.NewComponent()
	for i := range types {
		types[i].DryRun = true
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"build":  "type: command\ncommand: echo {{.partition}} > SIDE.txt\ntarget:\n  file: SIDE.txt\n",
		"review": "type: llm\nmodel: fast\nprompt: 'review {{ output \"build\" | trim }}'\nrequires:\n  - table_pattern: build\n  - table_pattern: approve\n    external: true\n",
		"notes":  "type: template\ntext: '{{ file \"NOTES.md\" }}'\nrequires:\n  - table_pattern: review\n",
	})
	seq := strings.Join(run(t, root, types, defs), " | ")
	for _, want := range []string{"done build", "done review", "done notes"} {
		if !strings.Contains(seq, want) {
			t.Errorf("%q missing from %q", want, seq)
		}
	}
	if strings.Contains(seq, "failed") {
		t.Errorf("a dry run fails nothing: %q", seq)
	}
	if _, err := os.Stat(filepath.Join(root, "SIDE.txt")); err == nil {
		t.Error("the command ran: SIDE.txt was written")
	}
	if len(model.calls) != 0 {
		t.Errorf("the model was called: %v", model.calls)
	}
	out := Outputs{Dir: filepath.Join(root, "out")}
	build, _ := out.Read("wf.steps.build", "2026-10-02")
	if !strings.Contains(build, "dry run of wf.steps.build: nothing ran") || !strings.Contains(build, "command:\necho 2026-10-02 > SIDE.txt") || !strings.Contains(build, "target.file:\nSIDE.txt") {
		t.Errorf("build's answer:\n%s", build)
	}
	review, _ := out.Read("wf.steps.review", "2026-10-02")
	if !strings.Contains(review, "prompt:\nreview dry run of wf.steps.build") {
		t.Errorf("review's prompt reads build's dry answer:\n%s", review)
	}
	notes, _ := out.Read("wf.steps.notes", "2026-10-02")
	if !strings.Contains(notes, "text: does not render yet:") || !strings.Contains(notes, "NOTES.md") {
		t.Errorf("notes' answer:\n%s", notes)
	}
}

// Off, nothing changes: the same DAG's command runs and its target is its proof.
func TestWithoutDryRunATaskRunsAndIsProvenByItsTarget(t *testing.T) {
	root := t.TempDir()
	types := []Base{Command()}
	reg := factory.NewComponent()
	reg.Add(&types[0])
	defs := writeDAG(t, root, reg, map[string]string{
		"build": "type: command\ncommand: echo made > SIDE.txt\ntarget:\n  file: SIDE.txt\n",
	})
	if seq := strings.Join(run(t, root, types, defs), " | "); !strings.Contains(seq, "done build") {
		t.Fatalf("events = %q", seq)
	}
	if _, err := os.Stat(filepath.Join(root, "SIDE.txt")); err != nil {
		t.Fatal("the command ran and made its target")
	}
}
