#!/usr/bin/env bash
# bridge-live-trial.sh — drive the chit-gh live acceptance trial.
#
# WHEN TO USE: after any change to chit-gh, to re-run the one live
# acceptance trial against a real GitHub repo and a real ledger store. The
# fixture suite proves the laws; this proves the two CLIs really behave the
# way the fixture models them (every live round of this design so far has
# caught at least one thing the green fixture suite did not).
#
# WHY A SCRIPT: the trial is a dozen interleaved board writes, GitHub actions
# and bridge runs whose ORDER is the whole point. Typed by hand it is neither
# reproducible nor auditable, and a half-finished trial leaves litter in a
# shared repo.
#
# SAFETY: writes to exactly one GitHub repo, named in REPO below. It refuses
# to run against any other.
#
# Usage:
#   scripts/bridge-live-trial.sh setup          # build binaries, make a board
#   scripts/bridge-live-trial.sh sync [args..]  # one bridge run
#   scripts/bridge-live-trial.sh chit <args>    # a board command
#   scripts/bridge-live-trial.sh gh <args>      # a GitHub command
#   scripts/bridge-live-trial.sh audit          # the standing invariants
#   scripts/bridge-live-trial.sh replica        # clone the board to replica b
#
# Everything lands under $TRIAL (default: a fresh dir under the scratchpad),
# and every invocation is appended to $TRIAL/trial.log.

set -euo pipefail

REPO="${REPO:-prime-radiant-inc/ledger-bridge-spike}"
SLUG="${SLUG:-issues}"
TRIAL="${TRIAL:?set TRIAL to the trial directory}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "$REPO" != "prime-radiant-inc/ledger-bridge-spike" ]; then
  echo "refusing to write to '$REPO': the trial has exactly one sanctioned repo" >&2
  exit 2
fi

LEDGER="$TRIAL/bin/chit"
BRIDGE="$TRIAL/bin/chit-gh"
A="$TRIAL/a"

log() { printf '\n=== %s %s\n' "$(date +%H:%M:%S)" "$*" | tee -a "$TRIAL/trial.log"; }
run() { "$@" 2>&1 | tee -a "$TRIAL/trial.log"; return "${PIPESTATUS[0]}"; }

cmd_setup() {
  mkdir -p "$TRIAL/bin" "$A"
  log "build"
  (cd "$ROOT/ledger" && go build -o "$LEDGER" . && go build -o "$BRIDGE" ./bridge)
  "$LEDGER" version | tee -a "$TRIAL/trial.log"
  log "board"
  (cd "$A" && git init -q . && git config user.email trial@example.com && git config user.name trial)
  run "$LEDGER" --store "$A" init
  run "$LEDGER" --store "$A" create "$SLUG" --scope "live bridge trial" \
    --field status=open,in-progress,closed,wontfix \
    --terminal status=closed,wontfix --guard status --guard blocked-by \
    --multi-field labels --multi-field blocked-by \
    --require-evidence status=closed --stale-after 2h --as jesse
}

cmd_sync() {
  log "chit-gh sync $*"
  run "$BRIDGE" sync --repo "$REPO" --ledger "$SLUG" --store "$A" \
    --ledger-bin "$LEDGER" "$@"
}

cmd_ledger() {
  log "chit $*"
  run "$LEDGER" --store "$A" "$@"
}

cmd_gh() {
  log "gh $*"
  run gh "$@" --repo "$REPO"
}

# cmd_replica clones the board into a second store through a bare remote, so
# the two-replica step is a real partition and a real sentinel merge.
cmd_replica() {
  log "replica"
  mkdir -p "$TRIAL/remote.git" "$TRIAL/b"
  git init -q --bare "$TRIAL/remote.git"
  (cd "$A" && git remote add origin "$TRIAL/remote.git" 2>/dev/null || true)
  run "$LEDGER" --store "$A" push "$SLUG"
  (cd "$TRIAL/b" && git init -q . && git config user.email b@example.com && git config user.name b \
    && git remote add origin "$TRIAL/remote.git")
  run "$LEDGER" --store "$TRIAL/b" init
  run "$LEDGER" --store "$TRIAL/b" sync
}

# cmd_audit is the standing invariant set: no duplicate links, no override
# forced past a `human` signal, no mirrored comment posted twice.
#
# The comment check is scoped to $SINCE because this scratch repo carries
# litter from earlier design rounds — including deliberately duplicated
# markers from an older marker-edges probe. An unscoped check reports those
# as this build's failures.
cmd_audit() {
  SINCE="${SINCE:-1970-01-01T00:00:00Z}"
  log "audit"
  {
    echo "-- link notes (one established per key):"
    "$LEDGER" --store "$A" notes -k github-link -n 0 --ledger "$SLUG" \
      | python3 -c 'import json,sys,collections
d=json.load(sys.stdin)
c=collections.Counter((n["key"],n["text"].strip()) for n in d["notes"])
dupes={k:v for k,v in c.items() if v>1}
print("  notes:",len(d["notes"]),"duplicates:",dupes or "none")
per=collections.Counter(n["key"] for n in d["notes"] if not n["text"].startswith("github: retracted"))
print("  keys with >1 link:",{k:v for k,v in per.items() if v>1} or "none")'
    echo "-- overrides the bridge FABRICATED (a human signal it must never force):"
    "$LEDGER" --store "$A" tail -n 0 --raw --ledger "$SLUG" \
      | python3 -c 'import json,sys
d=json.load(sys.stdin)
bridge=[(e["id"],e.get("author"),e["override"]) for e in d["events"]
        if e.get("override") and (str(e.get("author","")).startswith("github:@") or e.get("author")=="github-bridge")]
# Law 5 sanctions claim/settled auto-overrides and attributes them to the
# GitHub actor. `human` is the one it must NEVER force.
bad=[b for b in bridge if "human" in b[2]]
print("   forbidden (human):",bad or "none")
print("   sanctioned (claim/settled):",[b for b in bridge if "human" not in b[2]] or "none")'
    echo "-- mirrored comments posted more than once SINCE $SINCE (must be none):"
    gh issue list --repo "$REPO" --state all --limit 250 --json number,comments \
      | SINCE="$SINCE" python3 -c 'import json,sys,re,os,collections
issues=json.load(sys.stdin)
since=os.environ["SINCE"]
rx=re.compile(r"^\*\*(.+?)\*\* \(via ledger, ([0-9a-f]+)\):$")
bad={}
for i in issues:
    c=collections.Counter()
    for cm in i["comments"]:
        if cm.get("createdAt","") < since: continue   # older rounds litter this repo
        m=rx.match(cm["body"].strip().split("\n")[0])
        if m: c[m.group(2)]+=1
    d={k:v for k,v in c.items() if v>1}
    if d: bad[i["number"]]=d
print("  ",bad or "none")'
  } | tee -a "$TRIAL/trial.log"
}

case "${1:-}" in
  setup)   shift; cmd_setup "$@" ;;
  sync)    shift; cmd_sync "$@" ;;
  chit)    shift; cmd_ledger "$@" ;;
  gh)      shift; cmd_gh "$@" ;;
  replica) shift; cmd_replica "$@" ;;
  audit)   shift; cmd_audit "$@" ;;
  *) sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 2 ;;
esac
