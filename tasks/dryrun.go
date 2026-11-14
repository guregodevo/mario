package tasks

import (
	"fmt"
	"strings"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

// A dry run walks the DAG and runs nothing (Base.DryRun, as mario-llm's
// LLMComponent.DryRun): each task's text is rendered and logged, no command
// runs and no model is called. The task's answer is what it would have run,
// and that answer is its proof, not its target, which nothing made. An
// external is taken as made, so the walk never waits at a gate.
//
// A text that reads what an earlier task makes at run time (a file it
// writes, a JSON answer) may not render: that is said in the answer and the
// walk goes on. What does not render whatever the run, the compile refuses
// before anything starts.

// dry is the task's answer in a dry run: each of its texts, rendered.
func (b *Base) dry(d *templates.YamlTaskDefinition, name string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "dry run of %s: nothing ran\n", name)
	texts := []struct{ key, text string }{
		{"when", d.When}, {"system", d.System}, {"command", d.Command}, {"text", d.Text},
		{"condition", d.Condition}, {"state", d.State}, {"else", d.Else},
	}
	if d.Target != nil {
		texts = append(texts, struct{ key, text string }{"target.file", d.Target.File}, struct{ key, text string }{"target.command", d.Target.Command})
	}
	if strings.TrimSpace(d.Prompt) != "" {
		prompt, err := b.Prompt(d)
		writeDry(&out, "prompt", prompt, err)
	}
	for _, t := range texts {
		if strings.TrimSpace(t.text) == "" {
			continue
		}
		rendered, err := b.Render(d, t.text)
		writeDry(&out, t.key, rendered, err)
	}
	logger.Log.Info(strings.TrimRight(out.String(), "\n"), "component", "workflow")
	return out.String()
}

func writeDry(out *strings.Builder, key, rendered string, err error) {
	if err != nil {
		fmt.Fprintf(out, "%s: does not render yet: %v\n", key, err)
		return
	}
	fmt.Fprintf(out, "%s:\n%s\n", key, strings.TrimRight(rendered, "\n"))
}

// dryEndpoint is a task's proof in a dry run: its answer for a task of this
// run, made for an external.
func (b *Base) dryEndpoint(name string) workflow.DataEndpoint {
	if _, defined := b.Defs[name]; defined {
		return b.Outputs.Endpoint(name, b.Partition)
	}
	return madeEndpoint(name)
}

type madeEndpoint string

func (m madeEndpoint) Name() string { return string(m) }
func (m madeEndpoint) Exists() bool { return true }
