package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Board is the board side of the bridge, reached ONLY through the `ledger`
// CLI as a subprocess (spec rule, v1). Board doctrine — CAS, standing
// signals, evidence — binds the bridge exactly as it binds any agent, so
// every write here goes through the same doors an agent's would.
type Board struct {
	Bin   string // path to the ledger binary
	Slug  string
	Store string // --store, empty to let ledger resolve
}

// openValue is the board value a seed and an inbound reopen write. There is
// no flag for it: `ledger create` PINS a ready-capable board's non-terminal
// status vocabulary to exactly {open, in-progress} (probed — a third
// non-terminal value is refused at create), so reopen ⇒ `open`, always.
const openValue = "open"

// BoardErr is a ledger CLI error with its machine-readable identifier — the
// bridge's whole decision logic (claim_lost, needs_override,
// reset_required, partial_failure) keys off it, never off English prose.
type BoardErr struct {
	Code, Message, Hint string
	// Signals is the needs_override document's machine-readable signal
	// names (tool rev 16). Law 5 turns on the DISTINCTION between `human`
	// and `claim`/`settled`.
	//
	// The field FAILS CLOSED: see autoOverridable. A reader that failed
	// open auto-overrode the one write Law 3 exists to prevent, against any
	// pre-rev-16 binary.
	Signals []string
	// Outcomes is the per-slug outcome list sync/push write on their exit-3
	// partial_failure document, which lets the bridge scope its abort to its
	// OWN slug instead of coupling its availability to every remote in the
	// operator's store.
	Outcomes []SlugOutcome
	Raw      string
}

// SlugOutcome mirrors the tool's per-slug sync/push result.
type SlugOutcome struct {
	Slug   string `json:"slug"`
	Result string `json:"result"`
	Detail string `json:"detail,omitempty"`
}

func (o SlugOutcome) failed() bool {
	return o.Result == "refused" || o.Result == "failed" || o.Result == "rejected"
}

func (e *BoardErr) Error() string {
	if e.Code == "" {
		return e.Raw
	}
	return e.Code + ": " + e.Message
}

func code(err error) string {
	be, ok := err.(*BoardErr)
	if !ok {
		return ""
	}
	return be.Code
}

func (e *BoardErr) hasSignal(name string) bool {
	for _, s := range e.Signals {
		if s == name {
			return true
		}
	}
	return false
}

// autoOverridable is Law 5's whole ruling on a needs_override, and it FAILS
// CLOSED in both of the ways it can be uncertain.
//
//   - `claim`/`settled`: a real person's decision, tool-recorded for triage
//     — auto-override, attributed to the GitHub actor.
//   - `human`: NEVER. That is Law 3's refusal path (login↔label identity
//     mapping is v2).
//   - NO signals at all: UNKNOWN, so refusal. An empty read against a
//     pre-rev-16 binary is exactly the document that has no `signals` field,
//     and reading it as "no human signal, therefore override" auto-overrode
//     human stop signs. The startup capability probe is the operator's early
//     warning; this is the guard that does not depend on it.
//   - Any signal name this bridge does not know: also refusal. A future
//     signal must not be silently overridden by an old bridge.
func (e *BoardErr) autoOverridable() bool {
	if len(e.Signals) == 0 {
		return false
	}
	for _, s := range e.Signals {
		if s != "claim" && s != "settled" {
			return false
		}
	}
	return true
}

func (b Board) args(extra []string) []string {
	args := []string{}
	if b.Store != "" {
		args = append(args, "--store", b.Store)
	}
	args = append(args, extra...)
	// Read and data verbs address their ledger by flag; the bridge always
	// names it rather than relying on ambient resolution, since a bridge run
	// in a repo with two open ledgers must never guess.
	return append(args, "--ledger", b.Slug)
}

// run invokes one ledger verb and decodes its JSON envelope. stdout is
// always JSON here: the subprocess has no TTY, which is exactly when the
// tool's JSON-by-default rule applies.
func (b Board) run(extra ...string) (map[string]any, error) {
	return b.exec(b.args(extra))
}

// runBare invokes a verb that takes no --ledger flag (sync, push: they
// address slugs positionally or not at all).
func (b Board) runBare(extra ...string) (map[string]any, error) {
	args := []string{}
	if b.Store != "" {
		args = append(args, "--store", b.Store)
	}
	return b.exec(append(args, extra...))
}

func (b Board) exec(args []string) (map[string]any, error) {
	cmd := exec.Command(b.Bin, args...)
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	if err != nil {
		be := &BoardErr{Raw: strings.TrimSpace(se.String() + so.String())}
		var doc map[string]any
		// Most failures put {error,message,hint} on stderr. sync/push's
		// partial_failure is the exception: the payload (carrying
		// error:"partial_failure" and the per-slug outcomes) goes to STDOUT
		// and the exit code alone is the failure, so both streams have to be
		// tried before the error is called unstructured.
		if json.Unmarshal([]byte(se.String()), &doc) != nil {
			doc = nil
			if json.Unmarshal([]byte(so.String()), &doc) != nil {
				doc = nil
			}
		}
		if doc != nil {
			be.Code, _ = doc["error"].(string)
			be.Message, _ = doc["message"].(string)
			be.Hint, _ = doc["hint"].(string)
			if raw, ok := doc["signals"].([]any); ok {
				for _, s := range raw {
					if name, ok := s.(string); ok {
						be.Signals = append(be.Signals, name)
					}
				}
			}
			for _, verb := range []string{"synced", "pushed"} {
				blob, mErr := json.Marshal(doc[verb])
				if mErr != nil {
					continue
				}
				var outcomes []SlugOutcome
				if json.Unmarshal(blob, &outcomes) == nil && len(outcomes) > 0 {
					be.Outcomes = outcomes
				}
			}
		}
		if be.Raw == "" {
			be.Raw = fmt.Sprintf("ledger %s: %v", strings.Join(args, " "), err)
		}
		return nil, be
	}
	var doc map[string]any
	if e := json.Unmarshal([]byte(so.String()), &doc); e != nil {
		return nil, fmt.Errorf("ledger %s: undecodable output: %s", strings.Join(args, " "), so.String())
	}
	return doc, nil
}

// CheckCapable is the PRE-SYNC binary capability probe: does this `ledger`
// understand the rename event, i.e. is it tool rev 16 or later?
//
// It is a refusal by name rather than a runtime surprise because Law 5's
// `signals` field fails closed: against an old binary EVERY needs_override
// would carry no signals and take the refusal path, so the operator would
// watch a board full of handoff notes and wonder why nothing auto-overrides.
//
// The probe is a capability question, not a version-string comparison —
// source builds report `dev` — so it asks the one verb whose help text names
// the feature. `set --help` writes to stdout and touches no store.
func (b Board) CheckCapable() error {
	cmd := exec.Command(b.Bin, "set", "--help")
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cannot run the ledger binary %q (%v): %s", b.Bin, err, strings.TrimSpace(se.String()))
	}
	if !strings.Contains(so.String()+se.String(), "--rename") {
		return fmt.Errorf("the ledger binary %q predates tool rev 16: `set --rename` is missing. "+
			"The bridge needs it for title mirroring, and rev 16's machine-readable `signals` in "+
			"needs_override documents for Law 5 — without it every guarded intake write takes the "+
			"refusal path. Upgrade ledger (ledger update), or point --ledger-bin at a rev-16 binary", b.Bin)
	}
	return nil
}

// KeyState is one board key as the bridge needs it: the current title (with
// the id of the rename event that set it — the CAS ticket for an intake
// rename) and the current status plus that status event's id — the CAS
// ticket every guarded write the bridge makes must carry.
type KeyState struct {
	Key      string
	Title    string
	RenameID string // latest rename event id, "" if never renamed
	Status   string
	StatusID string
}

// Snapshot folds `show` into per-key state and the board's declared field
// vocabulary. One read, whole board: the vocabulary refusals and the key
// states come from the same call.
func (b Board) Snapshot() (map[string]*KeyState, map[string][]string, error) {
	doc, err := b.run("show")
	if err != nil {
		return nil, nil, err
	}
	keys := map[string]*KeyState{}
	rows, _ := doc["rows"].([]any)
	for _, r := range rows {
		m, _ := r.(map[string]any)
		key, _ := m["key"].(string)
		if key == "" {
			continue
		}
		ks := keys[key]
		if ks == nil {
			ks = &KeyState{Key: key}
			keys[key] = ks
		}
		if t, ok := m["title"].(string); ok {
			ks.Title = t
		}
		if rn, ok := m["renamed"].(map[string]any); ok {
			ks.RenameID, _ = rn["id"].(string)
		}
		if f, _ := m["field"].(string); f == "status" {
			ks.Status, _ = m["value"].(string)
			ks.StatusID, _ = m["id"].(string)
		}
	}
	schema := map[string][]string{}
	if sc, ok := doc["schema"].(map[string]any); ok {
		for field, raw := range sc {
			vals, _ := raw.([]any)
			for _, v := range vals {
				if s, ok := v.(string); ok {
					schema[field] = append(schema[field], s)
				}
			}
		}
	}
	return keys, schema, nil
}

// CheckReadyCapable asks the tool's OWN ready-capability oracle. The
// terminality oracle below (declared − {open, in-progress}) is sound only
// because `create` pins the non-terminal vocabulary — and it pins it only on
// ready-capable boards. On a plain board the same vocabulary would read as
// terminal while `human` gates nothing, guards guard nothing, and Law 3's
// whole refusal path would silently never fire.
//
// `ready` is the cheapest read that answers it: no read verb surfaces
// `--terminal`/`--guard`, and `ready` refuses a plain board by name with the
// create-time fix in its hint.
func (b Board) CheckReadyCapable() error {
	_, err := b.run("ready")
	be, ok := err.(*BoardErr)
	if !ok {
		return err // nil, or something that is not a CLI refusal at all
	}
	if be.Code == "bad_usage" {
		return fmt.Errorf("board '%s' is not ready-capable, so the bridge refuses to run: %s. %s",
			b.Slug, be.Message, be.Hint)
	}
	return be
}

// notesOfKind returns every note of a kind, oldest first. `-n 0` is
// UNBOUNDED and mandatory: the default limit of 10 silently truncates the
// identity map at ten issues and mints duplicates for everything past it.
func (b Board) notesOfKind(kind string) ([]map[string]any, error) {
	return b.noteList("notes", "-k", kind, "-n", "0")
}

// NotesOnKey returns every note on one key, all kinds, oldest first — the
// input to Law 6's issue-creation backfill.
func (b Board) NotesOnKey(key string) ([]Note, error) {
	raw, err := b.noteList("notes", "--key", key, "-n", "0")
	if err != nil {
		return nil, err
	}
	notes := make([]Note, 0, len(raw))
	for _, m := range raw {
		n := Note{}
		n.ID, _ = m["id"].(string)
		n.Kind, _ = m["kind"].(string)
		n.Key, _ = m["key"].(string)
		n.Author, _ = m["by"].(string)
		n.Text, _ = m["text"].(string)
		notes = append(notes, n)
	}
	return notes, nil
}

// Note is a board note as the backfill needs it.
//
// There is deliberately no ImportedFrom here: `ledger notes` does not
// surface the field (only `since`/`tail`'s event documents do), so the
// bridge derives it from the whole-chain read it already holds — see
// Syncer.importedFromOf. A consumer WITHOUT that read cannot, which is a
// named tool-backlog item.
type Note struct {
	ID, Kind, Key, Author, Text string
}

func (b Board) noteList(extra ...string) ([]map[string]any, error) {
	doc, err := b.run(extra...)
	if err != nil {
		return nil, err
	}
	raw, _ := doc["notes"].([]any)
	notes := make([]map[string]any, 0, len(raw))
	for _, n := range raw {
		if m, ok := n.(map[string]any); ok {
			notes = append(notes, m)
		}
	}
	return notes, nil
}

// LinkMap is the identity map: the board's `github-link` notes are THE
// authority for key↔issue, in both directions.
//
// ByKey is ONE issue per key — the key's ESTABLISHED link, the oldest IN
// FOLD ORDER non-retracted `github-bridge`-authored link note. Oldest, never
// newest: newest-wins flips when a loser's note arrives on a later sync, and
// cannot coexist with "a changed link is refused, never repointed" (under
// newest-wins the repoint has already happened by the time you refuse it).
// Never by timestamp either — a skewed clock must not move a link.
//
// ByIssue is derived from ByKey ALONE, so an issue that is not some key's
// established link is not an inbound writer either: keeping every issue ever
// linked as an inbound writer produced an unbounded flip-flop minting a
// fabricated override per run.
//
// Changed records a key whose chain carries another link note naming a
// DIFFERENT issue. Never obeyed, warned every run until retracted.
//
// Foreign records link notes written under some other author. Authorship is
// asserted, not enforced (the tool's stated v1 trust model), so this closes
// the cheap board-side path, not the impersonation one: a note authored
// `github-bridge` by somebody else still counts, greppably.
type LinkMap struct {
	ByKey   map[string]int
	ByIssue map[int]string
	Changed map[string][]int
	Foreign []string // "key -> #n by author", for the warning
}

// Links reads the identity map off the chain. Two body shapes, both authored
// `github-bridge`:
//
//	github: issues/<n>            — a link
//	github: retracted issues/<n>  — a retraction
//
// Retraction is what makes a duplicate link RESOLVABLE. Under plain
// append-only oldest-wins a duplicate was permanently unresolvable: the
// established note can never be outvoted. Retraction removes a candidate
// from the set, established is the oldest of what remains, and the whole
// thing is a set union — deterministic and merge-stable however the notes
// interleave across replicas.
func (b Board) Links() (*LinkMap, error) {
	notes, err := b.notesOfKind(kindLink)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		key string
		num int
	}
	var order []candidate
	retracted := map[candidate]bool{}
	m := &LinkMap{ByKey: map[string]int{}, ByIssue: map[int]string{}, Changed: map[string][]int{}}
	for _, n := range notes {
		key, _ := n["key"].(string)
		body, _ := n["text"].(string)
		author, _ := n["by"].(string)
		num, isRetraction := parseLinkBody(body)
		if key == "" || num == 0 {
			continue
		}
		if author != bridgeAuthor {
			m.Foreign = append(m.Foreign, fmt.Sprintf("%s -> #%d by %s", key, num, author))
			continue
		}
		c := candidate{key, num}
		if isRetraction {
			retracted[c] = true
			continue
		}
		order = append(order, c)
	}
	for _, c := range order {
		if retracted[c] {
			continue
		}
		have, bound := m.ByKey[c.key]
		switch {
		case !bound:
			m.ByKey[c.key] = c.num
			m.ByIssue[c.num] = c.key
		case have == c.num:
			// The same link re-asserted (a re-run whose idempotency key did
			// not travel, a merge): nothing to resolve.
		default:
			m.Changed[c.key] = appendUnique(m.Changed[c.key], c.num)
		}
	}
	return m, nil
}

func appendUnique(list []int, n int) []int {
	for _, have := range list {
		if have == n {
			return list
		}
	}
	return append(list, n)
}

func parseLinkBody(body string) (n int, retraction bool) {
	line := strings.TrimSpace(strings.SplitN(body, "\n", 2)[0])
	rest, ok := strings.CutPrefix(line, linkRetractPrefix)
	if ok {
		retraction = true
	} else if rest, ok = strings.CutPrefix(line, linkPrefix); !ok {
		return 0, false
	}
	num, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return num, retraction
}

// LoadState reads the newest bridge-state note AUTHORED `github-bridge`:
// its last line is the state JSON, its first is the greppable
// `bridge-cursor: <sha>` line. A board with no state note yet yields a zero
// State — a first run drains from the beginning of the chain, which is
// exactly right.
//
// Note the two bookkeeping classes have OPPOSITE tie-breaks: `github-link`
// reads oldest-wins (a link must not move), `bridge-state` reads
// newest-wins (a cursor must).
//
// The author filter matters: an any-author last-write-wins read let one note
// from any board writer wedge the bridge's whole state. Foreign state notes
// are inert AND reported, so the poisoning is visible rather than merely
// ineffective. The whole note list is read (-n 0) rather than the newest
// one, because the newest note under this key may be exactly the foreign
// one.
func (b Board) LoadState() (*State, []string, error) {
	doc, err := b.run("notes", "-k", kindState, "--key", stateKey, "-n", "0")
	if err != nil {
		return nil, nil, err
	}
	raw, _ := doc["notes"].([]any)
	st := &State{}
	var foreign []string
	var newest map[string]any
	for _, r := range raw {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if author, _ := m["by"].(string); author != bridgeAuthor {
			foreign = append(foreign, fmt.Sprintf("%v by %s", m["id"], author))
			continue
		}
		newest = m
	}
	if newest == nil {
		return st, foreign, nil
	}
	body, _ := newest["text"].(string)
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), st); err != nil {
		return nil, foreign, fmt.Errorf("bridge-state note %v is not decodable: %w", newest["id"], err)
	}
	return st, foreign, nil
}

// Since drains the board's events after a cursor — the cursor contract, the
// bridge's only incremental input.
func (b Board) Since(cursor string) ([]Event, string, error) {
	extra := []string{"since"}
	if cursor != "" {
		extra = append(extra, cursor)
	}
	doc, err := b.run(extra...)
	if err != nil {
		return nil, "", err
	}
	blob, _ := json.Marshal(doc["events"])
	var evs []Event
	if err := json.Unmarshal(blob, &evs); err != nil {
		return nil, "", fmt.Errorf("undecodable since payload: %w", err)
	}
	next, _ := doc["cursor"].(string)
	return evs, next, nil
}

// idemScope is the tool's OWN dedupe scope for a note, verbatim:
// (author, kind, key, idempotency-key). The derived index must match it
// exactly or it is not an index of the same thing. The bare key string was
// probed as a censorship primitive — one decoy note under any author, kind
// or key silently suppressed a real comment's import AND deleted the
// `deduped: true` impersonation detector.
type idemScope struct{ author, kind, key, idem string }

// ChainIndex reads the whole chain once and returns the marker oracle's
// domain and every idempotency key already spent, scoped. ONE read serves
// both — Law 2's dedupe and the marker verification ask the same chain two
// questions.
//
// The ids are the ORACLE: a mirrored comment's marker names the board event
// it echoes, and a marker naming a real event is proof the bridge wrote it
// (no login is compared anywhere). The domain is `{id} ∪ {imported_from}`:
// `export`/`import` re-mints every event id and preserves the old one only
// in `imported_from`, so an id-only oracle goes blind on exactly the
// recovery path Law 2 calls safe. The bound is honest and stated:
// `imported_from` is SINGLE-HOP — the tool overwrites it on each import — so
// the oracle survives exactly ONE export/import round trip.
//
// The idempotency keys are what let Law 2 delete the stored high-water map:
// they are ON THE CHAIN already, so the same set is derived instead of
// stored — mergeable, and nothing to lose on a sentinel merge.
func (b Board) ChainIndex() (evs []Event, ids map[string]bool, idem map[idemScope]bool, err error) {
	evs, _, err = b.Since("")
	if err != nil {
		return nil, nil, nil, err
	}
	ids, idem = make(map[string]bool, len(evs)), map[idemScope]bool{}
	for _, ev := range evs {
		if ev.ID != "" {
			ids[ev.ID] = true
		}
		if ev.ImportedFrom != "" {
			ids[ev.ImportedFrom] = true
		}
		if ev.IdemKey != "" {
			idem[idemScope{ev.Author, ev.Kind, ev.Key, ev.IdemKey}] = true
		}
	}
	return evs, ids, idem, nil
}

// Sync is Law 1 step 1: fetch and merge remote ledger history before doing
// anything else. Two replicas that have both drifted mint duplicate GitHub
// issues for each other's keys, because their link notes have not met yet.
//
// `ledger sync` takes no slug — it syncs every tracked slug in the store (a
// slug-selective sync is a named tool-backlog item the bridge adopts when it
// exists). So a partial_failure must be SCOPED: a fleet store holds slugs
// the bridge has nothing to do with, and aborting because somebody else's
// board has a dead remote couples the bridge's availability to every remote
// in the operator's store. Abort iff OUR OWN slug failed; warn on the rest.
func (b Board) Sync() (mine error, others []SlugOutcome) {
	_, err := b.runBare("sync")
	if err == nil {
		return nil, nil
	}
	be, ok := err.(*BoardErr)
	if !ok || be.Code != "partial_failure" || len(be.Outcomes) == 0 {
		// Anything but a scoped partial failure is total: a sync that could
		// not run at all leaves the replica stale, which is how duplicate
		// issues get minted.
		return err, nil
	}
	for _, o := range be.Outcomes {
		if !o.failed() {
			continue
		}
		if o.Slug == b.Slug {
			mine = &BoardErr{Code: "partial_failure", Message: o.Slug + ": " + o.Result + " " + o.Detail}
			continue
		}
		others = append(others, o)
	}
	return mine, others
}

// Push is Law 1 step 6: publish this board — and ONLY this board, because a
// bare `push` publishes every local slug and that is the skill's privacy
// lever. Always, and last: link notes and bridge state that never leave the
// replica make the sync-first law protect nothing.
func (b Board) Push() error {
	_, err := b.runBare("push", b.Slug)
	return err
}

// Event is the subset of a ledger event the bridge acts on.
type Event struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Key    string            `json:"key"`
	Kind   string            `json:"kind"`
	Text   string            `json:"text"`
	Rename string            `json:"rename"`
	Fields map[string]string `json:"fields"`
	// ImportedFrom is the event's id BEFORE an export/import round trip.
	// Ids are re-minted at the boundary, so this is the only thread back to
	// a marker the bridge posted before the recovery — the second half of
	// the oracle's domain.
	ImportedFrom string   `json:"imported_from"`
	IdemKey      string   `json:"idempotency_key"`
	Evidence     []string `json:"evidence"`
	Author       string   `json:"author"`
	TS           string   `json:"ts"`
}

// ---- writes ----

// write runs a write verb and returns the new event id.
func (b Board) write(extra ...string) (string, error) {
	id, _, err := b.writeDetail(extra...)
	return id, err
}

// writeDetail also reports whether the write DEDUPED against an earlier
// event carrying the same idempotency key. `deduped: true` is part of the
// contract everywhere the bridge writes: a deduped write is NOT a write, or
// a converged run can never report zero.
func (b Board) writeDetail(extra ...string) (string, bool, error) {
	doc, err := b.run(extra...)
	if err != nil {
		return "", false, err
	}
	id, _ := doc["id"].(string)
	deduped, _ := doc["deduped"].(bool)
	return id, deduped, nil
}

// Seed opens a brand-new key at `open` with the GitHub issue's title as the
// seed message — which IS the key's title under Part A's fold rule.
func (b Board) Seed(key, title, as string) (string, error) {
	return b.write("set", key, "status="+openValue, "--expect", "none", "-m", title, "--as", as)
}

// Rename writes an intake rename, CAS'd against the key's own rename stream
// (Law 5): a board rename racing an intake rename loses LOUDLY rather than
// silently overwriting. expect is "" for a never-renamed key, which passes
// `--expect none`.
func (b Board) Rename(key, title, as, expect string) (string, error) {
	if expect == "" {
		expect = "none"
	}
	return b.write("set", key, "--rename", title, "--expect", expect, "--as", as)
}

// SetStatus is the guarded write, doctrine-verbatim including the doctrine's
// own terminal exception (Law 5):
//
//   - `--expect` from a fresh read;
//   - needs_override from claim/settled: auto `--override`, attributed to
//     the GitHub actor — a real person's decision, tool-recorded for triage;
//   - needs_override from human, or from a document with no signals at all:
//     NEVER. That is Law 3's refusal path, and this returns the error for
//     the caller to converge on;
//   - claim_lost writing a TERMINAL value: straight out, no retry — "never
//     re-close blind" is the doctrine's own exception;
//   - claim_lost otherwise: one re-read and one retry — and the SAME rules
//     apply to the retry, since a retry that hits a signal takes the
//     signal's rule.
func (b Board) SetStatus(key, value, expect, msg, as string, evidence []string, terminal bool) (string, string, error) {
	build := func(exp string, override bool) []string {
		args := []string{"set", key, "status=" + value, "--expect", exp, "-m", msg, "--as", as}
		for _, e := range evidence {
			args = append(args, "--evidence", e)
		}
		if override {
			args = append(args, "--override")
		}
		return args
	}
	attempt := func(exp string, note string) (string, string, error) {
		id, err := b.write(build(exp, false)...)
		if err == nil {
			return id, note, nil
		}
		be, ok := err.(*BoardErr)
		if !ok || be.Code != "needs_override" {
			return "", note, err
		}
		if !be.autoOverridable() {
			return "", note, be // human, or an unknown/absent signals list: Law 3
		}
		id, err = b.write(build(exp, true)...)
		return id, strings.TrimPrefix(note+"+override", "+"), err
	}

	id, how, err := attempt(expect, "")
	if err == nil {
		return id, how, nil
	}
	be, ok := err.(*BoardErr)
	if !ok || be.Code != "claim_lost" {
		return "", how, err
	}
	if terminal {
		// The doctrine's exception: a lost CAS on a terminal write means
		// somebody else already decided this key's outcome. Re-closing blind
		// is exactly what must not happen.
		return "", how, be
	}
	fresh, _, ferr := b.Snapshot()
	if ferr != nil {
		return "", how, ferr
	}
	ks := fresh[key]
	if ks == nil {
		return "", how, be
	}
	return attempt(ks.StatusID, "retried")
}

// Note writes a board note. idemKey (scoped by the CLI to author+kind+key)
// is Law 2's dedupe handle: an imported comment and a bookkeeping note both
// carry one, so a crash anywhere re-runs into a no-op instead of a
// duplicate.
func (b Board) Note(key, kind, body, as, idemKey string) (string, bool, error) {
	args := []string{"note", "-k", kind, "-m", body, "--as", as}
	if key != "" {
		args = append(args, "--key", key)
	}
	if idemKey != "" {
		args = append(args, "--idempotency-key", idemKey)
	}
	return b.writeDetail(args...)
}

func (b Board) LinkNote(key string, issue int) (string, error) {
	id, _, err := b.Note(key, kindLink, fmt.Sprintf("%s%d", linkPrefix, issue), bridgeAuthor,
		fmt.Sprintf("gh-link-%s-%d", key, issue))
	return id, err
}

// SaveState writes the bridge's cursor and standing divergence records back
// onto the chain — the state lives where the board lives, so it survives a
// sentinel merge exactly the way every other event does.
func (b Board) SaveState(st *State) (string, error) {
	sort.Slice(st.Records, func(i, j int) bool { return st.Records[i].less(st.Records[j]) })
	blob, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	body := "bridge-cursor: " + st.Cursor + "\n" + string(blob)
	id, _, err := b.Note(stateKey, kindState, body, bridgeAuthor, "")
	return id, err
}
