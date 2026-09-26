package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupAgentRepo creates a repo with two modules and chdirs into it. The
// scripts append to log.txt so tests can assert what actually ran.
func setupAgentRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	logFile := filepath.Join(dir, "log.txt")
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("backend/.devops/commands.yaml", `
test:
  host:
    - echo human-backend-test >> "`+logFile+`"
`)
	write("backend/.devops/agents.yaml", `
test:
  host:
    - script: echo agent-backend-test "$@" >> "`+logFile+`"
      priority: 10
lint:
  host:
    - echo agent-backend-lint >> "`+logFile+`"
`)
	// frontend only has agents.yaml.
	write("frontend/.devops/agents.yaml", `
test:
  host:
    - echo agent-frontend-test >> "`+logFile+`"
`)
	t.Chdir(dir)
	return logFile
}

func readLog(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func setAgentEnv(t *testing.T, devopsAgent, claudeCode string) {
	t.Helper()
	t.Setenv(agentEnv, devopsAgent)
	t.Setenv(claudeCodeEnv, claudeCode)
}

func TestDiscoverModules_loadsAgentsYAML(t *testing.T) {
	setupAgentRepo(t)
	modules, err := discoverModules()
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Fatalf("discovered %d modules, want 2", len(modules))
	}
	backend, frontend := findModule(modules, "backend"), findModule(modules, "frontend")
	if backend == nil || frontend == nil {
		t.Fatalf("missing modules: %v", modules)
	}
	if _, ok := backend.Config["test"]; !ok {
		t.Error("backend commands.yaml not loaded")
	}
	if backend.Agents["test"]["host"][0].Priority != 10 {
		t.Errorf("backend agent test priority = %d, want 10", backend.Agents["test"]["host"][0].Priority)
	}
	if len(frontend.Config) != 0 || len(frontend.Agents) != 1 {
		t.Errorf("frontend Config=%v Agents=%v", frontend.Config, frontend.Agents)
	}

	agents := agentModules(modules)
	if got := allCommands(agents); strings.Join(got, ",") != "lint,test" {
		t.Errorf("agent commands = %v", got)
	}
}

func TestRunAgents_acrossModules(t *testing.T) {
	logFile := setupAgentRepo(t)
	setAgentEnv(t, "", "")
	if code := run([]string{"agents", "test", "x"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := readLog(t, logFile)
	// backend (priority 10) runs before frontend (100); humans' commands.yaml is not used.
	want := []string{"agent-backend-test x", "agent-frontend-test"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestRunAgents_moduleScoped(t *testing.T) {
	logFile := setupAgentRepo(t)
	setAgentEnv(t, "1", "")
	if code := run([]string{"agents", "backend", "lint"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := readLog(t, logFile); len(got) != 1 || got[0] != "agent-backend-lint" {
		t.Errorf("log = %q", got)
	}

	var code int
	stderr := captureStderr(t, func() { code = run([]string{"agents", "frontend", "lint"}) })
	if code != 1 || !strings.Contains(stderr, `unknown agent command "lint" for module frontend. Available: test`) {
		t.Errorf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestRunAgents_unknownCommandDoesNotPassThrough(t *testing.T) {
	setupAgentRepo(t)
	setAgentEnv(t, "", "")
	var code int
	stderr := captureStderr(t, func() { code = run([]string{"agents", "ps"}) })
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, `unknown agent command "ps". Available: lint, test`) {
		t.Errorf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "docker compose") {
		t.Errorf("fell through to docker compose: %q", stderr)
	}
}

func TestAgentMode_env(t *testing.T) {
	cases := []struct {
		devopsAgent, claudeCode string
		want                    bool
	}{
		{"", "", false},
		{"1", "", true},
		{"", "1", true},
		{"0", "1", false},
		{"1", "0", true},
	}
	for _, c := range cases {
		setAgentEnv(t, c.devopsAgent, c.claudeCode)
		if got := agentMode(); got != c.want {
			t.Errorf("DEVOPS_AGENT=%q CLAUDECODE=%q: agentMode = %v, want %v", c.devopsAgent, c.claudeCode, got, c.want)
		}
	}
}

func TestAgentMode_blocksNonAgentCommands(t *testing.T) {
	for _, env := range [][2]string{{"1", ""}, {"", "1"}} {
		logFile := setupAgentRepo(t)
		setAgentEnv(t, env[0], env[1])
		for _, args := range [][]string{
			{"test"},
			{"ps"},
			{"backend", "test"},
			{"backend", "exec"},
			{"all", "test"},
			{"reinstall"},
		} {
			var code int
			stderr := captureStderr(t, func() { code = run(args) })
			if code != 2 {
				t.Errorf("env %v, %v: exit = %d, want 2", env, args, code)
			}
			want := `devops: agent session, use "devops agents <command>". Available: lint, test`
			if !strings.Contains(stderr, want) {
				t.Errorf("env %v, %v: stderr = %q", env, args, stderr)
			}
		}
		if got := readLog(t, logFile); got != nil {
			t.Errorf("env %v: blocked commands ran: %q", env, got)
		}
		if code := run([]string{"help"}); code != 0 {
			t.Errorf("env %v: help exit = %d", env, code)
		}
	}
}

func TestAgentMode_disabledWithDevopsAgentZero(t *testing.T) {
	logFile := setupAgentRepo(t)
	setAgentEnv(t, "0", "1")
	if code := run([]string{"backend", "test"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := readLog(t, logFile); len(got) != 1 || got[0] != "human-backend-test" {
		t.Errorf("log = %q", got)
	}
}

func TestCanUseTUI_falseInAgentMode(t *testing.T) {
	setAgentEnv(t, "1", "")
	t.Setenv(noTUIEnv, "")
	if canUseTUI() {
		t.Error("canUseTUI = true in agent mode")
	}
}

func TestPanelCommand_agentsSubcommand(t *testing.T) {
	got := panelCommand("/bin/devops", []string{"agents"}, "api", "test", []string{"a b"})
	want := `'/bin/devops' 'agents' 'api' 'test' 'a b'`
	if got != want {
		t.Errorf("panelCommand = %q, want %q", got, want)
	}
	if got := panelCommand("/bin/devops", nil, "api", "test", nil); got != `'/bin/devops' 'api' 'test'` {
		t.Errorf("panelCommand without subcommand = %q", got)
	}
}
