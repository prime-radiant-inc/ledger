#!/usr/bin/env bash
# Cross-compile ledger for every release target and pack one flat tarball per
# (os,arch) plus a checksums.txt. Run by the release workflow; run it locally
# to smoke-test the matrix before tagging.
#
#   scripts/build-tarballs.sh <version> [output-dir]
#
# <version> may carry a leading v (the git tag form); it is stamped into the
# binary without it. Output dir defaults to dist/ at the repo root. Each
# tarball holds exactly one file — the binary — so install.sh, `ledger
# update`, and the homebrew formula all share one trivial extract step.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)

version="${1:?usage: build-tarballs.sh <version> [output-dir]}"
version="${version#v}"
out="${2:-$repo/dist}"
mkdir -p "$out"

targets="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

for t in $targets; do
    goos=${t%/*} goarch=${t#*/}
    bin=ledger
    [ "$goos" = windows ] && bin=ledger.exe
    stage=$(mktemp -d)
    (cd "$repo/ledger" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath \
        -ldflags "-s -w -X ledger/internal/cmd.Version=$version" \
        -o "$stage/$bin" .)
    tar -C "$stage" -czf "$out/ledger-$goos-$goarch.tar.gz" "$bin"
    rm -rf "$stage"
    echo "built ledger-$goos-$goarch.tar.gz"
done

cd "$out"
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ledger-*.tar.gz > checksums.txt
else
    shasum -a 256 ledger-*.tar.gz > checksums.txt
fi
echo
cat checksums.txt
