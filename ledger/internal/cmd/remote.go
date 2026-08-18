package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ledger/internal/gitx"
	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/store"
)

// This file holds everything sync and push share: which remote to talk to,
// the tracking-namespace refspec and its repair, and the fetch itself.
//
// Tracking refs live at refs/ledger-remote/<remote>/<slug> — a private
// namespace, NOT refs/remotes/, which git's default branch refspec also
// populates (verified fatal collision when a branch is named ledger/<x>).
// The accepted tradeoff is that `git remote rename/remove` doesn't maintain
// this namespace, which is exactly why repairRefspecs runs on every sync
// and push invocation rather than once at init.

// trackingNamespace is the destination prefix every ledger fetch refspec
// writes into. Its appearance in a refspec is how repairRefspecs recognizes
// a refspec as ours (and so as its business to rewrite).
const trackingNamespace = "refs/ledger-remote/"

func ledgerRefspec(remote string) string {
	return "+refs/ledger/*:" + trackingNamespace + remote + "/*"
}

// netRepo is the degraded-mode guard the spec requires on every network
// call: GIT_TERMINAL_PROMPT=0 plus blanked askpass helpers, so a credential
// prompt inside a non-interactive harness fails fast (looksLikeAuth below)
// instead of stalling.
func netRepo(repo gitx.Repo) gitx.Repo {
	return repo.WithEnv("GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
}

func remotes(repo gitx.Repo) []string {
	out, _, code := repo.Git("", "remote")
	if code != 0 || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// breadcrumbRemote reads the default sync remote out of the committed
// .ledger.toml breadcrumb. It is a git remote NAME only, never a URL — a
// committed URL would let anyone who lands a commit redirect every clone's
// sync to a host they control.
var breadcrumbRemoteRE = regexp.MustCompile(`(?m)^\s*remote\s*=\s*"([^"]+)"`)

func breadcrumbRemote(repoDir string) string {
	data, err := os.ReadFile(filepath.Join(repoDir, ".ledger.toml"))
	if err != nil {
		return ""
	}
	m := breadcrumbRemoteRE.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// breadcrumbExists reports whether the committed .ledger.toml breadcrumb is
// present, independent of whether it declares a remote — `ls`'s bootstrap
// hint (paired with installedRefspec below) needs the file's mere presence,
// not its content.
func breadcrumbExists(repoDir string) bool {
	_, err := os.Stat(filepath.Join(repoDir, ".ledger.toml"))
	return err == nil
}

// installedRefspec reports whether any configured remote already has the
// ledger fetch refspec installed for its own tracking namespace — the
// signal that `ledger init` (or a `sync`/`push` repair) has run at least
// once in THIS clone. Refspec and config are repo-local and never clone
// (spec: "every clone bootstraps itself"), so a fresh clone of a repo that
// already uses ledger starts with this false even though its breadcrumb is
// committed and present.
func installedRefspec(repo gitx.Repo) bool {
	for _, r := range remotes(repo) {
		for _, v := range configAll(repo, "remote."+r+".fetch") {
			if ns, ok := trackingDest(v); ok && ns == r {
				return true
			}
		}
	}
	return false
}

// resolveRemote picks the remote sync/push talk to: an explicit --remote
// (which must exist), else the breadcrumb's declared remote, else "origin",
// else the sole configured remote. An empty return with a nil error is the
// spec's degraded mode — zero remotes configured — and the caller answers
// with a clean no-op. Two or more remotes with nothing selecting one is
// ambiguous_remote (spec rev 5 — distinct from bad_value's "you named a
// remote that doesn't exist"), naming the candidates and the --remote fix.
func resolveRemote(c *Ctx, flag string) (string, error) {
	known := remotes(c.Store.Repo)
	if flag != "" {
		for _, r := range known {
			if r == flag {
				return flag, nil
			}
		}
		list := "none configured"
		if len(known) > 0 {
			list = strings.Join(known, ", ")
		}
		return "", out.Errf("bad_value", "git remote add <name> <url>, or pass one of: "+list, 4,
			"no git remote named '%s'", flag)
	}
	if b := breadcrumbRemote(c.Store.Repo.Dir); b != "" {
		for _, r := range known {
			if r == b {
				return b, nil
			}
		}
	}
	for _, r := range known {
		if r == "origin" {
			return "origin", nil
		}
	}
	switch len(known) {
	case 0:
		return "", nil
	case 1:
		return known[0], nil
	default:
		return "", out.Errf("ambiguous_remote",
			"candidates: "+strings.Join(known, ", ")+" — pass --remote <name>", 4,
			"multiple git remotes are configured and none is selected")
	}
}

// installRefspecBestEffort resolves a remote the same way resolveRemote
// does, minus the --remote flag and the breadcrumb (neither exists yet at
// `ledger init` time), and installs its refspec — so a freshly cloned repo
// with a lone "origin" is bootstrap-ready without waiting for `ledger
// sync`'s own repair. Unresolvable (zero or ambiguous remotes) is silently
// skipped: init has no --remote flag to disambiguate with, and sync repairs
// on every invocation regardless.
func installRefspecBestEffort(repo gitx.Repo) []string {
	remote := bestEffortRemote(repo)
	if remote == "" {
		return nil
	}
	return repairRefspecs(repo, remote)
}

// bestEffortRemote picks the same remote installRefspecBestEffort installs
// a refspec for: "origin" if configured, else the sole remote, else
// unresolvable ("") — init has no --remote flag to disambiguate zero or
// several candidates. It's also what the committed breadcrumb's remote line
// is written from, when resolvable (initRepoCase, initcmd.go).
func bestEffortRemote(repo gitx.Repo) string {
	known := remotes(repo)
	for _, r := range known {
		if r == "origin" {
			return "origin"
		}
	}
	if len(known) == 1 {
		return known[0]
	}
	return ""
}

// repairRefspecs is the every-invocation repair the spec requires (round 5
// verified the failure it prevents): install the correct refspec for the
// named remote — which fixes a remote added after `ledger init` — REWRITE OR
// REMOVE any refspec targeting a different remote's refs/ledger-remote/
// namespace — `git remote rename` leaves the old refspec behind, which
// otherwise repopulates the dead namespace after every prune, a permanent
// oscillation — and prune tracking refs for remotes that no longer exist.
// Returns a human-readable line per repair actually performed.
func repairRefspecs(repo gitx.Repo, remote string) []string {
	var repairs []string
	known := remotes(repo)

	for _, r := range known {
		vals := configAll(repo, "remote."+r+".fetch")
		kept := make([]string, 0, len(vals))
		changed := false
		haveOurs := false
		for _, v := range vals {
			ns, isOurs := trackingDest(v)
			switch {
			case !isOurs:
				kept = append(kept, v)
			case ns == r:
				// this remote's own ledger refspec: keep it, canonicalized
				want := ledgerRefspec(r)
				if v != want {
					changed = true
					v = want
				}
				kept = append(kept, v)
				haveOurs = true
			default:
				// a refspec on remote r writing into remote ns's tracking
				// namespace — the rename leftover. Drop it entirely; r's own
				// correct refspec is (re-)added below.
				changed = true
				repairs = append(repairs, "removed stale refspec on remote '"+r+"' targeting "+trackingNamespace+ns+"/")
			}
		}
		if r == remote && !haveOurs {
			kept = append(kept, ledgerRefspec(r))
			changed = true
			repairs = append(repairs, "installed ledger refspec for remote '"+r+"'")
		}
		if !changed {
			continue
		}
		repo.Git("", "config", "--unset-all", "remote."+r+".fetch")
		for _, v := range kept {
			repo.Git("", "config", "--add", "remote."+r+".fetch", v)
		}
	}

	// Prune tracking refs whose remote no longer exists. These are pure
	// rebuildable cache; nothing under refs/ledger/* is ever touched.
	for _, ns := range trackingNamespaces(repo) {
		if model.Contains(known, ns) {
			continue
		}
		for _, rf := range refsUnder(repo, trackingNamespace+ns+"/") {
			repo.Git("", "update-ref", "-d", rf)
		}
		repairs = append(repairs, "pruned tracking refs for removed remote '"+ns+"'")
	}
	return repairs
}

// trackingDest reports the remote namespace a fetch refspec writes into,
// and whether the refspec is one of ours at all. "+refs/ledger/*:refs/
// ledger-remote/origin/*" yields ("origin", true).
func trackingDest(refspec string) (string, bool) {
	i := strings.Index(refspec, trackingNamespace)
	if i < 0 {
		return "", false
	}
	rest := strings.TrimPrefix(refspec[i:], trackingNamespace)
	ns, _, _ := strings.Cut(rest, "/")
	return ns, ns != ""
}

// trackingNamespaces lists the remote names that currently have tracking
// refs on disk.
func trackingNamespaces(repo gitx.Repo) []string {
	var out []string
	seen := map[string]bool{}
	for _, rf := range refsUnder(repo, trackingNamespace) {
		rest := strings.TrimPrefix(rf, trackingNamespace)
		ns, _, ok := strings.Cut(rest, "/")
		if !ok || seen[ns] {
			continue
		}
		seen[ns] = true
		out = append(out, ns)
	}
	return out
}

func refsUnder(repo gitx.Repo, prefix string) []string {
	o, _, code := repo.Git("", "for-each-ref", "--format=%(refname)", prefix)
	if code != 0 || o == "" {
		return nil
	}
	return strings.Split(o, "\n")
}

func configAll(repo gitx.Repo, key string) []string {
	o, _, code := repo.Git("", "config", "--get-all", key)
	if code != 0 || o == "" {
		return nil
	}
	return strings.Split(o, "\n")
}

// fetchTracking refreshes this remote's tracking refs. --prune is scoped to
// the refspec, so it prunes only within refs/ledger-remote/<remote>/ and
// never touches a branch tracking ref (per-refspec prune scoping).
func fetchTracking(repo gitx.Repo, remote string) error {
	_, stderr, code := netRepo(repo).Git("", "fetch", "--prune", remote, ledgerRefspec(remote))
	if code == 0 {
		return nil
	}
	if looksLikeAuth(stderr) {
		return out.Errf("credentials_needed",
			"configure credentials for remote '"+remote+"' (ssh agent, credential helper), then re-run", 4,
			"remote '%s' needs credentials this non-interactive session cannot supply", remote)
	}
	return out.Errf("git_failed", "check the remote is reachable: git ls-remote "+remote, 1,
		"fetch from '%s' failed: %s", remote, firstLine(stderr))
}

// looksLikeAuth recognizes git's credential failures. GIT_TERMINAL_PROMPT=0
// turns a would-be prompt into one of these rather than a stall, which is
// the whole point of netRepo.
func looksLikeAuth(stderr string) bool {
	for _, pat := range []string{
		"could not read Username", "could not read Password", "terminal prompts disabled",
		"Authentication failed", "Permission denied (publickey)", "Host key verification failed",
	} {
		if strings.Contains(stderr, pat) {
			return true
		}
	}
	return false
}

// trackedSlugs lists the slugs this remote's tracking refs currently hold.
func trackedSlugs(repo gitx.Repo, remote string) []string {
	var slugs []string
	for _, rf := range refsUnder(repo, trackingNamespace+remote+"/") {
		slugs = append(slugs, strings.TrimPrefix(rf, trackingNamespace+remote+"/"))
	}
	return slugs
}

// creatorOf names a chain's creator and creation time from the meta.json on
// its root commit — the same-root rule's two-creator diagnosis, and the
// provenance line adoption announces. "unknown" when the root carries no
// meta.json (the plain same-root case never sees this; multi-root refusal's
// own naming, which must cope with a graft's meta-less foreign root, uses
// rootCreator in sync.go instead).
func creatorOf(s store.Store, root string) (by, created, scope string) {
	meta, ok := s.MetaAt(root)
	if !ok {
		return "unknown", "unknown", ""
	}
	return meta.CreatedBy, meta.Created, meta.Scope
}
