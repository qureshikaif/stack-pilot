# StackPilot

[![ci](https://github.com/qureshikaif/stack-pilot/actions/workflows/ci.yml/badge.svg)](https://github.com/qureshikaif/stack-pilot/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/qureshikaif/stack-pilot)](https://github.com/qureshikaif/stack-pilot/releases/latest)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Set up your machine for a tech stack with one command, check any repo against it, and undo it cleanly.**

Setting up a development machine means installing a runtime, a version manager, a JDK, an SDK, an editor and an AI assistant, then fixing `PATH`, `JAVA_HOME` and `ANDROID_HOME` until everything works. Android is the worst case. StackPilot asks what you're building, shows exactly what it will do, does it, and verifies the result.

```
$ stackpilot setup

? What do you want to set up? (numbers separated by commas, "all", or Enter for none)
  1) Node.js / TypeScript
  2) Python (uv)
  3) React Native / Expo (Android)
  4) Native Android (Kotlin / Java)
> 1,3

Plan
  ✓ Git                                            already set up: 2.50.1 at /usr/bin/git
  ✓ Node.js LTS                                    already set up: 22.14.0 at ~/.nvm/versions/node/v22.14.0/bin/node
  • Watchman (file watcher for React Native)       $ brew install watchman
  ✓ JDK 17                                         already set up: /Library/Java/JavaVirtualMachines/jdk-17.jdk/Contents/Home
  • Android SDK command-line tools                 download from dl.google.com (SHA-1 verified) into ~/Library/Android/sdk/cmdline-tools/latest
  • Android SDK packages (API 36)                  $ ~/Library/Android/sdk/cmdline-tools/latest/bin/sdkmanager --install platform-tools "platforms;android-36" ...
  • Android emulator (StackPilot_API_36)           $ ~/Library/Android/sdk/cmdline-tools/latest/bin/avdmanager create avd ...

? Run these 4 steps? [Y/n]
```

## Features

- **`setup`**: an interactive wizard (or flags, for scripts) that installs a complete stack: runtimes, JDK, Android SDK and emulator, editors and AI coding assistants.
- **`doctor`**: reads a repo's `package.json`, `.nvmrc`, `pyproject.toml`, `.env.example` and similar files, and tells you what this machine is missing to run it.
- **`uninstall`**: removes what `setup` installed, and only that.
- **Safe by default**: every command is shown before it runs, nothing that's already installed is reinstalled, and every config change is backed up first.

## Status

StackPilot is **early-stage**. Expect rough edges and please [open an issue](https://github.com/qureshikaif/stack-pilot/issues) when something breaks.

| Platform | `setup` / `uninstall` | `doctor` / `inspect` |
|----------|----------------------|----------------------|
| macOS (Apple Silicon, Intel) | Supported (Homebrew) | Supported |
| Ubuntu / Debian | Supported (apt, snap); less tested | Supported |
| Windows 10 / 11 | **Experimental** (winget) | Supported |
| Other Linux distros | Not yet | Supported |

## Install

**macOS / Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/qureshikaif/stack-pilot/main/install.sh | sh
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/qureshikaif/stack-pilot/main/install.ps1 | iex
```

Each script downloads the binary for your OS and CPU from the [latest release](https://github.com/qureshikaif/stack-pilot/releases/latest), checks its SHA-256 against the release's `checksums.txt`, and installs it:

| | Installs to | PATH |
|-|-------------|------|
| macOS / Linux | `~/.local/bin/stackpilot` | Tells you the line to add if it isn't already on your `PATH` |
| Windows | `%LOCALAPPDATA%\Programs\stackpilot\stackpilot.exe` | Added to your user `PATH` automatically |

No Go, Node.js or other runtime is needed: it's a single static binary. Run the same command again to update. You can set these environment variables before running the script:
- `STACKPILOT_VERSION=v0.1.0` to install a specific version.
- `STACKPILOT_INSTALL_DIR` to choose a different install folder.

<details>
<summary>Other ways to install</summary>

**Manual download:** get the archive for your system from [Releases](https://github.com/qureshikaif/stack-pilot/releases), check it against `checksums.txt`, extract it, and put `stackpilot` somewhere on your `PATH`.

**With Go 1.27+:**

```sh
go install github.com/qureshikaif/stack-pilot/cmd/stackpilot@latest
```

**From source:**

```sh
git clone https://github.com/qureshikaif/stack-pilot.git
cd stack-pilot
make build        # creates bin/stackpilot
```

**Uninstalling StackPilot itself:** delete the binary (`~/.local/bin/stackpilot`, or the `%LOCALAPPDATA%\Programs\stackpilot` folder on Windows). Run `stackpilot uninstall` first if you want to remove what it set up.

</details>

## Quick start

```sh
stackpilot setup --dry-run     # see what it would do, without changing anything
stackpilot setup               # answer a few questions, review the plan, confirm

cd ~/code/some-project
stackpilot doctor              # what is this machine missing for this repo?
```

## Commands

### `stackpilot setup`

Asks what you want to set up, shows a plan, installs what's missing, updates your environment variables and verifies the result.

```sh
stackpilot setup                                    # interactive
stackpilot setup --template react-native --dry-run  # show the plan only
stackpilot setup --template node,python --editor vscode --ai claude-code,codex --yes
```

| Template | Installs |
|----------|----------|
| `node` | Git, Node.js LTS (via [mise](https://mise.jdx.dev); the official installer on Windows) |
| `python` | Git, [uv](https://docs.astral.sh/uv/), Python 3.13 |
| `react-native` | Git, Node.js, Watchman (macOS), JDK 17, Android Studio, Android SDK, emulator |
| `android` | Git, JDK 17, Android Studio, Android SDK, emulator |

| Flag | Meaning |
|------|---------|
| `--template` | `node`, `python`, `react-native`, `android` (comma-separated) |
| `--editor` | `vscode`, `cursor` or `none` |
| `--ai` | `claude-code`, `codex` or `none` |
| `--android-api` | Android API level (default `36`) |
| `--python` | Python version (default `3.13`) |
| `--no-android-studio` | Install the SDK and emulator without the IDE |
| `--no-emulator` | Skip the emulator and system image |
| `--accept-android-licenses` | Accept Android SDK licenses without showing them (older SDKs only) |
| `--yes` | Don't ask questions; use flags and defaults |
| `--dry-run` | Show the plan without changing anything |

What it does:

- **Detects before installing.** Anything already present is skipped, even if it was installed another way (for example Node.js from nvm), and its dependencies aren't pulled in. If a run fails, run it again and it continues where it stopped.
- **Uses official sources.** Homebrew, apt/snap and winget for packages. The Android command-line tools are downloaded directly from Google's SDK repository and checked against Google's published SHA-1.
- **Fixes your environment.** Missing `ANDROID_HOME`, `JAVA_HOME` and `PATH` entries are added to a marked block in `~/.zshrc` or `~/.bashrc`:

  ```sh
  # >>> stackpilot >>>
  export ANDROID_HOME="$HOME/Library/Android/sdk"
  export PATH="$HOME/Library/Android/sdk/platform-tools:$PATH"
  # <<< stackpilot <<<
  ```

  On Windows they go to your user environment variables (written to the registry directly, because `setx` truncates long `PATH`s). The old file or values are backed up first, and only missing lines are added.
- **Verifies the result**, including whether the Android emulator can use hardware acceleration.
- **Handles failures.** You can retry, skip or stop. With `--yes` it carries on and lists failures at the end.

### `stackpilot doctor`

Checks the current machine against a project. It only reads files: it never runs project scripts and never prints the values of environment variables.

```
$ stackpilot doctor
Tools
  ✗ node      22.14.0 does not satisfy 18 (.nvmrc)
    → install a matching Node.js, e.g. `mise use node@18` or `nvm install 18`
  ✓ pnpm      9.1.0 satisfies 9.1.0 (package.json packageManager)
  ✗ python    3.9.6 does not satisfy >=3.11 (pyproject.toml)
  ✗ uv        not found on PATH (needed by uv.lock)

Environment
  ! DATABASE_URL   listed in .env.example but not set
  ✓ STRIPE_KEY     defined in .env

5 ok, 1 warnings, 3 failed
```

| Stack | Files read |
|-------|------------|
| Node.js | `package.json` (`engines`, `packageManager`), `.nvmrc`, `.node-version`, lockfiles |
| Python | `pyproject.toml` (`requires-python`, Poetry, uv), `.python-version`, `Pipfile`, `requirements.txt`, `uv.lock`, `poetry.lock` |
| Shared | `.tool-versions`, `.env.example` / `.env.sample` / `.env.template`, `.git` |

Version ranges are understood in both npm style (`^20`, `>=20 <23`, `20.x`) and Python style (`~=3.11`, `==3.12.*`).

`doctor` exits with status 1 when a check fails, and `--json` gives machine-readable output, so you can use it in CI or from an AI agent:

```sh
stackpilot doctor --json | jq '.checks[] | select(.status == "fail")'
```

### `stackpilot inspect`

Prints what a project needs (tools, versions and environment variables) without checking the machine.

```sh
stackpilot inspect ~/code/some-project --json
```

### `stackpilot uninstall`

Removes what `setup` installed.

```sh
stackpilot uninstall                          # choose from a list
stackpilot uninstall --all --dry-run          # see everything it would remove
stackpilot uninstall android-avd android-sdk --yes
```

- **Only what setup installed.** Setup records each install in `state.json` (see [Files StackPilot writes](#files-stackpilot-writes)). Tools that were already on the machine are never recorded, so they are never removed.
- **Removed with the tool that installed it**: `brew uninstall`, `apt-get remove`, `snap remove`, `winget uninstall`, `mise unuse`, `uv python uninstall`, `npm uninstall -g`, `sdkmanager --uninstall`, `avdmanager delete avd`.
- **Only the Android SDK packages setup added**; packages from Android Studio or your other projects stay.
- **In a safe order.** Anything that needs another tool to uninstall (Python needs uv) is removed before that tool.
- **Environment settings are a separate choice**, and only the lines setup added are removed.
- **Never removes Homebrew**, since that would remove every brew package. You get the official uninstall command instead. App settings such as `~/.claude` are left alone.

## Files StackPilot writes

| What | Where |
|------|-------|
| Install record | `~/Library/Application Support/stackpilot/state.json` (macOS), `~/.config/stackpilot/state.json` (Linux), `%AppData%\stackpilot\state.json` (Windows) |
| Shell settings | A `# >>> stackpilot >>>` block in `~/.zshrc` or `~/.bashrc` |
| Shell profile backups | `~/.zshrc.stackpilot-backup-<timestamp>` |
| Windows environment backups | `%LOCALAPPDATA%\stackpilot\env-backup-<timestamp>.txt` |
| Android SDK | `$ANDROID_HOME`, or the platform default (`~/Library/Android/sdk`, `~/Android/Sdk`, `%LOCALAPPDATA%\Android\Sdk`) |

## Development

```sh
make test       # go vet + go test
make build      # bin/stackpilot
goreleaser release --snapshot --clean   # build all release archives into dist/
```

### Releasing

```sh
git tag v0.1.0
git push origin v0.1.0
```

The [release workflow](.github/workflows/release.yml) runs the tests, builds binaries for macOS, Linux and Windows (amd64 and arm64) with [GoReleaser](https://goreleaser.com), publishes a GitHub Release with `checksums.txt`, and then installs that release with `install.sh` / `install.ps1` on all three OSes to confirm it works.

Optional tests:

```sh
# Downloads the real Android command-line tools (~150 MB) into a temp folder
STACKPILOT_NETWORK_TESTS=1 go test -run Network ./internal/setup/

# Windows only: writes to your real user environment and restores it.
# CI runs this on throwaway machines; avoid it on your own computer.
STACKPILOT_REGISTRY_TESTS=1 go test ./internal/setup/
```

On every push, CI runs these on Ubuntu, macOS and Windows:
- the tests;
- a dry run of `setup`;
- the install scripts, against locally built release archives.

### Project layout

```
cmd/stackpilot/        entry point
internal/cli/          argument parsing and terminal output
internal/project/      reads a repo's manifest and config files (inspect)
internal/probe/        finds tools on this machine and their versions
internal/doctor/       compares the two (doctor)
internal/version/      npm and Python version-range matching
internal/setup/        setup and uninstall
  catalog.go           every installable step, per platform
  setup.go             wizard, plan, run loop, verification
  uninstall.go         reversing installs from the record
  android.go           Android command-line tools download
  profile.go           shell profile edits
  winenv*.go           Windows environment variables (registry)
```

To support a new tool, add a step in `internal/setup/catalog.go` with its package name for each platform (`brew`/`cask`, `apt`/`snap`, `winget`) and a `Done` check.

## Roadmap

- `doctor` checks for Android projects (JDK ↔ Gradle compatibility, `compileSdk` vs installed platforms)
- `stackpilot fix`: install whatever `doctor` found missing in a repo
- Fedora (dnf) support, Flutter template
- Homebrew tap, Scoop and winget packages

## Contributing

Bug reports are especially welcome from Windows and Linux users, with the output of `stackpilot setup --dry-run`. Before opening a pull request, run `make test`.

## License

[MIT](LICENSE) © 2026 Kaif Qureshi
