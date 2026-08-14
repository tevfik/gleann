package gleann

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// defaultPluginOwner is the default GitHub/Gitea owner for gleann plugins.
const defaultPluginOwner = "tevfik"

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
	if strings.Contains(lang, "python") {
		return setupPythonPluginWithProgress(pluginDir, info.Name, progress)
	} else if strings.Contains(lang, "go") {
		return setupGoPluginWithProgress(pluginDir, info.Name, progress)
	}

	return fmt.Sprintf("Installed %s", info.Name), nil
}

func setupPythonPluginWithProgress(pluginDir, name string, progress chan<- string) (string, error) {
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
	if output, err := registerCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("register plugin: %s", string(output))
	}
	progress <- "✓ Plugin registered"

	return fmt.Sprintf("Installed %s (Python)", name), nil
}

func setupGoPluginWithProgress(pluginDir, name string, progress chan<- string) (string, error) {
	binaryPath := filepath.Join(pluginDir, name)

	if _, err := os.Stat(binaryPath); err == nil {
		progress <- "✓ Binary already installed"
		return fmt.Sprintf("Installed %s (Go binary)", name), nil
	}

	if fi, err := os.Stat(pluginDir); err == nil && !fi.IsDir() {
		progress <- "🔍 Detected pre-built binary in repository root..."
		realPath, _ := filepath.EvalSymlinks(pluginDir)
		repoRoot := filepath.Dir(realPath)
		candidate := filepath.Join(repoRoot, name)
		if _, err := os.Stat(candidate); err == nil {
			progress <- "✓ Binary found in repository root"
			return fmt.Sprintf("Installed %s (Go binary)", name), nil
		}
	}

	progress <- "📥 Downloading binary from GitHub releases..."
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

	if err := downloadBinaryFromGitHub(owner, repo, binaryPath, progress); err == nil {
		progress <- "✓ Binary downloaded successfully"
		return fmt.Sprintf("Installed %s (Go binary from GitHub)", name), nil
	} else {
		progress <- fmt.Sprintf("⚠️  Download failed: %v", err)
	}

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

	return fmt.Sprintf("Built and installed %s (Go)", name), nil
}

func UninstallPlugin(name string) (string, error) {
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
		var filtered []Plugin
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
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName    string `json:"tag_name"`
		TarballURL string `json:"tarball_url"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	progress <- fmt.Sprintf("   Downloading source for %s...", release.TagName)

	resp, err = http.Get(release.TarballURL)
	if err != nil {
		return fmt.Errorf("failed to download tarball: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("tarball download returned status %d", resp.StatusCode)
	}

	progress <- "   Extracting source code..."
	if err := extractTarballToDir(resp.Body, destDir); err != nil {
		return fmt.Errorf("failed to extract tarball: %w", err)
	}
	return nil
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
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)
	resp, err := http.Get(apiURL)
	if err != nil {
		return fmt.Errorf("failed to fetch releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	if len(release.Assets) == 0 {
		return fmt.Errorf("no assets found in latest release")
	}

	progress <- fmt.Sprintf("   Found release %s", release.TagName)

	var platformPattern string
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH == "arm64" {
			platformPattern = "linux-arm64"
		} else {
			platformPattern = "linux-amd64"
		}
	case "darwin":
		if runtime.GOARCH == "arm64" {
			platformPattern = "darwin-arm64"
		} else {
			platformPattern = "darwin-amd64"
		}
	case "windows":
		platformPattern = "windows-amd64"
	default:
		return fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	var downloadURL, assetName string
	variants := []string{"-onnx-", "-v", "-stub-"}

	for _, variant := range variants {
		for _, asset := range release.Assets {
			if strings.Contains(asset.Name, platformPattern) && strings.Contains(asset.Name, variant) {
				downloadURL = asset.BrowserDownloadURL
				assetName = asset.Name
				progress <- fmt.Sprintf("   Downloading %s...", asset.Name)
				break
			}
		}
		if downloadURL != "" {
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no binary found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	resp, err = http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download archive: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	progress <- "   Extracting binary..."
	if strings.HasSuffix(assetName, ".tar.gz") {
		if err := extractBinaryFromTarGz(resp.Body, destPath, repo); err != nil {
			return fmt.Errorf("failed to extract tar.gz: %w", err)
		}
	} else if strings.HasSuffix(assetName, ".zip") {
		if err := extractBinaryFromZip(resp.Body, destPath, repo); err != nil {
			return fmt.Errorf("failed to extract zip: %w", err)
		}
	} else {
		out, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("failed to create file: %w", err)
		}
		defer out.Close()

		if _, err := io.Copy(out, resp.Body); err != nil {
			return fmt.Errorf("failed to write binary: %w", err)
		}
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(destPath, 0o755); err != nil {
			return fmt.Errorf("failed to make executable: %w", err)
		}
	}

	return nil
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
