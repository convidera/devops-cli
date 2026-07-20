package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// ── moduleNameFromPath ────────────────────────────────────────────────────────

func TestModuleNameFromPath(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"backend/.devops/commands.yaml", "backend"},
		{"services/api/.devops/commands.yaml", "services"},
		{"a/b/c/.devops/commands.yaml", "a"},
		{".devops/commands.yaml", ".devops/commands.yaml"}, // fallback
	}
	for _, c := range cases {
		got := moduleNameFromPath(c.path)
		if got != c.want {
			t.Errorf("moduleNameFromPath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// ── Entry YAML unmarshaling ───────────────────────────────────────────────────

func TestEntryUnmarshalYAML_string(t *testing.T) {
	var e Entry
	if err := yaml.Unmarshal([]byte(`"php artisan migrate"`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Script != "php artisan migrate" {
		t.Errorf("Script = %q", e.Script)
	}
	if e.Priority != defaultPriority {
		t.Errorf("Priority = %d, want %d", e.Priority, defaultPriority)
	}
}

func TestEntryUnmarshalYAML_map_with_priority(t *testing.T) {
	src := `script: php artisan migrate
priority: 10`
	var e Entry
	if err := yaml.Unmarshal([]byte(src), &e); err != nil {
		t.Fatal(err)
	}
	if e.Script != "php artisan migrate" {
		t.Errorf("Script = %q", e.Script)
	}
	if e.Priority != 10 {
		t.Errorf("Priority = %d, want 10", e.Priority)
	}
}

func TestEntryUnmarshalYAML_map_default_priority(t *testing.T) {
	src := `script: npm test`
	var e Entry
	if err := yaml.Unmarshal([]byte(src), &e); err != nil {
		t.Fatal(err)
	}
	if e.Priority != defaultPriority {
		t.Errorf("Priority = %d, want %d", e.Priority, defaultPriority)
	}
}

// ── effectivePriority ─────────────────────────────────────────────────────────

func TestEffectivePriority(t *testing.T) {
	m := &Module{
		Name: "test",
		Config: CommandConfig{
			"build": {
				"app": {
					{Script: "step1", Priority: 50},
					{Script: "step2", Priority: 20},
				},
				"db": {
					{Script: "step3", Priority: 80},
				},
			},
		},
	}
	got := m.effectivePriority("build")
	if got != 20 {
		t.Errorf("effectivePriority = %d, want 20", got)
	}
}

func TestEffectivePriority_missing_command(t *testing.T) {
	m := &Module{Name: "test", Config: CommandConfig{}}
	got := m.effectivePriority("nonexistent")
	if got != defaultPriority {
		t.Errorf("effectivePriority = %d, want %d", got, defaultPriority)
	}
}

// ── findModule ────────────────────────────────────────────────────────────────

func TestFindModule(t *testing.T) {
	modules := []*Module{
		{Name: "alpha"},
		{Name: "beta"},
		{Name: "gamma"},
	}
	m := findModule(modules, "beta")
	if m == nil || m.Name != "beta" {
		t.Errorf("findModule returned %v", m)
	}
	if findModule(modules, "missing") != nil {
		t.Error("expected nil for missing module")
	}
}

// ── findModulesForCommand ─────────────────────────────────────────────────────

func TestFindModulesForCommand(t *testing.T) {
	modules := []*Module{
		{Name: "a", Config: CommandConfig{"test": {"app": {{Script: "a", Priority: 50}}}}},
		{Name: "b", Config: CommandConfig{"build": {"app": {{Script: "b", Priority: defaultPriority}}}}},
		{Name: "c", Config: CommandConfig{"test": {"app": {{Script: "c", Priority: 10}}}}},
	}
	found := findModulesForCommand(modules, "test")
	if len(found) != 2 {
		t.Fatalf("len = %d, want 2", len(found))
	}
	// Should be sorted by effective priority: c (10) before a (50)
	if found[0].Name != "c" || found[1].Name != "a" {
		t.Errorf("order = [%s, %s], want [c, a]", found[0].Name, found[1].Name)
	}
}

func TestFindModulesForCommand_none(t *testing.T) {
	modules := []*Module{
		{Name: "a", Config: CommandConfig{"build": {}}},
	}
	if got := findModulesForCommand(modules, "test"); len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// ── groupByPriority ───────────────────────────────────────────────────────────

func TestGroupByPriority(t *testing.T) {
	modules := []*Module{
		{Name: "a", Config: CommandConfig{"cmd": {"app": {{Script: "", Priority: 10}}}}},
		{Name: "b", Config: CommandConfig{"cmd": {"app": {{Script: "", Priority: 10}}}}},
		{Name: "c", Config: CommandConfig{"cmd": {"app": {{Script: "", Priority: 50}}}}},
	}
	groups := groupByPriority(modules, "cmd")
	if len(groups) != 2 {
		t.Fatalf("len(groups) = %d, want 2", len(groups))
	}
	if len(groups[0]) != 2 {
		t.Errorf("group[0] len = %d, want 2", len(groups[0]))
	}
	if len(groups[1]) != 1 {
		t.Errorf("group[1] len = %d, want 1", len(groups[1]))
	}
}

func TestGroupByPriority_empty(t *testing.T) {
	if groups := groupByPriority(nil, "cmd"); groups != nil {
		t.Errorf("expected nil, got %v", groups)
	}
}

// ── allCommands ───────────────────────────────────────────────────────────────

func TestAllCommands(t *testing.T) {
	modules := []*Module{
		{Config: CommandConfig{"test": {}, "build": {}}},
		{Config: CommandConfig{"test": {}, "migrate": {}}},
	}
	cmds := allCommands(modules)
	want := []string{"build", "migrate", "test"}
	if len(cmds) != len(want) {
		t.Fatalf("len = %d, want %d", len(cmds), len(want))
	}
	for i, c := range cmds {
		if c != want[i] {
			t.Errorf("cmds[%d] = %q, want %q", i, c, want[i])
		}
	}
}

// ── loadModule / discoverModules (filesystem integration) ─────────────────────

func TestLoadModule(t *testing.T) {
	dir := t.TempDir()
	content := `
test:
  app:
    - php artisan test
migrate:
  app:
    - script: php artisan migrate
      priority: 10
`
	path := filepath.Join(dir, "commands.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := loadModule("mymodule", path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "mymodule" {
		t.Errorf("Name = %q", m.Name)
	}
	if len(m.Config["test"]["app"]) != 1 {
		t.Errorf("test entries = %d", len(m.Config["test"]["app"]))
	}
	if m.Config["migrate"]["app"][0].Priority != 10 {
		t.Errorf("migrate priority = %d", m.Config["migrate"]["app"][0].Priority)
	}
}

func TestDiscoverModules(t *testing.T) {
	dir := t.TempDir()
	// Create two module configs one level deep.
	for _, mod := range []string{"svc-a", "svc-b"} {
		p := filepath.Join(dir, mod, ".devops")
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		content := "test:\n  app:\n    - echo " + mod + "\n"
		if err := os.WriteFile(filepath.Join(p, "commands.yaml"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir("/") })

	modules, err := discoverModules()
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 {
		t.Errorf("discovered %d modules, want 2", len(modules))
	}
}
