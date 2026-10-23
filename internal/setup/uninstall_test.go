package setup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLedgerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	l, err := loadLedger(path)
	if err != nil || len(l.Installed) != 0 {
		t.Fatalf("missing ledger should load empty: %v %+v", err, l)
	}
	l.add(Record{ID: "uv", Title: "uv", Undo: Undo{Method: "brew", Package: "uv"}})
	l.add(Record{ID: "python", Title: "Python", Undo: Undo{Method: "uv-python", Package: "3.13", Via: "uv"}})
	l.add(Record{ID: "uv", Title: "uv again", Undo: Undo{Method: "brew", Package: "uv"}}) // replaces
	l.addProfileLines("/home/me/.zshrc", []string{"a", "b"})
	l.addProfileLines("/home/me/.zshrc", []string{"b", "c"})
	if err := l.save(); err != nil {
		t.Fatal(err)
	}
	got, err := loadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Installed) != 2 || got.Installed[1].Title != "uv again" || got.Installed[0].Undo.Via != "uv" {
		t.Errorf("Installed = %+v", got.Installed)
	}
	if !reflect.DeepEqual(got.Env.ProfileLines, []string{"a", "b", "c"}) {
		t.Errorf("ProfileLines = %v", got.Env.ProfileLines)
	}
}

func TestSafeToDelete(t *testing.T) {
	// Real temp paths, so the test works with Windows paths too.
	home := filepath.Join(t.TempDir(), "me")
	sep := string(filepath.Separator)
	tests := map[string]bool{
		filepath.Join(home, ".local", "bin", "uv"): true,
		home:                              false, // never the home folder itself
		home + sep:                        false,
		home + sep + ".." + sep + "other": false,
		home + "ow" + sep + "x":           false,
		filepath.Join(t.TempDir(), "uv"):  false,
		filepath.Join("relative", "path"): false,
	}
	for p, want := range tests {
		if got := safeToDelete(p, home); got != want {
			t.Errorf("safeToDelete(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestUndoActions(t *testing.T) {
	e := &Env{Platform: Platform{OS: "linux", Home: "/home/me"}}
	cmd := func(r Record) string {
		a := e.undoAction(r)
		if len(a.Commands) != 1 {
			t.Fatalf("%s: commands %+v", r.Undo.Method, a)
		}
		return a.Commands[0].String()
	}
	tests := []struct {
		u    Undo
		want string
	}{
		{Undo{Method: "brew", Package: "uv"}, "brew uninstall uv"},
		{Undo{Method: "cask", Package: "zulu@17"}, "brew uninstall --cask zulu@17"},
		{Undo{Method: "apt", Package: "git"}, "sudo apt-get remove git"},
		{Undo{Method: "snap", Package: "code"}, "sudo snap remove code"},
		{Undo{Method: "winget", Package: "Git.Git"}, "winget uninstall --id Git.Git --exact --source winget --disable-interactivity"},
		{Undo{Method: "mise-tool", Package: "node@lts"}, "mise unuse --global node@lts"},
		{Undo{Method: "uv-python", Package: "3.13"}, "uv python uninstall 3.13"},
		{Undo{Method: "npm-global", Package: "@openai/codex"}, "npm uninstall --global @openai/codex"},
		{Undo{Method: "android-sdk", Tool: "/sdk/sdkmanager", Packages: []string{"platform-tools", "platforms;android-36"}},
			`/sdk/sdkmanager --uninstall platform-tools "platforms;android-36"`},
		{Undo{Method: "android-avd", Tool: "/sdk/avdmanager", Package: "StackPilot_API_36"}, "/sdk/avdmanager delete avd --name StackPilot_API_36"},
	}
	for _, tt := range tests {
		if got := cmd(Record{Undo: tt.u}); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.u.Method, got, tt.want)
		}
	}

	home := t.TempDir()
	inside := filepath.Join(home, ".local", "bin", "claude")
	outside := filepath.Join(t.TempDir(), "sdk", "cmdline-tools", "latest")
	fe := &Env{Platform: Platform{OS: runtime.GOOS, Home: home}}
	files := fe.undoAction(Record{Undo: Undo{Method: "files", Paths: []string{inside, outside}}})
	if !reflect.DeepEqual(files.Remove, []string{inside}) || !strings.Contains(files.Manual, outside) {
		t.Errorf("files action = %+v", files)
	}
	if a := e.undoAction(Record{Title: "Homebrew", Undo: Undo{Method: "manual", Package: "run this"}}); len(a.Commands) != 0 || !strings.Contains(a.Manual, "run this") {
		t.Errorf("manual action = %+v", a)
	}
	if a := e.undoAction(Record{Undo: Undo{Method: "android-sdk", Tool: "/sdk/sdkmanager"}}); len(a.Commands) != 0 {
		t.Errorf("android-sdk with no packages should do nothing, got %+v", a)
	}
}

func TestWithDependents(t *testing.T) {
	l := &Ledger{Installed: []Record{
		{ID: "mise", Title: "mise"},
		{ID: "node", Title: "Node.js", Undo: Undo{Via: "mise"}},
		{ID: "codex", Title: "Codex", Undo: Undo{Via: "node"}},
		{ID: "uv", Title: "uv"},
	}}
	got, notes := withDependents(l, []string{"mise"})
	if !reflect.DeepEqual(got, []string{"mise", "node", "codex"}) || len(notes) != 2 {
		t.Errorf("got %v, notes %v", got, notes)
	}
	if got, _ := withDependents(l, []string{"codex"}); !reflect.DeepEqual(got, []string{"codex"}) {
		t.Errorf("removing a dependent shouldn't pull in its tool: %v", got)
	}
}

func TestRemoveFromBlock(t *testing.T) {
	original := "alias ll='ls -l'\n"
	lines := []string{`export A="1"`, `export B="2"`}
	withBlock, _ := mergeBlock(original, lines)
	withTail := withBlock + "export EDITOR=vim\n"

	partial, removed := removeFromBlock(withTail, []string{`export A="1"`})
	if !reflect.DeepEqual(removed, []string{`export A="1"`}) || strings.Contains(partial, `A="1"`) ||
		!strings.Contains(partial, blockStart+"\n"+`export B="2"`+"\n"+blockEnd) || !strings.HasSuffix(partial, "export EDITOR=vim\n") {
		t.Errorf("partial removal:\n%s", partial)
	}

	// Removing everything restores the file exactly.
	all, _ := removeFromBlock(withTail, lines)
	if all != original+"export EDITOR=vim\n" {
		t.Errorf("full removal = %q", all)
	}
	if got, _ := removeFromBlock(withBlock, lines); got != original {
		t.Errorf("full removal without tail = %q, want %q", got, original)
	}

	// Lines outside the block are never removed.
	outside := `export A="1"` + "\n"
	if got, removed := removeFromBlock(outside, lines); got != outside || removed != nil {
		t.Errorf("touched lines outside the block: %q", got)
	}
}

func TestSetupRecordsOnlyWhatItInstalls(t *testing.T) {
	dir := t.TempDir()
	l, _ := loadLedger(filepath.Join(dir, "state.json"))
	e := newEnv(context.Background(), Platform{OS: runtime.GOOS, Home: dir}, Config{}, io.Discard)
	e.ledger = l

	installed := map[string]bool{"preexisting": true}
	done := func(id string) func(*Env) (bool, string) {
		return func(*Env) (bool, string) { return installed[id], "" }
	}
	ok := []Command{{Args: []string{"go", "version"}}}
	cat := map[string]*Step{
		"preexisting": {ID: "preexisting", Title: "Already here", Done: done("preexisting"), Commands: ok, Undo: Undo{Method: "brew", Package: "x"}},
		"new":         {ID: "new", Title: "New tool", Done: done("new"), Commands: ok, Undo: Undo{Method: "brew", Package: "new"}},
		"licenses":    {ID: "licenses", Title: "Licenses", Done: done("licenses"), Commands: ok},
	}
	plan := resolve(cat, []string{"preexisting", "new", "licenses"}, func(s *Step) (bool, string) { return s.Done(e) })
	if failed, err := execute(e, cat, plan, nil, false); err != nil || len(failed) != 0 {
		t.Fatalf("execute: %v %v", failed, err)
	}
	saved, _ := loadLedger(l.path)
	if len(saved.Installed) != 1 || saved.Installed[0].ID != "new" {
		t.Errorf("recorded %+v, want only the newly installed tool", saved.Installed)
	}
}

// End to end on a temp home: files, a manual record and the shell profile.
// Package-manager commands are covered by the dry-run test below.
func TestUninstallRemovesFilesAndProfile(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin", "claude")
	data := filepath.Join(home, ".local", "share", "claude")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(data, "versions"), 0o755)
	keep := filepath.Join(home, ".claude", "settings.json") // user settings
	os.MkdirAll(filepath.Dir(keep), 0o755)
	os.WriteFile(keep, []byte("{}"), 0o644)

	profile := filepath.Join(home, ".zshrc")
	lines := []string{`export PATH="$HOME/.local/bin:$PATH"`}
	os.WriteFile(profile, []byte("echo hi\n"), 0o644)
	writeProfile(profile, lines)

	l, _ := loadLedger(filepath.Join(home, "state.json"))
	l.add(Record{ID: "homebrew", Title: "Homebrew", Undo: Undo{Method: "manual", Package: "uninstall.sh"}, InstalledAt: time.Now()})
	l.add(Record{ID: "claude-code", Title: "Claude Code", Undo: Undo{Method: "files", Paths: []string{bin, data}}, InstalledAt: time.Now()})
	l.addProfileLines(profile, lines)
	l.save()

	var out bytes.Buffer
	p := Platform{OS: "linux", Arch: "amd64", Home: home}
	err := uninstall(context.Background(), p, l, UninstallConfig{All: true, Yes: true}, NewPrompter(strings.NewReader(""), &out), &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if exists(bin) || exists(data) {
		t.Error("Claude Code files were not deleted")
	}
	if !exists(keep) {
		t.Error("user settings in ~/.claude were deleted")
	}
	if b, _ := os.ReadFile(profile); string(b) != "echo hi\n" {
		t.Errorf("profile = %q, want original content", b)
	}
	if !strings.Contains(out.String(), "uninstall.sh") {
		t.Errorf("Homebrew manual instructions not shown:\n%s", out.String())
	}
	saved, _ := loadLedger(l.path)
	if len(saved.Installed) != 0 || !saved.Env.empty() {
		t.Errorf("ledger not cleared: %+v", saved)
	}
}

func TestUninstallDryRunPlan(t *testing.T) {
	home := t.TempDir()
	l, _ := loadLedger(filepath.Join(home, "state.json"))
	for _, r := range []Record{
		{ID: "uv", Title: "uv", Undo: Undo{Method: "brew", Package: "uv"}},
		{ID: "python", Title: "Python 3.13", Undo: Undo{Method: "uv-python", Package: "3.13", Via: "uv"}},
		{ID: "vscode", Title: "Visual Studio Code", Undo: Undo{Method: "cask", Package: "visual-studio-code"}},
	} {
		l.add(r)
	}
	l.save()

	var out bytes.Buffer
	p := Platform{OS: "darwin", Arch: "arm64", Home: home}
	// Interactive: pick only uv; Python must come along and go first.
	err := uninstall(context.Background(), p, l, UninstallConfig{DryRun: true}, NewPrompter(strings.NewReader("1\n"), &out), &out)
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	py, uv := strings.Index(s, "$ uv python uninstall 3.13"), strings.Index(s, "$ brew uninstall uv")
	if py < 0 || uv < 0 || py > uv {
		t.Errorf("want Python removed before uv:\n%s", s)
	}
	if strings.Contains(s, "visual-studio-code") {
		t.Errorf("VS Code was not selected:\n%s", s)
	}
	if !strings.Contains(s, "Also removing Python 3.13") || !strings.Contains(s, "Dry run") {
		t.Errorf("missing note or dry-run line:\n%s", s)
	}
	if saved, _ := loadLedger(l.path); len(saved.Installed) != 3 {
		t.Error("dry run changed the ledger")
	}
}

func TestUninstallRejectsUnknownName(t *testing.T) {
	home := t.TempDir()
	l, _ := loadLedger(filepath.Join(home, "state.json"))
	l.add(Record{ID: "uv", Title: "uv", Undo: Undo{Method: "brew", Package: "uv"}})
	err := uninstall(context.Background(), Platform{OS: "darwin", Home: home}, l,
		UninstallConfig{IDs: []string{"git"}, Yes: true}, NewPrompter(strings.NewReader(""), io.Discard), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "wasn't installed by StackPilot") {
		t.Errorf("err = %v", err)
	}
}
