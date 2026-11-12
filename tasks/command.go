package tasks

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
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
	cmd := shellCommand(ctx, b.Dir, line, b.Env)
	// Cancelling must end everything the line started, not only the shell:
	// a killed sh left its `sleep 60` holding stdout, and Run waited the
	// full minute for a stopped run (a test, 2026-10-04).
	killGroupOnCancel(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		// The status and what the command printed, not the command: it is
		// in the step's file, and a window that showed it, cut short, read
		// as if the workflow's files were missing (memdoor #128).
		return "", fmt.Errorf("task %s: %w%s", name, err, prefixed(msg))
	}
	return out.String(), nil
}

// maxArgLine is the longest line passed to /bin/sh as its -c argument. Linux
// caps one argument at 131072 bytes (MAX_ARG_STRLEN) and fork/exec fails past
// it with "argument list too long", before the shell starts.
const maxArgLine = 100_000

// shellCommand is line run by /bin/sh in dir, with the environment env
// answers, or this process's when env is nil: every command a task runs (its
// `command`, its `when`, its command target) is built here. A line over
// maxArgLine goes to `sh -s` on stdin instead; a command in it that reads
// stdin reads the rest of the line.
func shellCommand(ctx context.Context, dir, line string, env func() []string) *exec.Cmd {
	var cmd *exec.Cmd
	if len(line) > maxArgLine {
		cmd = exec.CommandContext(ctx, posixShell(), "-s")
		cmd.Stdin = strings.NewReader(line)
	} else {
		cmd = exec.CommandContext(ctx, posixShell(), "-c", line)
	}
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env()
	}
	return cmd
}

// posixShell is /bin/sh, or on Windows the sh on the PATH (Git for Windows
// ships one): a Windows process has no /bin/sh to start.
func posixShell() string {
	if runtime.GOOS != "windows" {
		return "/bin/sh"
	}
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	return "sh"
}

// shown is line as a message names it: the first 200 bytes, then its length.
func shown(line string) string {
	const max = 200
	if len(line) <= max {
		return line
	}
	return fmt.Sprintf("%s… (%d bytes)", strings.ToValidUTF8(line[:max], ""), len(line))
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
    "max_retries": {"type": "integer", "minimum": 0},
    "optional": {"type": "boolean", "description": "Failed past its retries, the task is passed over: what requires it still runs, with an empty output from it."},
    "when": {"type": "string"}
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
