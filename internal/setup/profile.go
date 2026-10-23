package setup

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	blockStart = "# >>> stackpilot >>>"
	blockEnd   = "# <<< stackpilot <<<"
)

// missingEnv returns the activations applied during setup that the user's
// environment didn't already have before we started.
func (e *Env) missingEnv() (vars []Var, paths []string) {
	for _, v := range e.applied.Vars {
		if e.origEnv[v.Name] != v.Value {
			vars = append(vars, v)
		}
	}
	for _, dir := range e.applied.Path {
		if !e.origPath[pathKey(dir)] {
			paths = append(paths, dir)
		}
	}
	return vars, paths
}

// profileLines formats missingEnv as shell profile lines.
func (e *Env) profileLines() []string {
	vars, paths := e.missingEnv()
	var lines []string
	for _, v := range vars {
		lines = append(lines, fmt.Sprintf(`export %s="%s"`, v.Name, e.shellPath(v.Value)))
	}
	for _, dir := range paths {
		lines = append(lines, fmt.Sprintf(`export PATH="%s:$PATH"`, e.shellPath(dir)))
	}
	return lines
}

// envLines formats missingEnv for display on the current platform.
func (e *Env) envLines() []string {
	if e.OS == "windows" {
		return windowsLines(e.missingEnv())
	}
	return e.profileLines()
}

func (e *Env) shellPath(p string) string {
	if e.Home != "" && strings.HasPrefix(p, e.Home+"/") {
		return "$HOME" + strings.TrimPrefix(p, e.Home)
	}
	return p
}

// mergeBlock adds lines to the stackpilot block in a shell profile, creating
// the block if needed. Lines already in the block are kept. It returns the new
// content and the lines that were actually added.
func mergeBlock(content string, lines []string) (string, []string) {
	var existing []string
	before, rest, hasBlock := strings.Cut(content, blockStart+"\n")
	after := ""
	if hasBlock {
		body, tail, ok := strings.Cut(rest, blockEnd)
		if !ok {
			// Unterminated block: treat everything after the marker as ours.
			body, tail = rest, ""
		}
		after = strings.TrimPrefix(tail, "\n")
		for _, l := range strings.Split(body, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				existing = append(existing, l)
			}
		}
	} else {
		before = content
	}

	var added []string
	for _, l := range lines {
		if !contains(existing, l) && !contains(added, l) {
			added = append(added, l)
		}
	}
	if len(added) == 0 {
		return content, nil
	}

	all := append(existing, added...)
	block := blockStart + "\n" + strings.Join(all, "\n") + "\n" + blockEnd + "\n"
	if !hasBlock {
		if before != "" && !strings.HasSuffix(before, "\n") {
			before += "\n"
		}
		if before != "" {
			before += "\n"
		}
	}
	return before + block + after, added
}

// writeProfile merges lines into path, backing up the original first.
// It returns the lines added and the backup path (empty if nothing changed).
func writeProfile(path string, lines []string) ([]string, string, error) {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	updated, added := mergeBlock(string(old), lines)
	if len(added) == 0 {
		return nil, "", nil
	}
	backup := ""
	if len(old) > 0 {
		backup = path + ".stackpilot-backup-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return nil, "", err
		}
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return added, backup, os.WriteFile(path, []byte(updated), mode)
}

// removeFromBlock deletes lines from the stackpilot block, and the block
// itself once it is empty. Lines outside the block are never touched.
func removeFromBlock(content string, lines []string) (string, []string) {
	before, rest, ok := strings.Cut(content, blockStart+"\n")
	if !ok {
		return content, nil
	}
	body, after, ok := strings.Cut(rest, blockEnd)
	if !ok {
		body, after = rest, ""
	}
	after = strings.TrimPrefix(after, "\n")

	var kept, removed []string
	for _, l := range strings.Split(body, "\n") {
		switch t := strings.TrimSpace(l); {
		case t == "":
		case contains(lines, t):
			removed = append(removed, t)
		default:
			kept = append(kept, t)
		}
	}
	if len(removed) == 0 {
		return content, nil
	}
	if len(kept) == 0 {
		// Drop the blank line mergeBlock put before the block.
		return strings.TrimSuffix(before, "\n") + after, removed
	}
	return before + blockStart + "\n" + strings.Join(kept, "\n") + "\n" + blockEnd + "\n" + after, removed
}

// unwriteProfile removes lines from the profile's stackpilot block after
// backing the file up. It returns the lines removed and the backup path.
func unwriteProfile(path string, lines []string) ([]string, string, error) {
	old, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	updated, removed := removeFromBlock(string(old), lines)
	if len(removed) == 0 {
		return nil, "", nil
	}
	backup := path + ".stackpilot-backup-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(backup, old, 0o600); err != nil {
		return nil, "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	return removed, backup, os.WriteFile(path, []byte(updated), info.Mode().Perm())
}
