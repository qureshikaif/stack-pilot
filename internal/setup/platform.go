package setup

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Platform describes the machine being set up.
type Platform struct {
	OS      string // runtime.GOOS
	Arch    string // runtime.GOARCH
	Distro  string // Linux only, from /etc/os-release
	Home    string
	Shell   string // "zsh", "bash", ...
	Profile string // shell startup file we manage, empty if unsupported shell
	Root    bool   // running as root (no sudo needed)

	// Windows only.
	LocalAppData string
	ProgramFiles string
}

func (p Platform) Name() string {
	switch p.OS {
	case "darwin":
		return fmt.Sprintf("macOS (%s)", p.Arch)
	case "windows":
		return fmt.Sprintf("Windows (%s)", p.Arch)
	case "linux":
		if p.Distro != "" {
			return fmt.Sprintf("%s (%s)", p.Distro, p.Arch)
		}
	}
	return fmt.Sprintf("%s (%s)", p.OS, p.Arch)
}

// DetectPlatform inspects the current machine and returns an error if setup
// does not support it yet.
func DetectPlatform() (Platform, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Platform{}, err
	}
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Home: home, Root: os.Geteuid() == 0}

	switch p.OS {
	case "darwin":
	case "linux":
		p.Distro = osRelease("PRETTY_NAME")
		if _, err := exec.LookPath("apt-get"); err != nil {
			return p, fmt.Errorf("setup supports Debian/Ubuntu (apt) on Linux; %s is not supported yet", p.Name())
		}
	case "windows":
		// Environment changes go to the user's registry environment, not a
		// shell profile.
		p.Shell = "powershell"
		p.LocalAppData = os.Getenv("LOCALAPPDATA")
		p.ProgramFiles = os.Getenv("ProgramFiles")
		if p.LocalAppData == "" || p.ProgramFiles == "" {
			return p, fmt.Errorf("LOCALAPPDATA or ProgramFiles is not set")
		}
		return p, nil
	default:
		return p, fmt.Errorf("setup does not support %s yet (macOS, Windows and Debian/Ubuntu only); `stackpilot doctor` works everywhere", p.OS)
	}

	p.Shell = filepath.Base(os.Getenv("SHELL"))
	switch p.Shell {
	case "zsh":
		p.Profile = filepath.Join(home, ".zshrc")
	case "bash":
		if p.OS == "darwin" {
			p.Profile = filepath.Join(home, ".bash_profile")
		} else {
			p.Profile = filepath.Join(home, ".bashrc")
		}
	}
	return p, nil
}

func osRelease(key string) string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if k, v, ok := strings.Cut(s.Text(), "="); ok && k == key {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// tilde shortens paths under home for display.
func (p Platform) tilde(path string) string {
	if p.Home != "" && strings.HasPrefix(path, p.Home) {
		return "~" + strings.TrimPrefix(path, p.Home)
	}
	return path
}
