package tasks

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

// systemModel takes a system message apart from the prompt.
type systemModel struct{ system, prompt string }

func (m *systemModel) Complete(_ context.Context, _, prompt string) (string, error) {
	m.prompt = prompt
	return "plain", nil
}

func (m *systemModel) CompleteWithSystem(_ context.Context, _, system, prompt string) (string, error) {
	m.system, m.prompt = system, prompt
	return "with system", nil
}

// An llm task's system message is rendered like its prompt and sent apart
// from it when the host takes one; a host that does not gets it ahead of the
// prompt. GVS5H's harness sends every role as a system message and the
// problem as the user message.
func TestAnLLMTaskSendsItsSystemMessageApart(t *testing.T) {
	root := t.TempDir()
	model := &systemModel{}
	types := []Base{LLM(model)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"plan": "type: llm\nsystem: You plan {{.what}}.\nprompt: the problem\nargs:\n  what: the team\n",
	})
	run(t, root, types, defs)
	if model.system != "You plan the team." || model.prompt != "the problem" {
		t.Fatalf("system %q, prompt %q", model.system, model.prompt)
	}
	out := Outputs{Dir: filepath.Join(root, "out")}
	if got, _ := out.Read("wf.steps.plan", "2026-10-02"); got != "with system" {
		t.Fatalf("output %q", got)
	}

	root = t.TempDir()
	plain := &echoModel{}
	types = []Base{LLM(plain)}
	reg = factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs = writeDAG(t, root, reg, map[string]string{
		"plan": "type: llm\nsystem: You plan.\nprompt: the problem\n",
	})
	run(t, root, types, defs)
	if len(plain.calls) != 1 || !strings.Contains(plain.calls[0], "You plan.\n\nthe problem") {
		t.Fatalf("a host without system messages got %v", plain.calls)
	}
}
