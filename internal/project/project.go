// Package project reads a repository's manifest and config files to work out
// what it needs. It only reads files; it never executes project code.
package project

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Requirement is a tool the project needs, optionally with a version constraint
// in the syntax of the file that declared it.
type Requirement struct {
	Tool       string `json:"tool"`
	Constraint string `json:"constraint,omitempty"`
	Source     string `json:"source"`
}

// EnvVar is an environment variable name the project expects. Values are never
// read or stored.
type EnvVar struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

type Project struct {
	Root         string        `json:"root"`
	Stacks       []string      `json:"stacks"`
	Requirements []Requirement `json:"requirements"`
	EnvVars      []EnvVar      `json:"envVars"`
	// LocalEnvFiles lists files such as .env that define variables locally.
	LocalEnvFiles []string `json:"localEnvFiles,omitempty"`
	Notes         []string `json:"notes,omitempty"`

	stacks map[string]bool
	seen   map[Requirement]bool
}

// Inspect scans root (non-recursively) and returns what the project needs.
func Inspect(root string) (*Project, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", abs)
	}

	p := &Project{Root: abs, stacks: map[string]bool{}, seen: map[Requirement]bool{}}
	p.inspectNode()
	p.inspectPython()
	p.inspectToolVersions()
	p.inspectEnv()
	if p.exists(".git") {
		p.require("git", "", ".git")
	}

	for s := range p.stacks {
		p.Stacks = append(p.Stacks, s)
	}
	sort.Strings(p.Stacks)
	return p, nil
}

func (p *Project) path(name string) string { return filepath.Join(p.Root, name) }

func (p *Project) exists(name string) bool {
	_, err := os.Stat(p.path(name))
	return err == nil
}

func (p *Project) note(format string, args ...any) {
	p.Notes = append(p.Notes, fmt.Sprintf(format, args...))
}

func (p *Project) require(tool, constraint, source string) {
	r := Requirement{Tool: tool, Constraint: strings.TrimSpace(constraint), Source: source}
	if p.seen[r] {
		return
	}
	p.seen[r] = true
	p.Requirements = append(p.Requirements, r)
}

// firstLine returns the first non-empty, non-comment line of a file.
func (p *Project) firstLine(name string) (string, bool) {
	f, err := os.Open(p.path(name))
	if err != nil {
		return "", false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			return line, true
		}
	}
	return "", false
}

func (p *Project) inspectNode() {
	for _, f := range []string{".nvmrc", ".node-version"} {
		if v, ok := p.firstLine(f); ok {
			p.stacks["node"] = true
			p.require("node", v, f)
		}
	}

	data, err := os.ReadFile(p.path("package.json"))
	if err != nil {
		return
	}
	p.stacks["node"] = true
	var pkg struct {
		Engines        map[string]string `json:"engines"`
		PackageManager string            `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		p.note("package.json is not valid JSON: %v", err)
		p.require("node", "", "package.json")
		return
	}
	p.require("node", pkg.Engines["node"], "package.json")
	for _, tool := range []string{"npm", "pnpm", "yarn", "bun"} {
		if c, ok := pkg.Engines[tool]; ok {
			p.require(tool, c, "package.json engines")
		}
	}

	if pkg.PackageManager != "" {
		// "pnpm@9.1.0+sha512.abc" -> pnpm, 9.1.0
		name, ver, _ := strings.Cut(pkg.PackageManager, "@")
		ver, _, _ = strings.Cut(ver, "+")
		p.require(name, ver, "package.json packageManager")
		if name != "npm" {
			p.note("packageManager pins %s; run `corepack enable` so the pinned version is used automatically", pkg.PackageManager)
		}
		return
	}
	lockfiles := []struct{ file, tool string }{
		{"pnpm-lock.yaml", "pnpm"},
		{"yarn.lock", "yarn"},
		{"bun.lock", "bun"},
		{"bun.lockb", "bun"},
		{"package-lock.json", "npm"},
	}
	for _, l := range lockfiles {
		if p.exists(l.file) {
			p.require(l.tool, "", l.file)
			return
		}
	}
	p.require("npm", "", "package.json")
}

// tomlSections returns key -> raw value for simple `key = value` lines, keyed
// as "section.key". It is deliberately minimal: enough for the handful of keys
// we read, without pulling in a TOML dependency.
func tomlSections(data string) map[string]string {
	out := map[string]string{}
	section := ""
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[] ")
			out[section] = ""
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[section+"."+strings.Trim(strings.TrimSpace(k), `"'`)] = strings.Trim(v, `"'`)
	}
	return out
}

func (p *Project) inspectPython() {
	if v, ok := p.firstLine(".python-version"); ok {
		p.stacks["python"] = true
		p.require("python", v, ".python-version")
	}

	if data, err := os.ReadFile(p.path("pyproject.toml")); err == nil {
		p.stacks["python"] = true
		toml := tomlSections(string(data))
		p.require("python", toml["project.requires-python"], "pyproject.toml")
		if c, ok := toml["tool.poetry.dependencies.python"]; ok {
			p.require("python", c, "pyproject.toml [tool.poetry]")
		}
		if _, ok := toml["tool.poetry"]; ok {
			p.require("poetry", "", "pyproject.toml [tool.poetry]")
		}
		if _, ok := toml["tool.uv"]; ok {
			p.require("uv", "", "pyproject.toml [tool.uv]")
		}
	}

	if data, err := os.ReadFile(p.path("Pipfile")); err == nil {
		p.stacks["python"] = true
		toml := tomlSections(string(data))
		p.require("python", toml["requires.python_version"], "Pipfile")
		p.require("pipenv", "", "Pipfile")
	}

	for _, f := range []string{"requirements.txt", "setup.py", "setup.cfg"} {
		if p.exists(f) {
			p.stacks["python"] = true
			p.require("python", "", f)
		}
	}
	if p.exists("uv.lock") {
		p.require("uv", "", "uv.lock")
	}
	if p.exists("poetry.lock") {
		p.require("poetry", "", "poetry.lock")
	}
}

// inspectToolVersions reads asdf/mise .tool-versions files.
func (p *Project) inspectToolVersions() {
	f, err := os.Open(p.path(".tool-versions"))
	if err != nil {
		return
	}
	defer f.Close()
	names := map[string]string{"nodejs": "node", "node": "node", "python": "python"}
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if tool, ok := names[fields[0]]; ok {
			p.stacks[tool] = true
			p.require(tool, fields[1], ".tool-versions")
		}
	}
}

// envKeys returns variable names defined in a dotenv-style file.
func envKeys(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var keys []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, _, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if ok && k != "" && !strings.ContainsAny(k, " \t") {
			keys = append(keys, k)
		}
	}
	return keys, s.Err()
}

func (p *Project) inspectEnv() {
	seen := map[string]bool{}
	for _, f := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		keys, err := envKeys(p.path(f))
		if err != nil {
			continue
		}
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				p.EnvVars = append(p.EnvVars, EnvVar{Name: k, Source: f})
			}
		}
	}
	for _, f := range []string{".env", ".env.local", ".env.development", ".env.development.local"} {
		if p.exists(f) {
			p.LocalEnvFiles = append(p.LocalEnvFiles, f)
		}
	}
}

// LocalEnvKeys returns the set of variable names defined in the project's
// local env files.
func (p *Project) LocalEnvKeys() map[string]string {
	out := map[string]string{}
	for _, f := range p.LocalEnvFiles {
		keys, _ := envKeys(p.path(f))
		for _, k := range keys {
			if _, ok := out[k]; !ok {
				out[k] = f
			}
		}
	}
	return out
}
