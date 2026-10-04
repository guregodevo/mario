package tasks

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/guregodevo/mario/templates"
)

// Command is the task type that runs a shell line in the run's directory —
// `command:` in the file, a template over args and partition — and keeps
// its stdout as the output. Deterministic work (a build, a fetch, a script,
// a browser driven from the shell) is a command task.
//
//	type: command
//	command: git log --oneline -8
func Command() Base {
	return Base{Type: "command", SchemaDoc: []byte(commandSchema), Run: runCommand}
}

func runCommand(ctx context.Context, b *Base, d *templates.YamlTaskDefinition, name string) (string, error) {
	if strings.TrimSpace(d.Command) == "" {
		return "", fmt.Errorf("task %s: a command task needs a command", name)
	}
	line, err := b.Render(d, d.Command)
	if err != nil {
		return "", fmt.Errorf("task %s: command template: %w", name, err)
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", line)
	cmd.Dir = b.Dir
	// Cancelling must end everything the line started, not only the shell:
	// a killed sh left its `sleep 60` holding stdout, and Run waited the
	// full minute for a stopped run (a Memdoor test, 2026-10-04).
	killGroupOnCancel(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("task %s: %s: %w%s", name, line, err, prefixed(msg))
	}
	return out.String(), nil
}

func prefixed(msg string) string {
	if msg == "" {
		return ""
	}
	return " — " + msg
}

const commandSchema = `{
  "description": "A shell line run in the project; its stdout is the output",
  "type": "object",
  "properties": {
    "type": {"enum": ["command"]},
    "command": {"type": "string", "description": "What to run, with /bin/sh. A Go template over args and partition."},
    "table_description": {"type": "string"},
    "dataset_description": {"type": "string"},
    "field_descriptions": {"type": "object"},
    "args": {"type": "object"},
    "requires": {"$ref": "#/definitions/requires"},
    "target": {"$ref": "#/definitions/target"},
    "timeout": {"type": "string"},
    "max_retries": {"type": "integer", "minimum": 0}
  },
  "required": ["type", "command"],
  "additionalProperties": false,
  "definitions": ` + commonDefinitions + `
}`

// commonDefinitions is what every type's schema shares: requirements and a target.
const commonDefinitions = `{
    "requires": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "table_pattern": {"type": "string", "pattern": "^[a-zA-Z0-9_-]+$", "description": "The task required, by name."},
          "project_id": {"type": "string", "description": "Another workflow (default: this one)."},
          "dataset_id": {"type": "string", "description": "Another group (default: this task's)."},
          "external": {"type": "boolean", "description": "Made outside this workflow: never run, only checked."}
        },
        "required": ["table_pattern"],
        "additionalProperties": false
      }
    },
    "target": {
      "type": "object",
      "description": "What proves the task done; without one, its output is the proof.",
      "properties": {
        "file": {"type": "string", "description": "A file that must exist under the project."},
        "command": {"type": "string", "description": "A command that must exit 0 in the project."}
      },
      "additionalProperties": false
    }
  }`
