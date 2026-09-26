package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// version is set at release time via -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Println(version)
		return 0
	}

	if agentMode() {
		if len(args) > 0 && isHelpArg(args[0]) {
			showAgentsHelp()
			return 0
		}
		if len(args) == 0 || args[0] != "agents" {
			return agentBlocked()
		}
	}

	if len(args) == 0 {
		showHelp()
		return 0
	}

	switch args[0] {
	case "help", "--help", "-h":
		showHelp()
		return 0
	case "agents":
		return runAgents(args[1:])
	case "all":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: devops all <command>")
			return 1
		}
		return runAllCommand(args[1], args[2:])
	case "reinstall":
		return runReinstall()
	}

	modules, err := discoverModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: error discovering modules: %v\n", err)
		return 1
	}

	// Is the first argument a known module name?
	if m := findModule(modules, args[0]); m != nil {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: devops %s <command>\n", args[0])
			return 1
		}
		switch args[1] {
		case "exec", "shell":
			if err := runExec(m, args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
			return 0
		default:
			return runSingleModuleCommand(m, args[1], args[2:])
		}
	}

	// Otherwise treat first argument as a command and run across all modules.
	return runAllCommand(args[0], args[1:])
}

func isHelpArg(arg string) bool {
	return arg == "help" || arg == "--help" || arg == "-h"
}

// runAllCommand runs command across all modules that define it, respecting
// priority order and launching same-priority groups in parallel.
func runAllCommand(command string, extraArgs []string) int {
	modules, err := discoverModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if len(findModulesForCommand(modules, command)) == 0 {
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

	return runAcross(modules, nil, command, extraArgs)
}

// runAcross runs command across the given modules by priority group.
// subcommand is inserted before the module name when re-invoking the binary
// for parallel groups (e.g. "agents").
func runAcross(modules []*Module, subcommand []string, command string, extraArgs []string) int {
	found := findModulesForCommand(modules, command)
	fmt.Fprintf(os.Stderr, "Running %q across %d module(s)...\n\n", command, len(found))

	for _, group := range groupByPriority(found, command) {
		if len(group) == 1 {
			m := group[0]
			fmt.Fprintf(os.Stderr, "=== [%s] ===\n", m.Name)
			if err := runModuleSequential(m, command, extraArgs); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			fmt.Fprintln(os.Stderr)
		} else {
			if !runGroupParallel(group, subcommand, command, extraArgs) {
				return 1
			}
			fmt.Fprintln(os.Stderr)
		}
	}

	fmt.Fprintf(os.Stderr, "Completed %q across all modules.\n", command)
	return 0
}

// runSingleModuleCommand runs a command scoped to one module.
func runSingleModuleCommand(m *Module, command string, extraArgs []string) int {
	if _, ok := m.Config[command]; !ok {
		fmt.Fprintf(os.Stderr, "Command %q not defined in module %s\n", command, m.Name)
		return 1
	}
	fmt.Fprintf(os.Stderr, "Running %q for module %s...\n\n", command, m.Name)
	if err := runModuleSequential(m, command, extraArgs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// runGroupParallel launches a same-priority group of modules in parallel — in
// the TUI when a terminal is attached, as prefixed log output otherwise.
// It calls the binary itself as a subprocess per module so each module's full
// execution logic (multiple containers, multiple scripts) runs inside a panel.
func runGroupParallel(modules []*Module, subcommand []string, command string, extraArgs []string) bool {
	self := selfPath()
	var panels []*panel
	for _, m := range modules {
		panels = append(panels, newPanel(m.Name, panelCommand(self, subcommand, m.Name, command, extraArgs)))
	}
	return runPanels(panels)
}

func panelCommand(self string, subcommand []string, module, command string, extraArgs []string) string {
	parts := append([]string{self}, subcommand...)
	parts = append(parts, module, command)
	return shellJoin(append(parts, extraArgs...))
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
	fmt.Println("  devops agents <command>            Run an agent command (see: devops agents help)")
	fmt.Println("  devops help                        Show this help")
	fmt.Println("  devops version                     Print the version")
	fmt.Println("  devops reinstall                   Download and install the latest release")
	fmt.Println()

	var withCommands []*Module
	for _, m := range modules {
		if len(m.Config) > 0 {
			withCommands = append(withCommands, m)
		}
	}
	if len(withCommands) == 0 {
		fmt.Println("No modules found (no .devops/commands.yaml files discovered).")
		return
	}

	fmt.Println("Available modules and commands:")
	fmt.Println()
	printModuleCommands(withCommands)
}

func printModuleCommands(modules []*Module) {
	for _, m := range modules {
		fmt.Printf("  [%s]\n", m.Name)
		cmds := allCommands([]*Module{m})
		width := 0
		for _, cmd := range cmds {
			width = max(width, len(cmd))
		}
		for _, cmd := range cmds {
			if desc := m.Descriptions[cmd]; desc != "" {
				fmt.Printf("    - %-*s  %s\n", width, cmd, desc)
			} else {
				fmt.Printf("    - %s\n", cmd)
			}
		}
		fmt.Println()
	}
}
