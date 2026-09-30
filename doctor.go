package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Check levels reported by `devops agents doctor`.
const (
	levelOK   = "ok"
	levelWarn = "warn"
	levelFail = "fail"
)

// Check is one doctor finding.
type Check struct {
	Name    string `json:"name"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// requiredAgentCommands are what docs/agent-ready.md says every project needs.
var requiredAgentCommands = []string{"bootstrap", "test", "lint"}

var composeFiles = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// runDoctor statically checks that a repo follows docs/agent-ready.md. It
// never starts containers. Exit 1 when any check fails, warnings are fine.
func runDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the checks as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := repoArg(fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 2
	}

	checks := doctorChecks(repo)
	failed := false
	for _, c := range checks {
		if c.Level == levelFail {
			failed = true
		}
	}

	if *asJSON {
		out, _ := json.MarshalIndent(checks, "", "  ")
		fmt.Println(string(out))
	} else {
		for _, c := range checks {
			fmt.Printf("%-4s  %-18s %s\n", strings.ToUpper(c.Level), c.Name, c.Message)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func doctorChecks(repo string) []Check {
	var out []Check
	add := func(name, level, msg string, a ...any) {
		out = append(out, Check{Name: name, Level: level, Message: fmt.Sprintf(msg, a...)})
	}
	path := func(p ...string) string { return filepath.Join(append([]string{repo}, p...)...) }

	// agents.yaml
	agentsPath := path(".devops", "agents.yaml")
	if !fileExists(agentsPath) {
		add("agents.yaml", levelFail, ".devops/agents.yaml not found")
	} else if cfg, _, _, err := loadConfig(agentsPath); err != nil {
		add("agents.yaml", levelFail, "does not parse: %v", err)
	} else {
		add("agents.yaml", levelOK, "parses, %d command(s)", len(cfg))
		for _, cmd := range requiredAgentCommands {
			if _, ok := cfg[cmd]; !ok {
				add("command:"+cmd, levelFail, "agents.yaml defines no %q command", cmd)
			} else {
				add("command:"+cmd, levelOK, "defined")
			}
		}
		for _, cmd := range builtinAgentCommands {
			if _, ok := cfg[cmd]; ok {
				add("command:"+cmd, levelWarn, "%q is a devops built-in; the agents.yaml entry is never run", cmd)
			}
		}
		if bootstrap, ok := cfg["bootstrap"]; ok {
			checkBootstrapScripts(repo, bootstrap, add)
		}
	}

	// compose
	found := ""
	for _, f := range composeFiles {
		if fileExists(path(f)) {
			found = f
			break
		}
	}
	if found == "" {
		add("compose", levelWarn, "no compose file at the repo root")
	} else {
		add("compose", levelOK, "%s found", found)
	}

	// docs for the agent
	claude, agentsMD := fileExists(path("CLAUDE.md")), fileExists(path("AGENTS.md"))
	switch {
	case !claude && !agentsMD:
		add("instructions", levelFail, "neither CLAUDE.md nor AGENTS.md found")
	case claude && agentsMD && !claudePointsToAgents(path("CLAUDE.md")):
		add("instructions", levelWarn, "CLAUDE.md and AGENTS.md both exist; make CLAUDE.md just `@AGENTS.md` so they cannot drift")
	default:
		add("instructions", levelOK, "agent instructions present")
	}

	// .claude/settings.json
	if settings := path(".claude", "settings.json"); fileExists(settings) {
		checkClaudeSettings(settings, add)
	}

	// .agent-secrets opt-in
	optIn := fileExists(path(".devops", "agent-secrets.yaml"))
	store := dirExists(path(".agent-secrets"))
	switch {
	case optIn && !store:
		add("agent-secrets", levelFail, ".devops/agent-secrets.yaml opts in but .agent-secrets/ does not exist")
	case store && !optIn:
		add("agent-secrets", levelWarn, ".agent-secrets/ exists without .devops/agent-secrets.yaml, so nothing is revealed")
	case optIn:
		checkSecretsOptIn(path(".devops", "agent-secrets.yaml"), add)
	}
	return out
}

type addFn func(name, level, msg string, a ...any)

// checkBootstrapScripts verifies that scripts referenced as .devops/agents/*.sh exist.
func checkBootstrapScripts(repo string, containers map[string][]Entry, add addFn) {
	missing := map[string]bool{}
	for _, entries := range containers {
		for _, e := range entries {
			for _, field := range strings.Fields(e.Script) {
				field = strings.Trim(field, `"'`)
				if strings.HasPrefix(field, ".devops/agents/") && strings.HasSuffix(field, ".sh") && !fileExists(filepath.Join(repo, field)) {
					missing[field] = true
				}
			}
		}
	}
	if len(missing) == 0 {
		add("bootstrap-script", levelOK, "referenced scripts exist")
		return
	}
	for f := range missing {
		add("bootstrap-script", levelFail, "bootstrap references %s, which does not exist", f)
	}
}

func claudePointsToAgents(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "@AGENTS.md")
}

// checkClaudeSettings enforces what docs/agent-ready.md step 8 and the
// runner's confine-repo-settings policy allow in a committed settings file.
func checkClaudeSettings(path string, add addFn) {
	b, err := os.ReadFile(path)
	if err != nil {
		add("claude-settings", levelFail, "cannot read .claude/settings.json: %v", err)
		return
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		add("claude-settings", levelFail, ".claude/settings.json is not valid JSON: %v", err)
		return
	}
	bad := false
	if _, ok := s["env"]; ok {
		add("claude-settings", levelFail, "committed settings must not set an env block")
		bad = true
	}
	if perms, ok := s["permissions"].(map[string]any); ok {
		if allow, ok := perms["allow"].([]any); ok {
			for _, a := range allow {
				if str, _ := a.(string); str == "Edit" || str == "Write" || str == "Bash" || str == "Bash(*)" {
					add("claude-settings", levelFail, "blanket allow rule %q is not allowed", str)
					bad = true
				}
			}
		}
	}
	if !bad {
		add("claude-settings", levelOK, "valid, no env block or blanket allow rules")
	}
}

func checkSecretsOptIn(path string, add addFn) {
	b, err := os.ReadFile(path)
	if err != nil {
		add("agent-secrets", levelFail, "cannot read %s: %v", path, err)
		return
	}
	var cfg struct {
		Enabled bool   `yaml:"enabled"`
		Ext     string `yaml:"secrets_extension"`
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		add("agent-secrets", levelFail, "agent-secrets.yaml does not parse: %v", err)
		return
	}
	switch {
	case !cfg.Enabled:
		add("agent-secrets", levelWarn, "agent-secrets.yaml has enabled: false")
	case cfg.Ext != ".agent.secret":
		add("agent-secrets", levelFail, "secrets_extension must be .agent.secret, got %q", cfg.Ext)
	default:
		add("agent-secrets", levelOK, "opt-in is valid")
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
