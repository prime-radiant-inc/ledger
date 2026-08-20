package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.1", "0.1.0", 0},
		{"0.1.1", "0.1", 1},
		{"2.0", "10.0", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestAssetName(t *testing.T) {
	if got := AssetName("darwin", "arm64"); got != "chit-darwin-arm64.tar.gz" {
		t.Fatalf("AssetName = %q", got)
	}
	if got := AssetName("windows", "amd64"); got != "chit-windows-amd64.tar.gz" {
		t.Fatalf("AssetName windows = %q", got)
	}
}

// TestBinaryName pins the executable name inside a release tarball to what
// scripts/build-tarballs.sh actually packs — the two have to agree or every
// self-update fails at the extract step.
func TestBinaryName(t *testing.T) {
	if got := BinaryName("darwin"); got != "chit" {
		t.Fatalf("BinaryName = %q", got)
	}
	if got := BinaryName("windows"); got != "chit.exe" {
		t.Fatalf("BinaryName windows = %q", got)
	}
}

func TestLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/prime-radiant-inc/ledger/releases/latest" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"tag_name": "v0.2.0"}`)
	}))
	defer srv.Close()

	tag, err := Latest(srv.Client(), srv.URL, "prime-radiant-inc/ledger")
	if err != nil || tag != "v0.2.0" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
	if _, err := Latest(srv.Client(), srv.URL, "prime-radiant-inc/nonesuch"); err == nil {
		t.Fatal("Latest on missing repo must error")
	}
}

// makeTarball builds a gzipped tarball holding a single file.
func makeTarball(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestFetchVerifiesChecksumAndExtracts(t *testing.T) {
	binContent := []byte("#!fake chit binary\n")
	asset := "chit-linux-amd64.tar.gz"
	tarball := makeTarball(t, "chit", binContent)
	sum := sha256.Sum256(tarball)
	checksums := fmt.Sprintf("%s  %s\n%s  other-file.tar.gz\n",
		hex.EncodeToString(sum[:]), asset, "0000000000000000000000000000000000000000000000000000000000000000")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prime-radiant-inc/ledger/releases/download/v0.2.0/" + asset:
			w.Write(tarball)
		case "/prime-radiant-inc/ledger/releases/download/v0.2.0/checksums.txt":
			fmt.Fprint(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	path, err := Fetch(srv.Client(), srv.URL, "prime-radiant-inc/ledger", "v0.2.0", asset, dir)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, binContent) {
		t.Fatalf("extracted binary content mismatch: %v", err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("extracted binary not executable: %v", fi.Mode())
	}
}

func TestFetchRejectsBadChecksum(t *testing.T) {
	asset := "chit-linux-amd64.tar.gz"
	tarball := makeTarball(t, "chit", []byte("real"))
	checksums := fmt.Sprintf("%064d  %s\n", 0, asset) // wrong sum

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case asset:
			w.Write(tarball)
		case "checksums.txt":
			fmt.Fprint(w, checksums)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, err := Fetch(srv.Client(), srv.URL, "prime-radiant-inc/ledger", "v0.2.0", asset, t.TempDir()); err == nil {
		t.Fatal("Fetch must reject a checksum mismatch")
	}
}

func TestFetchRejectsAssetMissingFromChecksums(t *testing.T) {
	asset := "chit-linux-amd64.tar.gz"
	tarball := makeTarball(t, "chit", []byte("real"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case asset:
			w.Write(tarball)
		case "checksums.txt":
			fmt.Fprint(w, "0000  unrelated.tar.gz\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if _, err := Fetch(srv.Client(), srv.URL, "prime-radiant-inc/ledger", "v0.2.0", asset, t.TempDir()); err == nil {
		t.Fatal("Fetch must reject an asset absent from checksums.txt")
	}
}

func TestReplaceSwapsBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "chit")
	newBin := filepath.Join(dir, "chit-new")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(target, newBin); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Fatalf("target after Replace = %q", got)
	}
	fi, _ := os.Stat(target)
	if fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("target lost exec bit: %v", fi.Mode())
	}
}

func TestManagedByHomebrew(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/Cellar/chit/0.1.0/bin/chit", true},
		{"/usr/local/Cellar/chit/0.1.0/bin/chit", true},
		{"/home/linuxbrew/.linuxbrew/Cellar/chit/0.1.0/bin/chit", true},
		{"/Users/x/.local/bin/chit", false},
		{"/usr/local/bin/chit", false},
	}
	for _, c := range cases {
		if got := ManagedByHomebrew(c.path); got != c.want {
			t.Errorf("ManagedByHomebrew(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestCheckStateDue(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	// no state file yet: a check is due
	if s := LoadState(dir); !Due(s, now) {
		t.Fatal("missing state must be due")
	}

	SaveState(dir, State{CheckedAt: now, Latest: "v0.2.0"})
	s := LoadState(dir)
	if s.Latest != "v0.2.0" {
		t.Fatalf("round-trip Latest = %q", s.Latest)
	}
	if Due(s, now.Add(23*time.Hour)) {
		t.Fatal("23h-old state must not be due")
	}
	if !Due(s, now.Add(25*time.Hour)) {
		t.Fatal("25h-old state must be due")
	}

	// corrupt state file reads as zero state: due again, never an error
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := LoadState(dir); !Due(s, now) {
		t.Fatal("corrupt state must be due")
	}
}
