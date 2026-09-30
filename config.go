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

// descriptionKey is a reserved key at the container level holding a
// command's help text instead of a container.
const descriptionKey = "description"

// Module is a discovered module with its parsed config.
type Module struct {
	Name         string
	Path         string
	Config       CommandConfig
	Descriptions map[string]string
	// Order lists each command's containers in file order, so they run
	// deterministically. Empty means sorted by name.
	Order map[string][]string
	// Agents holds the commands from .devops/agents.yaml, if present.
	Agents            CommandConfig
	AgentDescriptions map[string]string
	AgentsOrder       map[string][]string
	AgentsPath        string
}

// containers returns a command's container names in file order, falling back
// to sorted order for modules built without a file.
func (m *Module) containers(command string) []string {
	cfg := m.Config[command]
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Order[command] {
		if _, ok := cfg[c]; ok && !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	var rest []string
	for c := range cfg {
		if !seen[c] {
			rest = append(rest, c)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
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
	for _, command := range sortedKeys(m.Config) {
		if cs := m.containers(command); len(cs) > 0 {
			return cs[0]
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// discoverModules finds all .devops directories containing commands.yaml
// and/or agents.yaml, up to 3 levels deep.
func discoverModules() ([]*Module, error) {
	var modules []*Module

	root, err := loadModuleDir("root", ".devops")
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	if root != nil {
		modules = append(modules, root)
	}

	var candidates []string
	for _, pattern := range []string{
		"*/.devops",
		"*/*/.devops",
		"*/*/*/.devops",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		candidates = append(candidates, matches...)
	}

	names := uniqueModuleNames(candidates)
	for i, dir := range candidates {
		name := names[i]
		m, err := loadModuleDir(name, dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if m != nil {
			modules = append(modules, m)
		}
	}

	return modules, nil
}

// uniqueModuleNames names each .devops directory by its first path segment and
// falls back to the full relative path (minus "/.devops") for any name that
// more than one directory, or the reserved "root" module, would share.
func uniqueModuleNames(dirs []string) []string {
	count := map[string]int{"root": 1}
	for _, d := range dirs {
		count[moduleNameFromPath(d)]++
	}
	names := make([]string, len(dirs))
	for i, d := range dirs {
		names[i] = moduleNameFromPath(d)
		if count[names[i]] > 1 {
			full := filepath.ToSlash(filepath.Clean(d))
			names[i] = strings.TrimSuffix(full, "/.devops")
			if names[i] == "root" {
				names[i] = full
			}
		}
	}
	return names
}

func moduleNameFromPath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p != "." && p != ".devops" && p != "commands.yaml" && p != "agents.yaml" && p != "" {
			return p
		}
	}
	return path
}

// loadModuleDir loads commands.yaml and agents.yaml from a .devops directory.
// It returns nil if neither file exists.
func loadModuleDir(name, dir string) (*Module, error) {
	commandsPath := filepath.Join(dir, "commands.yaml")
	agentsPath := filepath.Join(dir, "agents.yaml")
	hasCommands, hasAgents := fileExists(commandsPath), fileExists(agentsPath)
	if !hasCommands && !hasAgents {
		return nil, nil
	}

	m := &Module{Name: name, Path: commandsPath}
	if hasCommands {
		loaded, err := loadModule(name, commandsPath)
		if err != nil {
			return nil, err
		}
		m = loaded
	}
	if hasAgents {
		cfg, descs, order, err := loadConfig(agentsPath)
		if err != nil {
			return nil, fmt.Errorf("agents.yaml: %w", err)
		}
		m.Agents = cfg
		m.AgentDescriptions = descs
		m.AgentsOrder = order
		m.AgentsPath = agentsPath
	}
	return m, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func loadModule(name, path string) (*Module, error) {
	cfg, descs, order, err := loadConfig(path)
	if err != nil {
		return nil, err
	}
	return &Module{Name: name, Path: path, Config: cfg, Descriptions: descs, Order: order}, nil
}

// loadConfig parses a commands/agents file, splitting off each command's
// optional scalar `description` so it is not treated as a container. The
// returned order holds each command's containers as written in the file.
func loadConfig(path string) (CommandConfig, map[string]string, map[string][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, nil, err
	}
	cfg := CommandConfig{}
	descs := map[string]string{}
	order := map[string][]string{}
	if len(doc.Content) == 0 {
		return cfg, descs, order, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, nil, nil, fmt.Errorf("%s: expected a mapping of commands", path)
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		command, body := top.Content[i].Value, top.Content[i+1]
		if body.Kind != yaml.MappingNode {
			return nil, nil, nil, fmt.Errorf("%s: command %q must map containers to scripts", path, command)
		}
		if _, dup := cfg[command]; dup {
			return nil, nil, nil, fmt.Errorf("%s: duplicate command %q", path, command)
		}
		cfg[command] = map[string][]Entry{}
		for j := 0; j+1 < len(body.Content); j += 2 {
			container, node := body.Content[j].Value, body.Content[j+1]
			if container == descriptionKey && node.Kind == yaml.ScalarNode {
				if _, dup := descs[command]; dup {
					return nil, nil, nil, fmt.Errorf("%s: command %q has duplicate %q", path, command, container)
				}
				descs[command] = node.Value
				continue
			}
			if _, dup := cfg[command][container]; dup {
				return nil, nil, nil, fmt.Errorf("%s: command %q has duplicate container %q", path, command, container)
			}
			var entries []Entry
			if err := node.Decode(&entries); err != nil {
				return nil, nil, nil, err
			}
			cfg[command][container] = entries
			order[command] = append(order[command], container)
		}
	}
	return cfg, descs, order, nil
}

// agentModules returns a view of modules whose Config is their agents.yaml,
// so the regular runner executes agent commands unchanged.
func agentModules(modules []*Module) []*Module {
	var out []*Module
	for _, m := range modules {
		if len(m.Agents) > 0 {
			out = append(out, &Module{Name: m.Name, Path: m.AgentsPath, Config: m.Agents, Descriptions: m.AgentDescriptions, Order: m.AgentsOrder})
		}
	}
	return out
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
