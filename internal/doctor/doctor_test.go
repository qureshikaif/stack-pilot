package doctor

import (
	"context"
	"testing"

	"github.com/qureshikaif/stack-pilot/internal/probe"
	"github.com/qureshikaif/stack-pilot/internal/project"
)

func fakeProbe(versions map[string]string) probe.Prober {
	return func(_ context.Context, name, _ string) probe.Tool {
		v, ok := versions[name]
		if !ok {
			return probe.Tool{Name: name}
		}
		return probe.Tool{Name: name, Found: true, Path: "/bin/" + name, Version: v}
	}
}

func TestRun(t *testing.T) {
	p := &project.Project{
		Root: t.TempDir(),
		Requirements: []project.Requirement{
			{Tool: "node", Constraint: ">=20", Source: "package.json"},
			{Tool: "node", Constraint: "18", Source: ".nvmrc"},
			{Tool: "node", Constraint: "lts/*", Source: ".nvmrc"},
			{Tool: "pnpm", Source: "pnpm-lock.yaml"},
			{Tool: "git", Source: ".git"},
		},
		EnvVars: []project.EnvVar{
			{Name: "HOME_SET", Source: ".env.example"},
			{Name: "MISSING", Source: ".env.example"},
		},
	}
	env := func(k string) (string, bool) { return "x", k == "HOME_SET" }

	r := Run(context.Background(), p, Options{
		Probe: fakeProbe(map[string]string{"node": "22.14.0", "git": "2.39.3"}),
		Env:   env,
	})

	want := []Status{OK, Fail, Warn, Fail, OK, OK, Warn}
	if len(r.Checks) != len(want) {
		t.Fatalf("got %d checks, want %d: %+v", len(r.Checks), len(want), r.Checks)
	}
	for i, c := range r.Checks {
		if c.Status != want[i] {
			t.Errorf("check %d (%s): status %s, want %s — %s", i, c.Name, c.Status, want[i], c.Detail)
		}
	}
	if !r.Failed() {
		t.Error("Failed() = false, want true")
	}
	if len(r.Tools) != 3 {
		t.Errorf("tools probed %d times, want once each (3)", len(r.Tools))
	}
	for _, c := range r.Checks {
		if c.Kind == "env" && (c.Detail == "x" || c.Hint == "x") {
			t.Error("env value leaked into report")
		}
	}
}
