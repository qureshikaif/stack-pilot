//go:build !windows

package setup

import "errors"

var errNoRegistry = errors.New("the Windows registry is only available on Windows")

func persistUserEnv([]Var, []string) (string, []string, error) {
	return "", nil, errNoRegistry
}

func removeUserEnv([]Var, []string) (string, []string, error) { return "", nil, errNoRegistry }

func refreshPath() {}
