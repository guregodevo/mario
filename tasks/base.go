// Package tasks is how a task TYPE is built from its YAML: the file says
// `type:`, the registered factory for that type builds the task. Base does
// everything mario needs from a definition — ids, executables, endpoints,
// retries, timeout, rendering — and takes one function, Run, which is all a
// new type has to write. command and llm are built on it; a host adds its
// own the same way (an agent turn, a browser, a query).
package tasks

import (
	"context"
	"errors"
	"fmt"
	"github.com/guregodevo/mario/logger"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/guregodevo/mario/static"
	"github.com/guregodevo/mario/templates"
	"github.com/guregodevo/mario/workflow"
)

// Run does one task of a type: given the bound factory (its directory,
// partition, helpers) and the definition, it answers the output that becomes
// the task's proof when the definition names no target.
type Run func(ctx context.Context, b *Base, def *templates.YamlTaskDefinition, name string) (string, error)

// Base is mario's TaskFactory for one type. Bind it to a run's definitions
// before registering it: the same Base serves every run of that type.
type Base struct {
	Type      string
	SchemaDoc []byte // the JSON schema for this type's files (factory.Schematic)
	Run       Run

	Defs map[string]*templates.YamlTaskDefinition // by mario name
	// Externals are definitions of tasks this run does not own — they live
	// in another DAG and are only checked here, by THEIR target (mario's
	// cross-DAG dependency through a data endpoint). Optional.
	Externals map[string]*templates.YamlTaskDefinition
	// ExternalDirs says where an external's target is checked when it is
	// not this run's directory: the project that makes it (a dependency on
	// another repository's workflow — each project publishes what it
	// produces as a target, and a consumer checks it there, never runs it).
	ExternalDirs map[string]string
	Dir          string // where targets are checked and commands run
	// Env is the environment a command, a `when` and a command target run
	// with; nil runs them with this process's. A caller that holds secrets
	// its tasks must not see (a model key) answers its environment without
	// them.
	Env     func() []string
	Outputs Outputs // where outputs are kept when no target is named
	// Isolator gives each attempt of a fan-out a working directory of its
	// own; nil: a fan-out fails and says why.
	Isolator Isolator
	// Decider reads a fan-out's evidence against its `until`; nil: the
	// `between` command's exit status settles it.
	Decider   Decider
	Partition string
	Version   string
}

// Bind answers a copy tied to one run: its definitions, directory, outputs,
// partition and version.
func (b Base) Bind(defs map[string]*templates.YamlTaskDefinition, dir string, outputs Outputs, partition, version string) *Base {
	b.Defs, b.Dir, b.Outputs, b.Partition, b.Version = defs, dir, outputs, partition, version
	return &b
}

func (b *Base) Name() []string { return []string{b.Type} }
func (b *Base) Schema() []byte { return b.SchemaDoc }

// Def answers the definition of a task of this run, if it has one. A name
// without one is an external: made elsewhere, only checked.
func (b *Base) Def(name string) (*templates.YamlTaskDefinition, bool) {
	d, ok := b.Defs[name]
	return d, ok
}

func (b *Base) NewWorkflow(name, version, partition, component string) (workflow.WorkflowInstanceId, error) {
	d, defined := b.Defs[name]
	retries := int32(0)
	if defined {
		retries = d.MaxRetries
	}
	id := b.builder().SetWorkflow(name, retries, !defined, version, component).SetRuntime(partition, workflow.Scheduled, 0).OfInstance()
	id.DOptional = b.optional(name)
	return id, nil
}

func (b *Base) NewExecutable(name string) *workflow.ExecutableWorkflowInstance {
	id, _ := b.NewWorkflow(name, b.Version, b.Partition, "")
	exe := b.builder().SetWorkflow(name, id.DMaxRetries, id.DExternal, b.Version, id.DComponent).
		SetRuntime(b.Partition, workflow.Scheduled, 0).SetConcrete(b.NewDataEndpoint(name), b.Fn(name)).Instance().ToExecutable()
	exe.DOptional = id.DOptional
	return exe
}

func (b *Base) ExecutableOf(exe workflow.WorkflowExecution) *workflow.ExecutableWorkflowInstance {
	out := b.builder().SetExecution(exe).SetConcrete(b.NewDataEndpoint(exe.WorkflowName()), b.Fn(exe.WorkflowName())).Instance().ToExecutable()
	// From the definition, not the record: a repository that stores the id's
	// columns one by one does not keep it.
	out.DOptional = b.optional(exe.WorkflowName())
	return out
}

// optional reads the flag off the definition; an external is never optional.
func (b *Base) optional(name string) bool {
	d, defined := b.Defs[name]
	return defined && d.Optional
}

// Fn is the task's run: Run under the definition's timeout, then the proof —
// the target it named must exist, or the output is written where the
// endpoint looks. A fan-out runs several attempts, each in a working
// directory of its own.
func (b *Base) Fn(name string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		d, defined := b.Defs[name]
		if !defined {
			return fmt.Errorf("task %s is external: it is made outside this workflow, never run by it", name)
		}
		if strings.TrimSpace(d.When) != "" {
			line, err := b.Render(d, d.When)
			if err != nil {
				return fmt.Errorf("task %s: when template: %w", name, err)
			}
			// The condition runs under the task's clock, as it always has.
			wctx := ctx
			if d.Timeout != "" {
				if to, err := ParseTimeout(d.Timeout); err == nil {
					var cancel context.CancelFunc
					wctx, cancel = context.WithTimeout(ctx, to)
					defer cancel()
				}
			}
			if err := shellCommand(wctx, b.Dir, line, b.Env).Run(); err != nil {
				logger.Log.Info(fmt.Sprintf("Skipping task %s: its condition does not hold (when: %s)", name, shown(line)), "component", "workflow")
				return b.Outputs.Write(name, b.Partition, "")
			}
		}
		var out string
		var err error
		switch {
		case d.Fanout != nil:
			out, err = b.fanout(ctx, d, name)
		default:
			out, err = b.once(ctx, d, name)
		}
		if err != nil {
			return err
		}
		// The answer is kept either way, so a task after it can read it with
		// {{ output "name" }}. With a target it is not the proof — the target
		// is — only what the task said. Dropping it failed every downstream
		// prompt that read a targeted task's answer ("output of … is not
		// there", a run 2026-10-04).
		return b.Outputs.Write(name, b.Partition, out)
	}
}

// once is one run of the task in b.Dir under its timeout, and its proof.
func (b *Base) once(ctx context.Context, d *templates.YamlTaskDefinition, name string) (string, error) {
	if d.Timeout != "" {
		to, err := ParseTimeout(d.Timeout)
		if err != nil {
			return "", fmt.Errorf("task %s: timeout %q: %w", name, d.Timeout, err)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, to)
		defer cancel()
	}
	out, err := b.Run(ctx, b, d, name)
	if err != nil {
		// Its own clock ran out: say so, not the kill's "signal: killed".
		if d.Timeout != "" && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// The target is the proof, the clock is not: a turn stopped
			// at its timeout with its target there did the work (an
			// agent that had changed the tree and kept polishing it
			// ended 13 runs of 500 unchecked, 2026-11-06).
			if HasTarget(d) && b.targetEndpoint(name).Exists() {
				note := fmt.Sprintf("timed out after %s (its timeout); its target is there", d.Timeout)
				logger.Log.Info(fmt.Sprintf("Task %s %s", name, note), "component", "workflow")
				return note, nil
			}
			return "", fmt.Errorf("task %s timed out after %s (its timeout) and was stopped", name, d.Timeout)
		}
		return "", err
	}
	if HasTarget(d) {
		// The target is the proof: a run that returned without error but
		// left no target behind did not do the work.
		if ep := b.targetEndpoint(name); !ep.Exists() {
			// Name the target as it was checked, rendered: a failure
			// that printed the template ("file rules/{{.partition}}.md")
			// sent a person looking for a file by that name (2026-10-07).
			what := DescribeTarget(d)
			if s, ok := ep.(fmt.Stringer); ok {
				what = s.String()
			}
			return "", fmt.Errorf("task %s finished but its target is not there (%s)", name, what)
		}
	}
	return out, nil
}

// NewDataEndpoint is the task's proof: the target its definition names, or
// its output by convention — also for an external, whose maker writes there.
// A fan-out's proof is its output: its target is about the copy each
// attempt ran in (checked there, by once), and in the run's directory it
// may hold before the task ever ran (an "uncommitted change" target holds in
// any dirty tree), which would skip it.
func (b *Base) NewDataEndpoint(name string) workflow.DataEndpoint {
	if d, known := b.Defs[name]; known && d.Fanout != nil {
		return b.Outputs.Endpoint(name, b.Partition)
	}
	return b.targetEndpoint(name)
}

// targetEndpoint is the target the definition names, checked in b.Dir, or
// its output when it names none.
func (b *Base) targetEndpoint(name string) workflow.DataEndpoint {
	d, known := b.Defs[name]
	dir := b.Dir
	if !known {
		d, known = b.Externals[name]
		if ext, ok := b.ExternalDirs[name]; ok && ext != "" {
			dir = ext // the maker's project, not this run's
		}
	}
	if known && HasTarget(d) {
		// A target is a template too: "DIGEST-{{.partition}}.md" is a file
		// each run makes its own; "DIGEST.md" is one file, done once it
		// exists on any run.
		if d.Target.File != "" {
			return FileEndpoint(name, dir, b.renderOr(d, d.Target.File))
		}
		return CommandEndpointWithEnv(name, dir, b.renderOr(d, d.Target.Command), b.Env)
	}
	return b.Outputs.Endpoint(name, b.Partition)
}

// renderOr renders text over the definition's args and partition, or
// answers it as written when it is not a template that renders.
func (b *Base) renderOr(d *templates.YamlTaskDefinition, text string) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	if out, err := b.Render(d, text); err == nil {
		return out
	}
	return text
}

func (b *Base) builder() workflow.WorflowBuilder {
	return &static.StaticWorflowBuilder{Inst: &static.CommonExecutable{}}
}

// Prompt is the definition's prompt, rendered — by the walk's template when
// it set one, else over args and partition here.
func (b *Base) Prompt(d *templates.YamlTaskDefinition) (string, error) {
	// A template directory's renderer is the one to use when there was
	// one, for the partition it was walked with; otherwise the type renders
	// the prompt itself, with the run's partition and outputs.
	if d.Templated && d.LazyRenderedField != nil && d.Partition == b.Partition {
		return d.LazyRenderedField()
	}
	return b.Render(d, d.Prompt)
}

// Render evaluates text as a Go template over the definition's args, its
// partition and its name — what ArgsOnly gives a walk.
func (b *Base) Render(d *templates.YamlTaskDefinition, text string) (string, error) {
	seg := strings.Split(d.Name, ".")
	for len(seg) < 3 {
		seg = append(seg, "")
	}
	vars, funcs, err := templates.ArgsOnly(d, b.Partition, seg[0], seg[1], seg[2])
	if err != nil {
		return "", err
	}
	// `output "log"` puts the kept output of a task of this run in the text
	// — a short name is a task of the same group, a dotted one is any.
	funcs["output"] = func(name string) (string, error) {
		full := name
		if !strings.Contains(name, ".") {
			full = seg[0] + "." + seg[1] + "." + name
		}
		out, err := b.Outputs.Read(full, b.Partition)
		if err != nil {
			// A targeted task skipped because its target already held (a
			// resume) never ran this time, so no answer was kept. Its target
			// is its proof: say that instead of failing the prompt.
			if d, ok := b.Defs[full]; ok && HasTarget(d) && b.NewDataEndpoint(full).Exists() {
				return "(" + name + " was already done: " + DescribeTarget(d) + " holds; its answer was not kept)", nil
			}
			// An optional task that was passed over answered nothing: the
			// prompt reads an empty string, not an error.
			if d, ok := b.Defs[full]; ok && d.Optional {
				return "", nil
			}
			return "", fmt.Errorf("output of %s is not there (is it required by this task?): %w", full, err)
		}
		return out, nil
	}
	// `file "x.diff"` puts a file of the project in the text, as it is when
	// the task runs: what a command before it wrote, read by a model call
	// that has no tools to open it.
	funcs["file"] = func(path string) (string, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(b.Dir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("file %s: %w", path, err)
		}
		return string(data), nil
	}
	// `exists "x.md"` says whether a file of the project is there, so a template reads a file a passed-over
	// task may not have written: {{ if exists "x.md" }}{{ file "x.md" }}{{ end }}.
	funcs["exists"] = func(path string) (bool, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(b.Dir, path)
		}
		fi, err := os.Stat(path)
		return err == nil && !fi.IsDir(), nil
	}
	// An arg's value is a template too, rendered once over the same args and funcs: a workflow that pulls
	// another in binds that one's args to its own outputs (`dir: '{{ output "ship.steps.handoff" }}'`).
	for k, v := range d.Args {
		if !strings.Contains(v, "{{") {
			continue
		}
		out, err := execute(d.Name+"."+k, v, funcs, vars)
		if err != nil {
			return "", fmt.Errorf("arg %s: %w", k, err)
		}
		vars[k] = out
	}
	return execute(d.Name, text, funcs, vars)
}

func execute(name, text string, funcs template.FuncMap, vars map[string]interface{}) (string, error) {
	t, err := template.New(name).Funcs(funcs).Parse(text)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := t.Execute(&sb, vars); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// HasTarget answers whether a definition names its own proof.
func HasTarget(d *templates.YamlTaskDefinition) bool {
	return d.Target != nil && (d.Target.File != "" || d.Target.Command != "")
}

// DescribeTarget reads a target for a person: "file X", "command: X", or "output".
func DescribeTarget(d *templates.YamlTaskDefinition) string {
	switch {
	case d.Target != nil && d.Target.File != "":
		return "file " + d.Target.File
	case d.Target != nil && d.Target.Command != "":
		return "command: " + d.Target.Command
	}
	return "output"
}

// ParseTimeout reads "20m" and, as the schema allows, plain seconds.
func ParseTimeout(s string) (time.Duration, error) {
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}
