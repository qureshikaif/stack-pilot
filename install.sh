#!/bin/sh
# Installs the stackpilot binary from GitHub Releases on macOS and Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/qureshikaif/stack-pilot/main/install.sh | sh
#
# Environment variables:
#   STACKPILOT_VERSION      release tag to install, e.g. v0.1.0 (default: latest)
#   STACKPILOT_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#   STACKPILOT_BASE_URL     download from a mirror instead of GitHub
set -eu

REPO="qureshikaif/stack-pilot"
VERSION="${STACKPILOT_VERSION:-latest}"
INSTALL_DIR="${STACKPILOT_INSTALL_DIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
fail() { printf 'stackpilot install: %s\n' "$*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fail "unsupported OS $(uname -s); on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported CPU $(uname -m)" ;;
esac

if [ -n "${STACKPILOT_BASE_URL:-}" ]; then
  base="$STACKPILOT_BASE_URL"
elif [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi
archive="stackpilot_${os}_${arch}.tar.gz"

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  fail "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  fail "sha256sum or shasum is required to verify the download"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

say "Downloading $archive ($VERSION)…"
fetch "$base/$archive" "$tmp/$archive" || fail "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "download failed: $base/checksums.txt"

want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || fail "$archive is not listed in checksums.txt"
got="$(sha256 "$tmp/$archive")"
[ "$got" = "$want" ] || fail "checksum mismatch for $archive (got $got, want $want)"
say "Checksum verified."

tar -xzf "$tmp/$archive" -C "$tmp" stackpilot
mkdir -p "$INSTALL_DIR"
mv -f "$tmp/stackpilot" "$INSTALL_DIR/stackpilot"
chmod 755 "$INSTALL_DIR/stackpilot"

say "Installed $("$INSTALL_DIR/stackpilot" version) to $INSTALL_DIR/stackpilot"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) say "Run: stackpilot setup" ;;
  *)
    case "${SHELL:-}" in
      */zsh) rc="$HOME/.zshrc" ;;
      */bash) rc="$HOME/.bashrc" ;;
      *) rc="your shell profile" ;;
    esac
    say ""
    say "$INSTALL_DIR is not on your PATH. Add it with:"
    say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> $rc"
    say "then open a new terminal and run: stackpilot setup"
    ;;
esac
