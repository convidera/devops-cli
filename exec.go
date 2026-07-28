package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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
		fmt.Printf("Running commands for: %s\n", container)
		for _, entry := range entries {
			fmt.Printf("Executing: %s\n", entry.Script)
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
	return cmd.Run()
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
	return cmd.Run()
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
