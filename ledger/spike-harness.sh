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
# Four sections, each with its own tally:
#   1. status-claim race (the original harness's round, ported): two
#      concurrent claims carrying the same --expect id — exactly one winner.
#   2. first-edge race: two concurrent `blocked-by=x --expect none` writes
#      on a brand-new key — exactly one winner (the 0->N edge race rev 4's
#      "already has edges?" gate reopened, dissolved by the none sentinel).
#   3. interleaved triage: a labels write (labels is UNguarded) racing a
#      status claim on the same key — the claim must succeed every round,
#      proving the precondition is field-scoped, not key-scoped (v2's bug).
#   4. signal rounds (rev 14, rule 5): mechanical, sequential (not forced
#      races) — cross-author live claim needs_override; a stale claim
#      reclaims without one; a human label blocks everyone including the
#      claimant's own close until overridden; a settled outcome blocks
#      re-resolution without one; the landed override event records the
#      tool-computed signal list. Two rounds per scenario, five scenarios.
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


# ---- Section 4: signal rounds (rule 5: standing signals + --override) ----
echo "=== Section 4: signal rounds (rule 5, 10 rounds) ==="
"$L" create signals --scope "rule 5 signal harness" \
    --field status=open,in-progress,closed --terminal status=closed \
    --multi-field blocked-by --multi-field labels \
    --guard status --guard blocked-by --as harness >/dev/null
# A second board with a short --stale-after, used only by the staleness
# round (B): the main "signals" board declares none at all (rule 6 — no
# horizon means no claim is ever stale), which keeps rounds A/C/D/E's claims
# reliably "live" across several sequential `ledger` subprocess invocations
# (each ~150-200ms of git overhead) without the wall-clock racing the claim
# check.
"$L" create signals-stale --scope "rule 5 staleness round" \
    --field status=open,in-progress,closed --terminal status=closed \
    --multi-field blocked-by --multi-field labels \
    --guard status --guard blocked-by --stale-after 200ms --as harness >/dev/null

# run_set captures a `set` invocation's stdout+stderr and exit code without
# tripping `set -e` — the assertions below need both, since a needs_override
# rejection is a nonzero exit with a JSON error document on stdout.
run_set() {
    if LAST_OUT=$("$L" "$@" --ledger signals 2>&1); then LAST_CODE=0; else LAST_CODE=$?; fi
}
json_field() { # json_field <field> <<< "$LAST_OUT"
    python3 -c "import json,sys
try:
    d = json.load(sys.stdin)
    print(d.get('$1', ''))
except Exception:
    print('')"
}
seed_id() { # seed_id <key> <as> <message>
    "$L" set "$1" status=open --expect none -m "$3" --as "$2" --ledger signals | json_field id
}
event_id() { # event_id <output-json>
    echo "$1" | json_field id
}

bad4=0
round=0

# Scenarios A/B use plain (non-human) keys; C uses a human-labeled key; D
# revises a settled outcome; E checks the override event's recorded signal
# list. Two rounds each, ten total.

# A: cross-author live claim -> needs_override
for i in 1 2; do
    round=$((round+1)); key="signal-cross$i"
    sid=$(seed_id "$key" harness "seed $key")
    run_set set "$key" status=in-progress --expect "$sid" -m claiming --as w1
    cid=$(event_id "$LAST_OUT")
    run_set set "$key" status=in-progress --expect "$cid" -m steal --as w2
    err=$(echo "$LAST_OUT" | json_field error)
    if [ "$LAST_CODE" -ne 0 ] && [ "$err" = "needs_override" ]; then
        echo "round $round: OK (cross-author live claim -> needs_override)"
    else
        echo "round $round: BAD (cross-author claim: code=$LAST_CODE err=$err)"; bad4=$((bad4+1)); echo "$LAST_OUT"
    fi
done

# B: stale claim reclaims without --override (staleness dissolves the
# signal) — the only round using signals-stale's short horizon.
for i in 1 2; do
    round=$((round+1)); key="signal-stale$i"
    sid=$("$L" set "$key" status=open --expect none -m "seed $key" --as harness --ledger signals-stale | json_field id)
    if LAST_OUT=$("$L" set "$key" status=in-progress --expect "$sid" -m claiming --as w1 --ledger signals-stale 2>&1); then LAST_CODE=0; else LAST_CODE=$?; fi
    cid=$(event_id "$LAST_OUT")
    sleep 1 # comfortably past the 200ms --stale-after horizon
    if LAST_OUT=$("$L" set "$key" status=in-progress --expect "$cid" -m "reclaiming from w1: stale" --as w2 --ledger signals-stale 2>&1); then LAST_CODE=0; else LAST_CODE=$?; fi
    if [ "$LAST_CODE" -eq 0 ] && [ "$(echo "$LAST_OUT" | json_field error)" = "" ]; then
        echo "round $round: OK (stale claim reclaims without --override)"
    else
        echo "round $round: BAD (stale reclaim: code=$LAST_CODE out=$LAST_OUT)"; bad4=$((bad4+1))
    fi
done

# C: human label blocks everyone, including the claimant's own close, until
# --override — labels is unguarded so the label write itself needs none, but
# every guarded write on the key after it does.
for i in 1 2; do
    round=$((round+1)); key="signal-human$i"
    "$L" set "$key" labels=human --expect none -m "reserve" --as harness --ledger signals >/dev/null
    run_set set "$key" status=open --expect none -m "reserved title" --as owner
    err1=$(echo "$LAST_OUT" | json_field error)
    if [ "$LAST_CODE" -eq 0 ] || [ "$err1" != "needs_override" ]; then
        echo "round $round: BAD (human-labeled seed landed without --override: code=$LAST_CODE err=$err1)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=open --expect none --override -m "reserved title — for owner: planned work" --as owner
    sid=$(event_id "$LAST_OUT")
    if [ "$LAST_CODE" -ne 0 ] || [ -z "$sid" ]; then
        echo "round $round: BAD (human-labeled seed with --override didn't land: $LAST_OUT)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=in-progress --expect "$sid" -m claiming --as owner
    err2=$(echo "$LAST_OUT" | json_field error)
    if [ "$LAST_CODE" -eq 0 ] || [ "$err2" != "needs_override" ]; then
        echo "round $round: BAD (claim on human key landed without --override: code=$LAST_CODE err=$err2)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=in-progress --expect "$sid" --override -m "claiming my own reserved key" --as owner
    cid=$(event_id "$LAST_OUT")
    if [ "$LAST_CODE" -ne 0 ] || [ -z "$cid" ]; then
        echo "round $round: BAD (claim on human key with --override didn't land: $LAST_OUT)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=closed --evidence commit:abc --expect "$cid" -m done --as owner
    err3=$(echo "$LAST_OUT" | json_field error)
    if [ "$LAST_CODE" -eq 0 ] || [ "$err3" != "needs_override" ]; then
        echo "round $round: BAD (own close on human key landed without --override: code=$LAST_CODE err=$err3)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=closed --evidence commit:abc --expect "$cid" --override -m "closing my own human-labeled key" --as owner
    if [ "$LAST_CODE" -ne 0 ] || [ -n "$(echo "$LAST_OUT" | json_field error)" ]; then
        echo "round $round: BAD (own close with --override didn't land: $LAST_OUT)"; bad4=$((bad4+1)); continue
    fi
    echo "round $round: OK (human label gated seed, claim, and own close until --override)"
done

# D: settled blocks re-resolution without --override, for the closer's own
# author too.
for i in 1 2; do
    round=$((round+1)); key="signal-settled$i"
    sid=$(seed_id "$key" harness "seed $key")
    run_set set "$key" status=in-progress --expect "$sid" -m claiming --as closer
    cid=$(event_id "$LAST_OUT")
    run_set set "$key" status=closed --evidence commit:abc --expect "$cid" -m done --as closer
    ccid=$(event_id "$LAST_OUT")
    run_set set "$key" status=open --expect "$ccid" -m reopen --as closer
    err=$(echo "$LAST_OUT" | json_field error)
    if [ "$LAST_CODE" -eq 0 ] || [ "$err" != "needs_override" ]; then
        echo "round $round: BAD (reopen of own settled close landed without --override: code=$LAST_CODE err=$err)"; bad4=$((bad4+1)); continue
    fi
    run_set set "$key" status=open --expect "$ccid" --override -m "reopening: not actually fixed" --as closer
    if [ "$LAST_CODE" -ne 0 ] || [ -n "$(echo "$LAST_OUT" | json_field error)" ]; then
        echo "round $round: BAD (reopen with --override didn't land: $LAST_OUT)"; bad4=$((bad4+1)); continue
    fi
    echo "round $round: OK (settled blocks re-resolution without --override, even the closer's own)"
done

# E: the override event records the tool-computed signal list, composed
# (both signals standing at once -> both names, comma-joined, tool order).
for i in 1 2; do
    round=$((round+1)); key="signal-composed$i"
    "$L" set "$key" labels=human --expect none -m "reserve" --as harness --ledger signals >/dev/null
    run_set set "$key" status=open --expect none --override -m "reserved — for closer: planned" --as closer
    sid=$(event_id "$LAST_OUT")
    run_set set "$key" status=closed --evidence commit:abc --expect "$sid" --override -m "closing" --as closer
    ccid=$(event_id "$LAST_OUT")
    run_set set "$key" status=open --expect "$ccid" --override -m "reopening: composed signals" --as intruder
    reopenID=$(event_id "$LAST_OUT")
    if [ "$LAST_CODE" -ne 0 ] || [ -z "$reopenID" ]; then
        echo "round $round: BAD (composed-override write didn't land: $LAST_OUT)"; bad4=$((bad4+1)); continue
    fi
    recorded=$("$L" tail --raw --ledger signals -n 1 | python3 -c "import json,sys
d = json.load(sys.stdin)
print(d['events'][-1].get('override',''))")
    if [ "$recorded" = "human,settled" ]; then
        echo "round $round: OK (override event recorded 'human,settled')"
    else
        echo "round $round: BAD (expected override 'human,settled', got '$recorded')"; bad4=$((bad4+1))
    fi
done

echo "TALLY (signal rounds): $((10-bad4))/10 rounds correct, $bad4 bad"
echo

total_bad=$((bad1+bad2+bad3+bad4))
total_rounds=$((3*ROUNDS+10))
echo "=== GRAND TOTAL: $((total_rounds-total_bad))/$total_rounds rounds correct, $total_bad bad ==="
[ "$total_bad" -eq 0 ]
