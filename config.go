package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultPriority = 100

// Entry is one script item: either a plain string or {script, priority}.
type Entry struct {
	Script   string
	Priority int
}

func (e *Entry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Tag {
	case "!!str":
		e.Script = value.Value
		e.Priority = defaultPriority
		return nil
	case "!!map":
		type raw struct {
			Script   string `yaml:"script"`
			Priority *int   `yaml:"priority"`
		}
		var r raw
		if err := value.Decode(&r); err != nil {
			return err
		}
		e.Script = r.Script
		if r.Priority != nil {
			e.Priority = *r.Priority
		} else {
			e.Priority = defaultPriority
		}
		return nil
	default:
		return fmt.Errorf("unexpected YAML node tag %q for entry", value.Tag)
	}
}

// CommandConfig: command → container → []Entry
type CommandConfig map[string]map[string][]Entry

// Module is a discovered module with its parsed config.
type Module struct {
	Name   string
	Path   string
	Config CommandConfig
}

// effectivePriority is the lowest priority value across all entries for a command.
func (m *Module) effectivePriority(command string) int {
	containers, ok := m.Config[command]
	if !ok {
		return defaultPriority
	}
	min := defaultPriority
	for _, entries := range containers {
		for _, e := range entries {
			if e.Priority < min {
				min = e.Priority
			}
		}
	}
	return min
}

// firstContainer returns the first container key found across all commands.
func (m *Module) firstContainer() string {
	for _, containers := range m.Config {
		for k := range containers {
			return k
		}
	}
	return ""
}

// discoverModules finds all .devops/commands.yaml files up to 3 levels deep.
func discoverModules() ([]*Module, error) {
	var modules []*Module

	if _, err := os.Stat(".devops/commands.yaml"); err == nil {
		m, err := loadModule("root", ".devops/commands.yaml")
		if err != nil {
			return nil, fmt.Errorf("root: %w", err)
		}
		modules = append(modules, m)
	}

	var candidates []string
	for _, pattern := range []string{
		"*/.devops/commands.yaml",
		"*/*/.devops/commands.yaml",
		"*/*/*/.devops/commands.yaml",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		candidates = append(candidates, matches...)
	}

	for _, path := range candidates {
		name := moduleNameFromPath(path)
		m, err := loadModule(name, path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		modules = append(modules, m)
	}

	return modules, nil
}

func moduleNameFromPath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p != "." && p != ".devops" && p != "commands.yaml" && p != "" {
			return p
		}
	}
	return path
}

func loadModule(name, path string) (*Module, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg CommandConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &Module{Name: name, Path: path, Config: cfg}, nil
}

// findModulesForCommand returns modules that define the given command, sorted
// by their effective priority (ascending).
func findModulesForCommand(modules []*Module, command string) []*Module {
	var found []*Module
	for _, m := range modules {
		if _, ok := m.Config[command]; ok {
			found = append(found, m)
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		return found[i].effectivePriority(command) < found[j].effectivePriority(command)
	})
	return found
}

// groupByPriority buckets the (already priority-sorted) modules into groups
// that share the same effective priority for the given command.
func groupByPriority(modules []*Module, command string) [][]*Module {
	if len(modules) == 0 {
		return nil
	}
	var groups [][]*Module
	var cur []*Module
	curPrio := -1

	for _, m := range modules {
		p := m.effectivePriority(command)
		if len(cur) > 0 && p != curPrio {
			groups = append(groups, cur)
			cur = nil
		}
		curPrio = p
		cur = append(cur, m)
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

func findModule(modules []*Module, name string) *Module {
	for _, m := range modules {
		if m.Name == name {
			return m
		}
	}
	return nil
}

// allCommands returns a sorted, deduplicated list of all commands defined
// across all modules.
func allCommands(modules []*Module) []string {
	seen := map[string]bool{}
	for _, m := range modules {
		for cmd := range m.Config {
			seen[cmd] = true
		}
	}
	cmds := make([]string, 0, len(seen))
	for c := range seen {
		cmds = append(cmds, c)
	}
	sort.Strings(cmds)
	return cmds
}
