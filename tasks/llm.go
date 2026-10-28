package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guregodevo/mario/templates"
)

// Completer is one model call, provided by the host: which model, what
// prompt, what came back. The engine knows no vendor; a host hands in its
// own client (an API key of its own, a local model, a gateway).
type Completer interface {
	Complete(ctx context.Context, model, prompt string) (string, error)
}

// LLM is the task type that makes one model call — `prompt:` rendered over
// args and partition, `model:` naming which — and keeps the answer as the
// output. No tools, no loop: one question, one answer, a proof. With a
// file target the answer is written to that file.
//
//	type: llm
//	model: fast
//	prompt: Summarize {{.topic}} in three lines.
func LLM(c Completer) Base {
	return Base{Type: "llm", SchemaDoc: []byte(llmSchema), Run: func(ctx context.Context, b *Base, d *templates.YamlTaskDefinition, name string) (string, error) {
		if c == nil {
			return "", fmt.Errorf("task %s: no model behind llm tasks on this host", name)
		}
		prompt, err := b.Prompt(d)
		if err != nil {
			return "", fmt.Errorf("task %s: prompt template: %w", name, err)
		}
		if strings.TrimSpace(prompt) == "" {
			return "", fmt.Errorf("task %s: an llm task needs a prompt", name)
		}
		answer, err := c.Complete(ctx, d.Model, prompt)
		if err != nil || d.Target == nil || d.Target.File == "" {
			return answer, err
		}
		// A file target is where the answer goes: the call has no tools to
		// write it, and the file is what the tasks after it read.
		path := b.renderOr(d, d.Target.File)
		if !filepath.IsAbs(path) {
			path = filepath.Join(b.Dir, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("task %s: %w", name, err)
		}
		if err := os.WriteFile(path, []byte(answer+"\n"), 0o644); err != nil {
			return "", fmt.Errorf("task %s: %w", name, err)
		}
		return answer, nil
	}}
}

const llmSchema = `{
  "description": "One model call; the answer is the output",
  "type": "object",
  "properties": {
    "type": {"enum": ["llm"]},
    "prompt": {"type": "string", "description": "The question. A Go template over args and partition."},
    "model": {"type": "string", "description": "Which model, as the host names them (default: the host's)."},
    "table_description": {"type": "string"},
    "dataset_description": {"type": "string"},
    "field_descriptions": {"type": "object"},
    "args": {"type": "object"},
    "requires": {"$ref": "#/definitions/requires"},
    "target": {"$ref": "#/definitions/target"},
    "timeout": {"type": "string"},
    "max_retries": {"type": "integer", "minimum": 0},
    "when": {"type": "string"}
  },
  "required": ["type", "prompt"],
  "additionalProperties": false,
  "definitions": ` + commonDefinitions + `
}`
