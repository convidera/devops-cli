package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func useStateRoot(t *testing.T) {
	t.Helper()
	old := stateRoot
	stateRoot = t.TempDir()
	t.Cleanup(func() { stateRoot = old })
}

func TestStateDirIsStablePerPath(t *testing.T) {
	useStateRoot(t)
	a, _ := stateDir("/tmp/a/repo")
	b, _ := stateDir("/tmp/b/repo")
	if a == b || !strings.HasPrefix(filepath.Base(a), "repo-") || len(filepath.Base(a)) != len("repo-")+8 {
		t.Errorf("stateDir: %q %q", a, b)
	}
	if again, _ := stateDir("/tmp/a/repo"); again != a {
		t.Errorf("not stable: %q vs %q", again, a)
	}
}

func TestStatusAndWaitReflectDoneFile(t *testing.T) {
	useStateRoot(t)
	repo := t.TempDir()
	dir, _ := stateDir(repo)

	if code := runStatus([]string{repo}); code != statusNone {
		t.Errorf("status before start = %d", code)
	}
	_ = os.MkdirAll(dir, 0o700)
	writeFile(t, dir, "done", "0\n")
	if code := runStatus([]string{repo}); code != statusReady {
		t.Errorf("status ready = %d", code)
	}
	if code := runWait([]string{repo}); code != 0 {
		t.Errorf("wait ready = %d", code)
	}
	writeFile(t, dir, "done", "7\n")
	writeFile(t, dir, "log", "boom\ncommand failed\n")
	if code := runStatus([]string{repo}); code != statusFailed {
		t.Errorf("status failed = %d", code)
	}
	if code := runWait([]string{repo}); code != 1 {
		t.Errorf("wait failed = %d", code)
	}
}

func TestInitRecordsExitCodeAndIsIdempotent(t *testing.T) {
	useStateRoot(t)
	repo := t.TempDir()
	writeFile(t, repo, ".devops/agents.yaml", "bootstrap:\n  host:\n    - echo booted >> ran.txt\n")
	t.Setenv(agentEnv, "1")
	bin := filepath.Join(t.TempDir(), "devops")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	bootstrapBinary = bin
	t.Cleanup(func() { bootstrapBinary = "" })

	if code := runInit([]string{repo}); code != 0 {
		t.Fatalf("init = %d", code)
	}
	dir, _ := stateDir(repo)
	if b, _ := os.ReadFile(filepath.Join(dir, "done")); strings.TrimSpace(string(b)) != "0" {
		t.Errorf("done = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "log")); !strings.Contains(string(b), "Completed") {
		t.Errorf("log lacks Completed marker: %q", b)
	}
	if code := runInit([]string{repo}); code != 0 {
		t.Fatalf("second init = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "ran.txt")); strings.Count(string(b), "booted") != 1 {
		t.Errorf("bootstrap ran again without --retry: %q", b)
	}
	if code := runInit([]string{"--retry", repo}); code != 0 {
		t.Fatalf("retry = %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "ran.txt")); strings.Count(string(b), "booted") != 2 {
		t.Errorf("--retry did not rerun: %q", b)
	}

	writeFile(t, repo, ".devops/agents.yaml", "bootstrap:\n  host:\n    - exit 3\n")
	if code := runInit([]string{"--retry", repo}); code == 0 {
		t.Errorf("failing bootstrap returned 0")
	}
	if st := readState(dir); st.state != statusFailed {
		t.Errorf("state after failure = %d", st.state)
	}
}

func TestDoctorChecks(t *testing.T) {
	repo := t.TempDir()
	levels := func() map[string]string {
		m := map[string]string{}
		for _, c := range doctorChecks(repo) {
			if m[c.Name] != levelFail {
				m[c.Name] = c.Level
			}
		}
		return m
	}
	if got := levels(); got["agents.yaml"] != levelFail || got["instructions"] != levelFail {
		t.Errorf("empty repo: %v", got)
	}

	writeFile(t, repo, ".devops/agents.yaml", "bootstrap:\n  host:\n    - .devops/agents/bootstrap.sh\ntest:\n  host:\n    - \"true\"\nlint:\n  host:\n    - \"true\"\nstatus:\n  host:\n    - \"true\"\n")
	writeFile(t, repo, "AGENTS.md", "# x\n")
	writeFile(t, repo, "CLAUDE.md", "@AGENTS.md\n")
	writeFile(t, repo, "docker-compose.yml", "services: {}\n")
	got := levels()
	if got["agents.yaml"] != levelOK || got["bootstrap-script"] != levelFail || got["command:status"] != levelWarn || got["instructions"] != levelOK {
		t.Errorf("partial repo: %v", got)
	}

	writeFile(t, repo, ".devops/agents/bootstrap.sh", "#!/bin/sh\n")
	writeFile(t, repo, ".claude/settings.json", `{"env":{"A":"b"},"permissions":{"allow":["Edit"]}}`)
	writeFile(t, repo, ".devops/agent-secrets.yaml", "enabled: true\nsecrets_extension: .agent.secret\n")
	got = levels()
	if got["bootstrap-script"] != levelOK || got["claude-settings"] != levelFail || got["agent-secrets"] != levelFail {
		t.Errorf("settings/secrets repo: %v", got)
	}
}

func TestDoctorExitCodeAndJSON(t *testing.T) {
	repo := t.TempDir()
	var code int
	out := captureStdout(t, func() { code = runDoctor([]string{"--json", repo}) })
	if code != 1 || !strings.Contains(out, `"level": "fail"`) {
		t.Errorf("code=%d out=%s", code, out)
	}
}

func TestAgentBuiltinsAreDispatched(t *testing.T) {
	for _, n := range []string{"init", "status", "wait", "doctor"} {
		if !isBuiltinAgentCommand(n) {
			t.Errorf("%s not builtin", n)
		}
	}
	if isBuiltinAgentCommand("bootstrap") {
		t.Error("bootstrap must stay project-defined")
	}
}

func TestContainersRunInFileOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "commands.yaml", "go:\n  zeta:\n    - a\n  alpha:\n    - b\n  mid:\n    - c\n")
	m, err := loadModule("m", filepath.Join(dir, "commands.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if got := m.containers("go"); !reflect.DeepEqual(got, []string{"zeta", "alpha", "mid"}) {
			t.Fatalf("order = %v", got)
		}
	}
	if m.firstContainer() != "zeta" {
		t.Errorf("firstContainer = %q", m.firstContainer())
	}
}

func TestUniqueModuleNames(t *testing.T) {
	got := uniqueModuleNames([]string{"apps/web/.devops", "apps/api/.devops", "svc/.devops"})
	if got[0] != "apps/web" || got[1] != "apps/api" || got[2] != "svc" {
		t.Errorf("got %v", got)
	}
	// "apps" is shared by two dirs only after first-segment naming.
	got = uniqueModuleNames([]string{"apps/web/.devops", "apps/api/.devops"})
	if got[0] == got[1] {
		t.Errorf("collision: %v", got)
	}
}

func TestReleaseAssetLinuxArm64(t *testing.T) {
	if a, ok := releaseAsset("linux", "arm64"); !ok || a != "devops-linux-arm64" {
		t.Errorf("got %q %v", a, ok)
	}
}

func TestUniqueModuleNamesRootDevopsStaysDistinct(t *testing.T) {
	got := uniqueModuleNames([]string{"root/.devops", "other/.devops"})
	if got[0] == "root" || got[0] == got[1] {
		t.Errorf("root/.devops must not collide with the reserved root module: %v", got)
	}
}

func TestLoadConfigRejectsDuplicates(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"command":     "test:\n  host:\n    - a\ntest:\n  host:\n    - b\n",
		"container":   "test:\n  host:\n    - a\n  host:\n    - b\n",
		"description": "test:\n  description: a\n  description: b\n  host:\n    - a\n",
	}
	for name, body := range cases {
		writeFile(t, dir, name+".yaml", body)
		if _, _, _, err := loadConfig(filepath.Join(dir, name+".yaml")); err == nil {
			t.Errorf("duplicate %s: expected an error", name)
		}
	}
}

func TestDoctorWildcardAndDisabledOptIn(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".claude/settings.json", `{"permissions":{"allow":["*"]}}`)
	writeFile(t, repo, ".devops/agent-secrets.yaml", "enabled: false\n")
	got := map[string]string{}
	for _, c := range doctorChecks(repo) {
		got[c.Name] = c.Level
	}
	if got["claude-settings"] != levelFail {
		t.Errorf(`"*" allow rule must fail: %v`, got)
	}
	if got["agent-secrets"] != levelWarn {
		t.Errorf("disabled opt-in without a store must only warn: %v", got)
	}
}
