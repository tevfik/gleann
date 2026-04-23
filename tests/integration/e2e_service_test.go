package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/autosetup"
	"github.com/tevfik/gleann/internal/service"
)

// ═══════════════════════════════════════════════════════════════
// E2E: Service Package — Cross-Platform Tests
// ═══════════════════════════════════════════════════════════════

func TestE2E_Service_StatusWhenStopped(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	st := service.GetStatus()
	if st.Running {
		t.Error("expected Running=false in clean temp dir")
	}
	if st.Platform != runtime.GOOS {
		t.Errorf("expected Platform=%s, got %s", runtime.GOOS, st.Platform)
	}
	if st.PID != 0 {
		t.Errorf("expected PID=0, got %d", st.PID)
	}
}

func TestE2E_Service_FormatStatusStopped(t *testing.T) {
	st := service.Status{
		Platform: runtime.GOOS,
	}
	output := service.FormatStatus(st)

	if !strings.Contains(output, "not running") {
		t.Error("expected 'not running' in format output")
	}
	if !strings.Contains(output, runtime.GOOS) {
		t.Errorf("expected platform %s in format output", runtime.GOOS)
	}
	if !strings.Contains(output, "not installed") {
		t.Error("expected 'not installed' in format output")
	}
}

func TestE2E_Service_FormatStatusRunning(t *testing.T) {
	st := service.Status{
		Running:   true,
		PID:       54321,
		Addr:      ":8080",
		Uptime:    "2h30m0s",
		Platform:  "linux",
		Installed: true,
	}
	output := service.FormatStatus(st)

	for _, want := range []string{"running", "54321", ":8080", "2h30m0s", "installed"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output, got:\n%s", want, output)
		}
	}
}

func TestE2E_Service_StopWhenNotRunning(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	err := service.Stop()
	if err == nil {
		t.Fatal("expected error when stopping non-running service")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("expected 'not running' in error, got: %s", err.Error())
	}
}

func TestE2E_Service_StalePidCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Create .gleann dir and stale PID file with non-existent PID.
	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")
	os.WriteFile(pidPath, []byte(`{"pid":99999999,"addr":":8080","started":"2024-01-01T00:00:00Z"}`), 0o644)

	st := service.GetStatus()
	if st.Running {
		t.Error("expected Running=false for dead PID")
	}

	// PID file should be cleaned up.
	if _, err := os.Stat(pidPath); err == nil {
		t.Error("expected stale PID file to be removed")
	}
}

func TestE2E_Service_ValidPidFileJSON(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// Use own PID (known alive).
	myPid := os.Getpid()
	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")

	pidData := map[string]any{
		"pid":     myPid,
		"addr":    ":9090",
		"started": "2024-06-15T10:30:00Z",
	}
	data, _ := json.Marshal(pidData)
	os.WriteFile(pidPath, data, 0o644)

	st := service.GetStatus()
	if !st.Running {
		t.Error("expected Running=true for own PID")
	}
	if st.PID != myPid {
		t.Errorf("expected PID=%d, got %d", myPid, st.PID)
	}
	if st.Addr != ":9090" {
		t.Errorf("expected Addr=:9090, got %s", st.Addr)
	}
	if st.Uptime == "" {
		t.Error("expected non-empty Uptime")
	}
}

func TestE2E_Service_MalformedPidFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidPath := filepath.Join(gleannD, "server.pid")

	// Write invalid JSON.
	os.WriteFile(pidPath, []byte("not json"), 0o644)

	st := service.GetStatus()
	if st.Running {
		t.Error("expected Running=false for malformed PID file")
	}
}

func TestE2E_Service_LogsTailing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)

	// Write 100 lines.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, fmt.Sprintf("log line %d", i))
	}
	os.WriteFile(filepath.Join(gleannD, "server.log"), []byte(strings.Join(lines, "\n")), 0o644)

	// Request last 10.
	output, err := service.Logs(10)
	if err != nil {
		t.Fatal(err)
	}
	outLines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(outLines) > 10 {
		t.Errorf("expected at most 10 lines, got %d", len(outLines))
	}
	// Last line should contain "99".
	if !strings.Contains(outLines[len(outLines)-1], "99") {
		t.Errorf("expected last line to contain '99', got %q", outLines[len(outLines)-1])
	}
}

func TestE2E_Service_LogsCRLF(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)

	// Write CRLF content (Windows-style).
	content := "line1\r\nline2\r\nline3\r\nline4\r\nline5\r\n"
	os.WriteFile(filepath.Join(gleannD, "server.log"), []byte(content), 0o644)

	output, err := service.Logs(3)
	if err != nil {
		t.Fatal(err)
	}
	// Should not have \r in the output.
	if strings.Contains(output, "\r") {
		t.Error("expected CRLF to be normalized to LF")
	}
}

func TestE2E_Service_LogsNoFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	_, err := service.Logs(10)
	if err == nil {
		t.Error("expected error when log file doesn't exist")
	}
}

// ── Platform-specific path validation ────────────────────────────────────

func TestE2E_Service_CrossPlatform_ServicePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	switch runtime.GOOS {
	case "linux":
		// systemd user service should be under ~/.config/systemd/user/
		expected := filepath.Join(home, ".config", "systemd", "user", "gleann.service")
		t.Logf("Linux systemd unit: %s", expected)
		// Verify path structure.
		if !strings.Contains(expected, "systemd") {
			t.Error("systemd path doesn't contain 'systemd'")
		}

	case "darwin":
		// launchd plist should be under ~/Library/LaunchAgents/
		expected := filepath.Join(home, "Library", "LaunchAgents", "com.gleann.server.plist")
		t.Logf("macOS launchd plist: %s", expected)
		if !strings.HasSuffix(expected, ".plist") {
			t.Error("launchd path doesn't end with .plist")
		}

	case "windows":
		// Task Scheduler uses schtasks command, no file path needed.
		t.Log("Windows: Task Scheduler (schtasks)")
	}
}

func TestE2E_Service_CrossPlatform_PidFilePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	expectedPid := filepath.Join(home, ".gleann", "server.pid")
	expectedLog := filepath.Join(home, ".gleann", "server.log")

	// On Windows, paths should use backslashes.
	if runtime.GOOS == "windows" {
		if strings.Contains(expectedPid, "/") {
			t.Error("Windows PID path should not contain forward slashes")
		}
	}

	t.Logf("PID file: %s", expectedPid)
	t.Logf("Log file: %s", expectedLog)
}

// ═══════════════════════════════════════════════════════════════
// E2E: Autosetup — Model Tiers, Detection, and Config
// ═══════════════════════════════════════════════════════════════

func TestE2E_Autosetup_DetectOllamaFull(t *testing.T) {
	// Mock Ollama with rich model set.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{
					{"name": "bge-m3:latest"},
					{"name": "nomic-embed-text:latest"},
					{"name": "gemma3:4b"},
					{"name": "qwen2.5:7b"},
					{"name": "bge-reranker:latest"},
				},
			})
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	res := autosetup.DetectOllama(srv.URL)
	if !res.OllamaFound {
		t.Fatal("expected OllamaFound=true")
	}
	if len(res.ModelsFound) != 5 {
		t.Fatalf("expected 5 models, got %d", len(res.ModelsFound))
	}
	// bge-m3 should be preferred over nomic-embed-text.
	if res.EmbeddingModel != "bge-m3:latest" {
		t.Errorf("expected bge-m3:latest as embedding, got %s", res.EmbeddingModel)
	}
	if res.RerankModel != "bge-reranker:latest" {
		t.Errorf("expected bge-reranker:latest, got %s", res.RerankModel)
	}
}

func TestE2E_Autosetup_DetectAllQuickStart(t *testing.T) {
	// No embedding model available — quick-start should pick nomic-embed-text.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "gemma3:4b"},
				{"name": "phi-4:latest"},
			},
		})
	}))
	defer srv.Close()

	dc := autosetup.DetectAll(srv.URL, true)
	if dc.EmbeddingModel != "nomic-embed-text" {
		t.Errorf("quick-start should pick nomic-embed-text, got %s", dc.EmbeddingModel)
	}
	if !dc.OllamaRunning {
		t.Error("expected OllamaRunning=true")
	}
}

func TestE2E_Autosetup_DetectAllNormalMode(t *testing.T) {
	// No embedding model available — normal mode should use bge-m3 default.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	dc := autosetup.DetectAll(srv.URL)
	if dc.EmbeddingModel != "bge-m3" {
		t.Errorf("normal mode should use bge-m3 fallback, got %s", dc.EmbeddingModel)
	}
}

func TestE2E_Autosetup_EnsureModelsFullCycle(t *testing.T) {
	pullRequested := make(map[string]bool)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{
					{"name": "gemma3:4b"},
				},
			})
		case r.URL.Path == "/api/pull" && r.Method == "POST":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if name, ok := req["name"].(string); ok {
				pullRequested[name] = true
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"status":"pulling manifest"}`)
			fmt.Fprintln(w, `{"status":"success"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	pulled, err := autosetup.EnsureModels(srv.URL, true, "bge-m3", "gemma3:4b")
	if err != nil {
		t.Fatal(err)
	}
	// bge-m3 is not available, should be pulled.
	if len(pulled) != 1 || pulled[0] != "bge-m3" {
		t.Errorf("expected [bge-m3] pulled, got %v", pulled)
	}
	// gemma3:4b is available, should not be pulled.
	if pullRequested["gemma3:4b"] {
		t.Error("gemma3:4b should not have been pulled (already available)")
	}
	if !pullRequested["bge-m3"] {
		t.Error("bge-m3 should have been pulled")
	}
}

func TestE2E_Autosetup_PullModelProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pull" && r.Method == "POST" {
			w.Header().Set("Content-Type", "application/x-ndjson")
			fmt.Fprintln(w, `{"status":"pulling manifest"}`)
			fmt.Fprintln(w, `{"status":"downloading","digest":"sha256:abc","total":10000,"completed":2000}`)
			fmt.Fprintln(w, `{"status":"downloading","digest":"sha256:abc","total":10000,"completed":5000}`)
			fmt.Fprintln(w, `{"status":"downloading","digest":"sha256:abc","total":10000,"completed":10000}`)
			fmt.Fprintln(w, `{"status":"verifying sha256 digest"}`)
			fmt.Fprintln(w, `{"status":"writing manifest"}`)
			fmt.Fprintln(w, `{"status":"success"}`)
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	var progressCalls []struct {
		Status    string
		Completed int64
		Total     int64
	}

	err := autosetup.PullModel(srv.URL, "test-model", func(status string, completed, total int64) {
		progressCalls = append(progressCalls, struct {
			Status    string
			Completed int64
			Total     int64
		}{status, completed, total})
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(progressCalls) < 5 {
		t.Errorf("expected at least 5 progress calls, got %d", len(progressCalls))
	}

	// Verify progress increases.
	var maxCompleted int64
	for _, p := range progressCalls {
		if p.Completed > maxCompleted {
			maxCompleted = p.Completed
		}
	}
	if maxCompleted != 10000 {
		t.Errorf("expected max completed=10000, got %d", maxCompleted)
	}
}

func TestE2E_Autosetup_PullModelNetworkError(t *testing.T) {
	err := autosetup.PullModel("http://localhost:19999", "model", nil)
	if err == nil {
		t.Fatal("expected error for unreachable host")
	}
	if !strings.Contains(err.Error(), "cannot reach Ollama") {
		t.Errorf("expected 'cannot reach Ollama' in error, got: %s", err.Error())
	}
}

func TestE2E_Autosetup_ApplyAndReadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	dc := autosetup.DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		OllamaRunning:  true,
		EmbeddingModel: "nomic-embed-text",
		LLMModel:       "gemma3:4b",
		RerankModel:    "bge-reranker",
		IndexDir:       filepath.Join(tmpDir, ".gleann", "indexes"),
		MCPEnabled:     true,
		ServerEnabled:  false,
	}

	// Apply config.
	if err := autosetup.ApplyDetectedConfig(dc); err != nil {
		t.Fatal(err)
	}

	// Read it back.
	cfgPath := filepath.Join(tmpDir, ".gleann", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}

	checks := map[string]any{
		"embedding_model":    "nomic-embed-text",
		"llm_model":          "gemma3:4b",
		"rerank_model":       "bge-reranker",
		"rerank_enabled":     true,
		"embedding_provider": "ollama",
		"completed":          true,
	}
	for key, want := range checks {
		got := cfg[key]
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("config[%s] = %v, want %v", key, got, want)
		}
	}
}

func TestE2E_Autosetup_FormatDetectedConfigBox(t *testing.T) {
	dc := autosetup.DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		OllamaRunning:  true,
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		RerankModel:    "",
		IndexDir:       "/home/test/.gleann/indexes",
		MCPEnabled:     true,
		ServerEnabled:  false,
	}

	box := autosetup.FormatDetectedConfig(dc)

	// Must contain box characters.
	if !strings.Contains(box, "┌") || !strings.Contains(box, "└") {
		t.Error("expected box drawing characters")
	}
	// Must contain all config values.
	for _, want := range []string{"bge-m3", "gemma3:4b", "(none)", "enabled", "Accept"} {
		if !strings.Contains(box, want) {
			t.Errorf("expected box to contain %q", want)
		}
	}
}

func TestE2E_Autosetup_TiersAreSorted(t *testing.T) {
	tiers := autosetup.EmbeddingTiers()
	if len(tiers) < 3 {
		t.Fatalf("expected at least 3 tiers, got %d", len(tiers))
	}

	// Tiers should be in ascending order.
	lastTier := 0
	for _, tier := range tiers {
		if tier.Tier < lastTier {
			t.Errorf("tiers not in ascending order: tier %d after tier %d (%s)",
				tier.Tier, lastTier, tier.Name)
		}
		lastTier = tier.Tier
	}

	// Each tier should have non-empty fields.
	for _, tier := range tiers {
		if tier.Name == "" {
			t.Error("tier has empty Name")
		}
		if tier.Size == "" {
			t.Error("tier has empty Size")
		}
		if tier.Tier < 1 || tier.Tier > 3 {
			t.Errorf("tier %s has invalid tier number: %d", tier.Name, tier.Tier)
		}
	}
}

func TestE2E_Autosetup_EnsureConfigIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "gemma3:4b"},
			},
		})
	}))
	defer srv.Close()

	// First call should bootstrap.
	bootstrapped, err := autosetup.EnsureConfig(srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrapped {
		t.Error("expected first call to bootstrap")
	}

	// Read the created config.
	cfgPath := filepath.Join(tmpDir, ".gleann", "config.json")
	data1, _ := os.ReadFile(cfgPath)

	// Second call should be no-op.
	bootstrapped, err = autosetup.EnsureConfig(srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapped {
		t.Error("expected second call to be no-op")
	}

	// File should not have changed.
	data2, _ := os.ReadFile(cfgPath)
	if string(data1) != string(data2) {
		t.Error("config file changed on second call")
	}
}

func TestE2E_Autosetup_HasModelPrefixMatching(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{
				{"name": "bge-m3:latest"},
				{"name": "nomic-embed-text:v1.5"},
			},
		})
	}))
	defer srv.Close()

	// Exact match.
	if !autosetup.HasModel(srv.URL, "bge-m3:latest") {
		t.Error("should find bge-m3:latest (exact)")
	}
	// Prefix match.
	if !autosetup.HasModel(srv.URL, "bge-m3") {
		t.Error("should find bge-m3 (prefix)")
	}
	if !autosetup.HasModel(srv.URL, "nomic-embed-text") {
		t.Error("should find nomic-embed-text (prefix)")
	}
	// No match.
	if autosetup.HasModel(srv.URL, "snowflake-arctic") {
		t.Error("should not find snowflake-arctic")
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: Cross-Platform Service Configuration Validation
// ═══════════════════════════════════════════════════════════════

func TestE2E_CrossPlatform_ServiceConfigDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}

	// Verify expected service config locations per platform.
	switch runtime.GOOS {
	case "linux":
		dir := filepath.Join(home, ".config", "systemd", "user")
		t.Logf("systemd user dir: %s", dir)
		// Should be a valid path (not necessarily existing).
		if !filepath.IsAbs(dir) {
			t.Error("expected absolute path")
		}

	case "darwin":
		dir := filepath.Join(home, "Library", "LaunchAgents")
		t.Logf("launchd agents dir: %s", dir)
		// This dir typically exists on macOS.
		if !filepath.IsAbs(dir) {
			t.Error("expected absolute path")
		}

	case "windows":
		t.Log("Windows uses Task Scheduler (schtasks.exe), no config directory")
	}
}

func TestE2E_CrossPlatform_ProcessAliveness(t *testing.T) {
	// Own PID is guaranteed alive on all platforms.
	myPid := os.Getpid()
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	gleannD := filepath.Join(tmpDir, ".gleann")
	os.MkdirAll(gleannD, 0o755)
	pidData, _ := json.Marshal(map[string]any{
		"pid":     myPid,
		"addr":    ":8080",
		"started": "2024-01-01T00:00:00Z",
	})
	os.WriteFile(filepath.Join(gleannD, "server.pid"), pidData, 0o644)

	st := service.GetStatus()
	if !st.Running {
		t.Errorf("expected own PID (%d) to be detected as running on %s", myPid, runtime.GOOS)
	}
}

func TestE2E_CrossPlatform_GleannDirCreation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("USERPROFILE", tmpDir)

	// ApplyDetectedConfig should create ~/.gleann/ directory.
	dc := autosetup.DetectedConfig{
		OllamaHost:     "http://localhost:11434",
		EmbeddingModel: "bge-m3",
		LLMModel:       "gemma3:4b",
		IndexDir:       filepath.Join(tmpDir, ".gleann", "indexes"),
	}

	if err := autosetup.ApplyDetectedConfig(dc); err != nil {
		t.Fatal(err)
	}

	// Verify directory was created.
	gleannD := filepath.Join(tmpDir, ".gleann")
	info, err := os.Stat(gleannD)
	if err != nil {
		t.Fatal("~/.gleann not created:", err)
	}
	if !info.IsDir() {
		t.Error("~/.gleann is not a directory")
	}

	// On Unix, check permissions.
	if runtime.GOOS != "windows" {
		perm := info.Mode().Perm()
		if perm&0o700 != 0o700 {
			t.Errorf("expected rwx for owner, got %o", perm)
		}
	}
}
