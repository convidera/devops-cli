package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJoinEntries_prependsSetE(t *testing.T) {
	got := joinEntries([]Entry{{Script: "echo one"}, {Script: "echo two"}})
	want := "set -e\necho one\necho two"
	if got != want {
		t.Errorf("joinEntries() = %q, want %q", got, want)
	}
}

func TestJoinEntries_empty(t *testing.T) {
	got := joinEntries(nil)
	if got != "set -e" {
		t.Errorf("joinEntries(nil) = %q, want %q", got, "set -e")
	}
}

// TestRunModuleSequential_stateCarriesAcrossEntries verifies that `cd` and
// `export` in one entry affect subsequent entries for the same container,
// which requires the whole container's entries to run as one shell process.
func TestRunModuleSequential_stateCarriesAcrossEntries(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "marker.txt")

	m := &Module{
		Name: "test",
		Config: CommandConfig{
			"go": {
				"host": []Entry{
					{Script: "cd " + sub},
					{Script: "export FOO=bar"},
					{Script: `pwd > "` + marker + `"; echo "$FOO" >> "` + marker + `"`},
				},
			},
		},
	}

	if err := runModuleSequential(m, "go", nil); err != nil {
		t.Fatalf("runModuleSequential() error = %v", err)
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("reading marker file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %q", string(data))
	}
	wantSub, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	gotSub, err := filepath.EvalSymlinks(lines[0])
	if err != nil {
		t.Fatalf("resolving pwd output %q: %v", lines[0], err)
	}
	if gotSub != wantSub {
		t.Errorf("cd did not carry over: pwd = %q, want %q", gotSub, wantSub)
	}
	if lines[1] != "bar" {
		t.Errorf("export did not carry over: FOO = %q, want %q", lines[1], "bar")
	}
}

// TestRunModuleSequential_stopsOnFailure verifies that `set -e` still makes a
// failing entry abort the remaining entries for that container.
func TestRunModuleSequential_stopsOnFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")

	m := &Module{
		Name: "test",
		Config: CommandConfig{
			"go": {
				"host": []Entry{
					{Script: "false"},
					{Script: `echo should-not-run > "` + marker + `"`},
				},
			},
		},
	}

	if err := runModuleSequential(m, "go", nil); err == nil {
		t.Fatal("expected error from failing entry, got nil")
	}

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("marker file should not exist, later entry ran despite earlier failure")
	}
}
