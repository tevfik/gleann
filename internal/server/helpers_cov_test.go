package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

// ── parseTier ─────────────────────────────────────────────────

func TestParseTierCov(t *testing.T) {
	tests := []struct {
		in   string
		want memory.Tier
	}{
		{"short", memory.TierShort},
		{"SHORT", memory.TierShort},
		{"medium", memory.TierMedium},
		{"Medium", memory.TierMedium},
		{"long", memory.TierLong},
		{"LONG", memory.TierLong},
		{"", memory.TierShort},
		{"unknown", memory.TierShort},
	}
	for _, tt := range tests {
		got := parseTier(tt.in)
		if got != tt.want {
			t.Errorf("parseTier(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// ── truncate ──────────────────────────────────────────────────

func TestTruncateCov(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Fatal("short string should not be truncated")
	}
	if truncate("hello world", 5) != "hello..." {
		t.Fatalf("got %q", truncate("hello world", 5))
	}
	if truncate("", 5) != "" {
		t.Fatal("empty string")
	}
}

// ── layerSet ──────────────────────────────────────────────────

func TestLayerSetCov(t *testing.T) {
	// Default: all layers
	m := layerSet(nil)
	if !m["blocks"] || !m["graph"] || !m["vector"] {
		t.Fatal("expected all defaults")
	}

	// Custom
	m = layerSet([]string{"Blocks", "GRAPH"})
	if !m["blocks"] || !m["graph"] || m["vector"] {
		t.Fatal("unexpected layers")
	}
}

// ── containsAllTags ───────────────────────────────────────────

func TestContainsAllTagsCov(t *testing.T) {
	if !containsAllTags([]string{"a", "b", "c"}, []string{"a", "c"}) {
		t.Fatal("should contain")
	}
	if containsAllTags([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("missing 'c'")
	}
	if !containsAllTags([]string{"A", "B"}, []string{"a", "b"}) {
		t.Fatal("case insensitive")
	}
	if !containsAllTags([]string{"x"}, nil) {
		t.Fatal("empty required = always match")
	}
}

// ── parseTimeOrDuration ───────────────────────────────────────

func TestParseTimeOrDurationCov(t *testing.T) {
	// RFC3339
	ts := parseTimeOrDuration("2024-01-15T10:00:00Z")
	if ts.Year() != 2024 || ts.Month() != 1 || ts.Day() != 15 {
		t.Fatalf("unexpected: %v", ts)
	}

	// Go duration
	before := time.Now()
	ts = parseTimeOrDuration("24h")
	// Should be approximately 24 hours ago
	diff := before.Sub(ts)
	if diff < 23*time.Hour || diff > 25*time.Hour {
		t.Fatalf("unexpected diff: %v", diff)
	}

	// Invalid
	ts = parseTimeOrDuration("not-a-time")
	if !ts.IsZero() {
		t.Fatal("expected zero time for invalid input")
	}
}

// ── parseDuration ─────────────────────────────────────────────

func TestParseDurationCov(t *testing.T) {
	// Standard Go duration
	d, err := parseDuration("2h")
	if err != nil || d != 2*time.Hour {
		t.Fatalf("expected 2h, got %v err=%v", d, err)
	}

	// Day shorthand
	d, err = parseDuration("7d")
	if err != nil || d != 7*24*time.Hour {
		t.Fatalf("expected 7 days, got %v err=%v", d, err)
	}

	// Week shorthand
	d, err = parseDuration("2w")
	if err != nil || d != 2*7*24*time.Hour {
		t.Fatalf("expected 2 weeks, got %v err=%v", d, err)
	}

	// Invalid
	_, err = parseDuration("xyz")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── parseIndexFromModel ───────────────────────────────────────

func TestParseIndexFromModelCov(t *testing.T) {
	tests := []struct {
		model string
		want  []string
	}{
		{"gleann/my-docs", []string{"my-docs"}},
		{"gleann/a,b", []string{"a", "b"}},
		{"gleann/", nil},
		{"gpt-4o", nil},
		{"gleann/a, b, c", []string{"a", "b", "c"}},
		{"", nil},
	}
	for _, tt := range tests {
		got := parseIndexFromModel(tt.model)
		if len(got) != len(tt.want) {
			t.Errorf("parseIndexFromModel(%q) = %v, want %v", tt.model, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseIndexFromModel(%q)[%d] = %q, want %q", tt.model, i, got[i], tt.want[i])
			}
		}
	}
}

// ── buildRecallContext ────────────────────────────────────────

func TestBuildRecallContextCov_Empty(t *testing.T) {
	resp := UnifiedRecallResponse{}
	ctx := buildRecallContext(resp)
	if ctx != "<memory_context>\n</memory_context>" {
		t.Fatalf("unexpected: %q", ctx)
	}
}

func TestBuildRecallContextCov_WithBlocks(t *testing.T) {
	resp := UnifiedRecallResponse{
		Blocks: []RecallBlock{
			{Tier: "long", Content: "test fact", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	ctx := buildRecallContext(resp)
	if len(ctx) < 30 {
		t.Fatal("expected longer context")
	}
}

func TestBuildRecallContextCov_WithGraph(t *testing.T) {
	resp := UnifiedRecallResponse{
		Graph: &RecallGraph{
			Nodes: []RecallGraphNode{{ID: "1", Content: "A"}},
			Edges: []RecallGraphEdge{{From: "A", Relation: "CALLS", To: "B"}},
		},
	}
	ctx := buildRecallContext(resp)
	if len(ctx) < 30 {
		t.Fatal("expected longer context")
	}
}

func TestBuildRecallContextCov_WithVector(t *testing.T) {
	resp := UnifiedRecallResponse{
		Vector: []RecallHit{
			{Content: "doc text", Score: 0.95, Source: "test.md"},
		},
	}
	ctx := buildRecallContext(resp)
	if len(ctx) < 30 {
		t.Fatal("expected longer context")
	}
}

// ── clientIP, sanitizeIP, isPrivate ───────────────────────────

func TestClientIPCov_Direct(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "203.0.113.50:12345",
	}
	ip := clientIP(r)
	if ip != "203.0.113.50" {
		t.Fatalf("expected 203.0.113.50, got %q", ip)
	}
}

func TestClientIPCov_Loopback_XFF(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "127.0.0.1:12345",
		Header:     http.Header{"X-Forwarded-For": {"1.2.3.4, 5.6.7.8"}},
	}
	ip := clientIP(r)
	if ip != "1.2.3.4" {
		t.Fatalf("expected 1.2.3.4, got %q", ip)
	}
}

func TestClientIPCov_Loopback_XRealIP(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "127.0.0.1:12345",
		Header:     make(http.Header),
	}
	r.Header.Set("X-Real-IP", "9.8.7.6")
	ip := clientIP(r)
	if ip != "9.8.7.6" {
		t.Fatalf("expected 9.8.7.6, got %q", ip)
	}
}

func TestClientIPCov_BadRemoteAddr(t *testing.T) {
	r := &http.Request{
		RemoteAddr: "not-valid",
	}
	ip := clientIP(r)
	if ip != "not-valid" {
		t.Fatalf("expected raw RemoteAddr, got %q", ip)
	}
}

func TestSanitizeIPCov2(t *testing.T) {
	if sanitizeIP("1.2.3.4") != "1.2.3.4" {
		t.Fatal("valid IP")
	}
	if sanitizeIP("  1.2.3.4") != "1.2.3.4" {
		t.Fatal("leading spaces")
	}
	if sanitizeIP("not-an-ip") != "" {
		t.Fatal("invalid IP should return empty")
	}
}

func TestIsPrivateCov2(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"192.168.1.1:80", true},
		{"10.0.0.1:80", true},
		{"203.0.113.50:80", false},
		{"not-parseable", false},
	}
	for _, tt := range tests {
		if got := isPrivate(tt.addr); got != tt.want {
			t.Errorf("isPrivate(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}

// ── envSeconds ────────────────────────────────────────────────

func TestEnvSecondsCov(t *testing.T) {
	// Not set
	if envSeconds("GLEANN_TEST_NONEXISTENT_ENV_VAR") != 0 {
		t.Fatal("expected 0 for unset env")
	}

	// Set
	t.Setenv("GLEANN_TEST_TIMEOUT_COV", "45")
	if envSeconds("GLEANN_TEST_TIMEOUT_COV") != 45 {
		t.Fatal("expected 45")
	}

	// Invalid
	t.Setenv("GLEANN_TEST_TIMEOUT_COV", "notanumber")
	if envSeconds("GLEANN_TEST_TIMEOUT_COV") != 0 {
		t.Fatal("expected 0 for invalid")
	}

	// Negative
	t.Setenv("GLEANN_TEST_TIMEOUT_COV", "-5")
	if envSeconds("GLEANN_TEST_TIMEOUT_COV") != 0 {
		t.Fatal("expected 0 for negative")
	}
}

// ── pickTimeout ───────────────────────────────────────────────

func TestPickTimeoutCov(t *testing.T) {
	tests := []struct {
		path string
		want time.Duration
	}{
		{"/api/ask", globalTimeouts.ask},
		{"/v1/chat/completions", globalTimeouts.ask},
		{"/api/search", globalTimeouts.search},
		{"/index/build", globalTimeouts.build},
		{"/api/other", globalTimeouts.dflt},
	}
	for _, tt := range tests {
		got := pickTimeout(tt.path)
		if got != tt.want {
			t.Errorf("pickTimeout(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// ── openapi helpers ───────────────────────────────────────────

func TestParamNameCov(t *testing.T) {
	m := paramName()
	if m["name"] != "name" {
		t.Fatal("expected name field")
	}
	if m["in"] != "path" {
		t.Fatal("expected path")
	}
	schema, ok := m["schema"].(map[string]any)
	if !ok || schema["type"] != "string" {
		t.Fatal("expected string type in schema")
	}
}

func TestRefSchemaCov(t *testing.T) {
	m := refSchema("SearchResult")
	ref, ok := m["$ref"].(string)
	if !ok {
		t.Fatal("expected $ref")
	}
	expected := fmt.Sprintf("#/components/schemas/%s", "SearchResult")
	if ref != expected {
		t.Fatalf("expected %q, got %q", expected, ref)
	}
}
