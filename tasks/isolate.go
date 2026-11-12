package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guregodevo/mario/logger"
	"github.com/guregodevo/mario/templates"
)

// Isolator gives a task a working directory of its own, a copy of the run's.
// Provided by the host, like Completer and Decider: a git worktree, a
// container, a plain copy. The engine knows only that a task can be isolated
// and that the isolation answers what changed in it.
type Isolator interface {
	Isolate(ctx context.Context, dir, name string) (Isolation, error)
}

// Isolation is one isolated working directory.
type Isolation interface {
	// Dir is where the task runs.
	Dir() string
	// Changes is what the task changed there, as a patch against the run's
	// directory as it was when the isolation was made.
	Changes(ctx context.Context) (string, error)
	// Close removes the directory.
	Close() error
}

// Applier is the optional half of an Isolator that puts a change into a
// directory: a fan-out with `pick` applies the winner to the run's.
type Applier interface {
	Apply(ctx context.Context, dir, patch string) error
}

// attempt is one isolated run of a fan-out: its number, its change, its
// failure, and, while the fan-out lasts, the directory it ran in (its checks
// run there).
type attempt struct {
	n     int
	patch string
	err   error
	iso   Isolation
}

// withN is the definition with the arg `n` set: the attempt's number, for
// its prompt, command, target and the fan-out's save path.
func withN(d *templates.YamlTaskDefinition, n int) *templates.YamlTaskDefinition {
	c := *d
	c.Args = make(map[string]string, len(d.Args)+1)
	for k, v := range d.Args {
		c.Args[k] = v
	}
	if n > 0 {
		c.Args["n"] = strconv.Itoa(n)
	}
	return &c
}

// isolated runs one attempt in a directory of its own. The error is the
// isolation's (none could be made); the attempt's own failure is in a.err,
// with whatever it changed before failing in a.patch. With keep, the
// directory stays open in a.iso for the caller to close.
func (b *Base) isolated(ctx context.Context, d *templates.YamlTaskDefinition, name string, n int, keep bool) (attempt, error) {
	a := attempt{n: n}
	if b.Isolator == nil {
		return a, fmt.Errorf("task %s: isolate needs a host that gives a task its own working directory, and this one does not", name)
	}
	label := name
	if n > 0 {
		label = fmt.Sprintf("%s-%d", name, n)
	}
	iso, err := b.Isolator.Isolate(ctx, b.Dir, label)
	if err != nil {
		return a, fmt.Errorf("task %s: isolate: %w", label, err)
	}
	if keep {
		a.iso = iso
	} else {
		defer iso.Close()
	}
	bb := *b
	bb.Dir = iso.Dir()
	dd := withN(d, n)
	_, a.err = bb.once(ctx, dd, name)
	// The change is read even when the run failed or ran out of time: what
	// it did is the evidence, and the clock may have been the only failure.
	cctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	patch, cerr := iso.Changes(cctx)
	if cerr != nil && a.err == nil {
		a.err = fmt.Errorf("task %s: reading its change: %w", label, cerr)
	}
	a.patch = patch
	return a, nil
}

// fanoutResult is a fan-out's output: each attempt, whether the attempts
// settled and what said so, and what `between` printed last.
type fanoutResult struct {
	Attempts []fanoutAttempt `json:"attempts"`
	Settled  bool            `json:"settled"`
	By       string          `json:"by"`
	P        *float64        `json:"p,omitempty"`
	Between  string          `json:"between,omitempty"`
	// The checks' evidence: how many discriminate, each attempt's points,
	// the matrix as lines, the checks that passed on the run's directory
	// as it stood (ignored), and the attempt picked.
	Checks  int            `json:"checks,omitempty"`
	Points  map[string]int `json:"points,omitempty"`
	Fails   map[string]int `json:"fails,omitempty"`
	Matrix  []string       `json:"matrix,omitempty"`
	Ignored []string       `json:"ignored,omitempty"`
	Winner  int            `json:"winner,omitempty"`
}

type fanoutAttempt struct {
	N     int    `json:"n"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Lines int    `json:"lines"`
	Patch string `json:"patch,omitempty"`
}

// fanout runs Min isolated attempts at once, then one more at a time until
// the attempts are settled or Max is reached. Settled: `between`, a command
// run in the run's directory after each wave with every attempt's patch
// saved, exits 0; or, when it does not, with `until` and a decision model on
// the host, the model reads what `between` printed and answers at or above
// the threshold. The model can only settle sooner, never run more.
// Without checks or `between` the fan-out is fixed at Min. Each attempt's
// patch is kept in the output.
func (b *Base) fanout(ctx context.Context, d *templates.YamlTaskDefinition, name string) (string, error) {
	f := d.Fanout
	max := f.Max
	if max < f.Min {
		max = f.Min
	}
	withChecks := len(f.Checks) > 0
	var atts []attempt
	defer func() {
		for _, a := range atts {
			if a.iso != nil {
				_ = a.iso.Close()
			}
		}
	}()
	wave := func(from, to int) error {
		res := make([]attempt, to-from+1)
		errs := make([]error, len(res))
		var wg sync.WaitGroup
		for i := range res {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res[i], errs[i] = b.isolated(ctx, d, name, from+i, withChecks)
			}(i)
		}
		wg.Wait()
		for i, a := range res {
			if errs[i] != nil {
				for _, r := range res {
					if r.iso != nil {
						_ = r.iso.Close()
					}
				}
				return errs[i]
			}
			atts = append(atts, a)
		}
		return nil
	}
	if err := wave(1, f.Min); err != nil {
		return "", err
	}
	// One copy of the run's directory as it stands, untouched by any
	// attempt: a check that passes there proves nothing.
	var base Isolation
	if withChecks {
		iso, err := b.Isolator.Isolate(ctx, b.Dir, name+"-base")
		if err != nil {
			return "", fmt.Errorf("task %s: the copy the checks are judged on: %w", name, err)
		}
		base = iso
		defer base.Close()
	}
	out := fanoutResult{}
	for {
		var m matrix
		if withChecks {
			var err error
			if m, err = b.evaluate(ctx, d, name, atts, base); err != nil {
				return "", err
			}
			out.Checks, out.Points, out.Fails, out.Matrix, out.Ignored = m.checks, m.pointsByName(), m.failsByName(), m.lines, m.ignored
		}
		settled, by, p, printed, err := b.settled(ctx, d, name, withChecks, m)
		if err != nil {
			return "", err
		}
		out.Settled, out.By, out.P, out.Between = settled, by, p, printed
		if settled || len(atts) >= max {
			break
		}
		logger.Log.Info(fmt.Sprintf("Task %s: %d attempts, not settled (%s); one more", name, len(atts), by), "component", "workflow")
		if err := wave(len(atts)+1, len(atts)+1); err != nil {
			return "", err
		}
	}
	ok := 0
	for _, a := range atts {
		fa := fanoutAttempt{N: a.n, OK: a.err == nil, Lines: strings.Count(a.patch, "\n")}
		if a.err != nil {
			fa.Error = a.err.Error()
		} else {
			ok++
		}
		fa.Patch = a.patch
		out.Attempts = append(out.Attempts, fa)
	}
	if ok == 0 {
		return "", fmt.Errorf("task %s: every attempt failed (%d): %s", name, len(atts), atts[len(atts)-1].err)
	}
	if f.Pick {
		w := winner(atts, out.Fails)
		ap, isApplier := b.Isolator.(Applier)
		if !isApplier {
			return "", fmt.Errorf("task %s: pick needs a host that applies a change, and this one does not", name)
		}
		if strings.TrimSpace(w.patch) != "" {
			if err := ap.Apply(ctx, b.Dir, w.patch); err != nil {
				return "", fmt.Errorf("task %s: applying attempt %d: %w", name, w.n, err)
			}
		}
		out.Winner = w.n
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// matrix is the checks' evidence after a wave.
type matrix struct {
	checks    int         // checks that fail on the run's directory as it stands
	points    map[int]int // per attempt
	possible  map[int]int // the points an attempt could have: the checks not its own
	lines     []string
	ignored   []string
	attempted []int
}

func (m matrix) pointsByName() map[string]int {
	out := make(map[string]int, len(m.points))
	for n, p := range m.points {
		out[strconv.Itoa(n)] = p
	}
	return out
}

// fails is the checks an attempt fails among those it is scored on. The
// attempts are ranked by it, fewest first, not by the checks they pass: an
// attempt is never scored on its own check, so one that writes no check is
// scored on one more than the others and led on points alone
// (django-11400, SWE-bench Verified, 2026-11-12: 5 points to 4, every
// attempt passing every check it was scored on; on 106 runs with every
// attempt graded the pick went from 92 right to 93, none lost).
func (m matrix) fails(n int) int { return m.possible[n] - m.points[n] }

func (m matrix) failsByName() map[string]int {
	out := make(map[string]int, len(m.points))
	for _, n := range m.attempted {
		out[strconv.Itoa(n)] = m.fails(n)
	}
	return out
}

// settledByChecks: no check tells the attempts apart, one leads, or every
// attempt passes every check that is not its own (a unanimous tie another
// attempt cannot break).
func (m matrix) settledByChecks() (bool, string) {
	if m.checks == 0 {
		return true, "no check fails on the run's directory as it stands"
	}
	fewest, atTop := -1, 0
	for _, n := range m.attempted {
		f := m.fails(n)
		if fewest < 0 || f < fewest {
			fewest, atTop = f, 1
		} else if f == fewest {
			atTop++
		}
	}
	if atTop == 1 {
		return true, "one attempt leads"
	}
	if fewest == 0 && atTop == len(m.attempted) {
		return true, "every attempt passes every other check"
	}
	return false, fmt.Sprintf("%d attempts tie at %d failed checks", atTop, fewest)
}

// checkTimeout bounds one check on one attempt.
const checkTimeout = 10 * time.Minute

// evaluate runs every check on the clean copy, then each one that fails
// there in every attempt's directory but its owner's.
func (b *Base) evaluate(ctx context.Context, d *templates.YamlTaskDefinition, name string, atts []attempt, base Isolation) (matrix, error) {
	m := matrix{points: map[int]int{}, possible: map[int]int{}}
	var files []string
	seen := map[string]bool{}
	for _, pat := range d.Fanout.Checks {
		rp, err := b.Render(d, pat)
		if err != nil {
			return m, fmt.Errorf("task %s: checks template %q: %w", name, pat, err)
		}
		if !filepath.IsAbs(rp) {
			rp = filepath.Join(b.Dir, rp)
		}
		found, _ := filepath.Glob(rp)
		for _, f := range found {
			if info, err := os.Stat(f); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 && !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	sort.Strings(files)
	owner := map[string]int{}
	if strings.TrimSpace(d.Fanout.Own) != "" {
		for _, a := range atts {
			own, err := b.Render(withN(d, a.n), d.Fanout.Own)
			if err != nil {
				return m, fmt.Errorf("task %s: own template: %w", name, err)
			}
			if !filepath.IsAbs(own) {
				own = filepath.Join(b.Dir, own)
			}
			owner[filepath.Clean(own)] = a.n
		}
	}
	for _, a := range atts {
		if a.iso != nil {
			m.attempted = append(m.attempted, a.n)
			m.points[a.n] = 0
		}
	}
	// A check is run by its path inside the directory it judges: one under
	// the run's directory (the run's folder, linked into every copy) is run
	// from the copy's side, so a check that finds the project from its own
	// location lands in that copy, not in the run's directory (a path check
	// that did `cd $(dirname $0)/../../../..` tested the run's directory for
	// every attempt, and failed all five, 2026-11-08).
	inDir := func(dir, f string) string {
		if rel, err := filepath.Rel(b.Dir, f); err == nil && !strings.HasPrefix(rel, "..") {
			if in := filepath.Join(dir, rel); fileExists(in) {
				return in
			}
		}
		return f // a copy without the run's folder: the check as it is
	}
	for _, f := range files {
		label := filepath.Base(f)
		if runCheck(ctx, base.Dir(), inDir(base.Dir(), f), b.Env) {
			m.ignored = append(m.ignored, label)
			m.lines = append(m.lines, fmt.Sprintf("check %s: passes on the run's directory as it stands: proves nothing", label))
			continue
		}
		m.checks++
		own := owner[filepath.Clean(f)]
		for _, a := range atts {
			if a.iso == nil || a.n == own {
				continue
			}
			m.possible[a.n]++
			if runCheck(ctx, a.iso.Dir(), inDir(a.iso.Dir(), f), b.Env) {
				m.points[a.n]++
				m.lines = append(m.lines, fmt.Sprintf("check %s on %d: passes", label, a.n))
			} else {
				m.lines = append(m.lines, fmt.Sprintf("check %s on %d: FAILS", label, a.n))
			}
		}
	}
	var pts []string
	for _, n := range m.attempted {
		pts = append(pts, fmt.Sprintf("%d %d", n, m.points[n]))
	}
	var fl []string
	for _, n := range m.attempted {
		fl = append(fl, fmt.Sprintf("%d %d", n, m.fails(n)))
	}
	m.lines = append(m.lines, fmt.Sprintf("attempts: %d; checks that fail on the run's directory as it stands: %d; points: %s; failed: %s", len(m.attempted), m.checks, strings.Join(pts, ", "), strings.Join(fl, ", ")))
	return m, nil
}

// runCheck runs an executable from dir: true when it exits 0.
func runCheck(ctx context.Context, dir, file string, env func() []string) bool {
	cctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, file)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env()
	}
	killGroupOnCancel(cmd)
	return cmd.Run() == nil
}

// winner is the attempt that fails the fewest checks, a tie to the smaller
// change; an attempt that failed counts only when every one did.
func winner(atts []attempt, fails map[string]int) attempt {
	var best *attempt
	for i := range atts {
		a := &atts[i]
		if a.err != nil {
			continue
		}
		if best == nil {
			best = a
			continue
		}
		fa, fb := fails[strconv.Itoa(a.n)], fails[strconv.Itoa(best.n)]
		if fa < fb || (fa == fb && len(a.patch) < len(best.patch)) {
			best = a
		}
	}
	if best == nil {
		best = &atts[0]
	}
	return *best
}

// settled answers whether the attempts so far are settled, who said so, the
// decision model's probability when it answered, and the evidence it read.
// What the checks or `between` settle is settled; when neither does, the
// decision model may read the evidence against `until` and settle sooner. It
// never runs more attempts than they would (on the sample it read a matrix
// with no check, answered 0.18 and ran five attempts where three had
// settled, 2026-11-07).
func (b *Base) settled(ctx context.Context, d *templates.YamlTaskDefinition, name string, withChecks bool, m matrix) (bool, string, *float64, string, error) {
	f := d.Fanout
	var evidence []string
	if withChecks {
		ok, why := m.settledByChecks()
		evidence = append(evidence, m.lines...)
		evidence = append(evidence, "checks: "+why)
		if ok {
			return true, "checks", nil, strings.Join(evidence, "\n"), nil
		}
	}
	if strings.TrimSpace(f.Between) != "" {
		line, err := b.Render(d, f.Between)
		if err != nil {
			return false, "", nil, "", fmt.Errorf("task %s: between template: %w", name, err)
		}
		cmd := shellCommand(ctx, b.Dir, line, b.Env)
		killGroupOnCancel(cmd)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		runErr := cmd.Run()
		if ctx.Err() != nil {
			return false, "", nil, "", fmt.Errorf("task %s: between stopped: %w", name, ctx.Err())
		}
		var exit *exec.ExitError
		if runErr != nil && !errors.As(runErr, &exit) {
			return false, "", nil, "", fmt.Errorf("task %s: between could not run: %w", name, runErr)
		}
		evidence = append(evidence, buf.String())
		if runErr == nil {
			return true, "between", nil, clipEnd(strings.Join(evidence, "\n")), nil
		}
	} else if !withChecks {
		return true, "fixed", nil, "", nil
	}
	printed := clipEnd(strings.Join(evidence, "\n"))
	by := "checks"
	if !withChecks {
		by = "between"
	}
	if strings.TrimSpace(f.Until) != "" && b.Decider != nil {
		until, rerr := b.Render(d, f.Until)
		if rerr == nil {
			p, derr := b.Decider.Decide(ctx, until, printed)
			if derr == nil && p >= 0 && p <= 1 {
				threshold := DefaultDecisionThreshold
				if t, ok := b.Decider.(Thresholder); ok {
					if v := t.DefaultThreshold(); v > 0 && v <= 1 {
						threshold = v
					}
				}
				return p >= threshold, ByDecision, &p, printed, nil
			}
			logger.Log.Info(fmt.Sprintf("Task %s: the decision model did not answer on the fan-out (%v)", name, derr), "component", "workflow")
		}
	}
	return false, by, nil, printed, nil
}

// maxBetween is how much of the evidence is kept and judged: the end, where
// a summary is.
const maxBetween = 8000

func clipEnd(s string) string {
	if len(s) > maxBetween {
		return "[…]\n" + s[len(s)-maxBetween:]
	}
	return s
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
