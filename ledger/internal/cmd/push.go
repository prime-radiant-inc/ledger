package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"ledger/internal/out"
	"ledger/internal/store"
)

func init() { register(newPushCmd) }

func newPushCmd(c *Ctx) *cobra.Command {
	var remote string
	cmd := &cobra.Command{Use: "push [<slug>...]", Short: "publish local ledger refs to a remote (non-force)",
		Long: "Pushes refs/ledger/<slug> to the same ref on the remote, never with --force.\n" +
			"With no arguments every local slug is pushed; naming slugs pushes only those —\n" +
			"the privacy lever, so one handoff ledger can go out without publishing everything.\n" +
			"Everything pushed is readable by anyone with read access to the repo.",
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error { return runPush(c, remote, args) }}
	cmd.Flags().StringVar(&remote, "remote", "", "git remote name (default: .ledger.toml's remote, else origin)")
	return cmd
}

func runPush(c *Ctx, remoteFlag string, args []string) error {
	remote, err := resolveRemote(c, remoteFlag)
	if err != nil {
		return err
	}
	if remote == "" {
		// Same degraded mode as sync: a repo with no remote configured is a
		// legitimate way to run, never an error.
		outEmit(c, map[string]any{"pushed": []SlugOutcome{}, "remote": nil,
			"note": "no git remote configured — nothing to push"},
			[]string{"no git remote configured — nothing to push (git remote add origin <url>)"})
		return nil
	}

	repairs := repairRefspecs(c.Store.Repo, remote)
	for _, r := range repairs {
		fmt.Fprintln(c.Stderr, "[chit] "+r)
	}

	slugs := args
	if len(slugs) == 0 {
		slugs, err = c.Store.Slugs()
		if err != nil {
			return out.Errf("git_failed", "", 1, "%s", err)
		}
	} else {
		// Named slugs must exist locally: an unknown one would hand git an
		// unresolvable refspec, which aborts the WHOLE batch (git validates
		// every refspec before pushing any of them) — silently losing every
		// other slug in the same invocation to one typo.
		for _, s := range slugs {
			if _, ok := c.Store.FullHead(s); !ok {
				return out.Errf("unknown_ledger", c.shadowHint("chit ls --all  (lists every ledger here)"),
					4, "no ledger '%s' here", s)
			}
		}
	}
	sort.Strings(slugs)

	outcomes := []SlugOutcome{}
	if len(slugs) > 0 {
		outcomes = c.pushBatch(remote, slugs)
	}
	return c.emitOutcomes("pushed", remote, outcomes)
}

// pushBatch is the batched, non-force publish for every selected slug: ONE
// `git push --porcelain` subprocess carries every refspec (the delta from
// the spike's per-slug push, which is dozens of round trips at fleet
// scale — production-build note, sync spec rev 4). Each ref's porcelain
// flag decides its outcome directly; any rejection triggers exactly ONE
// follow-up fetch of tracking refs (never one per rejected slug) so a root
// mismatch can be diagnosed with the two-creator error instead of the
// generic retry instruction.
func (c *Ctx) pushBatch(remote string, slugs []string) []SlugOutcome {
	refspecs := make([]string, len(slugs))
	for i, s := range slugs {
		refspecs[i] = store.Ref(s) + ":" + store.Ref(s)
	}
	repo := netRepo(c.Store.Repo)
	// advice.pushNonFastForward=false drops git's own "git pull" hint —
	// wrong advice for phantom refs, and this tool prints its own retry
	// instruction instead. Always non-force: no +refspec, no --force flag.
	args := append([]string{"-c", "advice.pushNonFastForward=false", "push", "--porcelain", remote}, refspecs...)
	stdout, stderr, code := repo.Git("", args...)
	flags := parsePushPorcelain(stdout)

	if code != 0 && len(flags) == 0 {
		// git never got as far as reporting per-ref results at all — an
		// unreachable remote, bad credentials, or an outright refusal.
		// Every selected slug shares the same cause.
		detail := firstLine(stderr)
		if looksLikeAuth(stderr) {
			detail = "remote '" + remote + "' needs credentials this non-interactive session cannot supply"
		}
		outcomes := make([]SlugOutcome, len(slugs))
		for i, s := range slugs {
			outcomes[i] = SlugOutcome{Slug: s, Result: "failed", Detail: detail}
		}
		return outcomes
	}

	outcomes := make([]SlugOutcome, len(slugs))
	var rejected []int
	for i, s := range slugs {
		flag, ok := flags[s]
		switch {
		case !ok:
			outcomes[i] = SlugOutcome{Slug: s, Result: "failed", Detail: "no push result reported for this ref"}
		case flag == '!':
			outcomes[i] = SlugOutcome{Slug: s, Result: "rejected"}
			rejected = append(rejected, i)
		default:
			outcomes[i] = SlugOutcome{Slug: s, Result: "pushed"}
		}
	}

	if len(rejected) > 0 {
		fetchErr := fetchTracking(c.Store.Repo, remote)
		for _, i := range rejected {
			// The curated instruction is the whole answer — git's own
			// rejection text is never echoed here, only this fixed detail
			// (or the two-creator diagnosis below).
			detail := "run `chit sync`, then retry `chit push`"
			if fetchErr == nil {
				trackRef := store.TrackingRef(remote, outcomes[i].Slug)
				if _, _, trackResult, err := c.Store.EventsDAGAt(trackRef); err == nil {
					if bad := c.rootMismatch(outcomes[i].Slug, trackResult.Roots); bad != nil {
						detail = bad.Detail
					}
				}
			}
			outcomes[i].Detail = detail
		}
	}
	return outcomes
}

// parsePushPorcelain reads `git push --porcelain`'s per-ref status lines —
// "<flag>\t<from>:<to>\t<summary>" — into a slug -> flag map. Flags: '='
// up to date, '*' a new ref, ' ' (space) a fast-forward, '!' rejected.
// '+' (forced) never appears — push is always non-force.
func parsePushPorcelain(stdout string) map[string]byte {
	prefix := store.Ref("")
	flags := make(map[string]byte)
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" || line == "Done" || strings.HasPrefix(line, "To ") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		flag := byte(' ')
		if len(parts[0]) > 0 {
			flag = parts[0][0]
		}
		_, to, ok := strings.Cut(parts[1], ":")
		if !ok || !strings.HasPrefix(to, prefix) {
			continue
		}
		flags[strings.TrimPrefix(to, prefix)] = flag
	}
	return flags
}
