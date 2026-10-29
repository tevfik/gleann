package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPluginInstallerHelpers(t *testing.T) {
	// 1. pluginOwner
	origOwner := os.Getenv("GLEANN_PLUGIN_OWNER")
	defer os.Setenv("GLEANN_PLUGIN_OWNER", origOwner)

	os.Setenv("GLEANN_PLUGIN_OWNER", "custom-org")
	if got := pluginOwner(); got != "custom-org" {
		t.Errorf("pluginOwner() = %q, want custom-org", got)
	}

	os.Unsetenv("GLEANN_PLUGIN_OWNER")
	if got := pluginOwner(); got != defaultPluginOwner {
		t.Errorf("pluginOwner() = %q, want %q", got, defaultPluginOwner)
	}

	// 2. repoName
	if got := repoName("https://github.com/foo/bar.git"); got != "bar" {
		t.Errorf("repoName() = %q, want bar", got)
	}
	if got := repoName("plain-name"); got != "plain-name" {
		t.Errorf("repoName() = %q, want plain-name", got)
	}

	// 3. parseGitHubURL
	owner, repo := parseGitHubURL("https://github.com/myorg/myrepo.git")
	if owner != "myorg" || repo != "myrepo" {
		t.Errorf("parseGitHubURL() = (%q, %q), want (myorg, myrepo)", owner, repo)
	}
	invalidOwner, invalidRepo := parseGitHubURL("https://gitlab.com/other")
	if invalidOwner != "" || invalidRepo != "" {
		t.Errorf("expected empty for invalid github url, got (%q, %q)", invalidOwner, invalidRepo)
	}

	// 4. venvBinary & findPython3
	vb := venvBinary("/tmp/venv", "pip")
	if vb == "" {
		t.Errorf("expected non-empty venvBinary")
	}
	py := findPython3()
	if py == "" {
		t.Errorf("expected non-empty python3 binary")
	}

	// 5. copyDir / linkOrCopy
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(srcDir, dstDir); err != nil {
		t.Fatalf("copyDir failed: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(dstDir, "test.txt"))
	if err != nil || string(copied) != "hello" {
		t.Fatalf("copyDir verification failed: %s, %v", string(copied), err)
	}

	linkDst := filepath.Join(tmp, "link-dst")
	if err := linkOrCopy(srcDir, linkDst); err != nil {
		t.Fatalf("linkOrCopy failed: %v", err)
	}
}

func TestPluginUpdateHelpers(t *testing.T) {
	if got := orUnknown(""); got != "unknown" {
		t.Errorf("orUnknown(\"\") = %q, want unknown", got)
	}
	if got := orUnknown("v1.0.0"); got != "v1.0.0" {
		t.Errorf("orUnknown(\"v1.0.0\") = %q, want v1.0.0", got)
	}

	home, _ := os.UserHomeDir()
	expanded := expandHome("~/myplugins", home)
	expected := filepath.Join(home, "/myplugins")
	if expanded != expected {
		t.Errorf("expandHome() = %q, want %q", expanded, expected)
	}

	noExpand := expandHome("/absolute/path", home)
	if noExpand != "/absolute/path" {
		t.Errorf("expandHome(/absolute/path) = %q", noExpand)
	}

	// fileExists test
	if fileExists("/non/existent/file") {
		t.Errorf("expected false for non-existent file")
	}
}
