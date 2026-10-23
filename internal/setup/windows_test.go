//go:build windows

package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// SDK package names contain ";", which cmd.exe treats as a separator unless
// quoted. This runs a real .bat file to check the arguments arrive intact.
func TestBatArgumentsKeepSemicolons(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "echoargs.bat")
	script := "@echo off\r\necho [%~1]\r\necho [%~2]\r\necho [%~3]\r\n"
	if err := os.WriteFile(bat, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e := &Env{Platform: Platform{OS: "windows"}, ctx: context.Background(), out: &out, ran: map[string]bool{}}
	err := e.exec(Command{Args: []string{bat, "--install", "platforms;android-36", "system-images;android-36;google_apis;x86_64"}, Input: "\n"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	want := "[--install]\r\n[platforms;android-36]\r\n[system-images;android-36;google_apis;x86_64]\r\n"
	if out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestOkExitCodes(t *testing.T) {
	var out bytes.Buffer
	e := &Env{Platform: Platform{OS: "windows"}, ctx: context.Background(), out: &out, ran: map[string]bool{}}
	// PowerShell's exit takes a signed int; -1978335135 is 0x8A150061.
	c := Command{Script: "exit -1978335135", Input: "\n", OkExit: wingetAlreadyInstalled}
	if err := e.exec(c); err != nil {
		t.Errorf("winget already-installed code should be success: %v", err)
	}
	c.OkExit = nil
	if err := e.exec(c); err == nil {
		t.Error("expected failure without OkExit")
	}
}

// Writes to the real HKCU\Environment and restores it afterwards. Opt in with
// STACKPILOT_REGISTRY_TESTS=1 (CI does this); don't run it on a machine you
// care about.
func TestPersistUserEnv(t *testing.T) {
	if os.Getenv("STACKPILOT_REGISTRY_TESTS") == "" {
		t.Skip("set STACKPILOT_REGISTRY_TESTS=1 to run")
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	oldPath, oldType, pathErr := k.GetStringValue("Path")
	t.Cleanup(func() {
		k.DeleteValue("STACKPILOT_TEST_VAR")
		switch {
		case pathErr != nil:
			k.DeleteValue("Path")
		case oldType == registry.EXPAND_SZ:
			k.SetExpandStringValue("Path", oldPath)
		default:
			k.SetStringValue("Path", oldPath)
		}
	})

	entry := filepath.Join(os.Getenv("LOCALAPPDATA"), "stackpilot-test", "bin")
	backup, added, err := persistUserEnv([]Var{{"STACKPILOT_TEST_VAR", `C:\sdk`}}, []string{entry})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || !strings.HasPrefix(added[0], "%LOCALAPPDATA%") {
		t.Errorf("added = %v", added)
	}
	defer os.Remove(backup)
	if b, _ := os.ReadFile(backup); !strings.Contains(string(b), "STACKPILOT_TEST_VAR (not set)") {
		t.Errorf("backup missing previous value:\n%s", b)
	}
	if v, _, _ := k.GetStringValue("STACKPILOT_TEST_VAR"); v != `C:\sdk` {
		t.Errorf("var = %q", v)
	}
	path, typ, err := k.GetStringValue("Path")
	if err != nil || !strings.Contains(path, `%LOCALAPPDATA%\stackpilot-test\bin`) {
		t.Errorf("Path = %q (%v), want the entry as %%LOCALAPPDATA%%", path, err)
	}
	if pathErr == nil && (typ != oldType || !strings.HasPrefix(path, strings.TrimRight(oldPath, ";"))) {
		t.Errorf("existing Path not preserved: type %d→%d\nold %q\nnew %q", oldType, typ, oldPath, path)
	}

	// A second run adds nothing.
	persistUserEnv(nil, []string{entry})
	if again, _, _ := k.GetStringValue("Path"); again != path {
		t.Errorf("second run changed Path:\n%q\n%q", path, again)
	}

	// refreshPath picks the new entry up for this process.
	refreshPath()
	if !strings.Contains(strings.ToLower(os.Getenv("PATH")), strings.ToLower(entry)) {
		t.Error("refreshPath did not add the new entry")
	}

	// Uninstall removes exactly what was added and restores the old Path.
	backup2, kept, err := removeUserEnv([]Var{{"STACKPILOT_TEST_VAR", `C:\sdk`}}, added)
	if err != nil || len(kept) != 0 {
		t.Fatalf("removeUserEnv: kept=%v err=%v", kept, err)
	}
	defer os.Remove(backup2)
	if _, _, err := k.GetStringValue("STACKPILOT_TEST_VAR"); !errors.Is(err, registry.ErrNotExist) {
		t.Errorf("var still set: %v", err)
	}
	if after, _, _ := k.GetStringValue("Path"); pathErr == nil && after != strings.TrimRight(oldPath, ";") && after != oldPath {
		t.Errorf("Path not restored:\nold %q\nnow %q", oldPath, after)
	}

	// A variable the user changed since setup is kept.
	k.SetStringValue("STACKPILOT_TEST_VAR", `D:\mine`)
	if _, kept, _ := removeUserEnv([]Var{{"STACKPILOT_TEST_VAR", `C:\sdk`}}, nil); len(kept) != 1 {
		t.Errorf("changed variable should be kept, kept=%v", kept)
	}
}
