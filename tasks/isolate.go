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

// attempt is one isolated run: its number in a fan-out (0 alone), its
// change, its answer and its failure.
type attempt struct {
	n      int
	patch  string
	answer string
	err    error
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

// isolated runs the task once in a directory of its own. The error is the
// isolation's (none could be made); the attempt's own failure is in a.err,
// with whatever it changed before failing in a.patch.
func (b *Base) isolated(ctx context.Context, d *templates.YamlTaskDefinition, name string, n int) (attempt, error) {
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
	defer iso.Close()
	bb := *b
	bb.Dir = iso.Dir()
	dd := withN(d, n)
	a.answer, a.err = bb.once(ctx, dd, name)
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
}

type fanoutAttempt struct {
	N     int    `json:"n"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Lines int    `json:"lines"`
	Saved string `json:"saved,omitempty"`
	Patch string `json:"patch,omitempty"`
}

// fanout runs Min isolated attempts at once, then one more at a time until
// the attempts are settled or Max is reached. Settled: `between`, a command
// run in the run's directory after each wave with every attempt's patch
// saved, exits 0; or, when it does not, with `until` and a decision model on
// the host, the model reads what `between` printed and answers at or above
// the threshold. The model can only settle sooner, never run more.
// Without `between` the fan-out is fixed at Min. Each attempt's patch is
// written to `save` ({{.n}} in it) when one is named, else kept in the output.
func (b *Base) fanout(ctx context.Context, d *templates.YamlTaskDefinition, name string) (string, error) {
	f := d.Fanout
	max := f.Max
	if max < f.Min {
		max = f.Min
	}
	var atts []attempt
	wave := func(from, to int) error {
		res := make([]attempt, to-from+1)
		errs := make([]error, len(res))
		var wg sync.WaitGroup
		for i := range res {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				res[i], errs[i] = b.isolated(ctx, d, name, from+i)
			}(i)
		}
		wg.Wait()
		for i, a := range res {
			if errs[i] != nil {
				return errs[i]
			}
			if err := b.save(d, a); err != nil {
				return fmt.Errorf("task %s: saving attempt %d: %w", name, a.n, err)
			}
			atts = append(atts, a)
		}
		return nil
	}
	if err := wave(1, f.Min); err != nil {
		return "", err
	}
	out := fanoutResult{}
	for {
		settled, by, p, printed, err := b.settled(ctx, d, name)
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
		if strings.TrimSpace(f.Save) != "" {
			fa.Saved, _ = b.Render(withN(d, a.n), f.Save)
		} else {
			fa.Patch = a.patch
		}
		out.Attempts = append(out.Attempts, fa)
	}
	if ok == 0 {
		return "", fmt.Errorf("task %s: every attempt failed (%d): %s", name, len(atts), atts[len(atts)-1].err)
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// save writes an attempt's patch where the fan-out's `save` names, relative
// to the run's directory. Nothing to do without one.
func (b *Base) save(d *templates.YamlTaskDefinition, a attempt) error {
	if strings.TrimSpace(d.Fanout.Save) == "" {
		return nil
	}
	path, err := b.Render(withN(d, a.n), d.Fanout.Save)
	if err != nil {
		return fmt.Errorf("save template: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.Dir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(a.patch), 0o644)
}

// maxBetween is how much of what `between` printed is kept and judged: the
// end, where a summary is.
const maxBetween = 8000

// settled answers whether the attempts so far are settled, who said so, the
// decision model's probability when it answered, and what `between` printed.
func (b *Base) settled(ctx context.Context, d *templates.YamlTaskDefinition, name string) (bool, string, *float64, string, error) {
	f := d.Fanout
	if strings.TrimSpace(f.Between) == "" {
		return true, "fixed", nil, "", nil
	}
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
	printed := buf.String()
	if len(printed) > maxBetween {
		printed = "[…]\n" + printed[len(printed)-maxBetween:]
	}
	// One way only: what `between` settles is settled. The decision model is
	// asked only when it says not, and can stop the fan-out early, never run
	// it longer than the command would (on the sample it read a matrix with
	// no check against `until`, answered 0.18 and ran five attempts where the
	// command had settled at three, 2026-11-07).
	if runErr == nil {
		return true, "between", nil, printed, nil
	}
	byExit := false
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
			logger.Log.Info(fmt.Sprintf("Task %s: the decision model did not answer on the fan-out (%v); between's exit status decides", name, derr), "component", "workflow")
		}
	}
	return byExit, "between", nil, printed, nil
}
