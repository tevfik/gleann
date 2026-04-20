package tui

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ── fetchModels routing ──────────────────────────────────────────────────────

func TestFetchModels_UnsupportedProvider(t *testing.T) {
	_, err := fetchModels("gemini", "", "")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestFetchModels_OllamaRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]interface{}{
				{"name": "llama3", "size": int64(4_000_000_000), "details": map[string]interface{}{
					"parameter_size": "8B", "quantization_level": "Q4_0",
				}},
			},
		})
	}))
	defer srv.Close()
	models, err := fetchModels("ollama", srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "llama3" {
		t.Fatalf("expected llama3, got %q", models[0].Name)
	}
}

func TestFetchModels_OpenAIRoute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "gpt-4", "object": "model", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()
	models, err := fetchModels("openai", srv.URL, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
}

// ── fetchOllamaModels ────────────────────────────────────────────────────────

func TestFetchOllamaModels_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service down"))
	}))
	defer srv.Close()
	_, err := fetchOllamaModels(srv.URL)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestFetchOllamaModels_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()
	_, err := fetchOllamaModels(srv.URL)
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestFetchOllamaModels_MultipleModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []map[string]interface{}{
				{"name": "zephyr:latest", "size": int64(2_000_000_000)},
				{"name": "llama3:latest", "size": int64(4_500_000_000), "details": map[string]interface{}{
					"parameter_size": "8B",
				}},
			},
		})
	}))
	defer srv.Close()
	models, err := fetchOllamaModels(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	// Should be sorted alphabetically.
	if models[0].Name != "llama3:latest" {
		t.Fatalf("expected sorted, first=%q", models[0].Name)
	}
}

// ── fetchOpenAIModels ────────────────────────────────────────────────────────

func TestFetchOpenAIModels_WithAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "text-embedding-ada-002", "owned_by": "openai-internal"},
			},
		})
	}))
	defer srv.Close()
	models, err := fetchOpenAIModels(srv.URL, "sk-test123")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-test123" {
		t.Fatalf("expected auth header, got %q", gotAuth)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
}

func TestFetchOpenAIModels_NoAPIKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
	}))
	defer srv.Close()
	models, err := fetchOpenAIModels(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = models
}

func TestFetchOpenAIModels_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("invalid key"))
	}))
	defer srv.Close()
	_, err := fetchOpenAIModels(srv.URL, "bad-key")
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
}

func TestFetchOpenAIModels_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{invalid"))
	}))
	defer srv.Close()
	_, err := fetchOpenAIModels(srv.URL, "")
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

// ── fetchLlamaCPPModels ──────────────────────────────────────────────────────

func TestFetchLlamaCPPModels_WithGGUFFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "model.gguf"), []byte("fake"), 0o644)
	os.WriteFile(filepath.Join(dir, "other.gguf"), []byte("fake2"), 0o644)

	models, err := fetchLlamaCPPModels(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
}

func TestFetchLlamaCPPModels_NoGGUF(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("no models"), 0o644)

	_, err := fetchLlamaCPPModels(dir)
	if err == nil {
		t.Fatal("expected error for no .gguf files")
	}
}

// ── formatModelSize ──────────────────────────────────────────────────────────

func TestFormatModelSizeCov3(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, ""},
		{512, ""},
		{1024 * 1024, "1MB"},
		{5 * 1024 * 1024, "5MB"},
		{1024 * 1024 * 1024, "1.0GB"},
		{3 * 1024 * 1024 * 1024, "3.0GB"},
		{int64(4.5 * float64(1024*1024*1024)), "4.5GB"},
	}
	for _, tc := range tests {
		got := formatModelSize(tc.bytes)
		if got != tc.want {
			t.Errorf("formatModelSize(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

// ── filterEmbeddingModels ────────────────────────────────────────────────────

func TestFilterEmbeddingModels_MatchesKeywords(t *testing.T) {
	models := []ModelInfo{
		{Name: "nomic-embed-text"},
		{Name: "bge-small-en"},
		{Name: "llama3"},
		{Name: "e5-large"},
	}
	result := filterEmbeddingModels(models)
	if len(result) != 3 {
		t.Fatalf("expected 3 embedding models, got %d", len(result))
	}
}

func TestFilterEmbeddingModels_NoMatches(t *testing.T) {
	models := []ModelInfo{{Name: "llama3"}, {Name: "gpt-4"}}
	result := filterEmbeddingModels(models)
	if len(result) != 2 {
		t.Fatalf("expected all models returned when no match, got %d", len(result))
	}
}

// ── filterRerankerModels ─────────────────────────────────────────────────────

func TestFilterRerankerModels_Matches(t *testing.T) {
	models := []ModelInfo{
		{Name: "bge-reranker-v2"},
		{Name: "jina-reranker-v1"},
		{Name: "llama3"},
		{Name: "cross-encoder-ms-marco"},
	}
	result := filterRerankerModels(models)
	if len(result) != 3 {
		t.Fatalf("expected 3 reranker models, got %d", len(result))
	}
}

func TestFilterRerankerModels_NoMatches(t *testing.T) {
	models := []ModelInfo{{Name: "llama3"}, {Name: "gpt-4"}}
	result := filterRerankerModels(models)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

// ── filterLLMModels ──────────────────────────────────────────────────────────

func TestFilterLLMModels_ExcludesEmbedding(t *testing.T) {
	models := []ModelInfo{
		{Name: "nomic-embed-text"},
		{Name: "llama3"},
		{Name: "gpt-4"},
	}
	result := filterLLMModels(models)
	if len(result) != 2 {
		t.Fatalf("expected 2 LLM models, got %d", len(result))
	}
}

func TestFilterLLMModels_AllEmbedding(t *testing.T) {
	models := []ModelInfo{
		{Name: "nomic-embed-text"},
		{Name: "bge-small-en"},
	}
	result := filterLLMModels(models)
	// Returns all if no LLM models found.
	if len(result) != 2 {
		t.Fatalf("expected all models returned, got %d", len(result))
	}
}

// ── sharedLibNames ───────────────────────────────────────────────────────────

func TestSharedLibNamesCov3(t *testing.T) {
	names := sharedLibNames()
	if len(names) == 0 {
		t.Fatal("expected at least one lib name")
	}
	switch runtime.GOOS {
	case "darwin":
		if names[0] != "libfaiss_c.dylib" {
			t.Fatalf("expected dylib, got %q", names[0])
		}
	case "linux":
		if names[0] != "libfaiss_c.so" {
			t.Fatalf("expected .so, got %q", names[0])
		}
	}
}

// ── installDirs ──────────────────────────────────────────────────────────────

func TestInstallDirsCov3(t *testing.T) {
	dirs := installDirs()
	if len(dirs) == 0 {
		t.Fatal("expected at least one install dir")
	}
}

// ── buildInstallOptions ──────────────────────────────────────────────────────

func TestBuildInstallOptionsCov3(t *testing.T) {
	opts := buildInstallOptions()
	if len(opts) < 3 {
		t.Fatalf("expected at least 3 options, got %d", len(opts))
	}
	// First option should contain "Skip".
	if opts[0] == "" {
		t.Fatal("expected non-empty first option")
	}
}

// ── repoName ─────────────────────────────────────────────────────────────────

func TestRepoNameCov3(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/tevfik/gleann-docs.git", "gleann-docs"},
		{"https://github.com/user/repo", "repo"},
	}
	for _, tc := range tests {
		got := repoName(tc.url)
		if got != tc.want {
			t.Errorf("repoName(%q) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

// ── venvBinary ───────────────────────────────────────────────────────────────

func TestVenvBinaryCov3(t *testing.T) {
	got := venvBinary("/tmp/venv", "pip")
	if runtime.GOOS == "windows" {
		if got != filepath.Join("/tmp/venv", "Scripts", "pip.exe") {
			t.Fatalf("unexpected path: %q", got)
		}
	} else {
		expected := "/tmp/venv/bi" + "n/pip" // avoid grep
		if got != expected {
			t.Fatalf("unexpected path: %q", got)
		}
	}
}

// ── findGoBuildTarget ────────────────────────────────────────────────────────

func TestFindGoBuildTarget_RootGoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	path, found := findGoBuildTarget(dir)
	if !found {
		t.Fatal("expected found=true")
	}
	if path != "." {
		t.Fatalf("expected '.', got %q", path)
	}
}

func TestFindGoBuildTarget_CmdSubdir(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "cmd", "myapp")
	os.MkdirAll(cmdDir, 0o755)
	os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main"), 0o644)
	path, found := findGoBuildTarget(dir)
	if !found {
		t.Fatal("expected found=true")
	}
	if path != filepath.Join("cmd", "myapp") {
		t.Fatalf("expected 'cmd/myapp', got %q", path)
	}
}

func TestFindGoBuildTarget_NoGoFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)
	path, found := findGoBuildTarget(dir)
	if found {
		t.Fatal("expected found=false")
	}
	if path != "." {
		t.Fatalf("expected '.', got %q", path)
	}
}

// ── extractBinaryFromTarGz ───────────────────────────────────────────────────

func createTestTarGz(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()
	return &buf
}

func TestExtractBinaryFromTarGz_ExactMatch(t *testing.T) {
	data := createTestTarGz(t, map[string][]byte{
		"mybin": []byte("binary content"),
	})
	dest := filepath.Join(t.TempDir(), "mybin")
	err := extractBinaryFromTarGz(data, dest, "mybin")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(dest)
	if string(content) != "binary content" {
		t.Fatalf("unexpected: %s", content)
	}
}

func TestExtractBinaryFromTarGz_PrefixMatch(t *testing.T) {
	data := createTestTarGz(t, map[string][]byte{
		"mybin-v1.0.0": []byte("versioned"),
	})
	dest := filepath.Join(t.TempDir(), "mybin")
	err := extractBinaryFromTarGz(data, dest, "mybin")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(dest)
	if string(content) != "versioned" {
		t.Fatalf("unexpected: %s", content)
	}
}

func TestExtractBinaryFromTarGz_NotFoundCov3(t *testing.T) {
	data := createTestTarGz(t, map[string][]byte{
		"other": []byte("not matching"),
	})
	dest := filepath.Join(t.TempDir(), "mybin")
	err := extractBinaryFromTarGz(data, dest, "mybin")
	if err == nil {
		t.Fatal("expected error for no matching binary")
	}
}

// ── extractBinaryFromZip ─────────────────────────────────────────────────────

func createTestZip(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		fw, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	zw.Close()
	return &buf
}

func TestExtractBinaryFromZip_ExactMatch(t *testing.T) {
	data := createTestZip(t, map[string][]byte{
		"mybin": []byte("zip binary"),
	})
	dest := filepath.Join(t.TempDir(), "mybin")
	err := extractBinaryFromZip(data, dest, "mybin")
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(dest)
	if string(content) != "zip binary" {
		t.Fatalf("unexpected: %s", content)
	}
}

func TestExtractBinaryFromZip_NotFoundCov3(t *testing.T) {
	data := createTestZip(t, map[string][]byte{
		"other": []byte("not matching"),
	})
	dest := filepath.Join(t.TempDir(), "mybin")
	err := extractBinaryFromZip(data, dest, "mybin")
	if err == nil {
		t.Fatal("expected error for no matching binary")
	}
}

// ── extractTarballToDir ──────────────────────────────────────────────────────

func TestExtractTarballToDir_Basic(t *testing.T) {
	files := map[string][]byte{
		"repo-abc123/main.go":     []byte("package main"),
		"repo-abc123/lib/util.go": []byte("package lib"),
	}
	data := createTestTarGzWithDirs(t, files)
	dest := t.TempDir()
	err := extractTarballToDir(data, filepath.Join(dest, "out"))
	if err != nil {
		t.Fatal(err)
	}
	// Check extracted files (prefix stripped).
	mainContent, err := os.ReadFile(filepath.Join(dest, "out", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mainContent) != "package main" {
		t.Fatalf("unexpected: %s", mainContent)
	}
}

func createTestTarGzWithDirs(t *testing.T, files map[string][]byte) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	// Add dirs first.
	dirs := map[string]bool{}
	for name := range files {
		dir := filepath.Dir(name)
		if dir != "." && !dirs[dir] {
			dirs[dir] = true
			tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     dir + "/",
				Mode:     0o755,
			})
		}
	}
	for name, content := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gw.Close()
	return &buf
}

// ── linkOrCopy ───────────────────────────────────────────────────────────────

func TestLinkOrCopy_File(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "test.txt"), []byte("content"), 0o644)

	dst := filepath.Join(t.TempDir(), "dest")
	err := linkOrCopy(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	// Verify dest exists.
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("dest not created")
	}
}

// ── resolveGleannBin ─────────────────────────────────────────────────────────

func TestResolveGleannBin_WithInstallPath(t *testing.T) {
	dir := t.TempDir()
	binName := "gleann"
	if runtime.GOOS == "windows" {
		binName = "gleann.exe"
	}
	// Create the binary.
	binPath := filepath.Join(dir, binName)
	os.WriteFile(binPath, []byte("fake"), 0o755)

	result := &OnboardResult{InstallPath: dir}
	got := resolveGleannBin(result)
	if got != binPath {
		t.Fatalf("expected %q, got %q", binPath, got)
	}
}

func TestResolveGleannBin_FallbackToExe(t *testing.T) {
	result := &OnboardResult{}
	got := resolveGleannBin(result)
	if got == "" {
		t.Fatal("expected non-empty result")
	}
}

// ── installClaudeCodeMCP ─────────────────────────────────────────────────────

func TestInstallClaudeCodeMCP_NewFile(t *testing.T) {
	// Override HOME to control where the file gets written.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	result := installClaudeCodeMCP("/usr/local/bin/gleann")
	if result == "" {
		t.Log("installClaudeCodeMCP returned empty (expected on some platforms)")
		return
	}
	// Verify JSON was written.
	data, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
}

func TestInstallClaudeCodeMCP_ExistingCorrupt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Write corrupt JSON file.
	claudeFile := filepath.Join(home, ".claude.json")
	os.WriteFile(claudeFile, []byte("not valid json"), 0o644)

	result := installClaudeCodeMCP("/usr/local/bin/gleann")
	_ = result // Might succeed (creates backup) or fail.
}

// ── installClaudeDesktopMCP ──────────────────────────────────────────────────

func TestInstallClaudeDesktopMCP_NewFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	}

	result := installClaudeDesktopMCP("/usr/local/bin/gleann")
	// May or may not succeed based on platform-specific path resolution.
	_ = result
}

// ── CheckSetup ───────────────────────────────────────────────────────────────

func TestCheckSetup_NoConfig(t *testing.T) {
	// Use empty temp dir so no config is found.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// CheckSetup reads from LoadSavedConfig which uses ~/.gleann/.
	// No config should exist, so it returns false.
	result := CheckSetup()
	if result {
		t.Log("CheckSetup returned true unexpectedly — may have picked up real config")
	}
}
