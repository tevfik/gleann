package gleann

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ReleaseAsset is one downloadable file of a GitHub release.
type ReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release is the part of a GitHub release the updaters need.
type Release struct {
	Tag    string         `json:"tag_name"`
	Assets []ReleaseAsset `json:"assets"`
}

// FetchLatestRelease returns the latest published release of owner/repo.
func FetchLatestRelease(ctx context.Context, owner, repo string) (*Release, error) {
	var rel Release
	if err := fetchLatestRelease(ctx, owner, repo, &rel); err != nil {
		return nil, fmt.Errorf("latest release of %s/%s: %w", owner, repo, err)
	}
	if rel.Tag == "" {
		return nil, fmt.Errorf("latest release of %s/%s has no tag", owner, repo)
	}
	return &rel, nil
}

var versionRe = regexp.MustCompile(`v?(\d+)\.(\d+)\.(\d+)`)

// ParseVersion reads the first MAJOR.MINOR.PATCH in s ("v1.8.6",
// "gleann v1.8.6-arm64-ab12"). ok is false for "dev" and other strings.
func ParseVersion(s string) (v [3]int, ok bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// IsNewer reports whether latest is a higher version than current.
// comparable is false when either side has no parsable version (a dev build).
func IsNewer(current, latest string) (newer, comparable bool) {
	c, okC := ParseVersion(current)
	l, okL := ParseVersion(latest)
	if !okC || !okL {
		return false, false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i], true
		}
	}
	return false, true
}

// SelectGleannAsset picks "<variant>-<tag>-<os>-<arch>.tar.gz|zip", the naming
// release.yml publishes. Exact matching keeps "gleann" from picking up the
// gleann-full or gleann-eif archives.
func SelectGleannAsset(names []string, variant, tag, goos, goarch string) (int, error) {
	pattern := releasePlatformPattern(goos, goarch)
	if pattern == "" {
		return -1, fmt.Errorf("no published binaries for %s/%s", goos, goarch)
	}
	for _, ext := range []string{".tar.gz", ".zip"} {
		want := fmt.Sprintf("%s-%s-%s%s", variant, tag, pattern, ext)
		for i, n := range names {
			if n == want {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("release %s has no %s build for %s", tag, variant, pattern)
}

// ReplaceBinary puts newPath at dest. The old file is renamed away first, so a
// running executable (Windows refuses to overwrite one) is replaced too; on
// failure the old file is restored.
func ReplaceBinary(dest, newPath string) error {
	old := dest + ".old"
	_ = os.Remove(old)
	hadOld := true
	if err := os.Rename(dest, old); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("move old binary aside: %w", err)
		}
		hadOld = false
	}
	if err := os.Rename(newPath, dest); err != nil {
		if hadOld {
			_ = os.Rename(old, dest)
		}
		return fmt.Errorf("install new binary: %w", err)
	}
	if hadOld {
		_ = os.Remove(old) // best effort; may fail on Windows while running
	}
	return nil
}

// checksumFor looks for a checksum of asset in the release (a SHA256SUMS /
// checksums.txt file or "<asset>.sha256") and returns it, or "" when the
// release publishes none.
func checksumFor(ctx context.Context, rel *Release, asset string) (string, error) {
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		single := a.Name == asset+".sha256"
		if !single && lower != "sha256sums" && lower != "sha256sums.txt" && lower != "checksums.txt" {
			continue
		}
		resp, err := httpGet(ctx, a.URL)
		if err != nil {
			return "", err
		}
		sc := bufio.NewScanner(resp.Body)
		sum := ""
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 0 {
				continue
			}
			if single {
				sum = f[0]
				break
			}
			if len(f) >= 2 && strings.TrimPrefix(f[1], "*") == asset {
				sum = f[0]
				break
			}
		}
		resp.Body.Close()
		if sum != "" {
			return strings.ToLower(sum), nil
		}
	}
	return "", nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DownloadReleaseBinary downloads the asset at index idx of rel, verifies its
// checksum when the release publishes one, extracts binaryName from the
// archive and returns the path of the extracted executable (next to dest, so
// the final rename stays on one filesystem). verified reports whether a
// checksum was available and matched.
func DownloadReleaseBinary(ctx context.Context, rel *Release, idx int, binaryName, dest string) (path string, verified bool, err error) {
	asset := rel.Assets[idx]
	archive, err := fetchToTemp(ctx, asset.URL)
	if err != nil {
		return "", false, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer os.Remove(archive)

	if want, err := checksumFor(ctx, rel, asset.Name); err != nil {
		return "", false, fmt.Errorf("checksum file: %w", err)
	} else if want != "" {
		got, err := fileSHA256(archive)
		if err != nil {
			return "", false, err
		}
		if got != want {
			return "", false, fmt.Errorf("checksum mismatch for %s: got %s, want %s", asset.Name, got, want)
		}
		verified = true
	}

	tmp := filepath.Join(filepath.Dir(dest), "."+filepath.Base(dest)+".new")
	_ = os.Remove(tmp)
	extract := func(r io.Reader) error { return extractBinaryFromTarGz(r, tmp, binaryName) }
	if strings.HasSuffix(asset.Name, ".zip") {
		extract = func(r io.Reader) error { return extractBinaryFromZip(r, tmp, binaryName) }
	}
	if err := extractFileWith(archive, extract); err != nil {
		_ = os.Remove(tmp)
		return "", false, fmt.Errorf("extract %s: %w", asset.Name, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0o755); err != nil {
			_ = os.Remove(tmp)
			return "", false, err
		}
	}
	return tmp, verified, nil
}
