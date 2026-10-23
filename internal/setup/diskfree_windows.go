//go:build windows

package setup

import "golang.org/x/sys/windows"

// freeBytes returns the space available to the current user at path.
func freeBytes(path string) (uint64, bool) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, false
	}
	return free, true
}
