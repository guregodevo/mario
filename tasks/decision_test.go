package tasks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

// fakeDecider answers one probability, or an error, and keeps what it was asked.
type fakeDecider struct {
	p         float64
	err       error
	threshold float64
	condition string
	state     string
}

func (f *fakeDecider) Decide(_ context.Context, condition, state string) (float64, error) {
	f.condition, f.state = condition, state
	return f.p, f.err
}

func (f *fakeDecider) DefaultThreshold() float64 { return f.threshold }

// decisionDAG is a command whose output a decision task scores: the review
// writes its verdict, the score judges it, with grep as the else.
func decisionDAG(extra string) map[string]string {
	return map[string]string{
		"review": "type: command\ncommand: |\n  printf 'verdict: PASS\\nlooks right\\n'\n",
		"score": "type: decision\ncondition: The change does what {{.what}} asks.\nstate: |\n  {{ output \"review\" }}\n" +
			"else: |\n  grep -q '^verdict: PASS' out/wf/steps/review/2026-10-02\nargs:\n  what: the task\nrequires:\n  - table_pattern: review\n" + extra,
	}
}

func runDecisionDAG(t *testing.T, dec Decider, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	types := []Base{Command(), Decision(dec)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, files)
	seq := run(t, root, types, defs)
	out, _ := Outputs{Dir: filepath.Join(root, "out")}.Read("wf.steps.score", "2026-10-02")
	return out, seq
}

func TestADecisionTaskScoresAnUpstreamOutput(t *testing.T) {
	dec := &fakeDecider{p: 0.9}
	out, seq := runDecisionDAG(t, dec, decisionDAG("threshold: 0.8\n"))
	if got := strings.Join(seq, " | "); got != "started review | done review | started score | done score" {
		t.Fatalf("events = %q", got)
	}
	sc, err := ParseScore(out)
	if err != nil {
		t.Fatal(err)
	}
	if sc.P != 0.9 || sc.Threshold != 0.8 || !sc.Pass || sc.By != ByDecision {
		t.Fatalf("score = %+v", sc)
	}
	if dec.condition != "The change does what the task asks." {
		t.Fatalf("condition rendered = %q", dec.condition)
	}
	if !strings.Contains(dec.state, "verdict: PASS") {
		t.Fatalf("state should carry the review's output, got %q", dec.state)
	}
}

func TestADecisionBelowTheThresholdFails(t *testing.T) {
	out, _ := runDecisionDAG(t, &fakeDecider{p: 0.75}, decisionDAG("threshold: 0.8\n"))
	sc, _ := ParseScore(out)
	if sc.Pass || sc.P != 0.75 || sc.By != ByDecision {
		t.Fatalf("score = %+v", sc)
	}
}

func TestTheThresholdIsTheFilesThenTheHostsThenTheDefault(t *testing.T) {
	out, _ := runDecisionDAG(t, &fakeDecider{p: 0.75, threshold: 0.6}, decisionDAG(""))
	sc, _ := ParseScore(out)
	if sc.Threshold != 0.6 || !sc.Pass {
		t.Fatalf("host default should apply: %+v", sc)
	}
	out, _ = runDecisionDAG(t, &fakeDecider{p: 0.75}, decisionDAG(""))
	sc, _ = ParseScore(out)
	if sc.Threshold != DefaultDecisionThreshold || !sc.Pass {
		t.Fatalf("engine default should apply: %+v", sc)
	}
}

func TestWithoutADeciderTheElseCommandAnswers(t *testing.T) {
	out, seq := runDecisionDAG(t, nil, decisionDAG(""))
	if got := strings.Join(seq, " | "); got != "started review | done review | started score | done score" {
		t.Fatalf("events = %q", got)
	}
	sc, err := ParseScore(out)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Pass || sc.P != 1 || sc.By != ByElse || sc.Threshold != DefaultDecisionThreshold {
		t.Fatalf("score = %+v", sc)
	}
}

func TestAnElseThatExitsNonZeroIsAFail(t *testing.T) {
	files := decisionDAG("")
	files["review"] = "type: command\ncommand: |\n  printf 'verdict: FAIL\\n'\n"
	out, _ := runDecisionDAG(t, nil, files)
	sc, _ := ParseScore(out)
	if sc.Pass || sc.P != 0 || sc.By != ByElse {
		t.Fatalf("score = %+v", sc)
	}
}

func TestADeciderThatErrsFallsBackToElse(t *testing.T) {
	out, _ := runDecisionDAG(t, &fakeDecider{err: errors.New("no model")}, decisionDAG("threshold: 0.8\n"))
	sc, _ := ParseScore(out)
	if !sc.Pass || sc.By != ByElse || sc.Threshold != 0.8 {
		t.Fatalf("score = %+v", sc)
	}
}

func TestADecisionWithAFileTargetWritesTheScoreThere(t *testing.T) {
	root := t.TempDir()
	types := []Base{Command(), Decision(&fakeDecider{p: 0.95})}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	files := decisionDAG("target:\n  file: score-{{.partition}}.json\n")
	defs := writeDAG(t, root, reg, files)
	run(t, root, types, defs)
	data, err := os.ReadFile(filepath.Join(root, "score-2026-10-02.json"))
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ParseScore(string(data))
	if err != nil || !sc.Pass || sc.By != ByDecision {
		t.Fatalf("file score = %+v, %v", sc, err)
	}
}

func TestADecisionTaskWithoutAnElseIsRefused(t *testing.T) {
	root := t.TempDir()
	types := []Base{Command(), Decision(nil)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	dir := filepath.Join(root, "wf", "steps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "score.yaml"), []byte("type: decision\ncondition: x\nstate: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := factory.NewValidator(reg)
	if err, _ := v.ValidateYAML([]byte("type: decision\ncondition: x\nstate: y\n")); err == nil {
		t.Fatal("a decision task without an else should not validate")
	}
}
