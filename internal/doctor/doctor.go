// Package doctor compares what a project needs with what the machine has.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/qureshikaif/stack-pilot/internal/probe"
	"github.com/qureshikaif/stack-pilot/internal/project"
	"github.com/qureshikaif/stack-pilot/internal/version"
)

type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

type Check struct {
	Kind   string `json:"kind"` // "tool" or "env"
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

type Report struct {
	Root   string         `json:"root"`
	OS     string         `json:"os"`
	Arch   string         `json:"arch"`
	Stacks []string       `json:"stacks"`
	Tools  []probe.Tool   `json:"tools"`
	Checks []Check        `json:"checks"`
	Notes  []string       `json:"notes,omitempty"`
	Counts map[Status]int `json:"counts"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool { return r.Counts[Fail] > 0 }

// Env abstracts os.LookupEnv for tests.
type Env func(string) (string, bool)

type Options struct {
	Probe probe.Prober
	Env   Env
}

func Run(ctx context.Context, p *project.Project, opts Options) Report {
	if opts.Probe == nil {
		opts.Probe = probe.Local
	}
	if opts.Env == nil {
		opts.Env = os.LookupEnv
	}
	r := Report{
		Root: p.Root, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Stacks: p.Stacks, Notes: p.Notes, Counts: map[Status]int{},
	}

	tools := map[string]probe.Tool{}
	for _, req := range p.Requirements {
		t, ok := tools[req.Tool]
		if !ok {
			t = opts.Probe(ctx, req.Tool, p.Root)
			tools[req.Tool] = t
			r.Tools = append(r.Tools, t)
		}
		r.add(checkTool(req, t))
	}

	local := p.LocalEnvKeys()
	for _, v := range p.EnvVars {
		r.add(checkEnv(v, opts.Env, local))
	}

	sort.SliceStable(r.Tools, func(i, j int) bool { return r.Tools[i].Name < r.Tools[j].Name })
	return r
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
	r.Counts[c.Status]++
}

func checkTool(req project.Requirement, t probe.Tool) Check {
	c := Check{Kind: "tool", Name: req.Tool}
	want := req.Constraint
	if want == "" {
		want = "any version"
	}
	switch {
	case !t.Found:
		c.Status = Fail
		c.Detail = fmt.Sprintf("not found on PATH (needed by %s)", req.Source)
		c.Hint = installHint(req.Tool, req.Constraint)
	case t.Version == "":
		c.Status = Warn
		c.Detail = fmt.Sprintf("found at %s but could not read its version: %s", t.Path, t.Error)
	case req.Constraint == "":
		c.Status = OK
		c.Detail = fmt.Sprintf("%s (needed by %s)", t.Version, req.Source)
	default:
		ok, err := version.Satisfies(t.Version, req.Constraint)
		switch {
		case errors.Is(err, version.ErrUnsupported):
			c.Status = Warn
			c.Detail = fmt.Sprintf("%s installed; cannot evaluate %q from %s", t.Version, req.Constraint, req.Source)
		case ok:
			c.Status = OK
			c.Detail = fmt.Sprintf("%s satisfies %s (%s)", t.Version, want, req.Source)
		default:
			c.Status = Fail
			c.Detail = fmt.Sprintf("%s does not satisfy %s (%s)", t.Version, want, req.Source)
			c.Hint = installHint(req.Tool, req.Constraint)
		}
	}
	return c
}

func checkEnv(v project.EnvVar, env Env, local map[string]string) Check {
	c := Check{Kind: "env", Name: v.Name}
	if _, ok := env(v.Name); ok {
		c.Status, c.Detail = OK, "set in environment"
	} else if f, ok := local[v.Name]; ok {
		c.Status, c.Detail = OK, "defined in "+f
	} else {
		c.Status = Warn
		c.Detail = fmt.Sprintf("listed in %s but not set", v.Source)
		c.Hint = fmt.Sprintf("add %s to .env or export it in your shell", v.Name)
	}
	return c
}

var plainVersion = regexp.MustCompile(`^v?\d+(\.\d+){0,2}$`)

func installHint(tool, constraint string) string {
	// Only put the constraint into a command when it is a plain version; a range
	// like ">=3.11" is not valid input for most installers.
	v := "<version>"
	switch {
	case constraint == "":
		v = "latest"
	case plainVersion.MatchString(constraint):
		v = strings.TrimPrefix(constraint, "v")
	}
	switch tool {
	case "node":
		return fmt.Sprintf("install a matching Node.js, e.g. `mise use node@%s` or `nvm install %s`", v, v)
	case "python":
		return fmt.Sprintf("install a matching Python, e.g. `uv python install %s` or `mise use python@%s`", v, v)
	case "pnpm", "yarn":
		return "run `corepack enable` (ships with Node.js) to get the project's pinned version"
	case "npm":
		return "npm ships with Node.js; install Node.js first"
	case "uv":
		return "see https://docs.astral.sh/uv/getting-started/installation/"
	case "poetry":
		return "see https://python-poetry.org/docs/#installation"
	case "pipenv":
		return "`pipx install pipenv`"
	case "bun":
		return "see https://bun.sh/docs/installation"
	case "git":
		if runtime.GOOS == "darwin" {
			return "`xcode-select --install` or `brew install git`"
		}
		return "install git with your system package manager"
	}
	return ""
}
