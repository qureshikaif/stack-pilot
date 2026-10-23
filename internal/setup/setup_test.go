package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	never := func(*Env) (bool, string) { return false, "" }
	cmd := []Command{{Args: []string{"true"}}}
	cat := map[string]*Step{
		"homebrew": {ID: "homebrew", Commands: cmd},
		"mise":     {ID: "mise", Needs: []string{"homebrew"}, Commands: cmd},
		"node":     {ID: "node", Needs: []string{"mise"}, Commands: cmd},
		"jdk":      {ID: "jdk", Needs: []string{"homebrew"}, Commands: cmd},
		"cursor":   {ID: "cursor", Manual: "download it"},
	}
	for _, s := range cat {
		s.Done = never
	}
	installed := map[string]bool{}
	done := func(s *Step) (bool, string) { return installed[s.ID], "" }

	ids := func(plan []PlanItem) []string {
		var out []string
		for _, it := range plan {
			out = append(out, it.Step.ID)
		}
		return out
	}

	plan := resolve(cat, []string{"node", "jdk", "watchman", "cursor"}, done)
	if got, want := ids(plan), []string{"homebrew", "mise", "node", "jdk", "cursor"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	if plan[4].State != StateManual {
		t.Errorf("cursor state = %v, want manual", plan[4].State)
	}

	// Node already present (say, from nvm): mise and Homebrew aren't pulled in.
	installed["node"] = true
	plan = resolve(cat, []string{"node"}, done)
	if got := ids(plan); !reflect.DeepEqual(got, []string{"node"}) || plan[0].State != StateDone {
		t.Errorf("plan = %v (state %v), want only done node", got, plan[0].State)
	}
}

func TestCatalogIntegrity(t *testing.T) {
	yes := true
	cfg := Config{AndroidAPI: "36", Python: "3.13", Emulator: &yes, AndroidStudio: &yes}
	for _, p := range []Platform{
		{OS: "darwin", Arch: "arm64", Home: "/Users/me"},
		{OS: "linux", Arch: "amd64", Home: "/home/me"},
		{OS: "windows", Arch: "amd64", Home: `C:\Users\me`, LocalAppData: `C:\Users\me\AppData\Local`, ProgramFiles: `C:\Program Files`},
	} {
		e := newEnv(context.Background(), p, cfg, io.Discard)
		e.AndroidHome = defaultAndroidHome(p)
		cat := e.catalog()

		var wanted []string
		for _, tpl := range Templates {
			wanted = append(wanted, tpl.Steps...)
		}
		wanted = append(wanted, androidSteps(cfg)...)
		wanted = append(wanted, "android-licenses")
		for _, c := range append(Editors, AITools...) {
			wanted = append(wanted, c.Key)
		}
		for _, id := range wanted {
			if _, ok := cat[id]; !ok && !(id == "watchman" && p.OS != "darwin") {
				t.Errorf("%s: step %q used by a template is missing", p.OS, id)
			}
		}
		for id, s := range cat {
			if s.ID != id || s.Title == "" || s.Done == nil {
				t.Errorf("%s: step %q is incomplete", p.OS, id)
			}
			if !s.automated() && s.Manual == "" {
				t.Errorf("%s: step %q has no install method or manual instructions", p.OS, id)
			}
			for _, n := range s.Needs {
				if _, ok := cat[n]; !ok {
					t.Errorf("%s: step %q needs unknown step %q", p.OS, id, n)
				}
			}
		}
	}
}

func TestSDKPackages(t *testing.T) {
	no := false
	e := &Env{Platform: Platform{Arch: "amd64"}, Config: Config{AndroidAPI: "35", Emulator: &no}, AndroidHome: "/sdk"}
	if got, want := e.sdkPackages(), []string{"platform-tools", "platforms;android-35", "build-tools;35.0.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sdkPackages = %v", got)
	}
	if got := e.sdkPackageDir("platforms;android-35"); got != filepath.Join("/sdk", "platforms", "android-35") {
		t.Errorf("sdkPackageDir = %s", got)
	}
	e.Arch = "arm64"
	if got := e.systemImage(); got != "system-images;android-35;google_apis;arm64-v8a" {
		t.Errorf("systemImage = %s", got)
	}
}

const repoFixture = `<?xml version="1.0"?>
<sdk:sdk-repository xmlns:sdk="http://schemas.android.com/sdk/android/repo/repository2/03">
  <remotePackage path="cmdline-tools;9.0">
    <archives><archive><complete><size>1</size><checksum type="sha1">old</checksum><url>old.zip</url></complete><host-os>linux</host-os></archive></archives>
  </remotePackage>
  <remotePackage path="cmdline-tools;latest">
    <archives>
      <archive><complete><size>10</size><checksum type="sha1">aaa</checksum><url>linux.zip</url></complete><host-os>linux</host-os></archive>
      <archive><complete><size>20</size><checksum type="sha1">bbb</checksum><url>mac-x64.zip</url></complete><host-os>macosx</host-os><host-arch>x64</host-arch></archive>
      <archive><complete><size>30</size><checksum type="sha1">ccc</checksum><url>mac-arm.zip</url></complete><host-os>macosx</host-os><host-arch>aarch64</host-arch></archive>
    </archives>
  </remotePackage>
</sdk:sdk-repository>`

func TestPickCmdlineTools(t *testing.T) {
	tests := []struct{ goos, goarch, url string }{
		{"linux", "amd64", "linux.zip"},
		{"linux", "arm64", "linux.zip"},
		{"darwin", "arm64", "mac-arm.zip"},
		{"darwin", "amd64", "mac-x64.zip"},
	}
	for _, tt := range tests {
		a, err := pickCmdlineTools(strings.NewReader(repoFixture), tt.goos, tt.goarch)
		if err != nil || a.URL != tt.url {
			t.Errorf("%s/%s: got %q, %v; want %q", tt.goos, tt.goarch, a.URL, err, tt.url)
		}
	}
	if _, err := pickCmdlineTools(strings.NewReader(repoFixture), "windows", "amd64"); err == nil {
		t.Error("expected error for windows")
	}
}

func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range entries {
		h := &zip.FileHeader{Name: name}
		h.SetMode(0o755)
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte(body))
	}
	w.Close()
	f.Close()
	return path
}

func TestExtractZip(t *testing.T) {
	dest := t.TempDir()
	if err := extractZip(makeZip(t, map[string]string{"cmdline-tools/bin/sdkmanager": "#!/bin/sh"}), dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "cmdline-tools", "bin", "sdkmanager"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit lost: %v", info.Mode())
	}

	if err := extractZip(makeZip(t, map[string]string{"../evil": "x"}), t.TempDir()); err == nil {
		t.Error("expected zip-slip entry to be rejected")
	}
}

func TestMergeBlock(t *testing.T) {
	lines := []string{`export ANDROID_HOME="$HOME/Library/Android/sdk"`, `export PATH="$HOME/x:$PATH"`}

	got, added := mergeBlock("alias ll='ls -l'", lines)
	want := "alias ll='ls -l'\n\n" + blockStart + "\n" + strings.Join(lines, "\n") + "\n" + blockEnd + "\n"
	if got != want || len(added) != 2 {
		t.Errorf("new block:\n%s\nwant:\n%s", got, want)
	}

	// Re-running with the same lines changes nothing.
	again, added := mergeBlock(got, lines)
	if again != got || added != nil {
		t.Errorf("rerun changed profile:\n%s", again)
	}

	// New lines are appended inside the existing block, content after it kept.
	withTail := got + "export EDITOR=vim\n"
	merged, added := mergeBlock(withTail, []string{`export JAVA_HOME="/jdk"`})
	if !reflect.DeepEqual(added, []string{`export JAVA_HOME="/jdk"`}) {
		t.Errorf("added = %v", added)
	}
	if strings.Count(merged, blockStart) != 1 || !strings.HasSuffix(merged, blockEnd+"\nexport EDITOR=vim\n") ||
		!strings.Contains(merged, `export PATH="$HOME/x:$PATH"`+"\n"+`export JAVA_HOME="/jdk"`) {
		t.Errorf("merged:\n%s", merged)
	}
}

func TestProfileLinesSkipsExisting(t *testing.T) {
	e := &Env{
		Platform: Platform{Home: "/Users/me"},
		origEnv:  map[string]string{"JAVA_HOME": "/jdk"},
		origPath: map[string]bool{"/opt/homebrew/bin": true},
		applied: Activation{
			Vars: []Var{{"JAVA_HOME", "/jdk"}, {"ANDROID_HOME", "/Users/me/Library/Android/sdk"}},
			Path: []string{"/opt/homebrew/bin", "/Users/me/Library/Android/sdk/platform-tools"},
		},
	}
	want := []string{
		`export ANDROID_HOME="$HOME/Library/Android/sdk"`,
		`export PATH="$HOME/Library/Android/sdk/platform-tools:$PATH"`,
	}
	if got := e.profileLines(); !reflect.DeepEqual(got, want) {
		t.Errorf("profileLines = %v", got)
	}
}

func TestWriteProfileBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(path, []byte("echo hi\n"), 0o600)
	added, backup, err := writeProfile(path, []string{`export A="1"`})
	if err != nil || len(added) != 1 || backup == "" {
		t.Fatalf("added=%v backup=%q err=%v", added, backup, err)
	}
	if b, _ := os.ReadFile(backup); string(b) != "echo hi\n" {
		t.Errorf("backup content = %q", b)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", info.Mode())
	}
	if added, backup, _ := writeProfile(path, []string{`export A="1"`}); added != nil || backup != "" {
		t.Error("second write should be a no-op")
	}
}

func TestAsk(t *testing.T) {
	// templates: 1,3 (node, react-native); Android Studio: n; emulator: Enter
	// (yes); API: 2 (35); editors: bogus then 1; AI: none.
	input := "1,3\nn\n\n2\n9\n1\n\n"
	var out bytes.Buffer
	cfg, err := ask(Config{}, NewPrompter(strings.NewReader(input), &out))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Templates, []string{"node", "react-native"}) ||
		cfg.androidStudio() || !cfg.emulator() || cfg.AndroidAPI != "35" ||
		!reflect.DeepEqual(cfg.Editors, []string{"vscode"}) || len(cfg.AI) != 0 || cfg.AI == nil {
		t.Errorf("cfg = %+v", cfg)
	}
	if !strings.Contains(out.String(), "not a number between 1 and 2") {
		t.Error("invalid answer was not reported")
	}
}

func TestAskNoInput(t *testing.T) {
	if _, err := ask(Config{}, NewPrompter(strings.NewReader(""), io.Discard)); err != ErrNoInput {
		t.Errorf("err = %v, want ErrNoInput", err)
	}
}

func TestAskSkipsAnsweredFlags(t *testing.T) {
	cfg := Config{Templates: []string{"python"}, Editors: []string{}, AI: []string{"codex"}}
	got, err := ask(cfg, NewPrompter(strings.NewReader(""), io.Discard))
	if err != nil || !reflect.DeepEqual(got, cfg) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestWindowsCatalog(t *testing.T) {
	p := Platform{OS: "windows", Arch: "amd64", Home: `C:\Users\me`, LocalAppData: `C:\Users\me\AppData\Local`, ProgramFiles: `C:\Program Files`}
	e := newEnv(context.Background(), p, Config{AndroidAPI: "36", Python: "3.13"}, io.Discard)
	e.AndroidHome = defaultAndroidHome(p)
	cat := e.catalog()

	for _, id := range []string{"git", "node", "uv", "jdk", "android-studio", "vscode", "cursor", "claude-code", "codex"} {
		s := cat[id]
		if len(s.Commands) != 1 || s.Commands[0].Args[0] != "winget" || !contains(s.Needs, "winget") {
			t.Errorf("%s: want a single winget install needing winget, got %+v", id, s)
			continue
		}
		if !contains(s.Commands[0].Args, "--exact") || len(s.Commands[0].OkExit) == 0 {
			t.Errorf("%s: winget command %v should be exact and tolerate already-installed", id, s.Commands[0])
		}
	}
	if _, ok := cat["mise"]; ok {
		t.Error("mise should not be offered on Windows")
	}
	if cat["winget"].automated() || cat["winget"].Manual == "" {
		t.Error("winget step should be manual with instructions")
	}
	if got := cat["android-avd"].Commands[0].Args[0]; !strings.HasSuffix(got, "avdmanager.bat") {
		t.Errorf("avdmanager = %s, want .bat", got)
	}
	if got := e.sdkmanager(); !strings.HasSuffix(got, "sdkmanager.bat") {
		t.Errorf("sdkmanager = %s, want .bat", got)
	}
}

func TestMergeWinPath(t *testing.T) {
	expand := func(s string) string {
		return strings.ReplaceAll(s, "%LOCALAPPDATA%", `C:\Users\me\AppData\Local`)
	}
	old := `%LOCALAPPDATA%\Microsoft\WindowsApps;C:\Tools\;`
	got, added := mergeWinPath(old, []string{
		`C:\Users\me\AppData\Local\Microsoft\WindowsApps`, // same as the %VAR% entry
		`c:\tools`, // same, different case and trailing slash
		`%LOCALAPPDATA%\Android\Sdk\platform-tools`,
		`%LOCALAPPDATA%\Android\Sdk\platform-tools`, // duplicate in input
	}, expand)
	want := `%LOCALAPPDATA%\Microsoft\WindowsApps;C:\Tools\;%LOCALAPPDATA%\Android\Sdk\platform-tools`
	if got != want || len(added) != 1 {
		t.Errorf("mergeWinPath = %q (added %v)\nwant %q", got, added, want)
	}
}

func TestUnexpandWin(t *testing.T) {
	vars := map[string]string{"LOCALAPPDATA": `C:\Users\me\AppData\Local`, "USERPROFILE": `C:\Users\me`}
	tests := map[string]string{
		`C:\Users\me\AppData\Local\Android\Sdk\emulator`: `%LOCALAPPDATA%\Android\Sdk\emulator`,
		`c:\users\me\.local\bin`:                         `%USERPROFILE%\.local\bin`,
		`C:\Program Files\Git\cmd`:                       `C:\Program Files\Git\cmd`,
		`C:\Users\me2\bin`:                               `C:\Users\me2\bin`,
	}
	for in, want := range tests {
		if got := unexpandWin(in, vars); got != want {
			t.Errorf("unexpandWin(%q) = %q, want %q", in, got, want)
		}
	}
}
