package main

import (
	"strings"
	"testing"
)

// ── stripANSI ─────────────────────────────────────────────────────────────────

func TestStripANSI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1;32mbold green\x1b[0m", "bold green"},
		{"no escapes", "no escapes"},
		{"\r\n", "\n"},
		{"text\x1b[?25lmore", "textmore"},
	}
	for _, c := range cases {
		got := stripANSI(c.in)
		if got != c.want {
			t.Errorf("stripANSI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── fitWidth ──────────────────────────────────────────────────────────────────

func TestFitWidth_truncates(t *testing.T) {
	got := fitWidth("hello world", 5)
	if got != "hello" {
		t.Errorf("fitWidth truncate = %q, want %q", got, "hello")
	}
}

func TestFitWidth_pads(t *testing.T) {
	got := fitWidth("hi", 6)
	if got != "hi    " {
		t.Errorf("fitWidth pad = %q, want %q", got, "hi    ")
	}
	if len([]rune(got)) != 6 {
		t.Errorf("fitWidth pad len = %d, want 6", len([]rune(got)))
	}
}

func TestFitWidth_exact(t *testing.T) {
	got := fitWidth("abc", 3)
	if got != "abc" {
		t.Errorf("fitWidth exact = %q", got)
	}
}

func TestFitWidth_strips_ansi(t *testing.T) {
	got := fitWidth("\x1b[31mhi\x1b[0m", 4)
	if !strings.HasPrefix(got, "hi") {
		t.Errorf("fitWidth strips ANSI, got %q", got)
	}
	if len([]rune(got)) != 4 {
		t.Errorf("fitWidth len = %d, want 4", len([]rune(got)))
	}
}

// ── imax / imin ───────────────────────────────────────────────────────────────

func TestImax(t *testing.T) {
	if imax(3, 5) != 5 {
		t.Error("imax(3,5)")
	}
	if imax(5, 3) != 5 {
		t.Error("imax(5,3)")
	}
	if imax(4, 4) != 4 {
		t.Error("imax(4,4)")
	}
}

func TestImin(t *testing.T) {
	if imin(3, 5) != 3 {
		t.Error("imin(3,5)")
	}
	if imin(5, 3) != 3 {
		t.Error("imin(5,3)")
	}
	if imin(4, 4) != 4 {
		t.Error("imin(4,4)")
	}
}

// ── statusFrame ───────────────────────────────────────────────────────────────

func TestStatusFrame_done(t *testing.T) {
	p := &panel{status: "done"}
	if got := statusFrame(p, 0); got != "✓" {
		t.Errorf("done frame = %q", got)
	}
}

func TestStatusFrame_failed(t *testing.T) {
	p := &panel{status: "failed"}
	if got := statusFrame(p, 0); got != "✗" {
		t.Errorf("failed frame = %q", got)
	}
}

func TestStatusFrame_running_cycles(t *testing.T) {
	p := &panel{status: "running"}
	frames := map[string]bool{}
	for i := 0; i < len(spinnerFrames)*2; i++ {
		frames[statusFrame(p, i)] = true
	}
	if len(frames) != len(spinnerFrames) {
		t.Errorf("spinner cycles through %d unique frames, want %d", len(frames), len(spinnerFrames))
	}
}

// ── panel ─────────────────────────────────────────────────────────────────────

func TestPanel_push_and_snap(t *testing.T) {
	p := newPanel("test", "echo hi")
	p.push("line1")
	p.push("line2")
	lines := p.snap()
	if len(lines) != 2 {
		t.Fatalf("snap len = %d", len(lines))
	}
	if lines[0] != "line1" || lines[1] != "line2" {
		t.Errorf("snap = %v", lines)
	}
}

func TestPanel_defaults(t *testing.T) {
	p := newPanel("lbl", "cmd")
	if p.status != "running" {
		t.Errorf("status = %q", p.status)
	}
	if !p.autoScroll {
		t.Error("autoScroll should default to true")
	}
}
