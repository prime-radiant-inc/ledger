#!/usr/bin/env bash
# Universal ledger installer for macOS and Linux.
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
#                    directory holding an existing ledger on PATH)
set -euo pipefail

LEDGER_REPO="${LEDGER_REPO:-prime-radiant-inc/ledger}"
LEDGER_VERSION="${LEDGER_VERSION:-}"

usage() {
    cat <<EOF
ledger installer — macOS + Linux

  curl -fsSL https://github.com/$LEDGER_REPO/releases/latest/download/install.sh | bash

Environment:
  LEDGER_REPO      GitHub repo to download from (default: $LEDGER_REPO)
  LEDGER_VERSION   tag to install (default: latest release)
  BINDIR           install directory (default: ~/.local/bin)

Windows: download ledger-windows-<arch>.tar.gz from the releases page and
put ledger.exe on your PATH; after that, \`ledger update\` keeps it fresh.
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
    *) echo "ledger: unsupported OS '$uname_s' (this installer covers macOS + Linux; see --help for Windows)" >&2; exit 1 ;;
esac
case "$uname_m" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "ledger: unsupported arch '$uname_m' (want amd64 or arm64)" >&2; exit 1 ;;
esac

have() { command -v "$1" >/dev/null 2>&1; }

download() {
    # $1 = url, $2 = output path
    if have curl; then curl -fsSL "$1" -o "$2"
    elif have wget; then wget -qO "$2" "$1"
    else
        echo "ledger: need 'curl' or 'wget' to download the release" >&2
        exit 1
    fi
}

if [ -n "$LEDGER_VERSION" ]; then
    base="https://github.com/$LEDGER_REPO/releases/download/$LEDGER_VERSION"
else
    base="https://github.com/$LEDGER_REPO/releases/latest/download"
fi
tarball="ledger-$goos-$arch.tar.gz"

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t ledger)
trap 'rm -rf "$tmpdir"' EXIT

echo ">> Downloading $base/$tarball"
if ! download "$base/$tarball" "$tmpdir/$tarball"; then
    echo "ledger: download failed. If no release is published yet at" >&2
    echo "  $base/$tarball" >&2
    echo "there is nothing to install. Build from source instead:" >&2
    echo "  git clone https://github.com/$LEDGER_REPO && cd \$(basename $LEDGER_REPO)/ledger && go build -o ledger ." >&2
    exit 1
fi

download "$base/checksums.txt" "$tmpdir/checksums.txt"
want=$(awk -v f="$tarball" '$2 == f || $2 == "*"f {print $1}' "$tmpdir/checksums.txt")
if [ -z "$want" ]; then
    echo "ledger: $tarball not listed in the release's checksums.txt" >&2
    exit 1
fi
if have sha256sum; then
    got=$(sha256sum "$tmpdir/$tarball" | awk '{print $1}')
else
    got=$(shasum -a 256 "$tmpdir/$tarball" | awk '{print $1}')
fi
if [ "$got" != "$want" ]; then
    echo "ledger: checksum mismatch for $tarball (got $got, want $want)" >&2
    exit 1
fi

tar -xzf "$tmpdir/$tarball" -C "$tmpdir"
if [ ! -f "$tmpdir/ledger" ]; then
    echo "ledger: binary missing from tarball" >&2
    exit 1
fi

# Default install dir: wherever THIS ledger already lives (so this doubles
# as an upgrade), else ~/.local/bin. "ledger" is also the name of a venerable
# accounting CLI, so only adopt an existing binary's directory if it answers
# like ours (JSON envelope with an arch field) and isn't Homebrew-managed.
if [ -z "${BINDIR:-}" ]; then
    BINDIR="$HOME/.local/bin"
    if existing=$(command -v ledger 2>/dev/null); then
        case "$existing" in
            */Cellar/*) ;; # brew-managed: leave it to brew
            *) if "$existing" version 2>/dev/null | grep -q '"arch"'; then
                   BINDIR=$(dirname "$existing")
               fi ;;
        esac
    fi
fi
mkdir -p "$BINDIR"

install -m 0755 "$tmpdir/ledger" "$BINDIR/ledger"
echo ">> Installed $("$BINDIR/ledger" version 2>/dev/null | head -1 || echo ledger) to $BINDIR/ledger"

case ":$PATH:" in
    *":$BINDIR:"*) ;;
    *) echo ">> NOTE: $BINDIR is not on your PATH — add it to your shell profile:" >&2
       echo "     export PATH=\"$BINDIR:\$PATH\"" >&2 ;;
esac
