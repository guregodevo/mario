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
