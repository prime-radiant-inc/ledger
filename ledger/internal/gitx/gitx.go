// Package gitx is the only place the ledger touches git: a thin exec layer.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type Repo struct{ Dir string }

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
