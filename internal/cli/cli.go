// Package cli implements the stackpilot command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/qureshikaif/stack-pilot/internal/doctor"
	"github.com/qureshikaif/stack-pilot/internal/project"
	"github.com/qureshikaif/stack-pilot/internal/setup"
)

// Version is set at release build time (see .goreleaser.yaml).
var Version = "dev"

// version falls back to the module version for `go install ...@v0.1.0` builds.
func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return Version
}

const usage = `stackpilot — figure out what a repo needs and whether your machine has it

Usage:
  stackpilot setup   [flags]             Set up this machine for a stack (asks questions)
  stackpilot uninstall [names] [flags]  Remove what setup installed
  stackpilot inspect [path] [--json]   Show what the project needs (read-only)
  stackpilot doctor  [path] [--json]   Check this machine against those needs
  stackpilot version

Run "stackpilot setup --help" or "stackpilot uninstall --help" for their flags.

doctor exits with status 1 when any check fails, so it can gate CI.
`

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "setup":
		return runSetup(rest, stdout, stderr)
	case "uninstall":
		return runUninstall(rest, stdout, stderr)
	}

	var asJSON bool
	var positional []string
	for _, a := range rest {
		switch a {
		case "--json", "-json":
			asJSON = true
		case "-h", "--help":
			fmt.Fprint(stdout, usage)
			return 0
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "unknown flag %s\n\n%s", a, usage)
				return 2
			}
			positional = append(positional, a)
		}
	}
	if len(positional) > 1 {
		fmt.Fprintf(stderr, "expected at most one path, got %d\n", len(positional))
		return 2
	}
	root := "."
	if len(positional) == 1 {
		root = positional[0]
	}

	switch cmd {
	case "inspect":
		p, err := project.Inspect(root)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if asJSON {
			return writeJSON(stdout, stderr, p)
		}
		printInspect(stdout, p)
		return 0
	case "doctor":
		p, err := project.Inspect(root)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		r := doctor.Run(context.Background(), p, doctor.Options{})
		code := 0
		if r.Failed() {
			code = 1
		}
		if asJSON {
			if c := writeJSON(stdout, stderr, r); c != 0 {
				return c
			}
			return code
		}
		printDoctor(stdout, r, useColor(stdout))
		return code
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "stackpilot", version())
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
	return 2
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func useColor(w io.Writer) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func printInspect(w io.Writer, p *project.Project) {
	fmt.Fprintf(w, "Project: %s\n", p.Root)
	if len(p.Stacks) == 0 {
		fmt.Fprintln(w, "\nNo supported stack detected (looked for Node.js and Python files).")
	} else {
		fmt.Fprintf(w, "Stacks:  %s\n", strings.Join(p.Stacks, ", "))
	}

	if len(p.Requirements) > 0 {
		fmt.Fprintln(w, "\nRequirements:")
		for _, r := range p.Requirements {
			c := r.Constraint
			if c == "" {
				c = "any"
			}
			fmt.Fprintf(w, "  %-8s %-16s from %s\n", r.Tool, c, r.Source)
		}
	}
	if len(p.EnvVars) > 0 {
		fmt.Fprintln(w, "\nEnvironment variables:")
		for _, v := range p.EnvVars {
			fmt.Fprintf(w, "  %-28s from %s\n", v.Name, v.Source)
		}
	}
	printNotes(w, p.Notes)
}

func printNotes(w io.Writer, notes []string) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintln(w, "\nNotes:")
	for _, n := range notes {
		fmt.Fprintf(w, "  - %s\n", n)
	}
}

func printDoctor(w io.Writer, r doctor.Report, color bool) {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return "\x1b[" + code + "m" + s + "\x1b[0m"
	}
	icon := map[doctor.Status]string{
		doctor.OK:   paint("32", "✓"),
		doctor.Warn: paint("33", "!"),
		doctor.Fail: paint("31", "✗"),
	}

	fmt.Fprintf(w, "Project: %s (%s/%s)\n", r.Root, r.OS, r.Arch)
	if len(r.Checks) == 0 {
		fmt.Fprintln(w, "\nNothing to check — no supported stack detected.")
		return
	}

	section := ""
	for _, c := range r.Checks {
		if c.Kind != section {
			section = c.Kind
			title := map[string]string{"tool": "Tools", "env": "Environment"}[section]
			fmt.Fprintf(w, "\n%s\n", paint("1", title))
		}
		fmt.Fprintf(w, "  %s %-28s %s\n", icon[c.Status], c.Name, c.Detail)
		if c.Hint != "" {
			fmt.Fprintf(w, "    %s %s\n", paint("2", "→"), c.Hint)
		}
	}
	printNotes(w, r.Notes)

	fmt.Fprintf(w, "\n%d ok, %d warnings, %d failed\n",
		r.Counts[doctor.OK], r.Counts[doctor.Warn], r.Counts[doctor.Fail])
}

// listFlag is a comma-separated flag; nil until set, so "not given" (ask) and
// "none" (empty) are distinguishable.
type listFlag struct{ vals *[]string }

func (l listFlag) String() string {
	if l.vals == nil {
		return ""
	}
	return strings.Join(*l.vals, ",")
}

func (l listFlag) Set(s string) error {
	vals := []string{}
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" && v != "none" {
			vals = append(vals, v)
		}
	}
	*l.vals = append(*l.vals, vals...)
	return nil
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	var cfg setup.Config
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Var(listFlag{&cfg.Templates}, "template", "stacks to set up: node, python, react-native, android")
	fs.Var(listFlag{&cfg.Editors}, "editor", "editors: vscode, cursor, or none")
	fs.Var(listFlag{&cfg.AI}, "ai", "AI coding assistants: claude-code, codex, or none")
	fs.StringVar(&cfg.AndroidAPI, "android-api", "", "Android API level (default "+setup.DefaultAndroidAPI+")")
	fs.StringVar(&cfg.Python, "python", setup.DefaultPython, "Python version to install")
	noStudio := fs.Bool("no-android-studio", false, "skip Android Studio (SDK and emulator only)")
	noEmulator := fs.Bool("no-emulator", false, "skip the emulator and system image")
	fs.BoolVar(&cfg.AcceptLicenses, "accept-android-licenses", false, "accept Android SDK licenses without showing them")
	fs.BoolVar(&cfg.Yes, "yes", false, "don't ask questions; use flags and defaults")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "show the plan without changing anything")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: stackpilot setup [flags]

Asks what you want to build, shows a plan, installs what is missing, sets
ANDROID_HOME / JAVA_HOME / PATH, and verifies the result. Safe to re-run:
anything already installed is skipped.

Examples:
  stackpilot setup
  stackpilot setup --template react-native --dry-run
  stackpilot setup --template node,python --editor vscode --ai claude-code --yes

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "no-android-studio":
			v := !*noStudio
			cfg.AndroidStudio = &v
		case "no-emulator":
			v := !*noEmulator
			cfg.Emulator = &v
		}
	})
	if cfg.Yes && len(cfg.Templates)+len(cfg.Editors)+len(cfg.AI) == 0 {
		fmt.Fprintln(stderr, "--yes needs at least one of --template, --editor or --ai")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := setup.Run(ctx, cfg, os.Stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "\nerror:", err)
		return 1
	}
	return 0
}

// flagsFirst moves flags ahead of positional arguments, since the flag
// package stops at the first non-flag ("uninstall uv --yes").
func flagsFirst(args []string) []string {
	var flags, rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			rest = append(rest, a)
		}
	}
	return append(flags, rest...)
}

func runUninstall(args []string, stdout, stderr io.Writer) int {
	var cfg setup.UninstallConfig
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&cfg.All, "all", false, "remove everything setup installed, and its environment settings")
	fs.BoolVar(&cfg.Yes, "yes", false, "don't ask questions (needs --all or names)")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "show the plan without changing anything")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: stackpilot uninstall [names...] [flags]

Removes what "stackpilot setup" installed, using the same package manager,
and the environment settings it added. Tools that were already on the machine
before setup are never touched. Without names or --all, it asks.

Names are the IDs shown in the list (e.g. uv, android-sdk), or "env" for the
environment settings.

Examples:
  stackpilot uninstall
  stackpilot uninstall --all --dry-run
  stackpilot uninstall android-avd android-sdk --yes

Flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(flagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	cfg.IDs = fs.Args()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := setup.Uninstall(ctx, cfg, os.Stdin, stdout); err != nil {
		fmt.Fprintln(stderr, "\nerror:", err)
		return 1
	}
	return 0
}
