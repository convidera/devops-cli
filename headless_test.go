package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestRunHeadless_success(t *testing.T) {
	var buf bytes.Buffer
	ok := runHeadless(&buf, []*panel{newPanel("api", "echo hello")})
	if !ok {
		t.Fatalf("runHeadless = false, want true\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "[api] hello") {
		t.Errorf("missing prefixed output line:\n%s", out)
	}
	if !strings.Contains(out, "✓  api") {
		t.Errorf("missing summary entry:\n%s", out)
	}
}

func TestRunHeadless_failure(t *testing.T) {
	var buf bytes.Buffer
	p := newPanel("web", "exit 3")
	if ok := runHeadless(&buf, []*panel{p}); ok {
		t.Fatalf("runHeadless = true for failing panel\n%s", buf.String())
	}
	p.mu.RLock()
	status, exitCode := p.status, p.exitCode
	p.mu.RUnlock()
	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if exitCode != 3 {
		t.Errorf("exitCode = %d, want 3", exitCode)
	}
	if out := buf.String(); !strings.Contains(out, "(exit 3)") {
		t.Errorf("missing exit code in output:\n%s", out)
	}
}

func TestRunHeadless_multiplePanelsAreLabeled(t *testing.T) {
	var buf bytes.Buffer
	panels := []*panel{
		newPanel("a", "echo one"),
		newPanel("longname", "echo two"),
	}
	if ok := runHeadless(&buf, panels); !ok {
		t.Fatalf("runHeadless = false\n%s", buf.String())
	}
	out := buf.String()
	// Labels are padded to a common width so lines stay aligned.
	if !strings.Contains(out, "[a]        one") {
		t.Errorf("short label not padded:\n%s", out)
	}
	if !strings.Contains(out, "[longname] two") {
		t.Errorf("long label output missing:\n%s", out)
	}
}

func TestRunHeadless_capturesStderr(t *testing.T) {
	var buf bytes.Buffer
	if ok := runHeadless(&buf, []*panel{newPanel("api", "echo oops >&2")}); !ok {
		t.Fatalf("runHeadless = false\n%s", buf.String())
	}
	if out := buf.String(); !strings.Contains(out, "[api] oops") {
		t.Errorf("stderr not captured:\n%s", out)
	}
}

func TestRunHeadless_writeError(t *testing.T) {
	if ok := runHeadless(errWriter{}, []*panel{newPanel("api", "true")}); ok {
		t.Fatal("runHeadless = true with failing writer")
	}
}

func TestCanUseTUI_envOverride(t *testing.T) {
	t.Setenv(noTUIEnv, "1")
	if canUseTUI() {
		t.Errorf("canUseTUI = true with %s=1", noTUIEnv)
	}
}

func TestCanUseTUI_falseWithoutTerminal(t *testing.T) {
	t.Setenv(noTUIEnv, "")
	// `go test` runs with stdout redirected, so the TUI must not be selected.
	if canUseTUI() {
		t.Errorf("canUseTUI = true without a terminal on stdout")
	}
}
