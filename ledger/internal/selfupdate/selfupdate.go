// Package selfupdate fetches and installs released ledger binaries from
// GitHub releases. It backs `ledger update` and the daily passive check;
// nothing here touches a ledger store.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	Repo                = "prime-radiant-inc/ledger"
	DefaultAPIBase      = "https://api.github.com"
	DefaultDownloadBase = "https://github.com"
)

// CompareVersions compares dotted numeric versions (an optional leading v is
// ignored), returning -1, 0, or 1. Missing segments count as 0, so
// "0.1" == "0.1.0". Non-numeric segments also count as 0 — release tags are
// always plain semver, so this only matters for garbage input.
func CompareVersions(a, b string) int {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}
	return 0
}

// AssetName is the release tarball for a platform. Every target ships as
// .tar.gz, Windows included (tar has been built into Windows since 10) — one
// archive format keeps this package and install.sh to a single code path.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("ledger-%s-%s.tar.gz", goos, goarch)
}

// BinaryName is the executable inside a release tarball.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "ledger.exe"
	}
	return "ledger"
}

// Latest returns the newest release tag (e.g. "v0.2.0").
func Latest(client *http.Client, apiBase, repo string) (string, error) {
	resp, err := client.Get(apiBase + "/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release lookup: %s", resp.Status)
	}
	var doc struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return "", fmt.Errorf("release lookup: %w", err)
	}
	if doc.TagName == "" {
		return "", fmt.Errorf("release lookup: no tag_name in response")
	}
	return doc.TagName, nil
}

// Fetch downloads asset for tag, verifies it against the release's
// checksums.txt, extracts the single binary inside, and returns the
// extracted file's path in destDir. destDir should be the directory the
// binary will finally live in, so the later rename stays on one filesystem.
func Fetch(client *http.Client, dlBase, repo, tag, asset, destDir string) (string, error) {
	base := dlBase + "/" + repo + "/releases/download/" + tag + "/"

	sums, err := get(client, base+"checksums.txt", 1<<20)
	if err != nil {
		return "", err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return "", fmt.Errorf("%s not listed in checksums.txt for %s", asset, tag)
	}

	tarball, err := get(client, base+asset, 1<<30)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(tarball)
	if got := hex.EncodeToString(sum[:]); got != want {
		return "", fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset, got, want)
	}

	return extractBinary(tarball, destDir)
}

func get(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// extractBinary pulls the single ledger executable out of a release tarball
// into destDir under a temporary name. Entries with path separators are
// rejected (tar-slip guard); our tarballs hold exactly one flat file.
func extractBinary(tarball []byte, destDir string) (string, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(tarball)))
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", fmt.Errorf("no ledger binary found in tarball")
		}
		if err != nil {
			return "", err
		}
		name := hdr.Name
		if hdr.Typeflag != tar.TypeReg || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if name != "ledger" && name != "ledger.exe" {
			continue
		}
		f, err := os.CreateTemp(destDir, ".ledger-update-*")
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", err
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return "", err
		}
		if err := os.Chmod(f.Name(), 0o755); err != nil {
			os.Remove(f.Name())
			return "", err
		}
		return f.Name(), nil
	}
}

// Replace swaps target for newBin. On Unix a rename over the live executable
// is atomic. Windows refuses to rename over a running binary, so the old one
// is moved aside first and left as <target>.old for the OS to let go of.
func Replace(target, newBin string) error {
	if runtime.GOOS == "windows" {
		old := target + ".old"
		os.Remove(old)
		if err := os.Rename(target, old); err != nil {
			return err
		}
		if err := os.Rename(newBin, target); err != nil {
			if rerr := os.Rename(old, target); rerr != nil {
				// worst case: nothing at target. Say exactly where the last
				// good binary still is instead of failing mysteriously.
				return fmt.Errorf("installing the new binary failed (%v) and restoring the old one failed too (%v) — the previous binary is intact at %s", err, rerr, old)
			}
			return err
		}
		return nil
	}
	return os.Rename(newBin, target)
}

// ManagedByHomebrew reports whether the executable lives in a Homebrew
// cellar — self-updating one of those fights `brew upgrade`, which would
// silently reinstall the old version later.
func ManagedByHomebrew(path string) bool {
	return strings.Contains(path, "/Cellar/")
}
