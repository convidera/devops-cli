package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// runModuleSequential runs all containers/entries for a module's command in order.
// Entries for the same container share a single shell invocation, so state like
// `cd` and exported variables carries over from one entry to the next.
func runModuleSequential(m *Module, command string, extraArgs []string) error {
	containers, ok := m.Config[command]
	if !ok {
		return fmt.Errorf("command %q not found in module %s", command, m.Name)
	}
	for container, entries := range containers {
		fmt.Fprintf(os.Stderr, "Running commands for: %s\n", container)
		for _, entry := range entries {
			fmt.Fprintf(os.Stderr, "Executing: %s\n", entry.Script)
		}
		if err := runScript(container, joinEntries(entries), extraArgs); err != nil {
			return fmt.Errorf("[%s/%s] command failed: %w", m.Name, container, err)
		}
	}
	return nil
}

// joinEntries combines a container's entries into a single shell script so
// they run in one process, letting `cd`/`export`/etc. persist across lines.
// `set -e` makes the script stop at the first failing entry, matching the
// previous per-entry fail-fast behavior.
func joinEntries(entries []Entry) string {
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, "set -e")
	for _, e := range entries {
		lines = append(lines, e.Script)
	}
	return strings.Join(lines, "\n")
}

// runScript executes a single script in the given container (or "host").
// extraArgs are passed as positional parameters so $@ expands correctly inside the script.
func runScript(container, script string, extraArgs []string) error {
	// sh -c 'script' sh arg1 arg2…  →  $0=sh, $1=arg1, $@=all args
	shArgs := append([]string{"-c", script, "sh"}, extraArgs...)

	var cmd *exec.Cmd
	if container == "host" {
		cmd = exec.Command("sh", shArgs...)
	} else {
		args := []string{"compose", "exec"}
		if !isTTY() {
			args = append(args, "-T")
		}
		args = append(args, container, "sh")
		args = append(args, shArgs...)
		cmd = exec.Command("docker", args...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runWithSignalForwarding(cmd)
}

// runExec opens an interactive shell (or runs a command) in a module's container.
func runExec(m *Module, extraArgs []string) error {
	container := m.firstContainer()
	if container == "" || container == "null" {
		return fmt.Errorf("no container found for module %s", m.Name)
	}
	if container == "host" {
		return fmt.Errorf("module %s runs on the host; there is no container to exec into", m.Name)
	}

	args := []string{"compose", "exec"}
	if isTTY() {
		args = append(args, "-it")
	}
	args = append(args, container)

	if len(extraArgs) == 0 {
		args = append(args, "sh", "-c", `exec "$(command -v bash || command -v sh)"`)
	} else {
		args = append(args, extraArgs...)
	}

	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return runWithSignalForwarding(cmd)
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// runWithSignalForwarding starts cmd and blocks until it exits, forwarding
// every SIGINT/SIGTERM we receive straight through to it instead of letting
// Go's default handling kill this process immediately. Without this, a
// single Ctrl+C would kill the devops-cli wrapper right away while a child
// like `docker compose up` kept running its own graceful shutdown in the
// background, printing to the shared terminal after control had already
// returned to the shell.
//
// Deliberately does NOT escalate to SIGKILL on a repeated signal: tools like
// `docker compose` already implement their own "first Ctrl+C graceful, second
// forces it" handling internally. If we SIGKILL the child ourselves instead
// of forwarding that second signal, we yank it out mid-shutdown before it
// can tell the engine to stop remaining containers, potentially leaving them
// running — worse than doing nothing.
func runWithSignalForwarding(cmd *exec.Cmd) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-sigCh:
				_ = cmd.Process.Signal(sig)
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	if code, ok := brokenPipe(err); ok {
		os.Exit(code)
	}
	return err
}

// brokenPipe reports whether a failed child stopped because our stdout was
// closed (e.g. `devops agents artisan route:list | head`) and the code to exit
// with quietly. `docker compose exec` reports that as exit 255, so the pipe
// itself is checked rather than only the child's status.
func brokenPipe(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGPIPE {
		code = 128 + int(syscall.SIGPIPE)
	}
	return code, code == 128+int(syscall.SIGPIPE) || pipeClosed(os.Stdout)
}

// pipeClosed reports whether f is a pipe or socket whose reader has gone away.
func pipeClosed(f *os.File) bool {
	fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLOUT}}
	n, err := unix.Poll(fds, 0)
	return err == nil && n > 0 && fds[0].Revents&(unix.POLLERR|unix.POLLHUP) != 0
}
