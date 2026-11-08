package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/templates"
)

// copyIsolator copies the run's f.txt into a fresh directory; the change is
// f.txt's new content, or "" when it did not change.
type copyIsolator struct {
	mu    sync.Mutex
	dirs  []string
	names []string
}

type copyIsolation struct {
	dir, before string
}

func (c *copyIsolator) Isolate(_ context.Context, dir, name string) (Isolation, error) {
	d, err := os.MkdirTemp("", "iso-*")
	if err != nil {
		return nil, err
	}
	before, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if err := os.WriteFile(filepath.Join(d, "f.txt"), before, 0o644); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.dirs, c.names = append(c.dirs, d), append(c.names, name)
	c.mu.Unlock()
	return &copyIsolation{dir: d, before: string(before)}, nil
}

func (i *copyIsolation) Dir() string { return i.dir }
func (i *copyIsolation) Changes(context.Context) (string, error) {
	now, err := os.ReadFile(filepath.Join(i.dir, "f.txt"))
	if err != nil {
		return "", err
	}
	if string(now) == i.before {
		return "", nil
	}
	return "f.txt: " + string(now), nil
}
func (i *copyIsolation) Close() error { return os.RemoveAll(i.dir) }

// seqDecider answers its probabilities in order, and keeps what it read.
type seqDecider struct {
	mu     sync.Mutex
	ps     []float64
	states []string
}

func (s *seqDecider) Decide(_ context.Context, _ string, state string) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, state)
	if len(s.ps) == 0 {
		return 0, fmt.Errorf("no answer")
	}
	p := s.ps[0]
	s.ps = s.ps[1:]
	return p, nil
}

func runShaped(t *testing.T, iso Isolator, dec Decider, files map[string]string) (string, map[string]*templates.YamlTaskDefinition, []string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("start\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := Command()
	cmd.Isolator, cmd.Decider = iso, dec
	types := []Base{cmd}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, files)
	return root, defs, run(t, root, types, defs)
}

func TestAnIsolatedTaskLeavesTheRunsTreeAndAnswersItsChange(t *testing.T) {
	iso := &copyIsolator{}
	root, _, seq := runShaped(t, iso, nil, map[string]string{
		"edit": "type: command\nisolate: true\ncommand: echo changed > f.txt\n",
	})
	if got := strings.Join(seq, " | "); got != "started edit | done edit" {
		t.Fatalf("events = %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(b) != "start\n" {
		t.Fatalf("the run's tree changed: %q", b)
	}
	if out, _ := (Outputs{Dir: filepath.Join(root, "out")}).Read("wf.steps.edit", "2026-10-02"); out != "f.txt: changed\n" {
		t.Fatalf("output = %q", out)
	}
	for _, d := range iso.dirs {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("isolation %s not removed", d)
		}
	}
}

func TestIsolateWithoutAnIsolatorFailsAndSaysWhy(t *testing.T) {
	_, _, seq := runShaped(t, nil, nil, map[string]string{
		"edit": "type: command\nisolate: true\ncommand: echo changed > f.txt\n",
	})
	if got := strings.Join(seq, " | "); !strings.Contains(got, "failed edit") || !strings.Contains(got, "own working directory") {
		t.Fatalf("events = %q", got)
	}
}

func readFanout(t *testing.T, root string) fanoutResult {
	t.Helper()
	out, err := (Outputs{Dir: filepath.Join(root, "out")}).Read("wf.steps.try", "2026-10-02")
	if err != nil {
		t.Fatal(err)
	}
	var r fanoutResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	return r
}

func TestAFanoutWithoutBetweenRunsMinAttemptsEachInItsOwnTree(t *testing.T) {
	iso := &copyIsolator{}
	root, _, _ := runShaped(t, iso, nil, map[string]string{
		"try": "type: command\nfanout:\n  min: 3\n  save: patches/{{.n}}.patch\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 3 || !r.Settled || r.By != "fixed" {
		t.Fatalf("result = %+v", r)
	}
	for n := 1; n <= 3; n++ {
		b, err := os.ReadFile(filepath.Join(root, "patches", fmt.Sprintf("%d.patch", n)))
		if err != nil || string(b) != fmt.Sprintf("f.txt: attempt %d\n", n) {
			t.Fatalf("patch %d = %q, %v", n, b, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(b) != "start\n" {
		t.Fatalf("the run's tree changed: %q", b)
	}
}

func TestAFanoutAddsAttemptsUntilBetweenSettlesIt(t *testing.T) {
	root, _, _ := runShaped(t, &copyIsolator{}, nil, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\n  max: 5\n  save: patches/{{.n}}.patch\n" +
			"  between: '[ $(ls patches | wc -l) -ge 3 ]'\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 3 || !r.Settled || r.By != "between" {
		t.Fatalf("result = %+v", r)
	}
}

func TestAFanoutStopsAtMaxUnsettled(t *testing.T) {
	root, _, _ := runShaped(t, &copyIsolator{}, nil, map[string]string{
		"try": "type: command\nfanout:\n  min: 1\n  max: 3\n  between: 'exit 1'\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 3 || r.Settled || r.By != "between" {
		t.Fatalf("result = %+v", r)
	}
	if r.Attempts[2].Patch != "f.txt: attempt 3\n" {
		t.Fatalf("without save the patch is in the output: %+v", r.Attempts[2])
	}
}

func TestTheDeciderSettlesAFanoutFromWhatBetweenPrinted(t *testing.T) {
	dec := &seqDecider{ps: []float64{0.2, 0.9}}
	root, _, _ := runShaped(t, &copyIsolator{}, dec, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\n  max: 5\n  save: patches/{{.n}}.patch\n" +
			"  between: 'ls patches; exit 1'\n  until: The attempts agree.\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 3 || !r.Settled || r.By != ByDecision || r.P == nil || *r.P != 0.9 {
		t.Fatalf("result = %+v", r)
	}
	if len(dec.states) != 2 || !strings.Contains(dec.states[1], "3.patch") {
		t.Fatalf("the decider read %q", dec.states)
	}
}

func TestADeciderThatDoesNotAnswerLeavesItToBetween(t *testing.T) {
	root, _, _ := runShaped(t, &copyIsolator{}, &seqDecider{}, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\n  max: 4\n  between: 'true'\n  until: The attempts agree.\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 2 || !r.Settled || r.By != "between" {
		t.Fatalf("result = %+v", r)
	}
}

func TestAFanoutsShapeIsChecked(t *testing.T) {
	types := []Base{Command()}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	v := factory.NewValidator(reg)
	for _, bad := range []string{
		"type: command\ncommand: x\nfanout:\n  min: 0\n",
		"type: command\ncommand: x\nfanout:\n  min: 2\n  max: 11\n",
		"type: command\ncommand: x\nfanout:\n  min: 3\n  max: 2\n",
		"type: command\ncommand: x\nfanout:\n  min: 2\n  until: agree\n",
		"type: command\ncommand: x\nfanout:\n  min: 2\n  howmany: 3\n",
		"type: command\ncommand: x\neach: [a, b]\nfanout:\n  min: 2\n",
		"type: command\ncommand: x\nisolate: yes please\n",
	} {
		if err, _ := templates.YamlParse(v, []byte(bad)); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
	for _, good := range []string{
		"type: command\ncommand: x\nisolate: true\n",
		"type: command\ncommand: x\nfanout:\n  min: 2\n  max: 5\n  between: 'true'\n  until: agree\n  save: p/{{.n}}\n",
	} {
		if err, _ := templates.YamlParse(v, []byte(good)); err != nil {
			t.Errorf("refused %q: %v", good, err)
		}
	}
}

// An isolated task's target is about its own copy: one that already holds
// in the run's directory (an "uncommitted change" in a dirty tree) neither
// skips it nor proves it; it runs, and its target is checked where it ran.
func TestAnIsolatedTasksTargetIsCheckedInItsCopyNotTheRunsDirectory(t *testing.T) {
	iso := &copyIsolator{}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("start\n"), 0o644)
	os.WriteFile(filepath.Join(root, "done.txt"), []byte("in the run's directory already\n"), 0o644)
	cmd := Command()
	cmd.Isolator = iso
	types := []Base{cmd}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\ncommand: echo attempt {{.n}} > f.txt\ntarget:\n  file: done.txt\n",
	})
	seq := run(t, root, types, defs)
	joined := strings.Join(seq, " | ")
	if strings.Contains(joined, "skipped") || !strings.Contains(joined, "failed try") {
		t.Fatalf("the copy has no done.txt: every attempt fails its target, the task is not skipped: %q", joined)
	}
	// The same, with attempts that make their target in their copy: done.
	root2 := t.TempDir()
	os.WriteFile(filepath.Join(root2, "f.txt"), []byte("start\n"), 0o644)
	os.WriteFile(filepath.Join(root2, "done.txt"), []byte("in the run's directory already\n"), 0o644)
	defs2 := writeDAG(t, root2, reg, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\ncommand: echo attempt {{.n}} > f.txt && echo ok > done.txt\ntarget:\n  file: done.txt\n",
	})
	if got := strings.Join(run(t, root2, types, defs2), " | "); got != "started try | done try" {
		t.Fatalf("attempts that make their target in their copy: %q", got)
	}
}

// The decision model cannot overrule a settled `between`: asked nothing, it
// runs no more attempts.
func TestTheDeciderCannotRunMoreThanBetweenSettled(t *testing.T) {
	dec := &seqDecider{ps: []float64{0.1, 0.1, 0.1}}
	root, _, _ := runShaped(t, &copyIsolator{}, dec, map[string]string{
		"try": "type: command\nfanout:\n  min: 2\n  max: 5\n  between: 'true'\n  until: The attempts agree.\ncommand: echo attempt {{.n}} > f.txt\n",
	})
	r := readFanout(t, root)
	if len(r.Attempts) != 2 || !r.Settled || r.By != "between" || len(dec.states) != 0 {
		t.Fatalf("result = %+v, decider asked %d times", r, len(dec.states))
	}
}

// applyIsolator is copyIsolator that can also apply a change: "f.txt: X"
// writes X to f.txt.
type applyIsolator struct{ copyIsolator }

func (a *applyIsolator) Apply(_ context.Context, dir, patch string) error {
	if !strings.HasPrefix(patch, "f.txt: ") {
		return fmt.Errorf("not a change: %q", patch)
	}
	return os.WriteFile(filepath.Join(dir, "f.txt"), []byte(strings.TrimPrefix(patch, "f.txt: ")), 0o644)
}

func runChecked(t *testing.T, iso Isolator, files func(root string) map[string]string, checks map[string]string) (string, fanoutResult) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("start\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "checks"), 0o755)
	for name, body := range checks {
		os.WriteFile(filepath.Join(root, "checks", name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	cmd := Command()
	cmd.Isolator = iso
	types := []Base{cmd}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, files(root))
	run(t, root, types, defs)
	return root, readFanout(t, root)
}

// The engine runs the checks in each attempt's directory: one that passes on
// the run's directory as it stands is ignored; the attempt the others' checks
// pass leads, the fan-out settles, and pick puts its change in the run's
// directory.
func TestAFanoutsChecksPickTheAttemptTheyPass(t *testing.T) {
	root, r := runChecked(t, &applyIsolator{}, func(string) map[string]string {
		return map[string]string{"try": "type: command\nfanout:\n  min: 3\n  max: 5\n  checks: [checks/*]\n  pick: true\ncommand: echo attempt {{.n}} > f.txt\n"}
	}, map[string]string{"wants-2": "grep -q 'attempt 2' f.txt", "vacuous": "test -f f.txt"})
	if len(r.Attempts) != 3 || !r.Settled || r.By != "checks" || r.Winner != 2 || r.Checks != 1 {
		t.Fatalf("result = %+v", r)
	}
	if len(r.Ignored) != 1 || r.Ignored[0] != "vacuous" {
		t.Fatalf("ignored = %v", r.Ignored)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(b) != "attempt 2\n" {
		t.Fatalf("pick did not apply the winner: %q", b)
	}
}

// An attempt never scores on its own check; when each passes only its own,
// the checks tie, and the fan-out runs to max.
func TestAnAttemptNeverScoresOnItsOwnCheck(t *testing.T) {
	_, r := runChecked(t, &applyIsolator{}, func(root string) map[string]string {
		own := filepath.Join(root, "checks", "own-{{.n}}")
		return map[string]string{"try": "type: command\nfanout:\n  min: 2\n  max: 3\n  checks: [checks/*]\n  own: checks/own-{{.n}}\n" +
			"command: echo attempt {{.n}} > f.txt && printf '#!/bin/sh\\ngrep -q \"attempt {{.n}}\" f.txt\\n' > " + own + " && chmod +x " + own + "\n"}
	}, nil)
	if len(r.Attempts) != 3 || r.Settled || r.By != "checks" {
		t.Fatalf("result = %+v", r)
	}
	for n, p := range r.Points {
		if p != 0 {
			t.Fatalf("attempt %s scored %d on checks that are others' and pass only on their owner", n, p)
		}
	}
}

// When every attempt passes every check but its own, they agree: settled.
func TestAUnanimousTieIsSettled(t *testing.T) {
	_, r := runChecked(t, &applyIsolator{}, func(root string) map[string]string {
		own := filepath.Join(root, "checks", "own-{{.n}}")
		return map[string]string{"try": "type: command\nfanout:\n  min: 3\n  max: 5\n  checks: [checks/*]\n  own: checks/own-{{.n}}\n" +
			"command: echo attempt {{.n}} > f.txt && printf '#!/bin/sh\\ngrep -q attempt f.txt\\n' > " + own + " && chmod +x " + own + "\n"}
	}, nil)
	if len(r.Attempts) != 3 || !r.Settled || r.By != "checks" {
		t.Fatalf("result = %+v", r)
	}
}

func TestPickNeedsAHostThatApplies(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("start\n"), 0o644)
	cmd := Command()
	cmd.Isolator = &copyIsolator{}
	types := []Base{cmd}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, map[string]string{"try": "type: command\nfanout:\n  min: 2\n  pick: true\ncommand: echo attempt {{.n}} > f.txt\n"})
	if got := strings.Join(run(t, root, types, defs), " | "); !strings.Contains(got, "pick needs a host that applies") {
		t.Fatalf("events = %q", got)
	}
}
