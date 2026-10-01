package templates

import (
	"bytes"
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
