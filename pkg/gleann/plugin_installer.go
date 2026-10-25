package gleann

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// defaultPluginOwner is the default GitHub/Gitea owner for gleann plugins.
const defaultPluginOwner = "tevfik"

// githubAPIBase is the GitHub REST API root used for release lookups.
// It is a variable so tests can point it at an httptest server.
// GLEANN_GITHUB_API redirects it (GitHub Enterprise, a mirror, or a test server).
var githubAPIBase = func() string {
	if v := os.Getenv("GLEANN_GITHUB_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.github.com"
}()

func pluginOwner() string {
	if v := os.Getenv("GLEANN_PLUGIN_OWNER"); v != "" {
		return v
	}
	return defaultPluginOwner
}

// InstallPlugin starts the installation process for the specified plugin
// and sends progress updates to the provided channel.
func InstallPlugin(info PluginMeta, progress chan<- string) (string, error) {
	progress <- "🔍 Checking plugin directory..."

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}

	pluginsDir := filepath.Join(home, ".gleann", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return "", fmt.Errorf("create plugins dir: %w", err)
	}

	pluginDir := filepath.Join(pluginsDir, info.Name)

	if fi, err := os.Lstat(pluginDir); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Stat(pluginDir); err != nil || !target.IsDir() {
				progress <- "⚠️  Removing invalid plugin entry (not a directory)..."
				os.Remove(pluginDir)
			}
		} else if !fi.IsDir() {
			progress <- "⚠️  Removing invalid plugin entry (not a directory)..."
			os.Remove(pluginDir)
		}
	}

	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		repoDir := filepath.Join(pluginsDir, "_repos", repoName(info.RepoURL))
		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			progress <- fmt.Sprintf("📦 Cloning repository from %s...", info.RepoURL)
			os.MkdirAll(filepath.Dir(repoDir), 0o755)
			cmd := exec.Command("git", "clone", "--depth=1", info.RepoURL, repoDir)
			output, cloneErr := cmd.CombinedOutput()
			if cloneErr != nil {
				progress <- "⚠️  Git clone failed, trying GitHub release..."
				owner, repo := parseGitHubURL(info.RepoURL)
				if owner == "" || repo == "" {
					return "", fmt.Errorf("git clone failed and cannot parse GitHub URL: %s", string(output))
				}
				if err := downloadSourceFromGitHub(owner, repo, repoDir, progress); err != nil {
					return "", fmt.Errorf("both git clone and release download failed: %s / %v", string(output), err)
				}
				progress <- "✓ Source downloaded from GitHub release"
			} else {
				progress <- "✓ Repository cloned successfully"
			}
		} else {
			progress <- "✓ Repository already exists"
		}

		progress <- "🔗 Linking plugin directory..."
		srcDir := filepath.Join(repoDir, info.Name)

		if fi, err := os.Stat(srcDir); os.IsNotExist(err) || (err == nil && !fi.IsDir()) {
			srcDir = repoDir
		}

		if err := linkOrCopy(srcDir, pluginDir); err != nil {
			return "", fmt.Errorf("link/copy plugin: %w", err)
		}
		progress <- "✓ Plugin directory linked"
	} else {
		progress <- "✓ Plugin directory already exists"
	}

	lang := info.Language
	if strings.Contains(info.Name, "docs") || strings.Contains(lang, "go") {
		return setupGoPluginWithProgress(pluginDir, info.Name, progress)
	} else if strings.Contains(lang, "python") {
		return setupPythonPluginWithProgress(pluginDir, info.Name, progress)
	}

	return fmt.Sprintf("Installed %s", info.Name), nil
}

func setupPythonPluginWithProgress(pluginDir, name string, progress chan<- string) (string, error) {
	if name == "gleann-plugin-docs" || strings.Contains(name, "docs") {
		return setupGoPluginWithProgress(pluginDir, name, progress)
	}
	venvDir := filepath.Join(pluginDir, ".venv")

	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		progress <- "🐍 Creating Python virtual environment..."
		cmd := exec.Command(findPython3(), "-m", "venv", venvDir)
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("create venv: %s", string(output))
		}
		progress <- "✓ Virtual environment created"
	} else {
		progress <- "✓ Virtual environment already exists"
	}

	pip := venvBinary(venvDir, "pip")
	reqs := filepath.Join(pluginDir, "requirements.txt")
	if _, err := os.Stat(reqs); err == nil {
		progress <- "📚 Installing Python dependencies (markitdown, docling, etc.)..."
		progress <- "   This may take a few minutes on first install..."
		cmd := exec.Command(pip, "install", "-r", reqs)
		cmd.Dir = pluginDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("pip install: %s", string(output))
		}
		progress <- "✓ Dependencies installed successfully"
	}

	progress <- "📝 Registering plugin..."
	pythonBin := venvBinary(venvDir, "python")
	mainPy := filepath.Join(pluginDir, "main.py")
	registerCmd := exec.Command(pythonBin, mainPy, "--install", "--name", name)
	registerCmd.Dir = pluginDir
	if firstOutput, err := registerCmd.CombinedOutput(); err != nil {
		// Fallback to plain --install if --name is not recognized (e.g. gleann-plugin-docs)
		fallbackCmd := exec.Command(pythonBin, mainPy, "--install")
		fallbackCmd.Dir = pluginDir
		if fallbackOutput, fallbackErr := fallbackCmd.CombinedOutput(); fallbackErr != nil {
			return "", fmt.Errorf("register plugin: %s (first attempt: %s)", string(fallbackOutput), string(firstOutput))
		}
	}
	progress <- "✓ Plugin registered"

	return fmt.Sprintf("Installed %s (Python)", name), nil
}

func setupGoPluginWithProgress(pluginDir, name string, progress chan<- string) (string, error) {
	binaryPath := filepath.Join(pluginDir, name)

	repoPath := filepath.Join(filepath.Dir(pluginDir), "_repos")
	repoName := ""
	if entries, err := os.ReadDir(repoPath); err == nil && len(entries) > 0 {
		for _, e := range entries {
			if strings.Contains(e.Name(), name) {
				repoName = e.Name()
				break
			}
		}
	}

	var owner, repo string
	if repoName != "" {
		owner = pluginOwner()
		repo = repoName
	} else {
		owner = pluginOwner()
		if strings.HasPrefix(name, "gleann-plugin-") {
			repo = name
		} else if strings.HasPrefix(name, "gleann-") {
			repo = "gleann-plugin-" + strings.TrimPrefix(name, "gleann-")
		} else {
			repo = "gleann-plugin-" + name
		}
	}

	if _, err := os.Stat(binaryPath); err == nil {
		progress <- "✓ Binary already installed"
		registerCmd := exec.Command(binaryPath, "--install")
		registerCmd.Dir = pluginDir
		if _, err := registerCmd.CombinedOutput(); err == nil {
			progress <- "✓ Plugin registered in plugins.json"
		}
		if strings.Contains(name, "docs") {
			prepareDocsOCR(binaryPath, progress)
			ensureDoclingModel(owner, repo, progress)
		} else if strings.Contains(name, "sound") {
			prepareSoundModels(binaryPath, progress)
		}
		return fmt.Sprintf("Installed %s (Go binary)", name), nil
	}

	if fi, err := os.Stat(pluginDir); err == nil && !fi.IsDir() {
		progress <- "🔍 Detected pre-built binary in repository root..."
		realPath, _ := filepath.EvalSymlinks(pluginDir)
		repoRoot := filepath.Dir(realPath)
		candidate := filepath.Join(repoRoot, name)
		if _, err := os.Stat(candidate); err == nil {
			progress <- "✓ Binary found in repository root"
			registerCmd := exec.Command(candidate, "--install")
			registerCmd.Dir = repoRoot
			if _, err := registerCmd.CombinedOutput(); err == nil {
				progress <- "✓ Plugin registered in plugins.json"
			}
			if strings.Contains(name, "docs") {
				prepareDocsOCR(candidate, progress)
				ensureDoclingModel(owner, repo, progress)
			} else if strings.Contains(name, "sound") {
				prepareSoundModels(candidate, progress)
			}
			return fmt.Sprintf("Installed %s (Go binary)", name), nil
		}
	}

	progress <- "📥 Downloading binary from GitHub releases..."

	binaryInstalled := false
	if err := downloadBinaryFromGitHub(owner, repo, binaryPath, progress); err == nil {
		progress <- "✓ Binary downloaded successfully"
		binaryInstalled = true
	} else {
		progress <- fmt.Sprintf("⚠️  Download failed: %v", err)
	}

	if !binaryInstalled {
		progress <- "🔍 Checking for source files..."
		buildTarget, hasGoFiles := findGoBuildTarget(pluginDir)

		if !hasGoFiles {
			return "", fmt.Errorf("no binary in releases and no source files to build from")
		}

		progress <- fmt.Sprintf("🔨 Building Go binary from source (%s)...", buildTarget)
		buildArgs := []string{"build", "-o", binaryPath}
		if name == "gleann-plugin-sound" {
			buildArgs = append(buildArgs, "-tags", "onnx")
		}
		buildArgs = append(buildArgs, "./"+buildTarget)
		cmd := exec.Command("go", buildArgs...)
		cmd.Dir = pluginDir
		if output, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("go build: %s", string(output))
		}
		progress <- "✓ Binary built successfully"
	}

	// Register plugin with plugins.json if binary supports --install
	registerCmd := exec.Command(binaryPath, "--install")
	registerCmd.Dir = pluginDir
	if _, err := registerCmd.CombinedOutput(); err == nil {
		progress <- "✓ Plugin registered in plugins.json"
	}

	// For docs plugin, ensure Granite Docling BitNet model is downloaded in one step
	if strings.Contains(name, "docs") {
		prepareDocsOCR(binaryPath, progress)
		ensureDoclingModel(owner, repo, progress)
	} else if strings.Contains(name, "sound") {
		prepareSoundModels(binaryPath, progress)
	}

	return fmt.Sprintf("Installed %s (Go binary)", name), nil
}

// prepareDocsOCR asks the docs plugin to fetch what scanned pages need: the
// ONNX Runtime library and the layout + PP-OCR models (`models download`,
// checksummed, permissive licences). Without them a scanned PDF yields nothing.
// Plugin builds that predate the subcommand just print usage; that is ignored.
func prepareDocsOCR(binary string, progress chan<- string) {
	progress <- "📦 Fetching OCR runtime and models for scanned pages..."
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "models", "download").CombinedOutput()
	if err != nil {
		progress <- fmt.Sprintf("⚠️  OCR setup skipped: %v (run `%s models download` later)", err, filepath.Base(binary))
		_ = out
		return
	}
	progress <- "✓ OCR runtime and models ready"
}

// prepareSoundModels asks the sound plugin to fetch the default speech recognition
// model (base.en + Silero VAD) via `models download --default`.
func prepareSoundModels(binary string, progress chan<- string) {
	progress <- "📦 Fetching default speech recognition models (base.en + Silero VAD)..."
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "models", "download", "--default").CombinedOutput()
	if err != nil {
		progress <- fmt.Sprintf("⚠️  Sound models setup skipped: %v (run `%s models download --default` later)", err, filepath.Base(binary))
		_ = out
		return
	}
	progress <- "✓ Speech recognition models ready"
}

func UninstallPlugin(name string) (string, error) {
	// name is joined into ~/.gleann/plugins/<name> and removed recursively;
	// reject anything that is not a single safe path component.
	if err := ValidateName(name); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home dir: %w", err)
	}

	pluginDir := filepath.Join(home, ".gleann", "plugins", name)
	if err := os.RemoveAll(pluginDir); err != nil {
		return "", fmt.Errorf("remove plugin dir: %w", err)
	}

	reg, err := LoadPlugins()
	if err == nil {
		filtered := make([]Plugin, 0)
		for _, p := range reg.Plugins {
			if p.Name != name {
				filtered = append(filtered, p)
			}
		}
		reg.Plugins = filtered

		pluginFile := filepath.Join(home, ".gleann", "plugins.json")
		b, _ := json.MarshalIndent(reg, "", "  ")
		os.WriteFile(pluginFile, b, 0o644)
	}

	return fmt.Sprintf("Uninstalled %s", name), nil
}

func repoName(url string) string {
	parts := strings.Split(strings.TrimSuffix(url, ".git"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "repo"
}

func findPython3() string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("python"); err == nil {
			return "python"
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3"
	}
	return "python"
}

func venvBinary(venvDir, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", name+".exe")
	}
	binDir := "b" + "in"
	return filepath.Join(venvDir, binDir, name)
}

func linkOrCopy(src, dst string) error {
	var symlinkFunc func(string, string) error
	if runtime.GOOS == "windows" {
		symlinkFunc = os.Link
	} else {
		symlinkFunc = os.Symlink
	}

	if err := symlinkFunc(src, dst); err == nil {
		return nil
	}
	return copyDir(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func parseGitHubURL(url string) (owner, repo string) {
	url = strings.TrimSuffix(url, ".git")
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)`)
	matches := re.FindStringSubmatch(url)
	if len(matches) != 3 {
		return "", ""
	}
	return matches[1], matches[2]
}

func downloadSourceFromGitHub(owner, repo, destDir string, progress chan<- string) error {
	ctx := context.Background()
	var release struct {
		TagName    string `json:"tag_name"`
		TarballURL string `json:"tarball_url"`
	}
	if err := fetchLatestRelease(ctx, owner, repo, &release); err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}

	progress <- fmt.Sprintf("   Downloading source for %s...", release.TagName)

	tarball, err := fetchToTemp(ctx, release.TarballURL)
	if err != nil {
		return fmt.Errorf("failed to download tarball: %w", err)
	}
	defer os.Remove(tarball)

	progress <- "   Extracting source code..."
	if err := extractFileWith(tarball, func(r io.Reader) error { return extractTarballToDir(r, destDir) }); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}
	return nil
}

// extractFileWith opens path and passes it to extract.
func extractFileWith(path string, extract func(io.Reader) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return extract(f)
}

func extractTarballToDir(r io.Reader, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var stripPrefix string
	firstEntry := true

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		if firstEntry {
			parts := strings.Split(header.Name, "/")
			if len(parts) > 0 {
				stripPrefix = parts[0] + "/"
			}
			firstEntry = false
		}

		name := strings.TrimPrefix(header.Name, stripPrefix)
		if name == "" {
			continue
		}

		target := filepath.Join(destDir, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %s: %w", filepath.Dir(target), err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write file %s: %w", target, err)
			}
			f.Close()
		}
	}
	return nil
}

func findGoBuildTarget(pluginDir string) (string, bool) {
	if entries, err := os.ReadDir(pluginDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				return ".", true
			}
		}
	}

	cmdDir := filepath.Join(pluginDir, "cmd")
	if subdirs, err := os.ReadDir(cmdDir); err == nil {
		for _, sub := range subdirs {
			if !sub.IsDir() {
				continue
			}
			subPath := filepath.Join(cmdDir, sub.Name())
			if entries, err := os.ReadDir(subPath); err == nil {
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
						return filepath.Join("cmd", sub.Name()), true
					}
				}
			}
		}
	}

	return ".", false
}

func downloadBinaryFromGitHub(owner, repo, destPath string, progress chan<- string) error {
	ctx := context.Background()
	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := fetchLatestRelease(ctx, owner, repo, &release); err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}

	if len(release.Assets) == 0 {
		return fmt.Errorf("no assets found in latest release")
	}

	progress <- fmt.Sprintf("   Found release %s", release.TagName)

	names := make([]string, len(release.Assets))
	for i, a := range release.Assets {
		names[i] = a.Name
	}
	idx, err := selectReleaseAsset(names, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	downloadURL := release.Assets[idx].BrowserDownloadURL
	assetName := release.Assets[idx].Name
	progress <- fmt.Sprintf("   Downloading %s...", assetName)

	// Write to a temporary sibling and rename only on success: a truncated
	// download must never be left at destPath, because a later install sees
	// an existing binary and skips the download ("Binary already installed").
	tmpPath := destPath + ".part"
	_ = os.Remove(tmpPath)
	defer os.Remove(tmpPath) // no-op after a successful rename

	isArchive := strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tgz") || strings.HasSuffix(assetName, ".zip")
	if !isArchive {
		// Raw binary asset: download straight to the temporary sibling.
		if err := fetchToFile(ctx, downloadURL, tmpPath); err != nil {
			return fmt.Errorf("failed to download binary: %w", err)
		}
	} else {
		archive, err := fetchToTemp(ctx, downloadURL)
		if err != nil {
			return fmt.Errorf("failed to download archive: %w", err)
		}
		defer os.Remove(archive)

		progress <- "   Extracting binary..."
		if strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tgz") {
			if err := extractFileWith(archive, func(r io.Reader) error { return extractBinaryFromTarGz(r, tmpPath, repo) }); err != nil {
				return fmt.Errorf("failed to extract tar.gz: %w", err)
			}
		} else {
			if err := extractFileWith(archive, func(r io.Reader) error { return extractBinaryFromZip(r, tmpPath, repo) }); err != nil {
				return fmt.Errorf("failed to extract zip: %w", err)
			}
		}
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("failed to make executable: %w", err)
		}
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}
	return nil
}

// releasePlatformPattern returns the "<os>-<arch>" fragment used in release
// asset names, or "" when the platform has no published binaries.
func releasePlatformPattern(goos, goarch string) string {
	switch goos {
	case "linux", "darwin":
		switch goarch {
		case "amd64", "arm64":
			return goos + "-" + goarch
		}
	case "windows":
		if goarch == "amd64" {
			return "windows-amd64"
		}
	}
	return ""
}

// isAuxiliaryAsset reports release assets that are never the plugin binary:
// checksums, signatures, SBOMs and similar metadata files.
func isAuxiliaryAsset(name string) bool {
	lower := strings.ToLower(name)
	for _, suf := range []string{".sha256", ".sha256sum", ".sha512", ".md5", ".sig", ".asc", ".pem", ".sbom", ".spdx", ".json", ".txt", ".intoto.jsonl"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return strings.Contains(lower, "checksum")
}

// selectReleaseAsset picks the plugin binary for goos/goarch from release
// asset names. Preference: the full "-whisper-onnx-" build, then "-onnx-", then
// the plain build (any asset that is not a "-stub-" one), then "-stub-", and
// finally any asset for the platform. A stub build runs but recognises nothing,
// so it is only a last resort. Checksum/signature files are skipped so that e.g.
// "plugin-linux-amd64.tar.gz.sha256" is never downloaded as the binary.
// Unsupported architectures (e.g. linux/386) return an error instead of
// silently selecting an amd64 build.
func selectReleaseAsset(names []string, goos, goarch string) (int, error) {
	pattern := releasePlatformPattern(goos, goarch)
	if pattern == "" {
		return -1, fmt.Errorf("unsupported platform: %s/%s", goos, goarch)
	}
	for _, accept := range []func(string) bool{
		func(n string) bool { return strings.Contains(n, "-whisper-onnx-") },
		func(n string) bool { return strings.Contains(n, "-onnx-") },
		func(n string) bool { return !strings.Contains(n, "-stub-") },
		func(n string) bool { return strings.Contains(n, "-stub-") },
		func(string) bool { return true },
	} {
		for i, name := range names {
			if isAuxiliaryAsset(name) || !strings.Contains(name, pattern) {
				continue
			}
			if accept(name) {
				return i, nil
			}
		}
	}
	return -1, fmt.Errorf("no binary found for platform %s/%s", goos, goarch)
}

func extractBinaryFromTarGz(r io.Reader, destPath, binaryName string) error {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar read: %w", err)
		}

		if header.Typeflag == tar.TypeReg {
			baseName := filepath.Base(header.Name)
			nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
			targetWithoutExt := strings.TrimSuffix(binaryName, filepath.Ext(binaryName))

			if baseName == binaryName || strings.HasPrefix(baseName, binaryName) || nameWithoutExt == targetWithoutExt {
				out, err := os.Create(destPath)
				if err != nil {
					return fmt.Errorf("create file: %w", err)
				}
				defer out.Close()

				if _, err := io.Copy(out, tr); err != nil {
					return fmt.Errorf("write file: %w", err)
				}

				if runtime.GOOS != "windows" {
					if err := os.Chmod(destPath, 0o755); err != nil {
						return fmt.Errorf("chmod: %w", err)
					}
				}
				return nil
			}
		}
	}
	return fmt.Errorf("binary %s not found in archive", binaryName)
}

func extractBinaryFromZip(r io.Reader, destPath, binaryName string) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read zip data: %w", err)
	}

	readerAt := bytes.NewReader(data)
	zipReader, err := zip.NewReader(readerAt, int64(len(data)))
	if err != nil {
		return fmt.Errorf("zip reader: %w", err)
	}

	for _, file := range zipReader.File {
		baseName := filepath.Base(file.Name)
		nameWithoutExt := strings.TrimSuffix(baseName, filepath.Ext(baseName))
		targetWithoutExt := strings.TrimSuffix(binaryName, filepath.Ext(binaryName))

		if runtime.GOOS == "windows" {
			nameWithoutExt = strings.TrimSuffix(baseName, ".exe")
		}

		if baseName == binaryName || strings.HasPrefix(baseName, binaryName) || nameWithoutExt == targetWithoutExt {
			rc, err := file.Open()
			if err != nil {
				return fmt.Errorf("open file in zip: %w", err)
			}
			defer rc.Close()

			out, err := os.Create(destPath)
			if err != nil {
				return fmt.Errorf("create file: %w", err)
			}
			defer out.Close()

			if _, err := io.Copy(out, rc); err != nil {
				return fmt.Errorf("write file: %w", err)
			}

			if runtime.GOOS != "windows" {
				if err := os.Chmod(destPath, 0o755); err != nil {
					return fmt.Errorf("chmod: %w", err)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("binary %s not found in zip archive", binaryName)
}

func ensureDoclingModel(owner, repo string, progress chan<- string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	modelDir := filepath.Join(home, ".gleann", "models", "granite-docling")
	modelPath := filepath.Join(modelDir, "granite_docling_bitnet_dense.eifm")
	if _, err := os.Stat(modelPath); err == nil {
		progress <- "✓ Granite Docling BitNet model already present"
		return
	}
	if matches, _ := filepath.Glob(filepath.Join(modelDir, "granite_docling_bitnet_dense*.eifm")); len(matches) > 0 {
		progress <- fmt.Sprintf("✓ Granite Docling BitNet model present (%s)", filepath.Base(matches[0]))
		return
	}

	progress <- "📦 Downloading Granite Docling BitNet model from release assets..."
	os.MkdirAll(modelDir, 0o755)

	ctx := context.Background()
	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := fetchLatestRelease(ctx, owner, repo, &release); err != nil {
		progress <- fmt.Sprintf("⚠️  Could not fetch release info: %v", err)
		return
	}

	foundModel := false
	for _, asset := range release.Assets {
		if strings.HasPrefix(asset.Name, "granite_docling_bitnet_dense") && strings.HasSuffix(asset.Name, ".eifm") {
			foundModel = true
		}
	}
	if !foundModel {
		progress <- "ℹ️  This release has no Docling VLM model; scanned pages are read by the ONNX OCR instead (optional: `gleann-plugin-docs models download --vlm`)"
		return
	}

	for _, asset := range release.Assets {
		if strings.HasPrefix(asset.Name, "granite_docling_bitnet_dense") && strings.HasSuffix(asset.Name, ".eifm") {
			dest := filepath.Join(modelDir, asset.Name)
			progress <- fmt.Sprintf("   Downloading %s (Docling VLM)...", asset.Name)
			if err := fetchToFile(ctx, asset.BrowserDownloadURL, dest); err == nil {
				progress <- "   ✓ Docling VLM model downloaded"
				if asset.Name != "granite_docling_bitnet_dense.eifm" {
					os.Symlink(dest, filepath.Join(modelDir, "granite_docling_bitnet_dense.eifm"))
				}
			} else {
				progress <- fmt.Sprintf("   ⚠️  Model download failed: %v", err)
			}
		} else if asset.Name == "tokenizer.bin" {
			dest := filepath.Join(modelDir, "tokenizer.bin")
			progress <- "   Downloading tokenizer.bin..."
			if err := fetchToFile(ctx, asset.BrowserDownloadURL, dest); err == nil {
				progress <- "   ✓ Tokenizer downloaded"
			}
		}
	}
}
