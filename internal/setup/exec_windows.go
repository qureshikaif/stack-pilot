//go:build windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// prepareCmd runs .bat/.cmd files (sdkmanager.bat, npm.cmd) through cmd.exe
// with every argument quoted. Without quotes cmd.exe treats ";" as a
// separator, which splits SDK package names like "platforms;android-36".
func prepareCmd(cmd *exec.Cmd, args []string) {
	ext := strings.ToLower(filepath.Ext(cmd.Path))
	if ext != ".bat" && ext != ".cmd" {
		return
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = `C:\Windows\System32\cmd.exe`
	}
	quoted := make([]string, len(args))
	quoted[0] = `"` + cmd.Path + `"`
	for i, a := range args[1:] {
		quoted[i+1] = `"` + strings.ReplaceAll(a, `"`, `""`) + `"`
	}
	// /s strips the outer quotes and runs the rest verbatim.
	cmd.Path = comspec
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /d /s /c "` + strings.Join(quoted, " ") + `"`,
	}
}
