package setup

import (
	"fmt"
	"strings"
)

// mergeWinPath appends entries to a Windows PATH value (";"-separated) unless
// already present, comparing case-insensitively and after expanding
// %VARIABLES%. It returns the new value and the entries added.
func mergeWinPath(old string, entries []string, expand func(string) string) (string, []string) {
	norm := func(s string) string {
		return strings.ToLower(strings.TrimRight(expand(strings.TrimSpace(s)), `\`))
	}
	have := map[string]bool{}
	var parts []string
	for _, p := range strings.Split(old, ";") {
		if strings.TrimSpace(p) == "" {
			continue
		}
		parts = append(parts, p)
		have[norm(p)] = true
	}
	var added []string
	for _, e := range entries {
		if have[norm(e)] {
			continue
		}
		have[norm(e)] = true
		parts = append(parts, e)
		added = append(added, e)
	}
	return strings.Join(parts, ";"), added
}

// removeWinPath drops entries from a Windows PATH value, matching
// case-insensitively and ignoring trailing backslashes.
func removeWinPath(old string, entries []string) (string, []string) {
	norm := func(s string) string { return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), `\`)) }
	drop := map[string]bool{}
	for _, e := range entries {
		drop[norm(e)] = true
	}
	var kept, removed []string
	for _, p := range strings.Split(old, ";") {
		switch {
		case strings.TrimSpace(p) == "":
		case drop[norm(p)]:
			removed = append(removed, p)
		default:
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ";"), removed
}

// unexpandWin rewrites a path under a known folder as %VAR%\..., the way
// Windows itself stores user PATH entries.
func unexpandWin(path string, vars map[string]string) string {
	for _, name := range []string{"LOCALAPPDATA", "USERPROFILE"} {
		if dir := vars[name]; dir != "" && strings.HasPrefix(strings.ToLower(path), strings.ToLower(dir)+`\`) {
			return "%" + name + "%" + path[len(dir):]
		}
	}
	return path
}

// windowsLines describes pending Windows environment changes for display.
func windowsLines(vars []Var, paths []string) []string {
	var lines []string
	for _, v := range vars {
		lines = append(lines, fmt.Sprintf("%s = %s", v.Name, v.Value))
	}
	for _, p := range paths {
		lines = append(lines, "Path += "+p)
	}
	return lines
}
