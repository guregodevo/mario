package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
)

// Decider answers how likely it is that a condition holds of a state, as a
// probability in [0, 1]. Provided by the host, like Completer: the engine
// knows no decision model, only that one can be asked.
type Decider interface {
	Decide(ctx context.Context, condition, state string) (float64, error)
}

// Thresholder is a Decider that also names the threshold a decision task
// passes at when its file names none. Without it, DefaultDecisionThreshold.
type Thresholder interface {
	DefaultThreshold() float64
}

// DefaultDecisionThreshold is the probability a decision task passes at
// when neither its file nor the host says otherwise.
const DefaultDecisionThreshold = 0.7

// Score is a decision task's output: the probability, the threshold it was
// held to, whether it passed, and who answered — the decision model, or the
// task's `else` command when none did.
type Score struct {
	P         float64 `json:"p"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
	By        string  `json:"by"`
}

const (
	// ByDecision says a decision model answered.
	ByDecision = "decision"
	// ByElse says the task's `else` command answered: no decision model on
	// the host, or the one there did not answer in time.
	ByElse = "else"
)

// ParseScore reads a decision task's output.
func ParseScore(s string) (Score, error) {
	var sc Score
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &sc); err != nil {
		return Score{}, fmt.Errorf("not a decision task's output: %w", err)
	}
	return sc, nil
}

// Decision is the task type whose proof is a probability. `condition:` is
// a statement, `state:` the evidence it is judged on (a template over args,
// partition, outputs and files, like a prompt), `threshold:` the
// probability it passes at, and `else:` a shell line whose exit status
// answers when no decision model does — 0 is pass. A nil Decider is a valid
// host: every decision task then answers by its `else`, so a workflow that
// names the type never needs a decision model to run.
//
//	type: decision
//	condition: The change does what the task asks, completely and nothing else.
//	state: |
//	  {{ output "review" }}
//	threshold: 0.8
//	else: grep -q '^verdict: PASS' review.md
func Decision(d Decider) Base {
	return Base{Type: "decision", SchemaDoc: []byte(decisionSchema), Run: func(ctx context.Context, b *Base, def *templates.YamlTaskDefinition, name string) (string, error) {
		return runDecision(ctx, d, b, def, name)
	}}
}

func runDecision(ctx context.Context, dec Decider, b *Base, d *templates.YamlTaskDefinition, name string) (string, error) {
	if strings.TrimSpace(d.Condition) == "" {
		return "", fmt.Errorf("task %s: a decision task needs a condition", name)
	}
	if strings.TrimSpace(d.Else) == "" {
		return "", fmt.Errorf("task %s: a decision task needs an else command, the answer without a decision model", name)
	}
	threshold := d.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = DefaultDecisionThreshold
		if t, ok := dec.(Thresholder); ok && dec != nil {
			if v := t.DefaultThreshold(); v > 0 && v <= 1 {
				threshold = v
			}
		}
	}
	score := Score{Threshold: threshold}
	if dec != nil {
		p, err := b.decide(ctx, dec, d, name)
		if err == nil {
			score.P, score.Pass, score.By = p, p >= threshold, ByDecision
		} else {
			logger.Log.Info(fmt.Sprintf("Task %s: the decision model did not answer (%s); its else command decides", name, err), "component", "workflow")
		}
	}
	if score.By == "" {
		pass, err := b.decideByElse(ctx, d, name)
		if err != nil {
			return "", err
		}
		score.Pass, score.By = pass, ByElse
		if pass {
			score.P = 1
		}
	}
	out, err := json.Marshal(score)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", name, err)
	}
	answer := string(out)
	if d.Target == nil || d.Target.File == "" {
		return answer, nil
	}
	// A file target is where the score goes, as an llm task's answer goes:
	// the task has no other way to write it, and the file is what a
	// command after it reads.
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
}

// decide asks the host's decision model, with the condition and the state
// rendered over the run.
func (b *Base) decide(ctx context.Context, dec Decider, d *templates.YamlTaskDefinition, name string) (float64, error) {
	condition, err := b.Render(d, d.Condition)
	if err != nil {
		return 0, fmt.Errorf("condition template: %w", err)
	}
	state, err := b.Render(d, d.State)
	if err != nil {
		return 0, fmt.Errorf("state template: %w", err)
	}
	p, err := dec.Decide(ctx, condition, state)
	if err != nil {
		return 0, err
	}
	if p < 0 || p > 1 {
		return 0, fmt.Errorf("probability %v is not in [0, 1]", p)
	}
	return p, nil
}

// decideByElse runs the task's `else` line in the run's directory: exit 0
// is pass, anything else is fail. An error starting it is the task's.
func (b *Base) decideByElse(ctx context.Context, d *templates.YamlTaskDefinition, name string) (bool, error) {
	line, err := b.Render(d, d.Else)
	if err != nil {
		return false, fmt.Errorf("task %s: else template: %w", name, err)
	}
	cmd := shellCommand(ctx, b.Dir, line, b.Env)
	killGroupOnCancel(cmd)
	err = cmd.Run()
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, fmt.Errorf("task %s: else command stopped: %w", name, ctx.Err())
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return false, nil
	}
	return false, fmt.Errorf("task %s: else command could not run: %w", name, err)
}

const decisionSchema = `{
  "description": "A decision model scores a condition on evidence; the output is the probability, pass or fail at a threshold, and who answered",
  "type": "object",
  "properties": {
    "type": {"enum": ["decision"]},
    "condition": {"type": "string", "description": "The statement the decision model scores, true or false of the state. A Go template over args and partition."},
    "state": {"type": "string", "description": "The evidence the condition is judged on: a template over args, partition, outputs ({{ output \"task\" }}) and files ({{ file \"x\" }})."},
    "threshold": {"type": "number", "exclusiveMinimum": 0, "maximum": 1, "description": "The probability the task passes at (default: the host's, else 0.7)."},
    "else": {"type": "string", "description": "A shell line run in the project when no decision model answers: exit 0 is pass. A Go template like the command."},
    "table_description": {"type": "string"},
    "dataset_description": {"type": "string"},
    "field_descriptions": {"type": "object"},
    "args": {"type": "object"},
    "requires": {"$ref": "#/definitions/requires"},
    "target": {"$ref": "#/definitions/target"},
    "timeout": {"type": "string"},
    "max_retries": {"type": "integer", "minimum": 0},
    "optional": {"type": "boolean", "description": "Failed past its retries, the task is passed over: what requires it still runs, with an empty output in its place."},
    "when": {"type": "string"}
  },
  "required": ["type", "condition", "state", "else"],
  "additionalProperties": false,
  "definitions": ` + commonDefinitions + `
}`
