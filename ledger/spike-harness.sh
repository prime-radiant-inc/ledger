#!/usr/bin/env bash
# Forced-race harness for the issues-tracker spike's `set --expect` invariant
# (rev 5: every write to a guarded field carries --expect, "none" is the
# first-touch sentinel, and the precondition is field-scoped). Extends
# research/scripts/expect-race-harness.sh (main branch) with two more rounds
# the rev-5 spec's validation plan calls for; this copy lives on
# wip/issues-spike only — the original in research/ is untouched.
#
#   ledger/spike-harness.sh <ledger-binary> [rounds=10]
#
# Three sections, each with its own tally:
#   1. status-claim race (the original harness's round, ported): two
#      concurrent claims carrying the same --expect id — exactly one winner.
#   2. first-edge race: two concurrent `blocked-by=x --expect none` writes
#      on a brand-new key — exactly one winner (the 0->N edge race rev 4's
#      "already has edges?" gate reopened, dissolved by the none sentinel).
#   3. interleaved triage: a labels write (labels is UNguarded) racing a
#      status claim on the same key — the claim must succeed every round,
#      proving the precondition is field-scoped, not key-scoped (v2's bug).
set -euo pipefail
L="${1:?usage: spike-harness.sh <ledger-binary> [rounds]}"
ROUNDS="${2:-10}"

dir=$(mktemp -d)
trap 'rm -rf "$dir" 2>/dev/null || find "$dir" -depth -delete' EXIT
cd "$dir"
"$L" init >/dev/null
"$L" create race --scope "expect race harness (spike v3)" \
    --field status=open,in-progress,closed --terminal status=closed \
    --multi-field blocked-by --multi-field labels \
    --guard status --guard blocked-by --as harness >/dev/null

# ---- Section 1: status-claim race (ported from research/scripts/expect-race-harness.sh) ----
echo "=== Section 1: status-claim race (same --expect id, $ROUNDS rounds) ==="
bad1=0
for i in $(seq 1 "$ROUNDS"); do
    key="claim$i"
    seed=$("$L" set "$key" status=open --expect none -m seed --as harness | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    "$L" set "$key" status=in-progress --expect "$seed" -m claim --as w1 >"$dir/o1" 2>&1 &
    p1=$!
    "$L" set "$key" status=in-progress --expect "$seed" -m claim --as w2 >"$dir/o2" 2>&1 &
    p2=$!
    s=0; wait $p1 && s=$((s+1)); wait $p2 && s=$((s+1)) || true
    losses=$(grep -c claim_lost "$dir/o1" "$dir/o2" | awk -F: '{n+=$2} END {print n}')
    if [ "$s" -eq 1 ] && [ "$losses" -eq 1 ]; then
        echo "round $i: OK (1 success, 1 claim_lost)"
    else
        echo "round $i: BAD (successes=$s claim_lost=$losses)"; bad1=$((bad1+1))
        cat "$dir/o1" "$dir/o2"
    fi
done
echo "TALLY (status-claim): $((ROUNDS-bad1))/$ROUNDS rounds correct, $bad1 bad"
echo

# ---- Section 2: first-edge race (0 -> N boundary, --expect none) ----
echo "=== Section 2: first-edge race (blocked-by --expect none, $ROUNDS rounds) ==="
"$L" set edge-target status=open --expect none -m "blocked-by target" --as harness >/dev/null
bad2=0
for i in $(seq 1 "$ROUNDS"); do
    key="edge$i"
    "$L" set "$key" blocked-by=edge-target --expect none -m "first edge" --as w1 >"$dir/e1" 2>&1 &
    p1=$!
    "$L" set "$key" blocked-by=edge-target --expect none -m "first edge" --as w2 >"$dir/e2" 2>&1 &
    p2=$!
    s=0; wait $p1 && s=$((s+1)); wait $p2 && s=$((s+1)) || true
    losses=$(grep -c claim_lost "$dir/e1" "$dir/e2" | awk -F: '{n+=$2} END {print n}')
    if [ "$s" -eq 1 ] && [ "$losses" -eq 1 ]; then
        echo "round $i: OK (1 success, 1 claim_lost)"
    else
        echo "round $i: BAD (successes=$s claim_lost=$losses)"; bad2=$((bad2+1))
        cat "$dir/e1" "$dir/e2"
    fi
done
echo "TALLY (first-edge): $((ROUNDS-bad2))/$ROUNDS rounds correct, $bad2 bad"
echo

# ---- Section 3: interleaved triage (field-scoping proof) ----
echo "=== Section 3: interleaved triage (labels write vs status claim, $ROUNDS rounds) ==="
bad3=0
for i in $(seq 1 "$ROUNDS"); do
    key="triage$i"
    sid=$("$L" set "$key" status=open --expect none -m seed --as harness | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
    "$L" set "$key" labels=urgent -m "labeling" --as triager >"$dir/t1" 2>&1 &
    p1=$!
    "$L" set "$key" status=in-progress --expect "$sid" -m claiming --as claimant >"$dir/t2" 2>&1 &
    p2=$!
    wait $p1 || true
    if wait $p2; then
        echo "round $i: OK (status claim succeeded despite concurrent labels write)"
    else
        echo "round $i: BAD (status claim failed — field-scoping broken)"; bad3=$((bad3+1))
        cat "$dir/t1" "$dir/t2"
    fi
done
echo "TALLY (interleaved-triage): $((ROUNDS-bad3))/$ROUNDS rounds correct, $bad3 bad"
echo

total_bad=$((bad1+bad2+bad3))
echo "=== GRAND TOTAL: $((3*ROUNDS-total_bad))/$((3*ROUNDS)) rounds correct, $total_bad bad ==="
[ "$total_bad" -eq 0 ]
