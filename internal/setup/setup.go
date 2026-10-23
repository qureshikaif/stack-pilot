// Package setup installs and configures a development environment for a
// chosen stack: it asks what you are building, shows a plan, installs what is
// missing, fixes environment variables, and verifies the result.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/qureshikaif/stack-pilot/internal/probe"
)

type Config struct {
	Templates []string
	// Editors and AI are nil when not given, which means "ask".
	Editors []string
	AI      []string

	AndroidAPI    string // empty means "ask", then DefaultAndroidAPI
	Python        string
	AndroidStudio *bool // nil means "ask", then true
	Emulator      *bool // nil means "ask", then true

	Yes            bool // no questions; accept defaults
	DryRun         bool
	AcceptLicenses bool
}

const (
	DefaultAndroidAPI = "36"
	DefaultPython     = "3.13"
)

func (c Config) androidStudio() bool { return c.AndroidStudio == nil || *c.AndroidStudio }
func (c Config) emulator() bool      { return c.Emulator == nil || *c.Emulator }

func (c Config) wantsAndroid() bool {
	for _, t := range Templates {
		if t.Android && contains(c.Templates, t.Key) {
			return true
		}
	}
	return false
}

func validKeys(kind string, got []string, valid []Choice) error {
	for _, g := range got {
		ok := false
		for _, v := range valid {
			ok = ok || v.Key == g
		}
		if !ok {
			keys := make([]string, len(valid))
			for i, v := range valid {
				keys[i] = v.Key
			}
			return fmt.Errorf("unknown %s %q (choose from %s)", kind, g, strings.Join(keys, ", "))
		}
	}
	return nil
}

func templateChoices() []Choice {
	out := make([]Choice, len(Templates))
	for i, t := range Templates {
		out[i] = Choice{Key: t.Key, Label: t.Label}
	}
	return out
}

func (c Config) validate() error {
	if err := validKeys("template", c.Templates, templateChoices()); err != nil {
		return err
	}
	if err := validKeys("editor", c.Editors, Editors); err != nil {
		return err
	}
	return validKeys("AI tool", c.AI, AITools)
}

// ask fills in anything the flags left open.
func ask(c Config, pr *Prompter) (Config, error) {
	var err error
	if len(c.Templates) == 0 {
		if c.Templates, err = pr.Multi("What do you want to set up?", templateChoices()); err != nil {
			return c, err
		}
	}
	if c.wantsAndroid() {
		if c.AndroidStudio == nil {
			v, err := pr.Confirm("Install Android Studio?", true)
			if err != nil {
				return c, err
			}
			c.AndroidStudio = &v
		}
		if c.Emulator == nil {
			v, err := pr.Confirm("Create an Android emulator?", true)
			if err != nil {
				return c, err
			}
			c.Emulator = &v
		}
		if c.AndroidAPI == "" {
			if c.AndroidAPI, err = pr.One("Android API level to install:", []Choice{
				{Key: "36", Label: "36 — Android 16 (latest stable)"},
				{Key: "35", Label: "35 — Android 15"},
			}); err != nil {
				return c, err
			}
		}
	}
	if c.Editors == nil {
		if c.Editors, err = pr.Multi("Which editors do you want?", Editors); err != nil {
			return c, err
		}
	}
	if c.AI == nil {
		if c.AI, err = pr.Multi("Which AI coding assistants do you want?", AITools); err != nil {
			return c, err
		}
	}
	return c, nil
}

func wantedSteps(c Config) []string {
	var ids []string
	for _, t := range Templates {
		if contains(c.Templates, t.Key) {
			ids = append(ids, t.Steps...)
		}
	}
	if c.wantsAndroid() {
		ids = append(ids, androidSteps(c)...)
	}
	ids = append(ids, c.Editors...)
	return append(ids, c.AI...)
}

// Run is the entry point for `stackpilot setup`.
func Run(ctx context.Context, cfg Config, in io.Reader, out io.Writer) error {
	p, err := DetectPlatform()
	if err != nil {
		return err
	}
	return run(ctx, p, cfg, NewPrompter(in, out), out)
}

func run(ctx context.Context, p Platform, cfg Config, pr *Prompter, out io.Writer) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	fmt.Fprintf(out, "StackPilot setup — %s\n", p.Name())

	interactive := !cfg.Yes
	if interactive {
		var err error
		if cfg, err = ask(cfg, pr); err != nil {
			return err
		}
	}
	if cfg.AndroidAPI == "" {
		cfg.AndroidAPI = DefaultAndroidAPI
	}
	if cfg.Python == "" {
		cfg.Python = DefaultPython
	}
	if len(cfg.Templates)+len(cfg.Editors)+len(cfg.AI) == 0 {
		fmt.Fprintln(out, "\nNothing selected.")
		return nil
	}

	e := newEnv(ctx, p, cfg, out)
	if !cfg.DryRun {
		path, err := LedgerPath()
		if err != nil {
			return err
		}
		if e.ledger, err = loadLedger(path); err != nil {
			return fmt.Errorf("reading install record %s: %w", path, err)
		}
	}
	cat := e.catalog()
	fmt.Fprintln(out, "\nChecking what is already installed…")
	plan := resolve(cat, wantedSteps(cfg), func(s *Step) (bool, string) { return s.Done(e) })
	pending := printPlan(out, e, plan)

	if cfg.DryRun {
		for _, it := range plan {
			if it.State == StateDone && it.Step.Activate != nil {
				e.apply(it.Step.Activate(e))
			}
		}
		if lines := e.envLines(); len(lines) > 0 {
			fmt.Fprintf(out, "\nEnvironment settings for tools already installed (more may follow installs):\n")
			for _, l := range lines {
				fmt.Fprintln(out, "  "+l)
			}
		}
		fmt.Fprintln(out, "\nDry run: nothing was changed.")
		return nil
	}

	if pending > 0 && interactive {
		ok, err := pr.Confirm(fmt.Sprintf("Run these %d steps?", pending), true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Cancelled; nothing was changed.")
			return nil
		}
	}

	failed, err := execute(e, cat, plan, pr, interactive)
	if err != nil {
		return err
	}
	if err := updateProfile(e, pr, interactive); err != nil {
		fmt.Fprintf(out, "\n! could not update shell profile: %v\n", err)
	}
	verify(e, plan)

	if len(failed) > 0 {
		return fmt.Errorf("%d step(s) did not complete: %s — fix the error above and run setup again; finished steps are skipped",
			len(failed), strings.Join(failed, ", "))
	}
	return nil
}

func printPlan(out io.Writer, e *Env, plan []PlanItem) (pending int) {
	fmt.Fprintf(out, "\nPlan\n")
	for _, it := range plan {
		s := it.Step
		switch it.State {
		case StateDone:
			fmt.Fprintf(out, "  ✓ %-46s already set up: %s\n", s.Title, it.Detail)
		case StateManual:
			fmt.Fprintf(out, "  ! %-46s manual: %s\n", s.Title, it.Detail)
		case StatePending:
			pending++
			if s.Run != nil {
				fmt.Fprintf(out, "  • %-46s %s\n", s.Title, s.RunDesc)
				continue
			}
			for i, c := range s.Commands {
				title := s.Title
				if i > 0 {
					title = ""
				}
				bullet := " "
				if i == 0 {
					bullet = "•"
				}
				fmt.Fprintf(out, "  %s %-46s $ %s\n", bullet, title, strings.ReplaceAll(c.String(), e.Home, "~"))
			}
		}
	}
	if pending == 0 {
		fmt.Fprintln(out, "\nEverything is already installed.")
		return 0
	}
	if e.OS == "windows" {
		fmt.Fprintln(out, "\nSome installers will show a Windows permission (UAC) prompt; approve it to continue.")
	}
	for _, it := range plan {
		if it.State == StatePending && it.Step.ID == "android-sdk" {
			fmt.Fprintln(out, "\nNote: Android SDK packages and the emulator image download several GB.")
			fmt.Fprintln(out, "Installing them means accepting the Android SDK License Agreement:")
			fmt.Fprintln(out, "https://developer.android.com/studio/terms")
			if free, ok := freeBytes(e.Home); ok && free < 15<<30 {
				fmt.Fprintf(out, "! Only %d GB free in your home directory; 15 GB or more is recommended.\n", free>>30)
			}
		}
	}
	return pending
}

func execute(e *Env, cat map[string]*Step, plan []PlanItem, pr *Prompter, interactive bool) ([]string, error) {
	out := e.out
	ok := map[string]bool{}
	var failed []string

	for _, it := range plan {
		s := it.Step
		if it.State == StateDone {
			if s.Activate != nil {
				e.apply(s.Activate(e))
			}
			ok[s.ID] = true
			continue
		}

		blocked := ""
		for _, n := range s.Needs {
			if _, inCatalog := cat[n]; inCatalog && !ok[n] {
				blocked = n
				break
			}
		}
		if blocked != "" {
			fmt.Fprintf(out, "\n- Skipping %s (needs %s)\n", s.Title, cat[blocked].Title)
			failed = append(failed, s.Title)
			continue
		}
		if it.State == StateManual {
			fmt.Fprintf(out, "\n! %s: %s\n", s.Title, s.Manual)
			continue
		}
		// An earlier step may have satisfied this one (e.g. Homebrew's git).
		if done, _ := s.Done(e); done {
			if s.Activate != nil {
				e.apply(s.Activate(e))
			}
			ok[s.ID] = true
			continue
		}

		fmt.Fprintf(out, "\n▶ %s\n", s.Title)
		stepFailed := false
		for {
			err := runStep(e, s)
			if err == nil {
				break
			}
			if errors.Is(e.ctx.Err(), context.Canceled) {
				return failed, e.ctx.Err()
			}
			fmt.Fprintf(out, "✗ %s failed: %v\n", s.Title, err)
			if !interactive {
				stepFailed = true
				break
			}
			choice, perr := pr.One("What now?", []Choice{
				{Key: "retry", Label: "Retry"}, {Key: "skip", Label: "Skip this step"}, {Key: "abort", Label: "Stop setup"},
			})
			if perr != nil {
				return failed, perr
			}
			if choice == "abort" {
				return failed, errors.New("setup stopped; run it again to continue where you left off")
			}
			if choice == "skip" {
				stepFailed = true
				break
			}
		}
		if stepFailed {
			failed = append(failed, s.Title)
			continue
		}

		e.recordInstall(s)
		if e.OS == "windows" {
			refreshPath() // pick up PATH entries the installer just added
		}
		if s.Activate != nil {
			e.apply(s.Activate(e))
		}
		ok[s.ID] = true
		if done, detail := s.Done(e); done {
			fmt.Fprintf(out, "✓ %s: %s\n", s.Title, detail)
		} else {
			fmt.Fprintf(out, "! %s finished but isn't detected yet; it may need a new terminal\n", s.Title)
		}
	}
	return failed, nil
}

func runStep(e *Env, s *Step) error {
	if s.Run != nil {
		return s.Run(e.ctx, e)
	}
	for _, c := range s.Commands {
		fmt.Fprintf(e.out, "  $ %s\n", c)
		if err := e.exec(c); err != nil {
			return err
		}
	}
	return nil
}

func updateProfile(e *Env, pr *Prompter, interactive bool) error {
	lines := e.envLines()
	if len(lines) == 0 {
		return nil
	}
	out := e.out
	if e.OS == "windows" {
		return updateWindowsEnv(e, pr, interactive, lines)
	}
	if e.Profile == "" {
		fmt.Fprintf(out, "\nAdd these lines to your %s startup file:\n", e.Shell)
		for _, l := range lines {
			fmt.Fprintln(out, "  "+l)
		}
		return nil
	}

	fmt.Fprintf(out, "\nThese environment settings are missing from your shell:\n")
	for _, l := range lines {
		fmt.Fprintln(out, "  "+l)
	}
	if interactive {
		ok, err := pr.Confirm(fmt.Sprintf("Add them to %s? (a backup is made first)", e.tilde(e.Profile)), true)
		if err != nil || !ok {
			return err
		}
	}
	added, backup, err := writeProfile(e.Profile, lines)
	if err != nil {
		return err
	}
	if e.ledger != nil && len(added) > 0 {
		e.ledger.addProfileLines(e.Profile, added)
		e.saveLedger()
	}
	if len(added) > 0 {
		fmt.Fprintf(out, "✓ Updated %s", e.tilde(e.Profile))
		if backup != "" {
			fmt.Fprintf(out, " (backup: %s)", e.tilde(backup))
		}
		fmt.Fprintln(out)
	}
	return nil
}

func updateWindowsEnv(e *Env, pr *Prompter, interactive bool, lines []string) error {
	out := e.out
	fmt.Fprintf(out, "\nThese environment settings are missing from your Windows user environment:\n")
	for _, l := range lines {
		fmt.Fprintln(out, "  "+l)
	}
	if interactive {
		ok, err := pr.Confirm("Save them to your user environment variables? (old values are backed up)", true)
		if err != nil || !ok {
			return err
		}
	}
	vars, paths := e.missingEnv()
	backup, addedPaths, err := persistUserEnv(vars, paths)
	if err != nil {
		return err
	}
	if e.ledger != nil {
		e.ledger.addWinEnv(vars, addedPaths)
		e.saveLedger()
	}
	fmt.Fprintf(out, "✓ Updated user environment variables (backup: %s)\n", backup)
	return nil
}

func verify(e *Env, plan []PlanItem) {
	out := e.out
	fmt.Fprintf(out, "\nVerification\n")
	for _, it := range plan {
		if done, detail := it.Step.Done(e); done {
			fmt.Fprintf(out, "  ✓ %-46s %s\n", it.Step.Title, detail)
		} else {
			fmt.Fprintf(out, "  ✗ %s\n", it.Step.Title)
		}
	}

	if e.wantsAndroid() && exists(e.sdk("platform-tools")) {
		if t := probe.Local(e.ctx, "adb", e.Home); t.Version != "" {
			fmt.Fprintf(out, "  ✓ %-46s %s\n", "adb", t.Version)
		}
		if e.emulator() && exists(e.emulatorBin()) {
			ctx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
			res, err := exec.CommandContext(ctx, e.emulatorBin(), "-accel-check").CombinedOutput()
			cancel()
			if err == nil {
				fmt.Fprintf(out, "  ✓ %-46s %s\n", "Emulator hardware acceleration", "available")
			} else {
				fmt.Fprintf(out, "  ! %-46s %s\n", "Emulator hardware acceleration", lastLine(string(res)))
				switch e.OS {
				case "linux":
					fmt.Fprintln(out, "    → enable virtualization (VT-x/AMD-V) in BIOS and run: sudo usermod -aG kvm $USER")
				case "windows":
					fmt.Fprintln(out, "    → enable virtualization in BIOS and turn on \"Windows Hypervisor Platform\" in Windows Features")
				}
			}
		}
	}

	fmt.Fprintln(out)
	switch {
	case e.OS == "windows":
		fmt.Fprintln(out, "Open a new terminal so the changes take effect.")
	case e.Profile != "":
		fmt.Fprintf(out, "Open a new terminal (or run `source %s`) so the changes take effect.\n", e.tilde(e.Profile))
	}
	if e.wantsAndroid() && e.emulator() {
		fmt.Fprintf(out, "Start the emulator with: emulator -avd %s\n", e.avdName())
	}
	fmt.Fprintln(out, "Inside a project, run `stackpilot doctor` to check it against this machine.")
	if e.ledger != nil && (len(e.ledger.Installed) > 0 || !e.ledger.Env.empty()) {
		fmt.Fprintln(out, "To remove what setup installed, run `stackpilot uninstall`.")
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
