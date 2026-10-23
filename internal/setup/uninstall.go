package setup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UninstallConfig selects what `stackpilot uninstall` removes.
type UninstallConfig struct {
	IDs    []string // record IDs, or "env" for environment settings
	All    bool
	Yes    bool
	DryRun bool
}

// envItemID names the environment settings in the uninstall list.
const envItemID = "env"

// winget exit code meaning the package is not installed.
const wingetNotInstalled = 0x8A150014 // APPINSTALLER_CLI_ERROR_NO_APPLICATIONS_FOUND

// undoAction is how one record is reversed.
type undoAction struct {
	Commands []Command
	Remove   []string // paths deleted directly
	Manual   string   // the user has to do it; shown, never run
}

type uninstallItem struct {
	ID     string
	Title  string
	Record *Record // nil for the environment item
	Action undoAction
}

// Uninstall is the entry point for `stackpilot uninstall`.
func Uninstall(ctx context.Context, cfg UninstallConfig, in io.Reader, out io.Writer) error {
	p, err := DetectPlatform()
	if err != nil {
		return err
	}
	path, err := LedgerPath()
	if err != nil {
		return err
	}
	l, err := loadLedger(path)
	if err != nil {
		return fmt.Errorf("reading install record %s: %w", path, err)
	}
	return uninstall(ctx, p, l, cfg, NewPrompter(in, out), out)
}

// safeToDelete allows deleting only paths strictly inside the home directory.
func safeToDelete(path, home string) bool {
	if home == "" || !filepath.IsAbs(path) {
		return false
	}
	path, home = filepath.Clean(path), filepath.Clean(home)
	return strings.HasPrefix(path, home+string(filepath.Separator))
}

func (e *Env) undoAction(r Record) undoAction {
	u := r.Undo
	switch u.Method {
	case "brew":
		return undoAction{Commands: []Command{{Args: []string{"brew", "uninstall", u.Package}}}}
	case "cask":
		return undoAction{Commands: []Command{{Args: []string{"brew", "uninstall", "--cask", u.Package}}}}
	case "apt":
		// No -y: apt lists anything else it would remove and asks first.
		return undoAction{Commands: []Command{{Args: e.sudo("apt-get", "remove", u.Package)}}}
	case "snap":
		return undoAction{Commands: []Command{{Args: e.sudo("snap", "remove", u.Package)}}}
	case "winget":
		return undoAction{Commands: []Command{{
			Args:   []string{"winget", "uninstall", "--id", u.Package, "--exact", "--source", "winget", "--disable-interactivity"},
			OkExit: []uint32{wingetNotInstalled},
		}}}
	case "mise-tool":
		return undoAction{Commands: []Command{{Args: []string{"mise", "unuse", "--global", u.Package}}}}
	case "uv-python":
		return undoAction{Commands: []Command{{Args: []string{"uv", "python", "uninstall", u.Package}}}}
	case "npm-global":
		return undoAction{Commands: []Command{{Args: []string{"npm", "uninstall", "--global", u.Package}}}}
	case "android-sdk":
		if len(u.Packages) == 0 {
			return undoAction{}
		}
		return undoAction{Commands: []Command{{Args: append([]string{u.Tool, "--uninstall"}, u.Packages...)}}}
	case "android-avd":
		return undoAction{Commands: []Command{{Args: []string{u.Tool, "delete", "avd", "--name", u.Package}}}}
	case "files":
		var a undoAction
		for _, p := range u.Paths {
			if safeToDelete(p, e.Home) {
				a.Remove = append(a.Remove, p)
			} else {
				a.Manual += "delete " + p + " yourself (outside your home folder, so StackPilot won't); "
			}
		}
		a.Manual = strings.TrimSuffix(a.Manual, "; ")
		return a
	case "manual":
		return undoAction{Manual: r.Title + " is shared by other software, so StackPilot won't remove it. To remove it and everything installed with it, run: " + u.Package}
	}
	return undoAction{Manual: "don't know how to undo install method " + u.Method}
}

func (e *Env) envItem(l *Ledger) (uninstallItem, bool) {
	if l.Env.empty() {
		return uninstallItem{}, false
	}
	it := uninstallItem{ID: envItemID}
	if len(l.Env.ProfileLines) > 0 {
		it.Title = fmt.Sprintf("Environment settings (%d lines in %s)", len(l.Env.ProfileLines), e.tilde(l.Env.Profile))
	} else {
		names := varNames(l.Env.WinVars)
		if len(l.Env.WinPaths) > 0 {
			names = append(names, fmt.Sprintf("%d PATH entries", len(l.Env.WinPaths)))
		}
		it.Title = "Environment settings (" + strings.Join(names, ", ") + ")"
	}
	return it, true
}

func varNames(vars []Var) []string {
	names := make([]string, len(vars))
	for i, v := range vars {
		names[i] = v.Name
	}
	return names
}

// uninstallItems lists everything that can be removed, oldest first.
func (e *Env) uninstallItems(l *Ledger) []uninstallItem {
	var items []uninstallItem
	for _, r := range l.Installed {
		// &r points at a per-iteration copy, not into the ledger slice,
		// which shifts as records are removed.
		items = append(items, uninstallItem{ID: r.ID, Title: r.Title, Record: &r, Action: e.undoAction(r)})
	}
	if it, ok := e.envItem(l); ok {
		items = append(items, it)
	}
	return items
}

// withDependents adds records that can only be undone with a selected tool
// (Python installed by uv must go before uv). It returns the notes to show.
func withDependents(l *Ledger, selected []string) ([]string, []string) {
	var notes []string
	titles := map[string]string{}
	for _, r := range l.Installed {
		titles[r.ID] = r.Title
	}
	for changed := true; changed; {
		changed = false
		for _, r := range l.Installed {
			if r.Undo.Via != "" && contains(selected, r.Undo.Via) && !contains(selected, r.ID) {
				selected = append(selected, r.ID)
				notes = append(notes, fmt.Sprintf("Also removing %s, because it is uninstalled with %s.", r.Title, titles[r.Undo.Via]))
				changed = true
			}
		}
	}
	return selected, notes
}

func uninstall(ctx context.Context, p Platform, l *Ledger, cfg UninstallConfig, pr *Prompter, out io.Writer) error {
	fmt.Fprintf(out, "StackPilot uninstall — %s\n", p.Name())
	e := newEnv(ctx, p, Config{}, out)
	e.ledger = l

	items := e.uninstallItems(l)
	if len(items) == 0 {
		fmt.Fprintln(out, "\nNothing to uninstall: StackPilot hasn't installed anything on this machine.")
		fmt.Fprintln(out, "Tools that were already installed before `stackpilot setup` are never removed.")
		return nil
	}

	var selected []string
	switch {
	case cfg.All:
		for _, it := range items {
			selected = append(selected, it.ID)
		}
	case len(cfg.IDs) > 0:
		for _, id := range cfg.IDs {
			found := false
			for _, it := range items {
				found = found || it.ID == id
			}
			if !found {
				var ids []string
				for _, it := range items {
					ids = append(ids, it.ID)
				}
				return fmt.Errorf("%q wasn't installed by StackPilot (choose from %s)", id, strings.Join(ids, ", "))
			}
		}
		selected = cfg.IDs
	case cfg.Yes:
		return errors.New("--yes needs --all or the names of what to remove")
	default:
		choices := make([]Choice, len(items))
		for i, it := range items {
			label := fmt.Sprintf("%s [%s]", it.Title, it.ID)
			if it.Record != nil {
				label += fmt.Sprintf("  installed %s", it.Record.InstalledAt.Format("2006-01-02"))
			}
			choices[i] = Choice{Key: it.ID, Label: label}
		}
		var err error
		if selected, err = pr.Multi("Installed by StackPilot. Which do you want to remove?", choices); err != nil {
			return err
		}
	}
	if len(selected) == 0 {
		fmt.Fprintln(out, "\nNothing selected.")
		return nil
	}
	selected, notes := withDependents(l, selected)

	// Undo in reverse install order, environment settings last.
	var plan []uninstallItem
	for i := len(items) - 1; i >= 0; i-- {
		if it := items[i]; it.Record != nil && contains(selected, it.ID) {
			plan = append(plan, it)
		}
	}
	for _, it := range items {
		if it.Record == nil && contains(selected, it.ID) {
			plan = append(plan, it)
		}
	}

	printUninstallPlan(e, l, plan, notes)
	if cfg.DryRun {
		fmt.Fprintln(out, "\nDry run: nothing was changed.")
		return nil
	}
	if !cfg.Yes {
		ok, err := pr.Confirm(fmt.Sprintf("Remove these %d items?", len(plan)), false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "Cancelled; nothing was changed.")
			return nil
		}
	}

	var failed []string
	removed := 0
	for _, it := range plan {
		done, err := runUndo(e, l, it, pr, !cfg.Yes)
		if err != nil {
			return err
		}
		if done {
			removed++
		} else {
			failed = append(failed, it.Title)
		}
	}

	fmt.Fprintf(out, "\nRemoved %d of %d.", removed, len(plan))
	if e.OS == "windows" {
		fmt.Fprintln(out, " Open a new terminal so the changes take effect.")
	} else {
		fmt.Fprintln(out, " Open a new terminal so PATH changes take effect.")
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d item(s) were not removed: %s — they stay in the record, so you can run uninstall again",
			len(failed), strings.Join(failed, ", "))
	}
	return nil
}

func printUninstallPlan(e *Env, l *Ledger, plan []uninstallItem, notes []string) {
	out := e.out
	for _, n := range notes {
		fmt.Fprintln(out, "\n"+n)
	}
	fmt.Fprintf(out, "\nPlan\n")
	hasApt := false
	for _, it := range plan {
		a := it.Action
		lines := []string{}
		for _, c := range a.Commands {
			lines = append(lines, "$ "+strings.ReplaceAll(c.String(), e.Home, "~"))
			hasApt = hasApt || strings.Contains(c.String(), "apt-get")
		}
		for _, p := range a.Remove {
			lines = append(lines, "delete "+e.tilde(p))
		}
		if it.Record == nil {
			if e.OS == "windows" {
				lines = append(lines, "remove from your user environment variables (backup first): "+strings.Join(windowsLines(l.Env.WinVars, l.Env.WinPaths), "; "))
			} else {
				lines = append(lines, fmt.Sprintf("remove these lines from %s (backup first):", e.tilde(l.Env.Profile)))
				for _, pl := range l.Env.ProfileLines {
					lines = append(lines, "  "+pl)
				}
			}
		}
		bullet := "•"
		if a.Manual != "" && len(lines) == 0 {
			bullet = "!"
			lines = append(lines, "manual: "+a.Manual)
		} else if a.Manual != "" {
			lines = append(lines, "manual: "+a.Manual)
		}
		if len(lines) == 0 {
			lines = []string{"nothing left to remove"}
		}
		for i, line := range lines {
			title := ""
			b := " "
			if i == 0 {
				title, b = it.Title, bullet
			}
			fmt.Fprintf(out, "  %s %-46s %s\n", b, title, line)
		}
	}
	fmt.Fprintln(out, "\nNot touched: anything that was installed before setup, your projects, and app settings")
	fmt.Fprintln(out, "(for example ~/.claude or your editor settings).")
	if hasApt {
		fmt.Fprintln(out, "apt will show anything else it would remove and ask you to confirm.")
	}
	if e.OS == "windows" {
		fmt.Fprintln(out, "Some uninstallers will show a Windows permission (UAC) prompt.")
	}
}

// runUndo reverses one item and updates the record. It reports whether the
// item was removed.
func runUndo(e *Env, l *Ledger, it uninstallItem, pr *Prompter, interactive bool) (bool, error) {
	out := e.out
	fmt.Fprintf(out, "\n▶ Removing %s\n", it.Title)

	if it.Record == nil {
		return true, removeEnv(e, l)
	}

	a := it.Action
	if a.Manual != "" && len(a.Commands) == 0 && len(a.Remove) == 0 {
		fmt.Fprintf(out, "! %s\n", a.Manual)
		l.remove(it.ID)
		e.saveLedger()
		return true, nil
	}

	for {
		err := runUndoAction(e, a)
		if err == nil {
			break
		}
		if errors.Is(e.ctx.Err(), context.Canceled) {
			return false, e.ctx.Err()
		}
		fmt.Fprintf(out, "✗ %v\n", err)
		if !interactive {
			return false, nil
		}
		choice, perr := pr.One("What now?", []Choice{
			{Key: "retry", Label: "Retry"},
			{Key: "keep", Label: "Skip; keep it in the record"},
			{Key: "forget", Label: "It's already gone; forget it"},
		})
		if perr != nil {
			return false, perr
		}
		if choice == "keep" {
			return false, nil
		}
		if choice == "forget" {
			break
		}
	}
	if a.Manual != "" {
		fmt.Fprintf(out, "! %s\n", a.Manual)
	}
	l.remove(it.ID)
	e.saveLedger()
	fmt.Fprintf(out, "✓ Removed %s\n", it.Title)
	return true, nil
}

func runUndoAction(e *Env, a undoAction) error {
	for _, c := range a.Commands {
		fmt.Fprintf(e.out, "  $ %s\n", c)
		if err := e.exec(c); err != nil {
			return fmt.Errorf("%s: %w", c, err)
		}
	}
	for _, p := range a.Remove {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
		fmt.Fprintf(e.out, "  deleted %s\n", e.tilde(p))
	}
	return nil
}

func removeEnv(e *Env, l *Ledger) error {
	out := e.out
	if e.OS == "windows" {
		backup, kept, err := removeUserEnv(l.Env.WinVars, l.Env.WinPaths)
		if err != nil {
			return err
		}
		for _, name := range kept {
			fmt.Fprintf(out, "  kept %s: you changed it after setup\n", name)
		}
		fmt.Fprintf(out, "✓ Updated user environment variables (backup: %s)\n", backup)
		l.Env.WinVars, l.Env.WinPaths = nil, nil
	} else {
		removed, backup, err := unwriteProfile(l.Env.Profile, l.Env.ProfileLines)
		if err != nil {
			return err
		}
		if len(removed) > 0 {
			fmt.Fprintf(out, "✓ Removed %d lines from %s (backup: %s)\n", len(removed), e.tilde(l.Env.Profile), e.tilde(backup))
		} else {
			fmt.Fprintf(out, "  %s no longer has these lines\n", e.tilde(l.Env.Profile))
		}
		l.Env.Profile, l.Env.ProfileLines = "", nil
	}
	e.saveLedger()
	return nil
}
