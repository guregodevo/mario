package tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guregodevo/mario/factory"
)

// A rule read from a model's answer without a shell: the verdict line when the answer is there, else the
// status of a check. exists lets the template read a file a passed-over task did not write.
func TestATemplateReadsAVerdictWithMatchAndExists(t *testing.T) {
	rule := "type: template\ntext: |\n" +
		`  {{ $v := "" }}{{ if exists "review.md" }}{{ $v = file "review.md" | head 10 | match "(?im)^[ \\t*_]*verdict:[ \\t*_]*(pass|fail)" }}{{ end }}` +
		`{{ if or ($v | match "(?i)^pass$") (and (not $v) (eq (file "status.txt" | trim) "0")) }}PASS{{ else }}FAIL{{ end }}` + "\n"
	for _, c := range []struct{ name, review, status, want string }{
		{"a verdict", "**Verdict:** pass\nbecause\n", "1", "PASS"},
		{"a failing verdict over a passing check", "verdict: FAIL\n", "0", "FAIL"},
		{"no verdict, the check passed", "Let me look at the diff.\n", "0", "PASS"},
		{"no verdict, the check failed", "Let me look at the diff.\n", "1", "FAIL"},
		{"a verdict past the tenth line is not one", strings.Repeat("thinking\n", 10) + "verdict: FAIL\n", "0", "PASS"},
		{"no review at all, the check passed", "", "0", "PASS"},
		{"no review at all, the check failed", "", "1", "FAIL"},
	} {
		root := t.TempDir()
		types := []Base{Template()}
		reg := factory.NewComponent()
		for i := range types {
			reg.Add(&types[i])
		}
		if c.review != "" {
			if err := os.WriteFile(filepath.Join(root, "review.md"), []byte(c.review), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, "status.txt"), []byte(c.status+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defs := writeDAG(t, root, reg, map[string]string{"rule": rule})
		if seq := strings.Join(run(t, root, types, defs), " | "); !strings.HasSuffix(seq, "done rule") {
			t.Fatalf("%s: events = %q", c.name, seq)
		}
		if out, err := (Outputs{Dir: filepath.Join(root, "out")}).Read("wf.steps.rule", "2026-10-02"); err != nil || strings.TrimSpace(out) != c.want {
			t.Errorf("%s: %q, %v; want %s", c.name, out, err, c.want)
		}
	}
}

// A match expression that is not a regular expression is refused before a run.
func TestCompileRefusesAMatchThatIsNotARegularExpression(t *testing.T) {
	err := compileOf(t, map[string]string{
		"rule": "type: template\ntext: '{{ \"x\" | match \"(pass|fail\" }}'\n",
	})
	if err == nil || !strings.Contains(err.Error(), `steps/rule.yaml: text: {{ match "(pass|fail" }} is not a regular expression`) || !strings.Contains(err.Error(), "Go's syntax") {
		t.Fatalf("got %v", err)
	}
	if err := compileOf(t, map[string]string{"rule": "type: template\ntext: '{{ \"x\" | match \"(pass|fail)\" }}'\n"}); err != nil {
		t.Fatalf("a good expression was refused: %v", err)
	}
}
