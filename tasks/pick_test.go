package tasks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
	"github.com/guregodevo/mario/templates"
)

// runPicked runs a fan-out `try` (pick: false) and the DAG's other tasks,
// a pick among them, with an isolator that applies; it returns the run's
// f.txt, the pick's output, and the events.
func runPicked(t *testing.T, files map[string]string, checks map[string]string) (string, Picked, []string) {
	t.Helper()
	return runPickedAs(t, "choose", files, checks)
}

// runPickedAs is runPicked with the output of the pick task named as.
func runPickedAs(t *testing.T, as string, files map[string]string, checks map[string]string) (string, Picked, []string) {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("start\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "checks"), 0o755)
	for name, body := range checks {
		os.WriteFile(filepath.Join(root, "checks", name), []byte("#!/bin/sh\n"+body+"\n"), 0o755)
	}
	iso := &applyIsolator{}
	cmd, pick := Command(), Pick()
	cmd.Isolator, pick.Isolator = iso, iso
	types := []Base{cmd, pick}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	defs := writeDAG(t, root, reg, files)
	seq := run(t, root, types, defs)
	out, _ := Outputs{Dir: filepath.Join(root, "out")}.Read("wf.steps."+as, "2026-10-02")
	var p Picked
	if out != "" {
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatalf("pick output %q: %v", out, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	return string(b), p, seq
}

const tryThree = "type: command\nfanout:\n  min: 3\n  max: 3\n  checks: [checks/*]\ncommand: echo attempt {{.n}} > f.txt\n"

// The checks favour one attempt: the pick applies it, whatever it prefers.
func TestAPickTakesTheAttemptTheChecksFavour(t *testing.T) {
	f, p, _ := runPicked(t, map[string]string{
		"try":    tryThree,
		"choose": "type: pick\nfrom: try\nprefer: 1\nrequires:\n  - table_pattern: try\n",
	}, map[string]string{"wants-2": "grep -q 'attempt 2' f.txt"})
	if p.Winner != 2 || p.By != PickedByChecks || f != "attempt 2\n" {
		t.Fatalf("picked %+v, f.txt %q", p, f)
	}
}

// A tie: the first by task that names a leader decides; one that names no
// leader, or is not there, passes it on; then prefer; then the smaller change.
func TestATieGoesToTheFirstByTaskThatNamesALeaderThenPrefer(t *testing.T) {
	for _, c := range []struct {
		name, by, prefer string
		want             int
		wantBy           string
	}{
		{"a tie-breaker names attempt 3", "by: [breaker]\n", "prefer: 1\n", 3, "breaker"},
		{"no tie-breaker: prefer", "", "prefer: 1\n", 1, PickedByPrefer},
		{"a tie-breaker that names no attempt: prefer", "by: [silent]\n", "prefer: 2\n", 2, PickedByPrefer},
		{"no tie-breaker, no prefer: the smaller change", "", "", 1, PickedBySmaller},
	} {
		files := map[string]string{
			"try":     tryThree,
			"breaker": "type: command\ncommand: echo attempt 3 differs\nrequires:\n  - table_pattern: try\n",
			"silent":  "type: command\ncommand: echo no difference\nrequires:\n  - table_pattern: try\n",
			"choose":  "type: pick\nfrom: try\n" + c.by + c.prefer + "requires:\n  - table_pattern: try\n  - table_pattern: breaker\n  - table_pattern: silent\n",
		}
		f, p, _ := runPicked(t, files, nil)
		want := "attempt " + string(rune('0'+c.want)) + "\n"
		if p.Winner != c.want || p.By != c.wantBy || f != want || len(p.Leaders) != 3 {
			t.Errorf("%s: picked %+v, f.txt %q", c.name, p, f)
		}
	}
}

// A fan-out that applied its own winner leaves nothing to pick: the pick
// fails and says why, and the tree keeps the fan-out's change only.
func TestAPickRefusesAFanoutThatAlreadyPicked(t *testing.T) {
	_, _, seq := runPicked(t, map[string]string{
		"try":    "type: command\nfanout:\n  min: 2\n  max: 2\n  pick: true\ncommand: echo attempt {{.n}} > f.txt\n",
		"choose": "type: pick\nfrom: try\nrequires:\n  - table_pattern: try\n",
	}, nil)
	if got := strings.Join(seq, " | "); !strings.Contains(got, "already applied attempt") {
		t.Fatalf("events = %q", got)
	}
}

func TestAPicksShapeIsChecked(t *testing.T) {
	types := []Base{Command(), Pick()}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	v := factory.NewValidator(reg)
	for _, bad := range []string{
		"type: pick\nprefer: 1\n",
		"type: pick\nfrom: try\nprefer: 0\n",
		"type: pick\nfrom: try\nby: breaker\n",
		"type: pick\nfrom: try\ncommand: x\n",
	} {
		if err, _ := templates.YamlParse(v, []byte(bad)); err == nil {
			t.Errorf("accepted:\n%s", bad)
		}
	}
	if err, _ := templates.YamlParse(v, []byte("type: pick\nfrom: try\nby: [a, b]\nprefer: 1\n")); err != nil {
		t.Errorf("refused a pick: %v", err)
	}
}

// A second pick that skips the first takes the next attempt by the same
// rules: the checks favour 2, so choose takes 2; again, with 2 left out,
// ties 1 and 3 and takes prefer 1. The tree is reset between the two.
func TestAPickThatSkipsAnEarlierPickTakesTheNextAttempt(t *testing.T) {
	f, p, _ := runPickedAs(t, "again", map[string]string{
		"try":    tryThree,
		"choose": "type: pick\nfrom: try\nprefer: 1\nrequires:\n  - table_pattern: try\n",
		"reset":  "type: command\ncommand: echo start > f.txt\nrequires:\n  - table_pattern: choose\n",
		"again":  "type: pick\nfrom: try\nskip: [choose]\nprefer: 1\nrequires:\n  - table_pattern: reset\n  - table_pattern: choose\n  - table_pattern: try\n",
	}, map[string]string{"wants-2": "grep -q 'attempt 2' f.txt"})
	if p.Winner != 1 || p.By != PickedByPrefer || f != "attempt 1\n" || len(p.Leaders) != 2 {
		t.Fatalf("picked %+v, f.txt %q", p, f)
	}
}

// With every attempt but the skipped one failed, nothing is left: the pick
// fails and says why, and the tree keeps what it had.
func TestAPickWithNothingLeftAfterSkippingFails(t *testing.T) {
	f, _, seq := runPickedAs(t, "again", map[string]string{
		"try":    "type: command\nfanout:\n  min: 2\n  max: 2\n  checks: [checks/*]\ncommand: '[ {{.n}} = 1 ] && echo attempt 1 > f.txt'\n",
		"choose": "type: pick\nfrom: try\nrequires:\n  - table_pattern: try\n",
		"again":  "type: pick\nfrom: try\nskip: [choose]\nrequires:\n  - table_pattern: choose\n  - table_pattern: try\n",
	}, nil)
	if got := strings.Join(seq, " | "); !strings.Contains(got, "no attempt of") || f != "attempt 1\n" {
		t.Fatalf("f.txt %q, events = %q", f, got)
	}
}

func diffOf(lines ...string) string {
	return "diff --git a/m.py b/m.py\n--- a/m.py\n+++ b/m.py\n@@ -1,2 +1,2 @@\n" + strings.Join(lines, "\n") + "\n"
}

// Consensus takes the leader whose change shares the most lines with the
// others': two attempts that change the same line outweigh the odd one, and
// whitespace does not count; equal scores stay tied.
func TestConsensusTakesTheChangeTheOthersShare(t *testing.T) {
	byN := map[int]fanoutAttempt{
		1: {N: 1, OK: true, Patch: diffOf("-x = a", "+x = b", "+log(x)")},
		2: {N: 2, OK: true, Patch: diffOf("-x = a", "+x  =  c")},
		3: {N: 3, OK: true, Patch: diffOf("-x = a", "+x = c")},
	}
	if got := consensus([]int{1, 2, 3}, byN); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("consensus = %v, want [2 3] (2 and 3 make the same change, whitespace aside)", got)
	}
	byN[3] = fanoutAttempt{N: 3, OK: true, Patch: diffOf("-x = a", "+x = c", "+check(x)")}
	if got := consensus([]int{1, 2, 3}, byN); len(got) != 1 || got[0] != 2 {
		t.Errorf("consensus = %v, want [2] (its change is the one the others share most)", got)
	}
}

func TestAPicksTieIsChecked(t *testing.T) {
	types := []Base{Command(), Pick()}
	reg := factory.NewComponent()
	for i := range types {
		reg.Add(&types[i])
	}
	v := factory.NewValidator(reg)
	if err, _ := templates.YamlParse(v, []byte("type: pick\nfrom: try\ntie: consensus\nprefer: 1\n")); err != nil {
		t.Errorf("refused tie: consensus: %v", err)
	}
	if err, _ := templates.YamlParse(v, []byte("type: pick\nfrom: try\ntie: vote\n")); err == nil {
		t.Errorf("accepted tie: vote")
	}
}

// An attempt that writes no check of its own is scored on one check more than
// the others, and led on points alone; ranked by the checks they fail, the
// three tie and the tie goes on (django-11400, 2026-11-12).
func TestAnAttemptWithNoCheckOfItsOwnDoesNotLeadByIt(t *testing.T) {
	f, p, _ := runPicked(t, map[string]string{
		"try":    "type: command\nfanout:\n  min: 3\n  max: 3\n  checks: [checks/*]\n  own: checks/own-{{.n}}\n  pick: false\ncommand: echo attempt {{.n}} > f.txt\n",
		"choose": "type: pick\nfrom: try\nprefer: 2\nrequires:\n  - table_pattern: try\n",
	}, map[string]string{
		"path":  "grep -q attempt f.txt",
		"own-2": "grep -q attempt f.txt",
		"own-3": "grep -q attempt f.txt",
	})
	if p.Points["1"] <= p.Points["2"] {
		t.Fatalf("points %v: attempt 1 should be scored on more checks than 2", p.Points)
	}
	if len(p.Leaders) != 3 || p.Winner != 2 || p.By != PickedByPrefer || f != "attempt 2\n" {
		t.Fatalf("picked %+v, f.txt %q: want the three tied at 0 failed checks and prefer 2", p, f)
	}
}
