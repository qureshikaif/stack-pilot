//go:build !windows

package setup

import "os/exec"

func prepareCmd(*exec.Cmd, []string) {}
