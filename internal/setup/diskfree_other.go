//go:build !unix && !windows

package setup

func freeBytes(string) (uint64, bool) { return 0, false }
