package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ledger/internal/selfupdate"
)

func TestVersionIsStorelessAndReportsPlatform(t *testing.T) {
	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"--store", t.TempDir(), "version"}, &so, &se)
	if code != 0 {
		t.Fatalf("version in a dir with no store must work: %d %s", code, se.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(so.Bytes(), &doc); err != nil {
		t.Fatalf("version output not JSON: %v\n%s", err, so.String())
	}
	if doc["version"] != "dev" || doc["os"] != runtime.GOOS || doc["arch"] != runtime.GOARCH {
		t.Fatalf("version envelope: %v", doc)
	}
}

// releaseServer serves a fake GitHub: latest-release API plus a downloadable
// release (tarball + checksums.txt). hits counts every request.
func releaseServer(t *testing.T, tag string, binContent []byte, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	asset := selfupdate.AssetName(runtime.GOOS, runtime.GOARCH)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	name := selfupdate.BinaryName(runtime.GOOS)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(binContent))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(binContent)
	tw.Close()
	gz.Close()
	tarball := buf.Bytes()
	sum := sha256.Sum256(tarball)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), asset)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		switch {
		case r.URL.Path == "/repos/"+selfupdate.Repo+"/releases/latest":
			fmt.Fprintf(w, `{"tag_name": %q}`, tag)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			fmt.Fprint(w, checksums)
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUpdateCheckReportsWithoutInstalling(t *testing.T) {
	srv := releaseServer(t, "v9.9.9", []byte("bin"), nil)
	defer srv.Close()
	t.Setenv("LEDGER_UPDATE_URL", srv.URL)

	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"update", "--check"}, &so, &se)
	if code != 0 {
		t.Fatalf("update --check: %d %s", code, se.String())
	}
	var doc map[string]any
	json.Unmarshal(so.Bytes(), &doc)
	if doc["current"] != "dev" || doc["latest"] != "v9.9.9" || doc["update_available"] != true {
		t.Fatalf("check envelope: %v", doc)
	}
}

func TestUpdateInstallsLatest(t *testing.T) {
	srv := releaseServer(t, "v9.9.9", []byte("new binary"), nil)
	defer srv.Close()
	t.Setenv("LEDGER_UPDATE_URL", srv.URL)

	dir := t.TempDir()
	target := filepath.Join(dir, selfupdate.BinaryName(runtime.GOOS))
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	restore := updateTarget
	updateTarget = func() (string, error) { return target, nil }
	defer func() { updateTarget = restore }()

	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"update"}, &so, &se)
	if code != 0 {
		t.Fatalf("update: %d %s", code, se.String())
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new binary" {
		t.Fatalf("binary not replaced: %q", got)
	}
	var doc map[string]any
	json.Unmarshal(so.Bytes(), &doc)
	if doc["installed"] != "v9.9.9" || doc["updated"] != true {
		t.Fatalf("update envelope: %v", doc)
	}
}

func TestUpdateAlreadyCurrentSkipsDownload(t *testing.T) {
	srv := releaseServer(t, "v0.2.0", []byte("bin"), nil)
	defer srv.Close()
	t.Setenv("LEDGER_UPDATE_URL", srv.URL)

	restoreV := Version
	Version = "0.2.0"
	defer func() { Version = restoreV }()

	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"update"}, &so, &se)
	if code != 0 {
		t.Fatalf("update when current: %d %s", code, se.String())
	}
	var doc map[string]any
	json.Unmarshal(so.Bytes(), &doc)
	if doc["updated"] != false || doc["latest"] != "v0.2.0" {
		t.Fatalf("already-current envelope: %v", doc)
	}
}

func TestUpdateRefusesHomebrewInstall(t *testing.T) {
	srv := releaseServer(t, "v9.9.9", []byte("bin"), nil)
	defer srv.Close()
	t.Setenv("LEDGER_UPDATE_URL", srv.URL)

	restore := updateTarget
	updateTarget = func() (string, error) {
		return "/opt/homebrew/Cellar/ledger/0.1.0/bin/ledger", nil
	}
	defer func() { updateTarget = restore }()

	var so, se bytes.Buffer
	code := ExecuteArgs([]string{"update"}, &so, &se)
	if code != 4 || !strings.Contains(se.String(), "brew_managed") || !strings.Contains(se.String(), "brew upgrade") {
		t.Fatalf("homebrew install must refuse with brew_managed + brew upgrade hint: %d %s", code, se.String())
	}
}

// TestPassiveCheckGating exercises the daily stderr notice directly: it must
// stay silent for non-TTY output, dev builds, and the opt-out env var; it
// must not touch the network inside the daily window; and it must print the
// notice from cached state when a newer version is known.
func TestPassiveCheckGating(t *testing.T) {
	var hits atomic.Int32
	srv := releaseServer(t, "v0.2.0", []byte("bin"), &hits)
	defer srv.Close()
	t.Setenv("LEDGER_UPDATE_URL", srv.URL)

	stateDir := t.TempDir()
	restoreDir := updateStateDir
	updateStateDir = func() string { return stateDir }
	defer func() { updateStateDir = restoreDir }()
	restoreV := Version
	Version = "0.1.0"
	defer func() { Version = restoreV }()

	// non-TTY: silent, no network
	var se bytes.Buffer
	passiveUpdateCheck(&Ctx{TTY: false, Stderr: &se}, "show")
	if se.Len() != 0 || hits.Load() != 0 {
		t.Fatalf("non-TTY passive check must be inert: %q, %d hits", se.String(), hits.Load())
	}

	// dev build: silent, no network
	Version = "dev"
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "show")
	if se.Len() != 0 || hits.Load() != 0 {
		t.Fatalf("dev-build passive check must be inert: %q", se.String())
	}
	Version = "0.1.0"

	// opt-out env: silent, no network
	t.Setenv("LEDGER_NO_UPDATE_CHECK", "1")
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "show")
	if se.Len() != 0 || hits.Load() != 0 {
		t.Fatalf("opted-out passive check must be inert: %q", se.String())
	}
	t.Setenv("LEDGER_NO_UPDATE_CHECK", "")

	// stale state + TTY: checks once, saves state, prints the notice
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "show")
	if hits.Load() != 1 {
		t.Fatalf("stale state must trigger exactly one check: %d", hits.Load())
	}
	if !strings.Contains(se.String(), "v0.2.0") || !strings.Contains(se.String(), "ledger update") {
		t.Fatalf("notice must name the version and the fix: %q", se.String())
	}

	// fresh state: no new network call, notice still printed from cache
	se.Reset()
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "show")
	if hits.Load() != 1 {
		t.Fatalf("fresh state must not re-check: %d hits", hits.Load())
	}
	if !strings.Contains(se.String(), "v0.2.0") {
		t.Fatalf("cached notice missing: %q", se.String())
	}

	// current binary + fresh state: nothing to say
	se.Reset()
	Version = "0.2.0"
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "show")
	if se.Len() != 0 {
		t.Fatalf("up-to-date build must stay silent: %q", se.String())
	}

	// update/version verbs never nag, even stale
	se.Reset()
	Version = "0.1.0"
	selfupdate.SaveState(stateDir, selfupdate.State{CheckedAt: time.Now().Add(-48 * time.Hour), Latest: "v0.2.0"})
	passiveUpdateCheck(&Ctx{TTY: true, Stderr: &se}, "update")
	if se.Len() != 0 || hits.Load() != 1 {
		t.Fatalf("update verb must skip the passive check: %q, %d hits", se.String(), hits.Load())
	}
}
