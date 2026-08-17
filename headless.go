package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// noTUIEnv forces the headless parallel runner even when a terminal is
// available. Useful for debugging and for scripts that want plain output.
const noTUIEnv = "DEVOPS_NO_TUI"

// canUseTUI reports whether the interactive parallel view can be started.
// Bubble Tea needs a real terminal: when stdin is not a TTY it tries to open
// /dev/tty for keyboard input, which fails outright in non-interactive
// contexts (CI, `devops lint | tee log`, editors and agents without a
// controlling terminal) with "could not open a new TTY". Detect that up front
// so the caller can fall back to plain logging instead of erroring out.
func canUseTUI() bool {
	if v := os.Getenv(noTUIEnv); v != "" && v != "0" {
		return false
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	// Even with both ends attached, a missing controlling terminal makes
	// Bubble Tea's /dev/tty fallback fail. Probe it the same way it does.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = tty.Close()
	return true
}

// runHeadless runs all panels concurrently without the TUI, streaming each
// panel's output to w prefixed with its module label so interleaved lines stay
// attributable. Returns true if all panels succeeded.
func runHeadless(w io.Writer, panels []*panel) bool {
	var mu sync.Mutex
	emit := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, format, args...)
	}

	width := 0
	for _, p := range panels {
		if n := len([]rune(p.label)); n > width {
			width = n
		}
	}
	prefix := func(idx int) string {
		label := panels[idx].label
		return "[" + label + "]" + strings.Repeat(" ", width-len([]rune(label))) + " "
	}

	for i := range panels {
		emit("%sstarting\n", prefix(i))
	}

	sink := panelSink{
		line: func(idx int, line string) {
			emit("%s%s\n", prefix(idx), line)
		},
		exit: func(idx, code int) {
			p := panels[idx]
			p.mu.Lock()
			p.exitCode = code
			if code == 0 {
				p.status = "done"
			} else {
				p.status = "failed"
			}
			p.mu.Unlock()
			if code == 0 {
				emit("%sdone\n", prefix(idx))
			} else {
				emit("%sfailed (exit %d)\n", prefix(idx), code)
			}
		},
	}

	// Forward interrupts to the children rather than dying first, matching
	// runWithSignalForwarding: tools like `docker compose` do their own
	// graceful shutdown and need the signal to reach them.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				for _, p := range panels {
					p.signal(sig)
				}
			case <-done:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i, p := range panels {
		wg.Add(1)
		go func(idx int, p *panel) {
			defer wg.Done()
			runPanel(idx, p, sink)
		}(i, p)
	}
	wg.Wait()
	close(done)

	mu.Lock()
	defer mu.Unlock()
	return printSummary(w, panels)
}
