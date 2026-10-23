package setup

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/qureshikaif/stack-pilot/internal/probe"
)

const jdkVersion = "17"

// Template is a stack the user can pick in the wizard.
type Template struct {
	Key, Label string
	Steps      []string
	Android    bool
}

var Templates = []Template{
	{Key: "node", Label: "Node.js / TypeScript", Steps: []string{"git", "node"}},
	{Key: "python", Label: "Python (uv)", Steps: []string{"git", "uv", "python"}},
	{Key: "react-native", Label: "React Native / Expo (Android)", Android: true,
		Steps: []string{"git", "node", "watchman", "jdk"}},
	{Key: "android", Label: "Native Android (Kotlin / Java)", Android: true,
		Steps: []string{"git", "jdk"}},
}

var Editors = []Choice{
	{Key: "vscode", Label: "Visual Studio Code"},
	{Key: "cursor", Label: "Cursor"},
}

var AITools = []Choice{
	{Key: "claude-code", Label: "Claude Code"},
	{Key: "codex", Label: "Codex CLI"},
}

// androidSteps follow the template steps when any Android template is chosen.
func androidSteps(cfg Config) []string {
	steps := []string{}
	if cfg.androidStudio() {
		steps = append(steps, "android-studio")
	}
	steps = append(steps, "android-cmdline-tools", "android-sdk")
	if cfg.emulator() {
		steps = append(steps, "android-avd")
	}
	return steps
}

// pkg lists the ways to install something; the first one that applies to the
// current platform wins.
type pkg struct {
	brew, cask string // macOS
	apt, snap  string // Linux
	script     string // Linux fallback: an official install script
	winget     string // Windows package ID
	manual     string // instructions when nothing above applies
	// scriptFiles are what the install script creates, deleted on uninstall
	// (from each tool's documented uninstall steps).
	scriptFiles []string
}

// winget exit codes that mean the package is already there.
var wingetAlreadyInstalled = []uint32{
	0x8A15002B, // APPINSTALLER_CLI_ERROR_UPDATE_NOT_APPLICABLE
	0x8A150061, // APPINSTALLER_CLI_ERROR_PACKAGE_ALREADY_INSTALLED
	0x8A15010D, // APPINSTALLER_CLI_ERROR_INSTALL_ALREADY_INSTALLED
}

func (e *Env) install(s *Step, k pkg) {
	switch e.OS {
	case "darwin":
		switch {
		case k.cask != "":
			s.Commands = []Command{{Args: []string{"brew", "install", "--cask", k.cask}}}
			s.Undo = Undo{Method: "cask", Package: k.cask}
		case k.brew != "":
			s.Commands = []Command{{Args: []string{"brew", "install", k.brew}}}
			s.Undo = Undo{Method: "brew", Package: k.brew}
		}
		if s.Commands != nil {
			s.Needs = append(s.Needs, "homebrew")
			return
		}
	case "linux":
		switch {
		case k.apt != "":
			s.Commands = []Command{
				{Args: e.sudo("apt-get", "update"), Once: "apt-update"},
				{Args: e.sudo("apt-get", "install", "-y", k.apt)},
			}
			s.Undo = Undo{Method: "apt", Package: k.apt}
			return
		case k.snap != "" && hasCommand("snap"):
			s.Commands = []Command{{Args: e.sudo("snap", "install", k.snap, "--classic")}}
			s.Undo = Undo{Method: "snap", Package: k.snap}
			return
		case k.script != "":
			s.Commands = []Command{{Script: k.script}}
			s.Needs = append(s.Needs, "curl")
			s.Undo = Undo{Method: "files", Paths: k.scriptFiles}
			return
		}
	case "windows":
		if k.winget != "" {
			s.Commands = []Command{{
				Args: []string{
					"winget", "install", "--id", k.winget, "--exact", "--source", "winget",
					"--accept-package-agreements", "--accept-source-agreements", "--disable-interactivity",
				},
				OkExit: wingetAlreadyInstalled,
			}}
			s.Needs = append(s.Needs, "winget")
			s.Undo = Undo{Method: "winget", Package: k.winget}
			return
		}
	}
	s.Manual = k.manual
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// toolDone reports a tool as done when it runs and prints a version.
func toolDone(name string) func(*Env) (bool, string) {
	return func(e *Env) (bool, string) {
		t := probe.Local(e.ctx, name, e.Home)
		if t.Found && t.Version != "" {
			return true, t.Version + " at " + e.tilde(t.Path)
		}
		return false, ""
	}
}

// pathDone reports done when any of the paths exists. Relative paths come from
// another platform's empty base folder and are ignored.
func pathDone(paths ...string) func(*Env) (bool, string) {
	return func(e *Env) (bool, string) {
		for _, p := range paths {
			if filepath.IsAbs(p) && exists(p) {
				return true, e.tilde(p)
			}
		}
		return false, ""
	}
}

func either(checks ...func(*Env) (bool, string)) func(*Env) (bool, string) {
	return func(e *Env) (bool, string) {
		for _, c := range checks {
			if ok, d := c(e); ok {
				return ok, d
			}
		}
		return false, ""
	}
}

func (e *Env) localBin() string { return filepath.Join(e.Home, ".local", "bin") }

// localBinActivation adds ~/.local/bin, where Linux install scripts put tools.
func localBinActivation(e *Env) Activation {
	if e.OS == "linux" {
		return Activation{Path: []string{e.localBin()}}
	}
	return Activation{}
}

func brewPrefix(p Platform) string {
	if p.Arch == "arm64" {
		return "/opt/homebrew"
	}
	return "/usr/local"
}

// findJDK returns the JAVA_HOME of an installed JDK of jdkVersion.
func findJDK(e *Env) string {
	switch e.OS {
	case "darwin":
		ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/libexec/java_home", "-v", jdkVersion).Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		matches, _ := filepath.Glob("/usr/lib/jvm/java-" + jdkVersion + "-openjdk*")
		for _, m := range matches {
			if exists(filepath.Join(m, "bin", "java")) {
				return m
			}
		}
	case "windows":
		for _, pattern := range [][]string{
			{"Microsoft", "jdk-" + jdkVersion + "*"},
			{"Eclipse Adoptium", "jdk-" + jdkVersion + "*"},
			{"Zulu", "zulu-" + jdkVersion + "*"},
			{"Java", "jdk-" + jdkVersion + "*"},
		} {
			matches, _ := filepath.Glob(filepath.Join(append([]string{e.ProgramFiles}, pattern...)...))
			for _, m := range matches {
				if exists(filepath.Join(m, "bin", "java.exe")) {
					return m
				}
			}
		}
	}
	return ""
}

func (e *Env) sdk(parts ...string) string {
	return filepath.Join(append([]string{e.AndroidHome}, parts...)...)
}

// sdkTool returns the path of a tool under cmdline-tools/latest/bin, which are
// .bat scripts on Windows.
func (e *Env) sdkTool(name string) string {
	if e.OS == "windows" {
		name += ".bat"
	}
	return e.sdk("cmdline-tools", "latest", "bin", name)
}

func (e *Env) sdkmanager() string { return e.sdkTool("sdkmanager") }

func (e *Env) emulatorBin() string {
	if e.OS == "windows" {
		return e.sdk("emulator", "emulator.exe")
	}
	return e.sdk("emulator", "emulator")
}

// legacySDKManager reports whether installed command-line tools predate the
// `android` CLI. Fresh installs always get the new tools.
func (e *Env) legacySDKManager() bool {
	android := e.sdk("cmdline-tools", "latest", "bin", "android")
	return exists(e.sdkmanager()) && !exists(android) && !exists(android+".exe")
}

func (e *Env) abi() string {
	if e.Arch == "arm64" {
		return "arm64-v8a"
	}
	return "x86_64"
}

func (e *Env) systemImage() string {
	return "system-images;android-" + e.AndroidAPI + ";google_apis;" + e.abi()
}

func (e *Env) avdName() string { return "StackPilot_API_" + e.AndroidAPI }

func (e *Env) sdkPackages() []string {
	pkgs := []string{"platform-tools", "platforms;android-" + e.AndroidAPI, "build-tools;" + e.AndroidAPI + ".0.0"}
	if e.emulator() {
		pkgs = append(pkgs, "emulator", e.systemImage())
	}
	return pkgs
}

// sdkPackageDir maps "platforms;android-36" to $ANDROID_HOME/platforms/android-36.
func (e *Env) sdkPackageDir(p string) string {
	return e.sdk(strings.Split(p, ";")...)
}

// catalog builds every step for the current platform and configuration.
func (e *Env) catalog() map[string]*Step {
	steps := []*Step{}
	add := func(s *Step) *Step { steps = append(steps, s); return s }

	if e.OS == "darwin" {
		prefix := brewPrefix(e.Platform)
		add(&Step{
			ID: "homebrew", Title: "Homebrew",
			Done:     either(toolDone("brew"), pathDone(filepath.Join(prefix, "bin", "brew"))),
			Commands: []Command{{Script: `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`}},
			// Removing Homebrew removes every brew package, so it is never
			// automatic.
			Undo: Undo{Method: "manual", Package: `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/uninstall.sh)"`},
			Activate: func(*Env) Activation {
				return Activation{Path: []string{filepath.Join(prefix, "bin")}}
			},
		})
	}
	if e.OS == "linux" {
		e.install(add(&Step{ID: "curl", Title: "curl", Done: toolDone("curl")}), pkg{apt: "curl"})
	}
	if e.OS == "windows" {
		add(&Step{
			ID: "winget", Title: "winget (Windows Package Manager)",
			Done:   either(toolDone("winget"), pathDone(filepath.Join(e.LocalAppData, "Microsoft", "WindowsApps", "winget.exe"))),
			Manual: "install \"App Installer\" from the Microsoft Store: https://apps.microsoft.com/detail/9NBLGGH4NNS1",
		})
	}

	e.install(add(&Step{ID: "git", Title: "Git", Done: toolDone("git")}),
		pkg{brew: "git", apt: "git", winget: "Git.Git"})

	if e.OS != "windows" {
		mise := add(&Step{
			ID: "mise", Title: "mise (runtime version manager)",
			Done: either(toolDone("mise"), pathDone(filepath.Join(e.localBin(), "mise"))),
			Activate: func(e *Env) Activation {
				a := localBinActivation(e)
				a.Path = append(a.Path, filepath.Join(e.Home, ".local", "share", "mise", "shims"))
				return a
			},
		})
		e.install(mise, pkg{brew: "mise", script: "curl -fsSL https://mise.run | sh", scriptFiles: []string{
			filepath.Join(e.localBin(), "mise"),
			filepath.Join(e.Home, ".local", "share", "mise"),
			filepath.Join(e.Home, ".local", "state", "mise"),
			filepath.Join(e.Home, ".cache", "mise"),
		}})
	}

	node := add(&Step{ID: "node", Title: "Node.js LTS", Done: toolDone("node")})
	if e.OS == "windows" {
		// mise works on Windows, but the official Node.js installer is the
		// path most Windows guides and tools expect.
		e.install(node, pkg{winget: "OpenJS.NodeJS.LTS"})
	} else {
		node.Needs = []string{"mise"}
		node.Commands = []Command{{Args: []string{"mise", "use", "--global", "node@lts"}}}
		node.Undo = Undo{Method: "mise-tool", Package: "node@lts", Via: "mise"}
	}

	e.install(add(&Step{
		ID: "uv", Title: "uv (Python package and version manager)",
		Done:     either(toolDone("uv"), pathDone(filepath.Join(e.localBin(), "uv"))),
		Activate: localBinActivation,
	}), pkg{brew: "uv", script: "curl -LsSf https://astral.sh/uv/install.sh | sh", winget: "astral-sh.uv", scriptFiles: []string{
		filepath.Join(e.localBin(), "uv"),
		filepath.Join(e.localBin(), "uvx"),
	}})

	add(&Step{
		ID: "python", Title: "Python " + e.Python, Needs: []string{"uv"},
		Done: func(e *Env) (bool, string) {
			ctx, cancel := context.WithTimeout(e.ctx, 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "uv", "python", "find", e.Python).Output()
			if err != nil {
				return false, ""
			}
			return true, e.tilde(strings.TrimSpace(string(out)))
		},
		Commands: []Command{{Args: []string{"uv", "python", "install", e.Python}}},
		Undo:     Undo{Method: "uv-python", Package: e.Python, Via: "uv"},
	})

	if e.OS == "darwin" {
		e.install(add(&Step{ID: "watchman", Title: "Watchman (file watcher for React Native)", Done: toolDone("watchman")}),
			pkg{brew: "watchman"})
	}

	e.install(add(&Step{
		ID: "jdk", Title: "JDK " + jdkVersion,
		Done: func(e *Env) (bool, string) {
			if home := findJDK(e); home != "" {
				return true, e.tilde(home)
			}
			return false, ""
		},
		Activate: func(e *Env) Activation {
			return Activation{Vars: []Var{{"JAVA_HOME", findJDK(e)}}}
		},
	}), pkg{cask: "zulu@" + jdkVersion, apt: "openjdk-" + jdkVersion + "-jdk", winget: "Microsoft.OpenJDK." + jdkVersion})

	e.install(add(&Step{
		ID: "android-studio", Title: "Android Studio",
		Done: pathDone(
			"/Applications/Android Studio.app",
			filepath.Join(e.Home, "Applications", "Android Studio.app"),
			"/snap/bin/android-studio",
			"/opt/android-studio/bin/studio.sh",
			filepath.Join(e.Home, "android-studio", "bin", "studio.sh"),
			filepath.Join(e.ProgramFiles, "Android", "Android Studio", "bin", "studio64.exe"),
		),
	}), pkg{cask: "android-studio", snap: "android-studio", winget: "Google.AndroidStudio",
		manual: "download from https://developer.android.com/studio"})

	androidActivation := func(e *Env) Activation {
		return Activation{
			Vars: []Var{{"ANDROID_HOME", e.AndroidHome}},
			Path: []string{
				e.sdk("cmdline-tools", "latest", "bin"),
				e.sdk("platform-tools"),
				e.sdk("emulator"),
			},
		}
	}
	add(&Step{
		ID: "android-cmdline-tools", Title: "Android SDK command-line tools",
		Done:     pathDone(e.sdkmanager()),
		Run:      installCmdlineTools,
		RunDesc:  "download from dl.google.com (SHA-1 verified) into " + e.tilde(e.sdk("cmdline-tools", "latest")),
		Activate: androidActivation,
		Undo:     Undo{Method: "files", Paths: []string{e.sdk("cmdline-tools", "latest")}},
	})

	licenseInput := ""
	if e.AcceptLicenses {
		licenseInput = strings.Repeat("y\n", 50)
	}
	add(&Step{
		ID: "android-licenses", Title: "Android SDK licenses (you review and accept)",
		Needs:    []string{"android-cmdline-tools", "jdk"},
		Done:     pathDone(e.sdk("licenses", "android-sdk-license")),
		Commands: []Command{{Args: []string{e.sdkmanager(), "--licenses"}, Input: licenseInput}},
	})

	// Since cmdline-tools 20 (2026), sdkmanager forwards to the new `android`
	// CLI, which accepts licenses during install and ignores --licenses. Only
	// older command-line tools need the separate license step.
	sdkNeeds := []string{"android-cmdline-tools", "jdk"}
	if e.legacySDKManager() {
		sdkNeeds = []string{"android-licenses"}
	}
	// Install (and later uninstall) only the packages that are missing, so
	// packages from Android Studio or other projects are never removed.
	sdkPkgs := e.sdkPackages()
	var missingPkgs []string
	for _, p := range sdkPkgs {
		if !exists(e.sdkPackageDir(p)) {
			missingPkgs = append(missingPkgs, p)
		}
	}
	add(&Step{
		ID: "android-sdk", Title: "Android SDK packages (API " + e.AndroidAPI + ")",
		Needs: sdkNeeds,
		Done: func(e *Env) (bool, string) {
			for _, p := range sdkPkgs {
				if !exists(e.sdkPackageDir(p)) {
					return false, ""
				}
			}
			return true, strings.Join(sdkPkgs, ", ")
		},
		Commands: []Command{{Args: append([]string{e.sdkmanager(), "--install"}, missingPkgs...), Input: licenseInput}},
		Activate: androidActivation,
		Undo:     Undo{Method: "android-sdk", Tool: e.sdkmanager(), Packages: missingPkgs, Via: "android-cmdline-tools"},
	})

	avdHome := filepath.Join(e.Home, ".android", "avd")
	add(&Step{
		ID: "android-avd", Title: "Android emulator (" + e.avdName() + ")",
		Needs: []string{"android-sdk"},
		Done:  pathDone(filepath.Join(avdHome, e.avdName()+".ini")),
		Commands: []Command{{
			Args: []string{
				e.sdkTool("avdmanager"), "create", "avd",
				"--name", e.avdName(), "--package", e.systemImage(), "--device", "pixel_7",
			},
			Input: "no\n", // "Do you wish to create a custom hardware profile?"
		}},
		Undo: Undo{Method: "android-avd", Tool: e.sdkTool("avdmanager"), Package: e.avdName(), Via: "android-cmdline-tools"},
	})

	e.install(add(&Step{
		ID: "vscode", Title: "Visual Studio Code",
		Done: either(toolDone("code"), pathDone(
			"/Applications/Visual Studio Code.app",
			filepath.Join(e.LocalAppData, "Programs", "Microsoft VS Code", "Code.exe"),
			filepath.Join(e.ProgramFiles, "Microsoft VS Code", "Code.exe"),
		)),
	}), pkg{cask: "visual-studio-code", snap: "code", winget: "Microsoft.VisualStudioCode",
		manual: "download from https://code.visualstudio.com/download"})

	e.install(add(&Step{
		ID: "cursor", Title: "Cursor",
		Done: either(toolDone("cursor"), pathDone(
			"/Applications/Cursor.app",
			filepath.Join(e.LocalAppData, "Programs", "cursor", "Cursor.exe"),
		)),
	}), pkg{cask: "cursor", winget: "Anysphere.Cursor", manual: "download from https://cursor.com/download"})

	e.install(add(&Step{
		ID: "claude-code", Title: "Claude Code",
		Done:     either(toolDone("claude"), pathDone(filepath.Join(e.localBin(), "claude"))),
		Activate: localBinActivation,
	}), pkg{cask: "claude-code", script: "curl -fsSL https://claude.ai/install.sh | bash", winget: "Anthropic.ClaudeCode", scriptFiles: []string{
		filepath.Join(e.localBin(), "claude"),
		filepath.Join(e.Home, ".local", "share", "claude"),
	}})

	codex := add(&Step{ID: "codex", Title: "Codex CLI", Done: toolDone("codex")})
	if e.OS != "linux" {
		e.install(codex, pkg{cask: "codex", winget: "OpenAI.Codex"})
	} else {
		codex.Needs = []string{"node"}
		codex.Commands = []Command{
			{Args: []string{"npm", "install", "--global", "@openai/codex"}},
			{Script: "if command -v mise >/dev/null; then mise reshim; fi"},
		}
		codex.Undo = Undo{Method: "npm-global", Package: "@openai/codex", Via: "node"}
	}

	out := map[string]*Step{}
	for _, s := range steps {
		out[s.ID] = s
	}
	return out
}
