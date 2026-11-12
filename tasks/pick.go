package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/guregodevo/mario/templates"
)

// Picked is a pick task's output: the attempt it applied, what decided, the
// attempts that led on the checks, and each attempt's points.
type Picked struct {
	Winner  int            `json:"winner"`
	By      string         `json:"by"`
	Leaders []int          `json:"leaders"`
	Points  map[string]int `json:"points,omitempty"`
	Fails   map[string]int `json:"fails,omitempty"`
}

// What decided a pick, when no `by` task did.
const (
	PickedByChecks    = "checks"
	PickedByPrefer    = "prefer"
	PickedBySmaller   = "smaller change"
	PickedByConsensus = "consensus"
)

// TieRule is a pick's rule for a tie its by tasks leave (`tie:`); the
// schema accepts only the rules below.
type TieRule string

// TieConsensus takes the leader whose change shares the most lines with the
// other leaders' changes.
const TieConsensus TieRule = "consensus"

// scoreTolerance is how close two consensus scores are to count as equal: a
// sum of fractions, compared after floating-point division.
const scoreTolerance = 1e-9

// Pick is the task type that chooses one attempt of a fan-out and applies
// it to the run's directory. `from:` names the fan-out (with `pick: false`,
// so it applies nothing itself). The attempt that fails the fewest checks wins. On
// a tie among the leaders, each task in `by:` is asked in order: the first
// whose output names one of the leaders decides (an attempt's number, the
// first in its output). Then `tie: consensus`: the leader whose change
// shares the most lines with the other leaders' changes. Then `prefer:`, then
// the smaller change. `skip:`
// names earlier pick tasks of the same fan-out: their winners are left out,
// so a second pick takes the next attempt by the same rules. Every rule
// is a task of the DAG or a field of this one: a tie-breaker is any task,
// required by this one, and one that is skipped or names no leader passes
// the tie on.
//
//	type: pick
//	from: direct
//	by: [distinguish]
//	prefer: 1
//	requires:
//	  - table_pattern: direct
//	  - table_pattern: distinguish
func Pick() Base {
	return Base{Type: "pick", SchemaDoc: []byte(pickSchema), Run: runPick}
}

func runPick(ctx context.Context, b *Base, d *templates.YamlTaskDefinition, name string) (string, error) {
	from := strings.TrimSpace(d.From)
	if from == "" {
		return "", fmt.Errorf("task %s: a pick task needs from, the fan-out whose attempts it chooses from", name)
	}
	raw, err := b.Outputs.Read(b.sibling(name, from), b.Partition)
	if err != nil {
		return "", fmt.Errorf("task %s: the output of its fan-out %s is not there (is it required by this task?): %w", name, from, err)
	}
	var fo fanoutResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &fo); err != nil || len(fo.Attempts) == 0 {
		return "", fmt.Errorf("task %s: %s is not a fan-out's output", name, from)
	}
	if fo.Winner != 0 {
		return "", fmt.Errorf("task %s: the fan-out %s already applied attempt %d (pick: true); a pick task reads a fan-out with pick: false", name, from, fo.Winner)
	}
	skipped := map[int]bool{}
	for _, ref := range d.Skip {
		said, err := b.Outputs.Read(b.sibling(name, strings.TrimSpace(ref)), b.Partition)
		if err != nil {
			continue
		}
		var p Picked
		if json.Unmarshal([]byte(strings.TrimSpace(said)), &p) == nil && p.Winner != 0 {
			skipped[p.Winner] = true
		}
	}
	byN := map[int]fanoutAttempt{}
	// The leaders fail the fewest checks (isolate.go, fails). A fan-out
	// output from before fails has only points: the most points lead.
	rank := func(n int) int { return -fo.Points[strconv.Itoa(n)] }
	if fo.Fails != nil {
		rank = func(n int) int { return fo.Fails[strconv.Itoa(n)] }
	}
	top, leaders := 0, []int{}
	for _, a := range fo.Attempts {
		if !a.OK || skipped[a.N] {
			continue
		}
		byN[a.N] = a
		r := rank(a.N)
		switch {
		case len(leaders) == 0 || r < top:
			top, leaders = r, []int{a.N}
		case r == top:
			leaders = append(leaders, a.N)
		}
	}
	if len(leaders) == 0 && len(skipped) > 0 {
		return "", fmt.Errorf("task %s: no attempt of %s is left once the winners of %s are skipped", name, from, strings.Join(d.Skip, ", "))
	}
	if len(leaders) == 0 {
		return "", fmt.Errorf("task %s: every attempt of %s failed", name, from)
	}
	sort.Ints(leaders)
	out := Picked{Leaders: leaders, Points: fo.Points, Fails: fo.Fails}
	isLeader := func(n int) bool {
		for _, l := range leaders {
			if l == n {
				return true
			}
		}
		return false
	}
	switch {
	case len(leaders) == 1:
		out.Winner, out.By = leaders[0], PickedByChecks
	default:
		for _, ref := range d.By {
			ref = strings.TrimSpace(ref)
			said, err := b.Outputs.Read(b.sibling(name, ref), b.Partition)
			if err != nil {
				continue
			}
			if n, ok := firstNumber(said); ok && isLeader(n) {
				out.Winner, out.By = n, ref
				break
			}
		}
		if out.Winner == 0 && TieRule(d.Tie) == TieConsensus {
			if best := consensus(leaders, byN); len(best) == 1 {
				out.Winner, out.By = best[0], PickedByConsensus
			} else {
				leaders = best
			}
		}
		if out.Winner == 0 && isLeader(d.Prefer) {
			out.Winner, out.By = d.Prefer, PickedByPrefer
		}
		if out.Winner == 0 {
			best := leaders[0]
			for _, n := range leaders[1:] {
				if len(byN[n].Patch) < len(byN[best].Patch) {
					best = n
				}
			}
			out.Winner, out.By = best, PickedBySmaller
		}
	}
	if patch := byN[out.Winner].Patch; strings.TrimSpace(patch) != "" {
		ap, ok := b.Isolator.(Applier)
		if !ok {
			return "", fmt.Errorf("task %s: a pick needs a host that applies a change, and this one does not", name)
		}
		if err := ap.Apply(ctx, b.Dir, patch); err != nil {
			return "", fmt.Errorf("task %s: applying attempt %d of %s: %w", name, out.Winner, from, err)
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("task %s: %w", name, err)
	}
	answer := string(data)
	if d.Target != nil && d.Target.File != "" {
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
	}
	return answer, nil
}

// consensus is the leaders whose change shares the most with the other
// leaders' changes: the sum, over the others, of the share of changed lines
// two changes have in common (added and removed lines, whitespace aside).
// Right attempts tend to make the same change and wrong ones to differ: on
// the SWE-bench Verified and LiveCodeBench runs where right and wrong
// attempts tied, it took a right one where prefer took a wrong one three
// times, and a wrong one where prefer was right once (two wrong attempts that
// wrote the same change; DeepSeek V4.1 Flash, 2026-11-11).
func consensus(leaders []int, byN map[int]fanoutAttempt) []int {
	lines := map[int]map[string]bool{}
	for _, n := range leaders {
		lines[n] = changedLines(byN[n].Patch)
	}
	score := map[int]float64{}
	for _, n := range leaders {
		for _, m := range leaders {
			if m == n {
				continue
			}
			both, either := 0, len(lines[m])
			for l := range lines[n] {
				if lines[m][l] {
					both++
				} else {
					either++
				}
			}
			if either > 0 {
				score[n] += float64(both) / float64(either)
			}
		}
	}
	top, best := -1.0, []int{}
	for _, n := range leaders {
		switch {
		case score[n] > top+scoreTolerance:
			top, best = score[n], []int{n}
		case score[n] > top-scoreTolerance:
			best = append(best, n)
		}
	}
	return best
}

var spaces = regexp.MustCompile(`\s+`)

// changedLines is a patch's added and removed lines, whitespace aside.
func changedLines(patch string) map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(patch, "\n") {
		if len(l) == 0 || (l[0] != '+' && l[0] != '-') || strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if t := spaces.ReplaceAllString(l[1:], ""); t != "" {
			out[l[:1]+t] = true
		}
	}
	return out
}

// sibling is the full name of a task a short name refers to: a task of the
// same group as name; a dotted name is any task.
func (b *Base) sibling(name, ref string) string {
	if strings.Contains(ref, ".") {
		return ref
	}
	if seg := strings.Split(name, "."); len(seg) >= 3 {
		return seg[0] + "." + seg[1] + "." + ref
	}
	return ref
}

var number = regexp.MustCompile(`\d+`)

// firstNumber is the first whole number in a tie-breaker's output: the
// attempt it names ("2", "attempt 2", {"winner": 2}).
func firstNumber(s string) (int, bool) {
	m := number.FindString(s)
	if m == "" {
		return 0, false
	}
	n, err := strconv.Atoi(m)
	return n, err == nil
}

const pickSchema = `{
  "description": "Chooses one attempt of a fan-out and applies it: the fewest failed checks, a tie to the first by task that names a leader, then prefer, then the smaller change",
  "type": "object",
  "properties": {
    "type": {"enum": ["pick"]},
    "from": {"type": "string", "description": "The fan-out task whose attempts are chosen from, required by this one, with pick: false. A short name is a task of the same group."},
    "by": {"type": "array", "items": {"type": "string"}, "description": "Tasks asked in order on a tie, each required by this one: the first whose output names a leading attempt's number decides."},
    "tie": {"enum": ["consensus"], "description": "The rule for a tie no by task settles, before prefer: consensus takes the leader whose change shares the most lines with the other leaders' changes."},
    "prefer": {"type": "integer", "minimum": 1, "description": "The attempt that wins a tie no by task settles."},
    "skip": {"type": "array", "items": {"type": "string"}, "description": "Earlier pick tasks of the same fan-out, each required by this one: their winners are left out, so this pick takes the next attempt by the same rules."},
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
  "required": ["type", "from"],
  "additionalProperties": false,
  "definitions": ` + commonDefinitions + `
}`
