#!/usr/bin/env bash
# Universal chit installer for macOS and Linux.
#
# One-line install (from a published GitHub release):
#
#   curl -fsSL https://github.com/prime-radiant-inc/ledger/releases/latest/download/install.sh | bash
#
# Detects OS and arch, downloads the matching release tarball, verifies it
# against the release's checksums.txt, and installs the binary.
#
# Environment overrides:
#   LEDGER_REPO      GitHub repo to download from (CI rewrites the default to
#                    the releasing repo at upload time)
#   LEDGER_VERSION   tag to install (e.g. v0.1.0; default: latest release)
#   BINDIR           install directory (default: ~/.local/bin, or the
#                    directory holding an existing chit on PATH)
set -euo pipefail

LEDGER_REPO="${LEDGER_REPO:-prime-radiant-inc/ledger}"
LEDGER_VERSION="${LEDGER_VERSION:-}"

usage() {
    cat <<EOF
chit installer — macOS + Linux

  curl -fsSL https://github.com/$LEDGER_REPO/releases/latest/download/install.sh | bash

Environment:
  LEDGER_REPO      GitHub repo to download from (default: $LEDGER_REPO)
  LEDGER_VERSION   tag to install (default: latest release)
  BINDIR           install directory (default: ~/.local/bin)

Windows: download chit-windows-<arch>.tar.gz from the releases page and
put chit.exe on your PATH; after that, \`chit update\` keeps it fresh.
EOF
}

for a in "$@"; do
    case "$a" in
        -h|--help) usage; exit 0 ;;
    esac
done

uname_s=$(uname -s)
uname_m=$(uname -m)
case "$uname_s" in
    Darwin) goos=darwin ;;
    Linux)  goos=linux ;;
    *) echo "chit: unsupported OS '$uname_s' (this installer covers macOS + Linux; see --help for Windows)" >&2; exit 1 ;;
esac
case "$uname_m" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "chit: unsupported arch '$uname_m' (want amd64 or arm64)" >&2; exit 1 ;;
esac

have() { command -v "$1" >/dev/null 2>&1; }

download() {
    # $1 = url, $2 = output path
    if have curl; then curl -fsSL "$1" -o "$2"
    elif have wget; then wget -qO "$2" "$1"
    else
        echo "chit: need 'curl' or 'wget' to download the release" >&2
        exit 1
    fi
}

if [ -n "$LEDGER_VERSION" ]; then
    base="https://github.com/$LEDGER_REPO/releases/download/$LEDGER_VERSION"
else
    base="https://github.com/$LEDGER_REPO/releases/latest/download"
fi
tarball="chit-$goos-$arch.tar.gz"

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t chit)
trap 'rm -rf "$tmpdir"' EXIT

echo ">> Downloading $base/$tarball"
if ! download "$base/$tarball" "$tmpdir/$tarball"; then
    echo "chit: download failed. If no release is published yet at" >&2
    echo "  $base/$tarball" >&2
    echo "there is nothing to install. Build from source instead:" >&2
    echo "  git clone https://github.com/$LEDGER_REPO && cd \$(basename $LEDGER_REPO)/ledger && go build -o chit ." >&2
    exit 1
fi

download "$base/checksums.txt" "$tmpdir/checksums.txt"
want=$(awk -v f="$tarball" '$2 == f || $2 == "*"f {print $1}' "$tmpdir/checksums.txt")
if [ -z "$want" ]; then
    echo "chit: $tarball not listed in the release's checksums.txt" >&2
    exit 1
fi
if have sha256sum; then
    got=$(sha256sum "$tmpdir/$tarball" | awk '{print $1}')
else
    got=$(shasum -a 256 "$tmpdir/$tarball" | awk '{print $1}')
fi
if [ "$got" != "$want" ]; then
    echo "chit: checksum mismatch for $tarball (got $got, want $want)" >&2
    exit 1
fi

tar -xzf "$tmpdir/$tarball" -C "$tmpdir"
if [ ! -f "$tmpdir/chit" ]; then
    echo "chit: binary missing from tarball" >&2
    exit 1
fi

# resolve_links follows a symlink chain by hand — `readlink -f` isn't on
# older macOS, and the brew check below must see the real Cellar path, not
# the /opt/homebrew/bin symlink that PATH finds.
resolve_links() {
    p="$1"
    while [ -L "$p" ]; do
        t=$(readlink "$p") || break
        case "$t" in
            /*) p="$t" ;;
            *)  p="$(dirname "$p")/$t" ;;
        esac
    done
    printf '%s\n' "$p"
}

# Default install dir: wherever THIS chit already lives (so this doubles
# as an upgrade), else ~/.local/bin. Only adopt an existing binary's
# directory if it answers like ours (JSON envelope with an arch field) and
# isn't Homebrew-managed (overwriting brew's symlink would detach it from
# brew upgrade/uninstall).
if [ -z "${BINDIR:-}" ]; then
    BINDIR="$HOME/.local/bin"
    if existing=$(command -v chit 2>/dev/null); then
        case "$(resolve_links "$existing")" in
            */Cellar/*)
                echo ">> Existing chit at $existing is Homebrew-managed; use \`brew upgrade chit\`." >&2
                echo ">> Installing to $BINDIR instead." >&2 ;;
            *) if "$existing" version 2>/dev/null | grep -q '"arch"'; then
                   BINDIR=$(dirname "$existing")
               fi ;;
        esac
    fi
fi
mkdir -p "$BINDIR"

# Stage beside the destination, then rename: atomic on one filesystem, so an
# interrupted upgrade never leaves a truncated binary where a working one was.
install -m 0755 "$tmpdir/chit" "$BINDIR/.chit.new.$$"
mv -f "$BINDIR/.chit.new.$$" "$BINDIR/chit"
# non-TTY `chit version` emits a JSON envelope; pull the version out of it
ver=$("$BINDIR/chit" version 2>/dev/null | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p')
echo ">> Installed chit${ver:+ $ver} to $BINDIR/chit"

case ":$PATH:" in
    *":$BINDIR:"*) ;;
    *) echo ">> NOTE: $BINDIR is not on your PATH — add it to your shell profile:" >&2
       echo "     export PATH=\"$BINDIR:\$PATH\"" >&2 ;;
esac
