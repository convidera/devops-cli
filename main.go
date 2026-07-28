package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		showHelp()
		return
	}

	switch args[0] {
	case "help", "--help", "-h":
		showHelp()
		return
	case "all":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: devops all <command>")
			os.Exit(1)
		}
		os.Exit(runAllCommand(args[1], args[2:]))
	case "reinstall":
		os.Exit(runReinstall())
	}

	modules, err := discoverModules()
	if err != nil {
		fatalf("error discovering modules: %v", err)
	}

	// Is the first argument a known module name?
	if m := findModule(modules, args[0]); m != nil {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: devops %s <command>\n", args[0])
			os.Exit(1)
		}
		switch args[1] {
		case "exec", "shell":
			if err := runExec(m, args[2:]); err != nil {
				fatalf("%v", err)
			}
		default:
			os.Exit(runSingleModuleCommand(m, args[1], args[2:]))
		}
		return
	}

	// Otherwise treat first argument as a command and run across all modules.
	os.Exit(runAllCommand(args[0], args[1:]))
}

// runAllCommand runs command across all modules that define it, respecting
// priority order and launching same-priority groups in parallel.
func runAllCommand(command string, extraArgs []string) int {
	modules, err := discoverModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	found := findModulesForCommand(modules, command)
	if len(found) == 0 {
		fmt.Fprintf(os.Stderr, "No mapping found for command: %s, passing to docker compose...\n", command)
		cmd := exec.Command("docker", append([]string{"compose", command}, extraArgs...)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := runWithSignalForwarding(cmd); err != nil {
			return 1
		}
		return 0
	}

	fmt.Printf("Running %q across %d module(s)...\n\n", command, len(found))

	for _, group := range groupByPriority(found, command) {
		if len(group) == 1 {
			m := group[0]
			fmt.Printf("=== [%s] ===\n", m.Name)
			if err := runModuleSequential(m, command, extraArgs); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Println()
		} else {
			if !runGroupParallel(group, command, extraArgs) {
				return 1
			}
			fmt.Println()
		}
	}

	fmt.Printf("Completed %q across all modules.\n", command)
	return 0
}

// runSingleModuleCommand runs a command scoped to one module.
func runSingleModuleCommand(m *Module, command string, extraArgs []string) int {
	if _, ok := m.Config[command]; !ok {
		fmt.Fprintf(os.Stderr, "Command %q not defined in module %s\n", command, m.Name)
		return 1
	}
	fmt.Printf("Running %q for module %s...\n\n", command, m.Name)
	if err := runModuleSequential(m, command, extraArgs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runGroupParallel launches a same-priority group of modules in the parallel TUI.
// It calls the binary itself as a subprocess per module so each module's full
// execution logic (multiple containers, multiple scripts) runs inside a panel.
func runGroupParallel(modules []*Module, command string, extraArgs []string) bool {
	self := selfPath()
	var panels []*panel
	for _, m := range modules {
		parts := append([]string{self, m.Name, command}, extraArgs...)
		panels = append(panels, newPanel(m.Name, shellJoin(parts)))
	}
	return runTUI(panels)
}

// shellJoin builds a shell-safe command string by single-quoting each argument.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func selfPath() string {
	exe, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return exe
	}
	return resolved
}

func showHelp() {
	modules, _ := discoverModules()

	fmt.Println("Usage:")
	fmt.Println("  devops <command>                   Run command across all modules")
	fmt.Println("  devops <module> <command>          Run command for a specific module")
	fmt.Println("  devops all <command>               Run command across all modules (explicit)")
	fmt.Println("  devops <module> exec [cmd...]      Open interactive shell in module's container")
	fmt.Println("  devops <module> shell              Alias for exec")
	fmt.Println("  devops help                        Show this help")
	fmt.Println("  devops reinstall                   Download and install the latest release")
	fmt.Println()

	if len(modules) == 0 {
		fmt.Println("No modules found (no .devops/commands.yaml files discovered).")
		return
	}

	fmt.Println("Available modules and commands:")
	fmt.Println()
	for _, m := range modules {
		fmt.Printf("  [%s]\n", m.Name)
		for _, cmd := range allCommands([]*Module{m}) {
			fmt.Printf("    - %s\n", cmd)
		}
		fmt.Println()
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
