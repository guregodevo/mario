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

// decisionDAG: a review writes its verdict, a rule task reads it the
// deterministic way, and a decision task scores it with the rule as its
// else — the branch without a model is a task of the DAG.
func decisionDAG(extra string) map[string]string {
	return map[string]string{
		"review": "type: command\ncommand: |\n  printf 'verdict: PASS\\nlooks right\\n'\n",
		"rule": "type: command\ncommand: |\n  if grep -q '^verdict: PASS' out/wf/steps/review/2026-10-02; then echo PASS; else echo FAIL; fi\n" +
			"requires:\n  - table_pattern: review\n",
		"score": "type: decision\ncondition: The change does what {{.what}} asks.\nstate: |\n  {{ output \"review\" }}\n" +
			"else: rule\nargs:\n  what: the task\nrequires:\n  - table_pattern: review\n  - table_pattern: rule\n" + extra,
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

const decisionOrder = "started review | done review | started rule | done rule | started score | done score"

func TestADecisionTaskScoresAnUpstreamOutput(t *testing.T) {
	dec := &fakeDecider{p: 0.9}
	out, seq := runDecisionDAG(t, dec, decisionDAG("threshold: 0.8\n"))
	if got := strings.Join(seq, " | "); got != decisionOrder {
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

func TestWithoutADeciderTheElseTaskAnswers(t *testing.T) {
	out, seq := runDecisionDAG(t, nil, decisionDAG(""))
	if got := strings.Join(seq, " | "); got != decisionOrder {
		t.Fatalf("events = %q", got)
	}
	sc, err := ParseScore(out)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.Pass || sc.P != 1 || sc.By != "rule" || sc.Threshold != DefaultDecisionThreshold {
		t.Fatalf("score = %+v", sc)
	}
}

func TestAnElseTaskThatSaysFailIsAFail(t *testing.T) {
	files := decisionDAG("")
	files["review"] = "type: command\ncommand: |\n  printf 'verdict: FAIL\\n'\n"
	out, _ := runDecisionDAG(t, nil, files)
	sc, _ := ParseScore(out)
	if sc.Pass || sc.P != 0 || sc.By != "rule" {
		t.Fatalf("score = %+v", sc)
	}
}

func TestADeciderThatErrsFallsBackToTheElseTask(t *testing.T) {
	out, _ := runDecisionDAG(t, &fakeDecider{err: errors.New("no model")}, decisionDAG("threshold: 0.8\n"))
	sc, _ := ParseScore(out)
	if !sc.Pass || sc.By != "rule" || sc.Threshold != 0.8 {
		t.Fatalf("score = %+v", sc)
	}
}

func TestAnElseTaskMayAnswerWithAScore(t *testing.T) {
	files := decisionDAG("")
	files["rule"] = "type: command\ncommand: |\n  echo '{\"p\":0,\"threshold\":0.5,\"pass\":false,\"by\":\"hand\"}'\nrequires:\n  - table_pattern: review\n"
	out, _ := runDecisionDAG(t, nil, files)
	sc, _ := ParseScore(out)
	if sc.Pass || sc.By != "rule" {
		t.Fatalf("score = %+v", sc)
	}
}

func TestAnElseTaskThatAnswersNeitherFailsTheDecision(t *testing.T) {
	files := decisionDAG("")
	files["rule"] = "type: command\ncommand: echo maybe\nrequires:\n  - table_pattern: review\n"
	_, seq := runDecisionDAG(t, nil, files)
	joined := strings.Join(seq, " | ")
	if !strings.Contains(joined, "failed score") || !strings.Contains(joined, "not PASS or FAIL") {
		t.Fatalf("events = %q", joined)
	}
}

func TestParseAnswerReadsWordsAndScores(t *testing.T) {
	for in, want := range map[string]bool{"PASS\n": true, "pass: all good": true, "True": true, "yes": true, "FAIL": false, "false": false, "No.": false, `{"pass":true}`: true} {
		got, ok := ParseAnswer(in)
		if !ok || got != want {
			t.Errorf("ParseAnswer(%q) = %v, %v", in, got, ok)
		}
	}
	if _, ok := ParseAnswer("maybe"); ok {
		t.Error("maybe is not an answer")
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
	types := []Base{Command(), Decision(nil)}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	v := factory.NewValidator(reg)
	if err, _ := v.ValidateYAML([]byte("type: decision\ncondition: x\nstate: y\n")); err == nil {
		t.Fatal("a decision task without an else should not validate")
	}
}
