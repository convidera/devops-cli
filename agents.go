package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	agentEnv      = "DEVOPS_AGENT"
	claudeCodeEnv = "CLAUDECODE"
)

// agentMode reports whether devops runs on behalf of an AI agent. DEVOPS_AGENT
// wins when set; otherwise Claude Code's CLAUDECODE=1 turns it on.
func agentMode() bool {
	if v := os.Getenv(agentEnv); v != "" {
		return v != "0"
	}
	return os.Getenv(claudeCodeEnv) == "1"
}

// runAgents handles `devops agents ...` using the commands from agents.yaml.
func runAgents(args []string) int {
	if len(args) == 0 || isHelpArg(args[0]) {
		showAgentsHelp()
		return 0
	}

	if isBuiltinAgentCommand(args[0]) {
		return runAgentBuiltin(args[0], args[1:])
	}

	modules, err := discoverModules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	agents := agentModules(modules)

	if m := findModule(agents, args[0]); m != nil {
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: devops agents %s <command>. Available: %s\n", m.Name, commandList([]*Module{m}))
			return 1
		}
		if _, ok := m.Config[args[1]]; !ok {
			fmt.Fprintf(os.Stderr, "devops: unknown agent command %q for module %s. Available: %s\n", args[1], m.Name, commandList([]*Module{m}))
			return 1
		}
		return runSingleModuleCommand(m, args[1], args[2:])
	}

	command := args[0]
	if len(findModulesForCommand(agents, command)) == 0 {
		fmt.Fprintf(os.Stderr, "devops: unknown agent command %q. Available: %s\n", command, commandList(agents))
		return 1
	}
	return runAcross(agents, []string{"agents"}, command, args[1:])
}

// agentBlocked prints the refusal shown for non-agent commands in agent mode.
func agentBlocked() int {
	modules, _ := discoverModules()
	fmt.Fprintf(os.Stderr, "devops: agent session, use \"devops agents <command>\". Available: %s\n", commandList(agentModules(modules)))
	return 2
}

func commandList(modules []*Module) string {
	cmds := allCommands(modules)
	if len(cmds) == 0 {
		return "(none)"
	}
	return strings.Join(cmds, ", ")
}

func showAgentsHelp() {
	modules, _ := discoverModules()
	agents := agentModules(modules)

	fmt.Println("Usage:")
	fmt.Println("  devops agents <command> [args]            Run agent command across all modules")
	fmt.Println("  devops agents <module> <command> [args]   Run agent command for a specific module")
	fmt.Println("  devops agents help                        Show this help")
	fmt.Println()
	fmt.Println("Built-in commands:")
	fmt.Println("  init [--retry] [dir]                      Run bootstrap once, recording log and exit code")
	fmt.Println("  status [dir]                              Show bootstrap state (exit 0 ready, 1 failed, 2 running, 3 not started)")
	fmt.Println("  wait [--timeout 10m] [dir]                Block until bootstrap finishes")
	fmt.Println("  doctor [--json] [dir]                     Check the repo is agent-ready")
	fmt.Println()

	if len(agents) == 0 {
		fmt.Println("No agent commands found (no .devops/agents.yaml files discovered).")
		return
	}

	fmt.Println("Available modules and agent commands:")
	fmt.Println()
	printModuleCommands(agents)
}
