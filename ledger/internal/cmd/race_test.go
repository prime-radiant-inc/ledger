// Race tests: the concurrency-harness rounds from
// research/scripts/expect-race-harness.sh (repo root, outside ledger/),
// ported into deterministic-enough Go tests. That script stays as the
// historical citation for the --expect atomicity claim (20/20 rounds,
// documented) — this file is its mechanized successor plus the field-
// scoping and reclaim families spec tests 4/5/7 also require.
//
// Every round here spawns two CONCURRENT invocations of the BUILT BINARY
// via os/exec against a shared t.TempDir() board. In-process cobra
// re-invocation would share Go-level state directly and never touch the
// real contention point (git update-ref's CAS on the shared ref) — it is
// not a real race. TestMain builds the binary once for the whole package's
// test run; every round below execs that same binary.
package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// binPath is the once-built race binary, set by TestMain before any test
// runs.
var binPath string

func TestMain(m *testing.M) { os.Exit(raceTestMain(m)) }

// raceTestMain does the build in its own function so the temp dir's cleanup
// (a defer) actually runs — os.Exit itself skips deferred calls.
func raceTestMain(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "ledger-race-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "race_test: mkdir temp:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	binPath = filepath.Join(tmp, "ledger")
	// internal/cmd -> ../.. is the module root (ledger/go.mod).
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "race_test: go build ledger binary: %v\n%s\n", err, out)
		return 1
	}
	return m.Run()
}

// execLedger runs one built-binary invocation against dir to completion —
// for sequential setup/verification steps around a round, never the raced
// pair itself.
func execLedger(t *testing.T, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"--store", dir}, args...)...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	return so.String(), se.String(), exitCode(err)
}

// raceResult is one side of a raced pair's outcome.
type raceResult struct {
	stdout, stderr string
	code           int
}

// raceLedger runs two built-binary invocations concurrently against the
// same dir, released off a shared start barrier so both processes' exec+run
// windows overlap rather than serializing on goroutine scheduling — the
// actual contention point is git update-ref's CAS on the shared ref inside
// the store, which two independent OS processes hit for real.
func raceLedger(t *testing.T, dir string, args1, args2 []string) (r1, r2 raceResult) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	fire := func(args []string, out *raceResult) {
		defer wg.Done()
		<-start
		cmd := exec.Command(binPath, append([]string{"--store", dir}, args...)...)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err := cmd.Run()
		out.stdout, out.stderr, out.code = so.String(), se.String(), exitCode(err)
	}
	wg.Add(2)
	go fire(args1, &r1)
	go fire(args2, &r2)
	close(start)
	wg.Wait()
	return r1, r2
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// raceSetupReady creates a ready-capable board (setupReady's canonical
// shape, mirrored from expect_test.go) via the built binary — round
// families that need no staleness horizon share this. extra appends further
// create flags (raceSetupReadyStale's --stale-after).
func raceSetupReady(t *testing.T, extra ...string) string {
	t.Helper()
	dir := initRepo(t)
	args := append([]string{"create", "race", "--scope", "concurrency harness rounds",
		"--field", "status=open,in-progress,closed",
		"--terminal", "status=closed",
		"--multi-field", "labels", "--multi-field", "blocked-by",
		"--guard", "status", "--guard", "blocked-by"}, extra...)
	_, se, code := execLedger(t, dir, args...)
	if code != 0 {
		t.Fatalf("create: %s", se)
	}
	return dir
}

// raceSetupReadyStale is raceSetupReady with a --stale-after horizon, for
// the reclaim family.
func raceSetupReadyStale(t *testing.T, staleAfter string) string {
	return raceSetupReady(t, "--stale-after", staleAfter)
}

// TestRaceStatusClaimSameExpect is the shell harness's core round, ported:
// 10 rounds, each seeding a fresh key then racing two same-`--expect`
// status claims. Every round must land exactly one success and one
// claim_lost (spec test 5's sibling for status; harness parity) — any round
// with two successes disproves --expect's atomicity and fails the test
// naming the round.
func TestRaceStatusClaimSameExpect(t *testing.T) {
	dir := raceSetupReady(t)
	wins := map[string]int{}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("claim-%d", i)
		so, se, code := execLedger(t, dir, "set", key, "status=open", "--expect", "none", "-m", "seed", "--as", "seeder")
		if code != 0 {
			t.Fatalf("round %d: seed failed: %s %s", i, so, se)
		}
		seedID := mustJSON(t, so)["id"].(string)

		r1, r2 := raceLedger(t, dir,
			[]string{"set", key, "status=in-progress", "--expect", seedID, "-m", "claim", "--as", "w1"},
			[]string{"set", key, "status=in-progress", "--expect", seedID, "-m", "claim", "--as", "w2"},
		)
		successes, losses := tallyRace(r1, r2)
		if successes != 1 || losses != 1 {
			t.Fatalf("round %d: want 1 success + 1 claim_lost, got successes=%d claim_lost=%d\n r1: code=%d out=%s err=%s\n r2: code=%d out=%s err=%s",
				i, successes, losses, r1.code, r1.stdout, r1.stderr, r2.code, r2.stdout, r2.stderr)
		}
		if r1.code == 0 {
			wins["w1"]++
		} else {
			wins["w2"]++
		}
	}
	if wins["w1"] == 0 || wins["w2"] == 0 {
		t.Fatalf("race was not genuinely contested across rounds — one side never won: %v", wins)
	}
}

// TestRaceFirstEdgeExpectNone is spec test 5's first-edge case: 10 rounds,
// each racing two writers seeding the SAME never-before-written key's
// blocked-by edge, both carrying `--expect none`. Exactly one may win the
// name; the loser gets the collision (not mismatch) claim_lost hint.
func TestRaceFirstEdgeExpectNone(t *testing.T) {
	dir := raceSetupReady(t)
	so, se, code := execLedger(t, dir, "set", "dep", "status=open", "--expect", "none", "-m", "dep", "--as", "a")
	if code != 0 {
		t.Fatalf("seed dep: %s %s", so, se)
	}

	wins := map[string]int{}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("edge-%d", i)
		r1, r2 := raceLedger(t, dir,
			[]string{"set", key, "blocked-by=dep", "--expect", "none", "--as", "w1"},
			[]string{"set", key, "blocked-by=dep", "--expect", "none", "--as", "w2"},
		)
		successes, losses := tallyRace(r1, r2)
		if successes != 1 || losses != 1 {
			t.Fatalf("round %d: want 1 success + 1 claim_lost, got successes=%d claim_lost=%d\n r1: code=%d out=%s err=%s\n r2: code=%d out=%s err=%s",
				i, successes, losses, r1.code, r1.stdout, r1.stderr, r2.code, r2.stdout, r2.stderr)
		}
		var loser raceResult
		if r1.code == 0 {
			wins["w1"]++
			loser = r2
		} else {
			wins["w2"]++
			loser = r1
		}
		if !strings.Contains(loser.stderr, "this key already has edges") {
			t.Fatalf("round %d: loser must get the first-edge collision hint, not the mismatch hint: %s", i, loser.stderr)
		}
	}
	if wins["w1"] == 0 || wins["w2"] == 0 {
		t.Fatalf("race was not genuinely contested across rounds — one side never won: %v", wins)
	}
}

// TestRaceLabelsFieldScopedAgainstStatus is spec test 4's field-scoping
// claim, mechanized: 10 rounds, each racing an unguarded first-ever labels
// write against a guarded status claim on the SAME key. Field-scoped CAS
// means these never contend — zero claim_lost must appear on either side,
// across every round, regardless of how the two processes interleave.
func TestRaceLabelsFieldScopedAgainstStatus(t *testing.T) {
	dir := raceSetupReady(t)
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("scoped-%d", i)
		so, se, code := execLedger(t, dir, "set", key, "status=open", "--expect", "none", "-m", "seed", "--as", "seeder")
		if code != 0 {
			t.Fatalf("round %d: seed failed: %s %s", i, so, se)
		}
		seedID := mustJSON(t, so)["id"].(string)

		r1, r2 := raceLedger(t, dir,
			[]string{"set", key, "status=in-progress", "--expect", seedID, "-m", "claim", "--as", "claimer"},
			[]string{"set", key, "labels=urgent", "--expect", "none", "--as", "labeler"},
		)
		for _, r := range []raceResult{r1, r2} {
			if r.code != 0 {
				t.Fatalf("round %d: field-scoped writes must never contend, got code=%d out=%s err=%s", i, r.code, r.stdout, r.stderr)
			}
			if strings.Contains(r.stderr, "claim_lost") {
				t.Fatalf("round %d: zero claim_lost expected from field-scoped writes, got: %s", i, r.stderr)
			}
		}
	}
}

// TestRaceLabelEditsWithExpectSerialize is spec test 4's label-edit clause:
// 10 rounds, each racing two --expect-carrying edits of the SAME labels
// value on the same key. Field-scoped CAS still applies within a single
// field — an unguarded field carrying --expect is real CAS (rule 3's
// non-guarded clause), so exactly one edit lands and the other claim_losts.
func TestRaceLabelEditsWithExpectSerialize(t *testing.T) {
	dir := raceSetupReady(t)
	wins := map[string]int{}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("labeledit-%d", i)
		so, se, code := execLedger(t, dir, "set", key, "labels=seed", "--expect", "none", "--as", "seeder")
		if code != 0 {
			t.Fatalf("round %d: seed failed: %s %s", i, so, se)
		}
		id1 := mustJSON(t, so)["id"].(string)

		r1, r2 := raceLedger(t, dir,
			[]string{"set", key, "labels=seed,w1", "--expect", id1, "--as", "w1"},
			[]string{"set", key, "labels=seed,w2", "--expect", id1, "--as", "w2"},
		)
		successes, losses := tallyRace(r1, r2)
		if successes != 1 || losses != 1 {
			t.Fatalf("round %d: want 1 success + 1 claim_lost, got successes=%d claim_lost=%d\n r1: code=%d out=%s err=%s\n r2: code=%d out=%s err=%s",
				i, successes, losses, r1.code, r1.stdout, r1.stderr, r2.code, r2.stdout, r2.stderr)
		}
		if r1.code == 0 {
			wins["w1"]++
		} else {
			wins["w2"]++
		}
	}
	if wins["w1"] == 0 || wins["w2"] == 0 {
		t.Fatalf("race was not genuinely contested across rounds — one side never won: %v", wins)
	}
}

// TestRaceReclaimStaleClaim is the trial-3/4 field scenario, mechanized: 10
// rounds (bumped from the brief's 5 — at 5 rounds a true 50/50 split
// spuriously fails the win-split check below ~6.25% of the time (2×0.5^5);
// 10 rounds brings that down to ~0.2%, matching the other contested
// families' margin), each aging a live claim past a short --stale-after
// horizon (the staleness signal dissolves, so no --override is needed) and
// then racing two --expect-the-stale-claim's-id reclaims. Exactly one may
// win.
func TestRaceReclaimStaleClaim(t *testing.T) {
	dir := raceSetupReadyStale(t, "300ms")
	wins := map[string]int{}
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("stale-%d", i)
		so, se, code := execLedger(t, dir, "set", key, "status=open", "--expect", "none", "-m", "seed", "--as", "seeder")
		if code != 0 {
			t.Fatalf("round %d: seed failed: %s %s", i, so, se)
		}
		seedID := mustJSON(t, so)["id"].(string)

		so2, se2, code := execLedger(t, dir, "set", key, "status=in-progress", "--expect", seedID, "-m", "claiming", "--as", "alice")
		if code != 0 {
			t.Fatalf("round %d: claim failed: %s %s", i, so2, se2)
		}
		claimID := mustJSON(t, so2)["id"].(string)

		time.Sleep(400 * time.Millisecond) // past the 300ms stale horizon

		r1, r2 := raceLedger(t, dir,
			[]string{"set", key, "status=in-progress", "--expect", claimID, "-m", "reclaim", "--as", "bob1"},
			[]string{"set", key, "status=in-progress", "--expect", claimID, "-m", "reclaim", "--as", "bob2"},
		)
		successes, losses := tallyRace(r1, r2)
		if successes != 1 || losses != 1 {
			t.Fatalf("round %d: want 1 success + 1 claim_lost, got successes=%d claim_lost=%d\n r1: code=%d out=%s err=%s\n r2: code=%d out=%s err=%s",
				i, successes, losses, r1.code, r1.stdout, r1.stderr, r2.code, r2.stdout, r2.stderr)
		}
		for _, r := range []raceResult{r1, r2} {
			if r.code == 4 && strings.Contains(r.stderr, "needs_override") {
				t.Fatalf("round %d: a stale claim must reclaim without --override, got needs_override: %s", i, r.stderr)
			}
		}
		if r1.code == 0 {
			wins["bob1"]++
		} else {
			wins["bob2"]++
		}
	}
	if wins["bob1"] == 0 || wins["bob2"] == 0 {
		t.Fatalf("race was not genuinely contested across rounds — one side never won: %v", wins)
	}
}

// tallyRace counts successes and claim_lost failures across a raced pair.
func tallyRace(r1, r2 raceResult) (successes, claimLosses int) {
	for _, r := range []raceResult{r1, r2} {
		switch {
		case r.code == 0:
			successes++
		case r.code == 4 && strings.Contains(r.stderr, "claim_lost"):
			claimLosses++
		}
	}
	return
}
