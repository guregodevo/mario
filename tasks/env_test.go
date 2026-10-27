package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

const secret = "MARIO_TEST_SECRET"

// withoutSecret is the environment a caller hands Base.Env: this process's,
// minus the one variable its tasks must not see.
func withoutSecret() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, secret+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// envDAG runs one task through every place a task runs a shell line: its
// `when`, its `command`, and its command target. Each one holds only when
// the secret is absent from the environment it runs with.
func envDAG(t *testing.T, env func() []string) (seq []string, out string) {
	t.Helper()
	t.Setenv(secret, "sk-must-not-leak")
	root := t.TempDir()
	types := []Base{Command()}
	types[0].Env = env
	reg := factory.NewComponent()
	reg.Add(&types[0])
	defs := writeDAG(t, root, reg, map[string]string{
		"probe": "type: command\n" +
			"when: test -z \"$" + secret + "\"\n" +
			"command: printf '[%s]' \"$" + secret + "\" > probe.txt\n" +
			"target:\n  command: test -f probe.txt && test -z \"$" + secret + "\"\n",
	})
	seq = run(t, root, types, defs)
	b, _ := os.ReadFile(filepath.Join(root, "probe.txt"))
	return seq, string(b)
}

// With Env, the command, its `when` and its target never see the secret.
func TestCommandsRunWithTheEnvironmentEnvAnswers(t *testing.T) {
	seq, out := envDAG(t, withoutSecret)
	if got := strings.Join(seq, " | "); got != "started probe | done probe" {
		t.Fatalf("events = %q", got)
	}
	if out != "[]" {
		t.Fatalf("the command saw the secret: %q", out)
	}
}

// Without Env, a command runs with this process's environment, as before:
// the `when` is false (the secret is there), so the task is skipped as done
// and never writes its file.
func TestANilEnvIsThisProcesssEnvironment(t *testing.T) {
	seq, out := envDAG(t, nil)
	if got := strings.Join(seq, " | "); got != "started probe | done probe" {
		t.Fatalf("events = %q", got)
	}
	if out != "" {
		t.Fatalf("the when should have skipped the command, it wrote %q", out)
	}
}
