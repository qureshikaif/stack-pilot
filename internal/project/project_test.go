package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectNode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"package.json":   `{"engines": {"node": ">=20"}, "packageManager": "pnpm@9.1.0+sha512.abc"}`,
		".nvmrc":         "v22.14.0\n",
		"pnpm-lock.yaml": "",
		".env.example":   "# comment\nDATABASE_URL=postgres://\nexport API_KEY=\nDATABASE_URL=dup\n",
		".env":           "API_KEY=secret\n",
	})

	p, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Stacks, []string{"node"}) {
		t.Errorf("Stacks = %v", p.Stacks)
	}
	want := []Requirement{
		{"node", "v22.14.0", ".nvmrc"},
		{"node", ">=20", "package.json"},
		{"pnpm", "9.1.0", "package.json packageManager"},
	}
	if !reflect.DeepEqual(p.Requirements, want) {
		t.Errorf("Requirements = %+v\nwant %+v", p.Requirements, want)
	}
	wantEnv := []EnvVar{{"DATABASE_URL", ".env.example"}, {"API_KEY", ".env.example"}}
	if !reflect.DeepEqual(p.EnvVars, wantEnv) {
		t.Errorf("EnvVars = %+v", p.EnvVars)
	}
	if got := p.LocalEnvKeys(); got["API_KEY"] != ".env" || len(got) != 1 {
		t.Errorf("LocalEnvKeys = %v", got)
	}
}

func TestInspectNodeLockfile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"package.json": `{}`, "yarn.lock": ""})
	p, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Requirement{{"node", "", "package.json"}, {"yarn", "", "yarn.lock"}}
	if !reflect.DeepEqual(p.Requirements, want) {
		t.Errorf("Requirements = %+v", p.Requirements)
	}
}

func TestInspectPython(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{
		"pyproject.toml": `
[project]
name = "api"
requires-python = ">=3.11" # comment

[tool.uv]
dev-dependencies = []
`,
		".python-version": "3.12\n",
		"uv.lock":         "",
	})
	p, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Requirement{
		{"python", "3.12", ".python-version"},
		{"python", ">=3.11", "pyproject.toml"},
		{"uv", "", "pyproject.toml [tool.uv]"},
		{"uv", "", "uv.lock"},
	}
	if !reflect.DeepEqual(p.Requirements, want) {
		t.Errorf("Requirements = %+v\nwant %+v", p.Requirements, want)
	}
}

func TestInspectPoetry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"pyproject.toml": `
[tool.poetry]
name = "x"

[tool.poetry.dependencies]
python = "^3.10"
`})
	p, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []Requirement{
		{"python", "", "pyproject.toml"},
		{"python", "^3.10", "pyproject.toml [tool.poetry]"},
		{"poetry", "", "pyproject.toml [tool.poetry]"},
	}
	if !reflect.DeepEqual(p.Requirements, want) {
		t.Errorf("Requirements = %+v\nwant %+v", p.Requirements, want)
	}
}

func TestInspectEmpty(t *testing.T) {
	p, err := Inspect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Stacks) != 0 || len(p.Requirements) != 0 {
		t.Errorf("expected nothing, got %+v", p)
	}
}
