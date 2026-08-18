// Package gitx is the only place the ledger touches git: a thin exec layer.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Repo optionally carries test-only instrumentation counters: Calls counts
// git subprocess invocations, Bytes counts total stdin+stdout+stderr bytes
// moved through GitRaw. Both are nil (zero cost) outside tests; scale tests
// use them to assert on the scaling SHAPE of a read (spec rule 8) — e.g.
// that a guarded write's precondition read stayed at Events' whole-chain-
// fold cost rather than degrading into a per-event subprocess pattern,
// something a subprocess *count* alone can't show (Events already reads
// any chain size in exactly two subprocesses).
type Repo struct {
	Dir   string
	Calls *int64
	Bytes *int64
	// Env holds extra KEY=VALUE entries appended to the subprocess
	// environment, on top of the inherited os.Environ() — set via WithEnv.
	// Empty (the zero value) means "inherit exactly", the ordinary case.
	Env []string
}

// WithEnv returns a copy of r whose subprocess environment additionally
// carries the given KEY=VALUE entries — sync/push's degraded-mode guard
// (GIT_TERMINAL_PROMPT=0, blanked askpass) uses this so a credential
// prompt inside a non-interactive harness fails fast instead of stalling.
// r itself is untouched.
func (r Repo) WithEnv(vars ...string) Repo {
	r.Env = append(append([]string{}, r.Env...), vars...)
	return r
}

func (r Repo) Git(stdin string, args ...string) (stdout, stderr string, code int) {
	so, se, code := r.GitRaw(stdin, args...)
	return strings.TrimRight(so, "\n"), strings.TrimRight(se, "\n"), code
}

// GitRaw is Git without trailing-newline trimming. Callers that parse exact
// byte lengths out of the output (e.g. cat-file --batch content sizes) must
// use this: trimming can eat bytes that belong to the payload, not the
// process's own trailing newline.
func (r Repo) GitRaw(stdin string, args ...string) (stdout, stderr string, code int) {
	full := args
	if r.Dir != "" {
		full = append([]string{"-C", r.Dir}, args...)
	}
	cmd := exec.Command("git", full...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if len(r.Env) > 0 {
		cmd.Env = append(os.Environ(), r.Env...)
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code = 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	if r.Calls != nil {
		atomic.AddInt64(r.Calls, 1)
	}
	if r.Bytes != nil {
		atomic.AddInt64(r.Bytes, int64(len(stdin)+so.Len()+se.Len()))
	}
	return so.String(), se.String(), code
}

var versionOnce sync.Once
var versionErr error

// CheckVersion enforces the floor the spec delegates to implementation:
// update-ref --stdin transactions and per-refspec prune-fetch are load-bearing.
func CheckVersion() error {
	versionOnce.Do(func() {
		out, _, code := Repo{}.Git("", "--version")
		if code != 0 {
			versionErr = fmt.Errorf("git_too_old: git not found")
			return
		}
		f := strings.Fields(out) // "git version 2.50.1"
		if len(f) < 3 {
			return
		}
		parts := strings.Split(f[2], ".")
		if len(parts) < 2 {
			return
		}
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		if major < 2 || (major == 2 && minor < 40) {
			versionErr = fmt.Errorf("git_too_old: need git >= 2.40, found %s", f[2])
		}
	})
	return versionErr
}

func IdentityArgs(author, committer string) []string {
	return []string{
		"-c", "user.name=" + author, "-c", "user.email=author@ledger.invalid",
		"-c", "committer.name=" + committer, "-c", "committer.email=marker@ledger.invalid",
	}
}
