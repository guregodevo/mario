package tasks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guregodevo/mario/templates"
)

// Template is a task whose answer is its text, rendered: a Go template over args, outputs, statuses and
// files. With target.file the text is written there, and the file is the proof. A pull request's body, an
// issue's body, a report: what a shell script assembled with printf and awk, a workflow now declares.
func Template() Base {
	return Base{Type: "template", SchemaDoc: []byte(templateSchema), Run: runTemplate}
}

func runTemplate(ctx context.Context, b *Base, d *templates.YamlTaskDefinition, name string) (string, error) {
	if strings.TrimSpace(d.Text) == "" {
		return "", fmt.Errorf("task %s: a template task needs a text", name)
	}
	text, err := b.Render(d, d.Text)
	if err != nil {
		return "", fmt.Errorf("task %s: text template: %w", name, err)
	}
	if d.Target != nil && d.Target.File != "" {
		path, err := b.Render(d, d.Target.File)
		if err != nil {
			return "", fmt.Errorf("task %s: target template: %w", name, err)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(b.Dir, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return "", fmt.Errorf("task %s: %w", name, err)
		}
	}
	return text, nil
}

const templateSchema = `{
  "description": "A Go template rendered: its text is the output, and with target.file, that file",
  "type": "object",
  "properties": {
    "type": {"enum": ["template"]},
    "text": {"type": "string", "description": "A Go template over args, partition, outputs (output), statuses (status) and files (file)."},
    "table_description": {"type": "string"},
    "dataset_description": {"type": "string"},
    "field_descriptions": {"type": "object"},
    "args": {"type": "object"},
    "requires": {"$ref": "#/definitions/requires"},
    "target": {"$ref": "#/definitions/target"},
    "timeout": {"type": "string"},
    "max_retries": {"type": "integer", "minimum": 0},
    "optional": {"type": "boolean"},
    "when": {"type": "string"}
  },
  "required": ["type", "text"],
  "additionalProperties": false,
  "definitions": ` + commonDefinitions + `
}`
