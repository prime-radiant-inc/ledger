package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"ledger/internal/model"
	"ledger/internal/out"
	"ledger/internal/selfupdate"
)

// Test seams: tests point these at a scratch binary and a scratch state dir
// so an update run can never touch the real executable or the user's cache.
var (
	updateTarget   = os.Executable
	updateStateDir = selfupdate.StateDir
)

// updateBases returns the API and download hosts. LEDGER_UPDATE_URL collapses
// both onto one base — a test hook, also usable behind a GitHub mirror.
func updateBases() (api, dl string) {
	if u := os.Getenv("LEDGER_UPDATE_URL"); u != "" {
		return u, u
	}
	return selfupdate.DefaultAPIBase, selfupdate.DefaultDownloadBase
}

func init() { register(newUpdateCmd) }

// newUpdateCmd never resolves a store (see root.go's exemption list) —
// updating the binary is meaningful anywhere, git repo or not.
func newUpdateCmd(c *Ctx) *cobra.Command {
	var check bool
	cmd := &cobra.Command{Use: "update", Short: "install the latest released ledger",
		Long: "Downloads the latest GitHub release for this platform, verifies its\n" +
			"checksum, and atomically replaces the running binary. --check only\n" +
			"reports. Homebrew installs are refused — use `brew upgrade ledger`.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			api, dl := updateBases()
			client := &http.Client{Timeout: 60 * time.Second}
			latest, err := selfupdate.Latest(client, api, selfupdate.Repo)
			if err != nil {
				return out.Errf("update_failed", "check your network and try again", 1, "%s", err)
			}
			avail := selfupdate.CompareVersions(Version, latest) < 0
			if check {
				line := fmt.Sprintf("up to date (%s)", Version)
				if avail {
					line = fmt.Sprintf("update available: %s (you have %s) — run `ledger update`", latest, Version)
				}
				out.Emit(c.Stdout, c.TTY, map[string]any{
					"current": Version, "latest": latest, "update_available": avail,
				}, []string{line})
				return nil
			}
			if !avail {
				out.Emit(c.Stdout, c.TTY, map[string]any{
					"current": Version, "latest": latest, "updated": false,
				}, []string{fmt.Sprintf("already up to date (%s)", Version)})
				return nil
			}
			target, err := updateTarget()
			if err != nil {
				return out.Errf("update_failed", "", 1, "locating the running binary: %s", err)
			}
			if resolved, err := filepath.EvalSymlinks(target); err == nil {
				target = resolved
			}
			if selfupdate.ManagedByHomebrew(target) {
				return out.Errf("brew_managed", "run: brew upgrade ledger", 4,
					"this binary was installed by Homebrew (%s); a self-update would be undone by the next brew upgrade", target)
			}
			// Fetch extracts into the install dir (keeps the final rename on
			// one filesystem), so an unwritable dir fails here, not in Replace
			// — both paths need the permissions hint.
			permHint := "if the install dir needs root, rerun with sudo"
			if runtime.GOOS == "windows" {
				permHint = "the install directory isn't writable — retry from an elevated prompt"
			}
			// sweep staging files a killed earlier update left behind — Fetch
			// stages in the install dir (same-filesystem rename), so orphans
			// would otherwise accumulate there forever
			if stale, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".ledger-update-*")); err == nil {
				for _, f := range stale {
					os.Remove(f)
				}
			}
			asset := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH)
			newBin, err := selfupdate.Fetch(client, dl, selfupdate.Repo, latest, asset, filepath.Dir(target))
			if err != nil {
				hint := "check your network and try again"
				if errors.Is(err, fs.ErrPermission) {
					hint = permHint
				}
				return out.Errf("update_failed", hint, 1, "%s", err)
			}
			if err := selfupdate.Replace(target, newBin); err != nil {
				os.Remove(newBin)
				return out.Errf("update_failed", permHint, 1, "%s", err)
			}
			out.Emit(c.Stdout, c.TTY, map[string]any{
				"current": Version, "installed": latest, "updated": true,
			}, []string{fmt.Sprintf("updated %s -> %s", Version, latest)})
			return nil
		}}
	cmd.Flags().BoolVar(&check, "check", false, "report whether an update exists without installing")
	return cmd
}

// passiveUpdateCheck is the daily availability nag, run after every
// successful command. It exists for humans at terminals only, so it is
// triple-gated: TTY output, a released (non-dev) build, and no
// LEDGER_NO_UPDATE_CHECK. The network check runs at most once per
// CheckInterval with a short timeout; the notice goes to stderr so stdout's
// JSON stays byte-pristine. Every failure path is silent — an update nag
// must never break a command that already succeeded.
func passiveUpdateCheck(c *Ctx, verb string) {
	if !c.TTY || Version == "dev" || os.Getenv("LEDGER_NO_UPDATE_CHECK") != "" {
		return
	}
	if verb == "update" || verb == "version" {
		return
	}
	dir := updateStateDir()
	st := selfupdate.LoadState(dir)
	now := model.Now()
	if selfupdate.Due(st, now) {
		api, _ := updateBases()
		client := &http.Client{Timeout: 2 * time.Second}
		tag, err := selfupdate.Latest(client, api, selfupdate.Repo)
		if err != nil {
			// record the attempt so a dead network is retried daily, not per command
			selfupdate.SaveState(dir, selfupdate.State{CheckedAt: now, Latest: st.Latest})
			return
		}
		st = selfupdate.State{CheckedAt: now, Latest: tag}
		selfupdate.SaveState(dir, st)
	}
	if st.Latest != "" && selfupdate.CompareVersions(Version, st.Latest) < 0 {
		fix := "run `ledger update`"
		if target, err := updateTarget(); err == nil {
			if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
				target = resolved
			}
			if selfupdate.ManagedByHomebrew(target) {
				// `ledger update` refuses brew installs — don't nag toward a
				// command that will only bounce the user to another one
				fix = "run `brew upgrade ledger`"
			}
		}
		fmt.Fprintf(c.Stderr, "ledger %s is available (you have %s) — %s\n", st.Latest, Version, fix)
	}
}
