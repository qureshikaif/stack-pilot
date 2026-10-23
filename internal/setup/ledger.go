package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Undo describes how to reverse a step. It is saved to the state file only
// when setup actually installs something, so uninstall never touches tools
// that were on the machine before.
type Undo struct {
	Method   string   `json:"method"` // brew, cask, apt, snap, winget, files, mise-tool, uv-python, npm-global, android-sdk, android-avd, manual
	Package  string   `json:"package,omitempty"`
	Packages []string `json:"packages,omitempty"`
	Paths    []string `json:"paths,omitempty"` // files or directories to delete
	Tool     string   `json:"tool,omitempty"`  // executable used to uninstall (sdkmanager, avdmanager)
	// Via is the step whose tool is needed to undo this one (Python needs
	// uv), so it must be removed first.
	Via string `json:"via,omitempty"`
}

// Record is one thing setup installed.
type Record struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Undo        Undo      `json:"undo"`
	InstalledAt time.Time `json:"installedAt"`
}

// EnvRecord lists environment changes setup made.
type EnvRecord struct {
	Profile      string   `json:"profile,omitempty"`
	ProfileLines []string `json:"profileLines,omitempty"`
	WinVars      []Var    `json:"winVars,omitempty"`
	WinPaths     []string `json:"winPaths,omitempty"`
}

func (r EnvRecord) empty() bool {
	return len(r.ProfileLines) == 0 && len(r.WinVars) == 0 && len(r.WinPaths) == 0
}

type Ledger struct {
	Version   int       `json:"version"`
	Installed []Record  `json:"installed"`
	Env       EnvRecord `json:"env"`

	path string
}

// LedgerPath is where setup records what it installed.
func LedgerPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stackpilot", "state.json"), nil
}

func loadLedger(path string) (*Ledger, error) {
	s := &Ledger{Version: 1, path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	s.path = path
	return s, nil
}

// save writes the state atomically so an interrupted run can't corrupt it.
func (s *Ledger) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// add records an install, replacing an older record for the same step.
func (s *Ledger) add(r Record) {
	s.remove(r.ID)
	s.Installed = append(s.Installed, r)
}

func (s *Ledger) remove(id string) {
	kept := s.Installed[:0]
	for _, r := range s.Installed {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	s.Installed = kept
}

func (s *Ledger) addProfileLines(profile string, lines []string) {
	s.Env.Profile = profile
	for _, l := range lines {
		if !contains(s.Env.ProfileLines, l) {
			s.Env.ProfileLines = append(s.Env.ProfileLines, l)
		}
	}
}

func (s *Ledger) addWinEnv(vars []Var, paths []string) {
	for _, v := range vars {
		replaced := false
		for i := range s.Env.WinVars {
			if s.Env.WinVars[i].Name == v.Name {
				s.Env.WinVars[i], replaced = v, true
			}
		}
		if !replaced {
			s.Env.WinVars = append(s.Env.WinVars, v)
		}
	}
	for _, p := range paths {
		if !contains(s.Env.WinPaths, p) {
			s.Env.WinPaths = append(s.Env.WinPaths, p)
		}
	}
}
