package templates

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// TestRenderWithArgs tests the Render function with args to check that variables are correctly injected.
func TestRenderWithArgs(t *testing.T) {
	// Define the test case
	tests := []struct {
		name      string
		taskDef   *YamlTaskDefinition
		fieldName string
		vars      map[string]interface{}
		want      string
		wantErr   bool
	}{
		{
			name: "Template with args (n = 10)",
			taskDef: &YamlTaskDefinition{
				ContentType: "json",
				Prompt:      `Summary this in {{.n}} words: The Norwegian Nobel Committee has decided to award the Nobel Peace Prize for 2024...`, // Template to render
				Args: map[string]string{
					"n": "10", // Pass "n" in the args
				},
			},
			fieldName: "Prompt",
			vars:      map[string]interface{}{
				// Mock data if needed in the vars map
			},
			want:    `Summary this in 10 words: The Norwegian Nobel Committee has decided to award the Nobel Peace Prize for 2024...`,
			wantErr: false,
		},
	}

	// Define a base template and a function map (funcs) for rendering
	tmpl := template.New("base")
	funcs := template.FuncMap{}

	// Run test cases
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Define the sub-template "nobel_prize_2024.txt"
			tmpl, err := tmpl.New("nobel_prize_2024.txt").Parse("The Norwegian Nobel Committee has decided to award the Nobel Peace Prize for 2024...")
			if err != nil {
				t.Fatalf("Error parsing sub-template: %v", err)
			}

			// Prepare the lazy-rendering function
			fn, err := RenderLazy(tmpl, tt.vars, funcs, tt.taskDef, tt.fieldName)
			if err != nil {
				t.Errorf("Error preparing lazy template for file %s: %v", tt.name, err)
				return
			}

			// Store the lazy-rendered function in the task definition for later execution
			tt.taskDef.LazyRenderedField = fn

			// Now invoke the lazy-rendering function to evaluate the template
			renderedValue, errRender := tt.taskDef.LazyRenderedField()
			if (errRender != nil) != tt.wantErr {
				t.Errorf("Render() error = %v, wantErr %v", errRender, tt.wantErr)
				return
			}

			// Check if the output matches the expected value
			if !tt.wantErr {
				got := normalizeWhitespace(renderedValue)
				want := normalizeWhitespace(tt.want)

				if got != want {
					t.Errorf("Render() got = %v, want = %v", got, want)
				}
			}
		})
	}
}

// Helper function to remove extra whitespaces for comparison
func normalizeWhitespace(s string) string {
	return string(bytes.TrimSpace([]byte(s)))
}

func TestStringFuncs(t *testing.T) {
	f := StringFuncs()
	if got := f["trim"].(func(string) string)("  a\n"); got != "a" {
		t.Errorf("trim %q", got)
	}
	if !f["contains"].(func(string, string) bool)("b", "abc") || f["contains"].(func(string, string) bool)("z", "abc") {
		t.Error("contains")
	}
	if !f["hasPrefix"].(func(string, string) bool)("ab", "abc") || !f["hasSuffix"].(func(string, string) bool)("bc", "abc") {
		t.Error("hasPrefix / hasSuffix")
	}
	d := f["default"].(func(string, string) string)
	if d("none", " \n") != "none" || d("none", "x") != "x" {
		t.Error("default")
	}
}

func TestHeadAndTailKeepLines(t *testing.T) {
	f := StringFuncs()
	head, tail := f["head"].(func(int, string) string), f["tail"].(func(int, string) string)
	for _, c := range []struct {
		name, got, want string
	}{
		{"head 1", head(1, "a\nb\nc\n"), "a\n"},
		{"head 1, no final newline", head(1, "a\nb"), "a"},
		{"head past the end", head(9, "a\nb\n"), "a\nb\n"},
		{"tail 2", tail(2, "a\nb\nc\n"), "b\nc\n"},
		{"tail 2, no final newline", tail(2, "a\nb\nc"), "b\nc"},
		{"tail past the end", tail(9, "a\nb"), "a\nb"},
		{"tail 0", tail(0, "a\nb"), ""},
		{"head of nothing", head(3, ""), ""},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestJSONReadsOneField(t *testing.T) {
	read := StringFuncs()["json"].(func(string, string) (string, error))
	doc := `{"workflow_run":{"id":12345678901234567890,"ok":true},"issue":{"title":"It \"breaks\"","body":null,"labels":[{"name":"bug"},{"name":"p1"}]}}`
	for path, want := range map[string]string{
		"workflow_run.id":     "12345678901234567890", // as written, not 1.2345678901234567e+19
		"workflow_run.ok":     "true",
		"issue.title":         `It "breaks"`,
		"issue.body":          "",
		"issue.labels.1.name": "p1",
		"issue.labels.7.name": "",
		"issue.labels.0":      `{"name":"bug"}`,
		"no.such.field":       "",
		"issue.title.deeper":  "",
	} {
		got, err := read(path, doc)
		if err != nil || got != want {
			t.Errorf("json %q: %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := read("a", "not json"); err == nil || !strings.Contains(err.Error(), "the text is not JSON") {
		t.Errorf("not JSON: err %v", err)
	}
}

func TestMatchFindsTheFirstGroupOrTheWholeMatch(t *testing.T) {
	find := StringFuncs()["match"].(func(string, string) (string, error))
	for _, c := range []struct{ pattern, text, want string }{
		{`(?im)^[ \t*_]*verdict:[ \t*_]*(pass|fail)`, "Looking at it.\n**Verdict:** PASS\nverdict: FAIL\n", "PASS"},
		{`(?im)^verdict: (pass|fail)`, "I think the verdict: PASS is right\n", ""},
		{`exit \d+`, "ran\nexit 3\n", "exit 3"},
		{`(a)(b)`, "ab", "a"},
		{`x`, "", ""},
	} {
		got, err := find(c.pattern, c.text)
		if err != nil || got != c.want {
			t.Errorf("match %q in %q: %q, %v; want %q", c.pattern, c.text, got, err, c.want)
		}
	}
	if _, err := find("(", "x"); err == nil || !strings.Contains(err.Error(), "not a regular expression") {
		t.Errorf("a bad expression: err %v", err)
	}
}
