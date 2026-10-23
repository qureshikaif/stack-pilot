// Package probe inspects the local machine: which tools are on PATH and what
// versions they report.
package probe

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/qureshikaif/stack-pilot/internal/version"
)

type Tool struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Prober looks up a tool. dir is the project root, so that version managers
// that read per-directory config (pyenv, corepack, mise) resolve correctly.
type Prober func(ctx context.Context, name, dir string) Tool

// binaries lists the executables to try for each tool, in order.
var binaries = map[string][]string{
	"python": {"python3", "python"},
}

const timeout = 10 * time.Second

// Local probes the real machine by running `<tool> --version`.
func Local(ctx context.Context, name, dir string) Tool {
	candidates := binaries[name]
	if candidates == nil {
		candidates = []string{name}
	}
	t := Tool{Name: name}
	for _, bin := range candidates {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		t.Found, t.Path = true, path

		ctx, cancel := context.WithTimeout(ctx, timeout)
		cmd := exec.CommandContext(ctx, path, "--version")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Error = strings.TrimSpace(firstLine(string(out)) + " " + err.Error())
			continue
		}
		t.Version, t.Error = version.Extract(string(out)), ""
		return t
	}
	return t
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
