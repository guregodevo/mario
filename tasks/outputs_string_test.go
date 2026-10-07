package tasks

import "testing"

// A target names itself as it was checked: the rendered path or command,
// what a failure prints so a person looks for the right file.
func TestATargetNamesItselfRendered(t *testing.T) {
	if got := FileEndpoint("build", "/p", "rules/2026-10-07.md").(interface{ String() string }).String(); got != "file /p/rules/2026-10-07.md" {
		t.Fatalf("file target = %q", got)
	}
	if got := CommandEndpoint("grade", "/p", "test a -nt b").(interface{ String() string }).String(); got != "command: test a -nt b" {
		t.Fatalf("command target = %q", got)
	}
}
