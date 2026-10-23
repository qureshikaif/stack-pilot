package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Step is one unit of setup work, such as "install JDK 17".
type Step struct {
	ID    string
	Title string
	// Needs lists steps that must succeed first. They are only pulled into the
	// plan when this step actually has work to do.
	Needs []string
	// Done reports whether the step is already satisfied, with a short detail
	// such as the installed version.
	Done func(e *Env) (bool, string)
	// Exactly one of Commands, Run or Manual describes how to do the step.
	Commands []Command
	Run      func(ctx context.Context, e *Env) error
	RunDesc  string // shown in the plan for Run steps
	Manual   string // instructions when there is no automated install here
	// Activate returns environment changes the step provides (ANDROID_HOME,
	// PATH entries). It is applied in-process so later steps can find the
	// tools, and written to the shell profile at the end.
	Activate func(e *Env) Activation
	// Undo is recorded when this step installs something; empty means the
	// step has nothing to uninstall (e.g. accepting licenses).
	Undo Undo
}

func (s *Step) automated() bool { return len(s.Commands) > 0 || s.Run != nil }

// Command is a process to run. Exactly one of Args or Script is set.
type Command struct {
	Args   []string
	Script string   // bash -c, or PowerShell on Windows; for official install scripts
	Input  string   // fed to stdin; otherwise the terminal is attached
	Once   string   // skip if a command with the same key already ran
	OkExit []uint32 // non-zero exit codes that still mean success
}

func (c Command) String() string {
	if c.Script != "" {
		return c.Script
	}
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		if strings.ContainsAny(a, " ;*$\"'") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

type Var struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Activation is a set of environment changes.
type Activation struct {
	Vars []Var
	Path []string // prepended to PATH
}

// Env carries everything steps need while planning and running.
type Env struct {
	Platform
	Config
	AndroidHome string

	ctx context.Context
	out io.Writer
	ran map[string]bool

	// Environment before we touched it, to decide what the profile needs.
	origEnv  map[string]string
	origPath map[string]bool

	applied Activation // accumulated, deduplicated

	ledger *Ledger // nil in dry runs and tests: nothing is recorded
}

// recordInstall notes that setup installed s, so uninstall can reverse it.
func (e *Env) recordInstall(s *Step) {
	if e.ledger == nil || s.Undo.Method == "" {
		return
	}
	e.ledger.add(Record{ID: s.ID, Title: s.Title, Undo: s.Undo, InstalledAt: time.Now()})
	e.saveLedger()
}

func (e *Env) saveLedger() {
	if e.ledger == nil {
		return
	}
	if err := e.ledger.save(); err != nil {
		fmt.Fprintf(e.out, "! could not save the install record (%v); `stackpilot uninstall` won't know about this change\n", err)
	}
}

func newEnv(ctx context.Context, p Platform, cfg Config, out io.Writer) *Env {
	e := &Env{
		Platform: p, Config: cfg, ctx: ctx, out: out, ran: map[string]bool{},
		origEnv: map[string]string{}, origPath: map[string]bool{},
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			e.origEnv[k] = v
		}
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		e.origPath[pathKey(dir)] = true
	}
	e.AndroidHome = defaultAndroidHome(p)
	if v := os.Getenv("ANDROID_HOME"); v != "" {
		e.AndroidHome = v
	}
	return e
}

func defaultAndroidHome(p Platform) string {
	switch p.OS {
	case "darwin":
		return filepath.Join(p.Home, "Library", "Android", "sdk")
	case "windows":
		return filepath.Join(p.LocalAppData, "Android", "Sdk")
	}
	return filepath.Join(p.Home, "Android", "Sdk")
}

// pathKey normalizes a PATH entry for comparison; Windows paths are
// case-insensitive.
func pathKey(dir string) string {
	dir = filepath.Clean(dir)
	if runtime.GOOS == "windows" {
		return strings.ToLower(dir)
	}
	return dir
}

// apply makes an activation take effect for this process and its children.
func (e *Env) apply(a Activation) {
	for _, v := range a.Vars {
		if v.Value == "" {
			continue
		}
		os.Setenv(v.Name, v.Value)
		found := false
		for i, have := range e.applied.Vars {
			if have.Name == v.Name {
				e.applied.Vars[i] = v
				found = true
			}
		}
		if !found {
			e.applied.Vars = append(e.applied.Vars, v)
		}
	}
	current := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, dir := range current {
		have[pathKey(dir)] = true
	}
	for _, dir := range a.Path {
		dir = filepath.Clean(dir)
		if !contains(e.applied.Path, dir) {
			e.applied.Path = append(e.applied.Path, dir)
		}
		if !have[pathKey(dir)] {
			have[pathKey(dir)] = true
			current = append([]string{dir}, current...)
		}
	}
	os.Setenv("PATH", strings.Join(current, string(os.PathListSeparator)))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (e *Env) sudo(args ...string) []string {
	if e.Root {
		return args
	}
	return append([]string{"sudo"}, args...)
}

func (e *Env) exec(c Command) error {
	if c.Once != "" && e.ran[c.Once] {
		return nil
	}
	var cmd *exec.Cmd
	switch {
	case c.Script != "" && e.OS == "windows":
		cmd = exec.CommandContext(e.ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", c.Script)
	case c.Script != "":
		cmd = exec.CommandContext(e.ctx, "/bin/bash", "-c", c.Script)
	default:
		cmd = exec.CommandContext(e.ctx, c.Args[0], c.Args[1:]...)
		prepareCmd(cmd, c.Args)
	}
	cmd.Stdout, cmd.Stderr = e.out, e.out
	if c.Input != "" {
		cmd.Stdin = strings.NewReader(c.Input)
	} else {
		cmd.Stdin = os.Stdin
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || !containsCode(c.OkExit, uint32(exit.ExitCode())) {
			return err
		}
	}
	if c.Once != "" {
		e.ran[c.Once] = true
	}
	return nil
}

func containsCode(codes []uint32, code uint32) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
