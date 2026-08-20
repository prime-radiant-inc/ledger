// The standing determinism test (sync spec Addition 4): a covered verb's
// projection is a deterministic function of (chain, evaluation time),
// independent of the git commit graph that happens to carry the chain. This
// file builds two replicas of ONE event set whose merge STRUCTURE and
// merge-PARENT ORDER differ, then drives the built binary as a real
// subprocess — under a clean baseline and a perturbed TZ/LC_ALL/HOME/USER
// environment, on both the pipe-JSON sink and a forced-TTY sink — and
// byte-diffs every covered verb's output between the two replicas. Any
// difference is a real bug: the replicas hold the same chain, so nothing
// about which commit graph carries it may leak into a render.
package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/creack/pty"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/store"
)

// determinismReplicas builds one shared chain — a root, then SIX real
// events on three sibling branches off the root (task-1 claimed twice,
// concurrently, by alice and bob — a genuine contested pair, since the
// cover-set pass's write-heads determination is exactly the kind of logic
// that could accidentally depend on merge shape — and RENAMED twice,
// concurrently, by alice and carol, which contests the pass's second
// stream and makes task-1 a renamed key in every projection; task-2 opened
// and then annotated by carol) — and assembles it into two replicas whose sentinel
// merges differ in BOTH structure and parent order:
//
//	replica A: one flat 3-parent (octopus) sentinel: -p alice -p bob -p carol
//	replica B: two nested sentinels, swapped order: merge(carol,bob) then
//	           merge(that, alice)
//
// The branch commits themselves are built ONCE (in a staging store) and the
// staging store's entire object database is then copied byte-for-byte into
// both replica directories, so every non-sentinel event is the SAME git
// object (same sha, same content) in both replicas — sentinels are the only
// thing that differs, and sentinels are invisible to every covered verb.
// cursor is the root event's id: present in both replicas at the identical
// sha (it predates the fork, and was physically copied, not rebuilt), so
// it's a valid fixed cursor for `since` on either replica.
func determinismReplicas(t *testing.T) (replicaA, replicaB, cursor string) {
	t.Helper()
	origin := initRepo(t)
	createOut := mustRun(t, origin, "create", "board", "--scope", "determinism test",
		"--field", "status=open,in-progress,done,failed",
		"--terminal", "status=done,failed",
		"--guard", "status", "--multi-field", "labels",
		"--stale-after", "2h", "--as", "root-creator")
	root := mustJSON(t, createOut)["id"].(string)

	forkPoint := git(t, origin, "rev-parse", store.Ref("board"))
	s := store.Store{Repo: gitx.Repo{Dir: origin}}

	aTip, err := s.BuildCommit("board", forkPoint, model.Event{
		TS: "2026-08-17T11:00:00.000", Type: "set", Key: "task-1",
		Fields: map[string]string{"status": "in-progress"}, Author: "alice",
	}, nil)
	if err != nil {
		t.Fatalf("build alice's claim: %v", err)
	}
	bTip, err := s.BuildCommit("board", forkPoint, model.Event{
		TS: "2026-08-17T11:05:00.000", Type: "set", Key: "task-1",
		Fields: map[string]string{"status": "in-progress"}, Author: "bob",
	}, nil)
	if err != nil {
		t.Fatalf("build bob's concurrent claim: %v", err)
	}
	cTip1, err := s.BuildCommit("board", forkPoint, model.Event{
		TS: "2026-08-17T11:10:00.000", Type: "set", Key: "task-2",
		Fields: map[string]string{"status": "open"}, Author: "carol",
	}, nil)
	if err != nil {
		t.Fatalf("build carol's task-2: %v", err)
	}
	cTip2, err := s.BuildCommit("board", cTip1, model.Event{
		TS: "2026-08-17T11:12:00.000", Type: "note", Key: "task-1", Kind: "handoff",
		Text: "branch note body\nsecond line", Author: "carol",
	}, nil)
	if err != nil {
		t.Fatalf("build carol's note: %v", err)
	}
	// The title stream, raced across the same partition: a renamed key in
	// the fixture (so every projection renders a renamed title) that is also
	// a live title contest (so the cover-set pass runs both streams under
	// two different merge shapes).
	aTip2, err := s.BuildCommit("board", aTip, model.Event{
		TS: "2026-08-17T11:20:00.000", Type: "set", Key: "task-1",
		Rename: "task one, alice's title", Author: "alice",
	}, nil)
	if err != nil {
		t.Fatalf("build alice's rename: %v", err)
	}
	cTip3, err := s.BuildCommit("board", cTip2, model.Event{
		TS: "2026-08-17T11:25:00.000", Type: "set", Key: "task-1",
		Rename: "task one, carol's title", Author: "carol",
	}, nil)
	if err != nil {
		t.Fatalf("build carol's concurrent rename: %v", err)
	}

	replicaA = filepath.Join(t.TempDir(), "replica-a")
	replicaB = filepath.Join(t.TempDir(), "replica-b")
	copyDir(t, origin, replicaA)
	copyDir(t, origin, replicaB)

	sa := store.Store{Repo: gitx.Repo{Dir: replicaA}}
	octopus, err := sa.BuildMerge([]string{aTip2, bTip, cTip3},
		model.Event{TS: "2026-08-17T12:00:00.000", Type: "sync", Author: "host-a"})
	if err != nil {
		t.Fatalf("build replica A's octopus sentinel: %v", err)
	}
	git(t, replicaA, "update-ref", store.Ref("board"), octopus, forkPoint)

	sb := store.Store{Repo: gitx.Repo{Dir: replicaB}}
	m1, err := sb.BuildMerge([]string{cTip3, bTip},
		model.Event{TS: "2026-08-17T12:30:00.000", Type: "sync", Author: "host-b1"})
	if err != nil {
		t.Fatalf("build replica B's first sentinel: %v", err)
	}
	m2, err := sb.BuildMerge([]string{m1, aTip2},
		model.Event{TS: "2026-08-17T12:45:00.000", Type: "sync", Author: "host-b2"})
	if err != nil {
		t.Fatalf("build replica B's second sentinel: %v", err)
	}
	git(t, replicaB, "update-ref", store.Ref("board"), m2, forkPoint)

	if got := git(t, replicaA, "rev-parse", store.Ref("board")); got != octopus {
		t.Fatalf("replica A's ref didn't land on the octopus sentinel: %s", got)
	}
	if got := git(t, replicaB, "rev-parse", store.Ref("board")); got != m2 {
		t.Fatalf("replica B's ref didn't land on the nested sentinel: %s", got)
	}
	// The two replicas' sentinel tips must actually differ — otherwise this
	// test would be vacuous (byte-diffing two runs of the identical ref).
	if octopus == m2 {
		t.Fatal("setup bug: the two replicas' sentinel tips must differ")
	}
	// ...and the fixture must genuinely carry a renamed key and a live
	// contest on BOTH streams, or the pass's most order-sensitive work goes
	// unmeasured while the byte-diff still passes.
	probe, _, code := runPiped(t, replicaA, nil, "ready", "--ledger", "board", "--at", "2030-01-01T00:00:00.000")
	if code != 0 {
		t.Fatalf("fixture probe: exit %d\n%s", code, probe)
	}
	for _, want := range []string{`"renamed"`, `"field": "title"`, `"field": "status"`} {
		if !strings.Contains(probe, want) {
			t.Fatalf("setup bug: the fixture must carry %s in its projection:\n%s", want, probe)
		}
	}

	return replicaA, replicaB, root
}

// copyDir physically duplicates src's entire tree (including .git) into
// dst, which must not yet exist — the mechanism that gives both replicas
// the SAME git objects (byte-identical shas) for every real event, rather
// than rebuilding them per replica and risking incidental divergence (e.g.
// commit dates) that has nothing to do with the thing under test.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	out, err := exec.Command("cp", "-R", src+"/.", dst).CombinedOutput()
	if err != nil {
		t.Fatalf("cp -R %s %s: %v\n%s", src, dst, err, out)
	}
}

// freshClone fetches slug's ref alone into a brand-new, empty repository —
// a genuinely fresh object store (via git's normal fetch negotiation, never
// a filesystem copy), guarding against a bug that only a repack/re-receive
// of the objects would surface.
func freshClone(t *testing.T, from, slug string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, "", "init", "-q", "-b", "main", dir)
	ref := store.Ref(slug)
	git(t, dir, "fetch", "-q", from, ref+":"+ref)
	return dir
}

// ---- environment perturbation ----

type envCase struct {
	name string
	env  []string // extra KEY=VALUE entries; nil means "inherit unperturbed"
}

func envCases(t *testing.T) []envCase {
	return []envCase{
		{name: "baseline", env: nil},
		{name: "perturbed", env: []string{
			"TZ=Asia/Katmandu",
			"LC_ALL=fr_FR.UTF-8",
			"HOME=" + t.TempDir(),
			"USER=other",
		}},
	}
}

// ---- subprocess drivers: the two sinks ----

// runPiped runs the built binary with plain pipes for stdout/stderr — the
// JSON sink (out.Emit's non-TTY branch, since os.Stdout attached to a pipe
// is never a character device).
func runPiped(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binPath, append([]string{"--store", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	return so.String(), se.String(), exitCode(err)
}

// runTTY runs the built binary with stdout attached to a real pty slave —
// the only way to make out.IsTTY (a Stat().Mode() character-device check on
// the live os.Stdout fd) observe a terminal in a genuinely separate
// process. stderr stays a plain pipe: ctx.TTY is decided from stdout alone
// (root.go), and the standing test's projection comparison never depends
// on stderr (freshness's TTY line is explicitly outside the projection).
func runTTY(t *testing.T, dir string, env []string, args ...string) (stdout string, code int) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()

	cmd := exec.Command(binPath, append([]string{"--store", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = tty
	var se bytes.Buffer
	cmd.Stderr = &se

	if err := cmd.Start(); err != nil {
		t.Fatalf("start under pty: %v", err)
	}
	// The parent's own reference to the slave must close so the master's
	// read can observe EOF/EIO once the child's copy closes too (on
	// process exit) — otherwise the master never sees the pty drain.
	if err := tty.Close(); err != nil {
		t.Fatalf("close pty slave: %v", err)
	}

	out := make(chan []byte, 1)
	go func() {
		b, _ := drainPTY(ptmx)
		out <- b
	}()
	err = cmd.Wait()
	return string(<-out), exitCode(err)
}

// drainPTY reads a pty master to completion. Linux reports the slave's
// final close as EIO on the master's read, not a clean io.EOF (a
// long-documented pty quirk); macOS/BSD report a plain EOF. Both are
// "there is no more output", never a real error.
func drainPTY(f *os.File) ([]byte, error) {
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buf.Bytes(), nil
			}
			var pe *fs.PathError
			if errors.As(err, &pe) && errors.Is(pe.Err, syscall.EIO) {
				return buf.Bytes(), nil
			}
			return buf.Bytes(), err
		}
	}
}

// diffOrFail byte-compares two replicas' output for one label (verb + sink
// + env combination), failing with both payloads on any difference.
func diffOrFail(t *testing.T, label, a, b string) {
	t.Helper()
	if a != b {
		t.Fatalf("%s: replicas diverged despite holding the same chain (different merge shape/parent order only):\nreplica A: %s\nreplica B: %s",
			label, a, b)
	}
}

// canonicalWithoutCursor re-marshals since's JSON payload with the
// excluded `cursor` field removed (Addition 4 rev 8: a replica-local
// resume token, the ref tip by the pager law — a sentinel sha after
// divergence — never chain content), asserting it was present
// beforehand so a caller can't accidentally pass an already-broken
// payload and have this helper mask it as a pass.
func canonicalWithoutCursor(t *testing.T, doc string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("since payload not JSON: %v\n%s", err, doc)
	}
	if _, present := m["cursor"]; !present {
		t.Fatalf("since payload must carry a cursor field pre-deletion: %s", doc)
	}
	delete(m, "cursor")
	canon, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		t.Fatalf("re-marshal since payload: %v", err)
	}
	return string(canon)
}

// mustExit0 fails the test if a subprocess run didn't exit cleanly —
// every invocation in the standing test is a happy-path read against a
// valid chain.
func mustExit0(t *testing.T, label string, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("%s: exit %d\nstdout: %s\nstderr: %s", label, code, stdout, stderr)
	}
}

// TestDeterminismAcrossMergeShapesEnvironmentsAndSinks is the sync spec's
// Addition 4 standing test, verbatim: every covered verb (show, status,
// tail, notes, ready, render, since), on both sinks, under a clean baseline
// and a perturbed TZ/LC_ALL/HOME/USER environment, byte-identical across
// two hand-built replicas of the same chain that differ only in merge
// structure and merge-parent order — plus a fresh-clone re-fold.
//
// since's emitted resume `cursor` field is handled separately (rev 8): this
// test's first run caught it diverging, since an unpaged drain's cursor is
// the ref tip (the pager law, Task 5), a SENTINEL sha after divergence —
// and sentinel structure is replica-local by this very section's own
// definition of "chain". Addition 4 rev 8 excludes the cursor field from
// the projection for exactly that reason: it's a replica-local resume
// token, not chain content. The delivered `events` array is the promise,
// and it's covered by the ordinary byte-diff below like every other verb.
func TestDeterminismAcrossMergeShapesEnvironmentsAndSinks(t *testing.T) {
	replicaA, replicaB, cursor := determinismReplicas(t)
	const at = "2030-01-01T00:00:00.000" // comfortably after every fixture event

	// verbCases is every covered verb's invocation, EXCEPT render and since
	// (both handled separately below): render's interesting output is the
	// written FILE, not its stdout confirmation, which echoes an
	// invocation-specific --to path that legitimately differs run to run;
	// since's piped sink carries the excluded cursor field, so it needs
	// the field stripped before a plain byte-diff applies.
	verbCases := [][]string{
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
		{"tail", "--ledger", "board", "--raw", "-n", "50"},
		{"notes", "--ledger", "board"},
		{"notes", "--ledger", "board", "--latest", "--at", at},
		{"ready", "--ledger", "board", "--at", at},
	}

	for _, ec := range envCases(t) {
		t.Run(ec.name, func(t *testing.T) {
			for _, args := range verbCases {
				label := ec.name + " piped " + strings.Join(args, " ")
				soA, seA, codeA := runPiped(t, replicaA, ec.env, args...)
				mustExit0(t, label+" (replica A)", codeA, soA, seA)
				soB, seB, codeB := runPiped(t, replicaB, ec.env, args...)
				mustExit0(t, label+" (replica B)", codeB, soB, seB)
				diffOrFail(t, label, soA, soB)

				labelTTY := ec.name + " tty " + strings.Join(args, " ")
				ttyA, codeA := runTTY(t, replicaA, ec.env, args...)
				mustExit0(t, labelTTY+" (replica A)", codeA, ttyA, "")
				ttyB, codeB := runTTY(t, replicaB, ec.env, args...)
				mustExit0(t, labelTTY+" (replica B)", codeB, ttyB, "")
				diffOrFail(t, labelTTY, ttyA, ttyB)
			}

			// render: byte-diff the WRITTEN FILE (the spec's explicit
			// callout), under both sinks, since render's file content must
			// not depend on whether its own invocation happened to be
			// piped or TTY-driven either.
			for _, sink := range []string{"piped", "tty"} {
				pathA := filepath.Join(t.TempDir(), "render-a.txt")
				pathB := filepath.Join(t.TempDir(), "render-b.txt")
				var codeA, codeB int
				if sink == "piped" {
					var soA, seA, soB, seB string
					soA, seA, codeA = runPiped(t, replicaA, ec.env, "render", "--to", pathA, "--ledger", "board")
					mustExit0(t, ec.name+" piped render (replica A)", codeA, soA, seA)
					soB, seB, codeB = runPiped(t, replicaB, ec.env, "render", "--to", pathB, "--ledger", "board")
					mustExit0(t, ec.name+" piped render (replica B)", codeB, soB, seB)
				} else {
					var ttyA, ttyB string
					ttyA, codeA = runTTY(t, replicaA, ec.env, "render", "--to", pathA, "--ledger", "board")
					mustExit0(t, ec.name+" tty render (replica A)", codeA, ttyA, "")
					ttyB, codeB = runTTY(t, replicaB, ec.env, "render", "--to", pathB, "--ledger", "board")
					mustExit0(t, ec.name+" tty render (replica B)", codeB, ttyB, "")
				}
				fileA, err := os.ReadFile(pathA)
				if err != nil {
					t.Fatalf("read replica A's render: %v", err)
				}
				fileB, err := os.ReadFile(pathB)
				if err != nil {
					t.Fatalf("read replica B's render: %v", err)
				}
				diffOrFail(t, ec.name+" "+sink+" render file", string(fileA), string(fileB))
			}

			// since: the piped sink's document carries the excluded
			// `cursor` field (Addition 4 rev 8) — present on both replicas
			// pre-deletion (asserted), then stripped before the byte-diff,
			// same mechanism TestDeterminismFreshnessExcludedFromProjection
			// uses for the freshness key. The TTY sink renders plain event
			// lines with no cursor at all, so it byte-diffs directly.
			sinceLabel := ec.name + " piped since " + cursor
			soA, seA, codeA := runPiped(t, replicaA, ec.env, "since", cursor, "--ledger", "board")
			mustExit0(t, sinceLabel+" (replica A)", codeA, soA, seA)
			soB, seB, codeB := runPiped(t, replicaB, ec.env, "since", cursor, "--ledger", "board")
			mustExit0(t, sinceLabel+" (replica B)", codeB, soB, seB)
			diffOrFail(t, sinceLabel, canonicalWithoutCursor(t, soA), canonicalWithoutCursor(t, soB))

			sinceLabelTTY := ec.name + " tty since " + cursor
			ttySinceA, codeA := runTTY(t, replicaA, ec.env, "since", cursor, "--ledger", "board")
			mustExit0(t, sinceLabelTTY+" (replica A)", codeA, ttySinceA, "")
			ttySinceB, codeB := runTTY(t, replicaB, ec.env, "since", cursor, "--ledger", "board")
			mustExit0(t, sinceLabelTTY+" (replica B)", codeB, ttySinceB, "")
			diffOrFail(t, sinceLabelTTY, ttySinceA, ttySinceB)
		})
	}

	// Fresh-clone re-fold: a brand-new object store, fetched (never
	// filesystem-copied) from replica A, must re-fold to the exact same
	// projection as replica A itself.
	fresh := freshClone(t, replicaA, "board")
	for _, args := range verbCases {
		soFresh, seFresh, codeFresh := runPiped(t, fresh, nil, args...)
		mustExit0(t, "fresh clone "+strings.Join(args, " "), codeFresh, soFresh, seFresh)
		soA, seA, codeA := runPiped(t, replicaA, nil, args...)
		mustExit0(t, "replica A "+strings.Join(args, " "), codeA, soA, seA)
		diffOrFail(t, "fresh-clone re-fold "+strings.Join(args, " "), soFresh, soA)
	}
	soFresh, seFresh, codeFresh := runPiped(t, fresh, nil, "since", cursor, "--ledger", "board")
	mustExit0(t, "fresh clone since "+cursor, codeFresh, soFresh, seFresh)
	soA, seA, codeA := runPiped(t, replicaA, nil, "since", cursor, "--ledger", "board")
	mustExit0(t, "replica A since "+cursor, codeA, soA, seA)
	diffOrFail(t, "fresh-clone re-fold since "+cursor, canonicalWithoutCursor(t, soFresh), canonicalWithoutCursor(t, soA))
}

// TestDeterminismFreshnessExcludedFromProjection verifies the spec's
// placement pin the other standing-test bullet calls out: the freshness
// key lives OUTSIDE the covered projection. Reuses the two merge-shape
// replicas (already proven byte-identical by the test above) and stages a
// fetched-but-unmerged tracking ref on replica A ALONE — its LOCAL chain
// never changes, only its tracking ref does — so any remaining difference
// against replica B's untouched output can only be the freshness key
// itself.
func TestDeterminismFreshnessExcludedFromProjection(t *testing.T) {
	replicaA, replicaB, _ := determinismReplicas(t)

	// Stage a remote that's one real event ahead of replica A's own chain,
	// fetch it into A's tracking ref (raw git — never `chit sync`, which
	// would merge it and erase the very state under test), and leave A's
	// local ref untouched.
	bareRemote := filepath.Join(t.TempDir(), "remote.git")
	git(t, "", "init", "--bare", "-q", bareRemote)
	git(t, replicaA, "push", "-q", bareRemote, store.Ref("board")+":"+store.Ref("board"))

	aHead := git(t, replicaA, "rev-parse", store.Ref("board"))
	remoteStore := store.Store{Repo: gitx.Repo{Dir: bareRemote}}
	advanced, err := remoteStore.BuildCommit("board", aHead, model.Event{
		TS: "2026-08-17T13:00:00.000", Type: "set", Key: "task-2",
		Fields: map[string]string{"status": "in-progress"}, Author: "dana",
	}, nil)
	if err != nil {
		t.Fatalf("advance the staged remote: %v", err)
	}
	git(t, bareRemote, "update-ref", store.Ref("board"), advanced, aHead)

	git(t, replicaA, "remote", "add", "stagingremote", bareRemote)
	rawFetchTracking(t, replicaA, "stagingremote")

	// replica A's own local ref must be untouched by the raw fetch.
	if got := git(t, replicaA, "rev-parse", store.Ref("board")); got != aHead {
		t.Fatalf("raw fetch must not move replica A's local ref: %s != %s", got, aHead)
	}

	for _, args := range [][]string{
		{"show", "--ledger", "board"},
		{"status", "--ledger", "board"},
		{"ready", "--ledger", "board", "--at", "2030-01-01T00:00:00.000"},
	} {
		soA, seA, codeA := runPiped(t, replicaA, nil, args...)
		mustExit0(t, "freshness-warned replica A "+strings.Join(args, " "), codeA, soA, seA)
		soB, seB, codeB := runPiped(t, replicaB, nil, args...)
		mustExit0(t, "clean replica B "+strings.Join(args, " "), codeB, soB, seB)

		var docA, docB map[string]any
		if err := json.Unmarshal([]byte(soA), &docA); err != nil {
			t.Fatalf("%v: replica A payload not JSON: %v\n%s", args, err, soA)
		}
		if err := json.Unmarshal([]byte(soB), &docB); err != nil {
			t.Fatalf("%v: replica B payload not JSON: %v\n%s", args, err, soB)
		}
		if _, present := docA["freshness"]; !present {
			t.Fatalf("%v: replica A (fetched-but-unmerged) must carry a freshness key: %s", args, soA)
		}
		if _, present := docB["freshness"]; present {
			t.Fatalf("%v: replica B (untouched) must carry no freshness key: %s", args, soB)
		}
		delete(docA, "freshness")

		canonA, _ := json.MarshalIndent(docA, "", " ")
		canonB, _ := json.MarshalIndent(docB, "", " ")
		diffOrFail(t, strings.Join(args, " ")+" (freshness stripped)", string(canonA), string(canonB))
	}
}
