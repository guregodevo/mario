package tasks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/guregodevo/mario/workflow"
)

// Outputs keeps each task's output as a file: <Dir>/<name segments>/<partition>.
// It is the proof of a task that names no target of its own, and where an
// external's maker writes (a person approving, another system dropping a
// file) — one convention for every type.
type Outputs struct {
	Dir string
}

// Path is the output file of a task for one partition.
func (o Outputs) Path(name, partition string) string {
	return filepath.Join(append([]string{o.Dir}, append(strings.Split(name, "."), partition)...)...)
}

// Write keeps an output.
func (o Outputs) Write(name, partition, data string) error {
	p := o.Path(name, partition)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(data), 0o644)
}

// Read answers an output.
func (o Outputs) Read(name, partition string) (string, error) {
	b, err := os.ReadFile(o.Path(name, partition))
	return string(b), err
}

// Endpoint is the output as a data endpoint: it exists when the file does.
func (o Outputs) Endpoint(name, partition string) workflow.DataEndpoint {
	return &fileEndpoint{name: name, path: o.Path(name, partition)}
}

// FileEndpoint is a target: a file under dir that must exist.
func FileEndpoint(name, dir, file string) workflow.DataEndpoint {
	if !filepath.IsAbs(file) {
		file = filepath.Join(dir, file)
	}
	return &fileEndpoint{name: name, path: file}
}

// CommandEndpoint is a target: a command that must exit 0 in dir.
func CommandEndpoint(name, dir, command string) workflow.DataEndpoint {
	return &commandEndpoint{name: name, dir: dir, command: command}
}

type fileEndpoint struct {
	name string
	path string
}

func (e *fileEndpoint) Name() string { return e.name }
func (e *fileEndpoint) Exists() bool {
	st, err := os.Stat(e.path)
	return err == nil && !st.IsDir()
}

type commandEndpoint struct {
	name    string
	dir     string
	command string
}

func (e *commandEndpoint) Name() string { return e.name }
func (e *commandEndpoint) Exists() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", e.command)
	cmd.Dir = e.dir
	return cmd.Run() == nil
}
