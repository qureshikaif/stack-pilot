//go:build windows

package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func openUserEnv() (registry.Key, error) {
	return registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
}

// readUserPath returns the raw (unexpanded) user Path and its registry type.
func readUserPath(k registry.Key) (string, uint32, error) {
	value, typ, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	return value, typ, err
}

func writeUserPath(k registry.Key, value string, typ uint32) error {
	if typ == registry.EXPAND_SZ {
		return k.SetExpandStringValue("Path", value)
	}
	return k.SetStringValue("Path", value)
}

// backupUserEnv saves the current values of names and Path to a file under
// %LOCALAPPDATA%\stackpilot before they are changed.
func backupUserEnv(k registry.Key, action string, names []string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "User environment values (HKCU\\Environment) before stackpilot %s\r\n\r\n", action)
	for _, name := range append(names, "Path") {
		if old, _, err := k.GetStringValue(name); err == nil {
			fmt.Fprintf(&b, "%s=%s\r\n", name, old)
		} else {
			fmt.Fprintf(&b, "%s (not set)\r\n", name)
		}
	}
	dir := filepath.Join(os.Getenv("LOCALAPPDATA"), "stackpilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	backup := filepath.Join(dir, "env-backup-"+time.Now().Format("20060102-150405.000")+".txt")
	return backup, os.WriteFile(backup, []byte(b.String()), 0o600)
}

// persistUserEnv saves variables and PATH entries to HKCU\Environment, the
// same place the "Edit environment variables for your account" dialog writes.
// It avoids setx, which truncates PATH at 1024 characters. Previous values
// are written to a backup file first. It returns the PATH entries added, as
// written to the registry.
func persistUserEnv(vars []Var, paths []string) (backup string, added []string, err error) {
	k, err := openUserEnv()
	if err != nil {
		return "", nil, err
	}
	defer k.Close()

	oldPath, typ, err := readUserPath(k)
	if err != nil {
		return "", nil, err
	}
	if backup, err = backupUserEnv(k, "setup", varNames(vars)); err != nil {
		return "", nil, err
	}

	for _, v := range vars {
		if err := k.SetStringValue(v.Name, v.Value); err != nil {
			return backup, nil, err
		}
	}

	if len(paths) > 0 {
		known := map[string]string{"LOCALAPPDATA": os.Getenv("LOCALAPPDATA"), "USERPROFILE": os.Getenv("USERPROFILE")}
		entries := make([]string, len(paths))
		for i, p := range paths {
			entries[i] = p
			if typ == registry.EXPAND_SZ {
				entries[i] = unexpandWin(p, known)
			}
		}
		expand := func(s string) string {
			if x, err := registry.ExpandString(s); err == nil {
				return x
			}
			return s
		}
		var newPath string
		newPath, added = mergeWinPath(oldPath, entries, expand)
		if len(added) > 0 {
			if err := writeUserPath(k, newPath, typ); err != nil {
				return backup, nil, err
			}
		}
	}

	broadcastEnvChange()
	return backup, added, nil
}

// removeUserEnv reverses persistUserEnv: it deletes variables that still have
// the value setup gave them and removes the PATH entries it added. Variables
// changed since are kept and returned.
func removeUserEnv(vars []Var, paths []string) (backup string, kept []string, err error) {
	k, err := openUserEnv()
	if err != nil {
		return "", nil, err
	}
	defer k.Close()

	if backup, err = backupUserEnv(k, "uninstall", varNames(vars)); err != nil {
		return "", nil, err
	}
	for _, v := range vars {
		current, _, err := k.GetStringValue(v.Name)
		switch {
		case errors.Is(err, registry.ErrNotExist):
		case err != nil:
			return backup, kept, err
		case current != v.Value:
			kept = append(kept, v.Name)
		default:
			if err := k.DeleteValue(v.Name); err != nil {
				return backup, kept, err
			}
		}
	}
	if len(paths) > 0 {
		oldPath, typ, err := readUserPath(k)
		if err != nil {
			return backup, kept, err
		}
		if newPath, removed := removeWinPath(oldPath, paths); len(removed) > 0 {
			if err := writeUserPath(k, newPath, typ); err != nil {
				return backup, kept, err
			}
		}
	}
	broadcastEnvChange()
	return backup, kept, nil
}

// broadcastEnvChange tells Explorer and other programs to reload the
// environment, so new terminals see the change without signing out.
func broadcastEnvChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	env, _ := windows.UTF16PtrFromString("Environment")
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}

// refreshPath adds PATH entries that installers wrote to the registry to this
// process's PATH, so tools installed by winget are found by later steps.
func refreshPath() {
	current := filepath.SplitList(os.Getenv("PATH"))
	have := map[string]bool{}
	for _, d := range current {
		have[pathKey(d)] = true
	}
	sources := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
		{registry.CURRENT_USER, `Environment`},
	}
	for _, src := range sources {
		k, err := registry.OpenKey(src.root, src.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		value, _, err := k.GetStringValue("Path")
		k.Close()
		if err != nil {
			continue
		}
		for _, d := range strings.Split(value, ";") {
			if x, err := registry.ExpandString(d); err == nil {
				d = x
			}
			if d = strings.TrimSpace(d); d != "" && !have[pathKey(d)] {
				have[pathKey(d)] = true
				current = append(current, d)
			}
		}
	}
	os.Setenv("PATH", strings.Join(current, string(os.PathListSeparator)))
}
