#!/usr/bin/env bash
# Forced-race harness for `set --expect` atomicity (issues-tracker spike).
# For each round: seed a fresh key, then fire two simultaneous claims carrying
# the SAME --expect id. Every round must yield exactly one success and one
# claim_lost; any round with two successes disproves atomicity.
#
#   research/scripts/expect-race-harness.sh <ledger-binary> [rounds=20]
#
# Output: per-round verdicts and a final tally. Referenced by the issues spec
# (rev 4) as the citation for the --expect atomicity claim.
set -euo pipefail
L="${1:?usage: expect-race-harness.sh <ledger-binary> [rounds]}"
ROUNDS="${2:-20}"

dir=$(mktemp -d)
trap 'rm -rf "$dir" 2>/dev/null || find "$dir" -depth -delete' EXIT
cd "$dir"
"$L" init >/dev/null
"$L" create race --scope "expect race harness" \
    --field status=open,in-progress,closed --terminal status=closed \
    --multi-field blocked-by --as harness >/dev/null

bad=0
for i in $(seq 1 "$ROUNDS"); do
    key="k$i"
    seed=$("$L" set "$key" status=open -m seed --as harness | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    "$L" set "$key" status=in-progress --expect "$seed" -m claim --as w1 >"$dir/o1" 2>&1 &
    p1=$!
    "$L" set "$key" status=in-progress --expect "$seed" -m claim --as w2 >"$dir/o2" 2>&1 &
    p2=$!
    s=0; wait $p1 && s=$((s+1)); wait $p2 && s=$((s+1)) || true
    losses=$(grep -c claim_lost "$dir/o1" "$dir/o2" | awk -F: '{n+=$2} END {print n}')
    if [ "$s" -eq 1 ] && [ "$losses" -eq 1 ]; then
        echo "round $i: OK (1 success, 1 claim_lost)"
    else
        echo "round $i: BAD (successes=$s claim_lost=$losses)"; bad=$((bad+1))
    fi
done
echo
echo "TALLY: $((ROUNDS-bad))/$ROUNDS rounds correct, $bad bad"
[ "$bad" -eq 0 ]
