// Package multimodal — cache + i18n behavioural tests.
package multimodal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFileFingerprint_ChangesWithInputs verifies the fingerprint includes
// content, model, prompt version, and lang so that none of those axes can
// be silently confused in the cache.
func TestFileFingerprint_ChangesWithInputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	if err := os.WriteFile(a, []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("BBBB"), 0o644); err != nil {
		t.Fatal(err)
	}

	fpA, _ := fileFingerprint(a, "m1", "en")
	fpA2, _ := fileFingerprint(a, "m1", "en")
	fpB, _ := fileFingerprint(b, "m1", "en")
	fpM, _ := fileFingerprint(a, "m2", "en")
	fpL, _ := fileFingerprint(a, "m1", "tr")

	if fpA == "" || fpA != fpA2 {
		t.Fatalf("identical inputs should yield identical fp: %q vs %q", fpA, fpA2)
	}
	if fpA == fpB || fpA == fpM || fpA == fpL {
		t.Fatalf("different inputs should yield different fps: %q %q %q %q", fpA, fpB, fpM, fpL)
	}
}

// TestCache_HitMissCycle covers the full store→load→stats path with an
// isolated cache directory so the user's real ~/.gleann is untouched.
func TestCache_HitMissCycle(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "")
	ResetCacheStats()

	fp := "deadbeef"
	if _, ok := loadCached(fp); ok {
		t.Fatal("empty cache should miss")
	}
	storeCached(fp, cachedDescription{Description: "hello", Model: "m", Lang: "en"})
	cd, ok := loadCached(fp)
	if !ok {
		t.Fatal("expected hit after store")
	}
	if cd.Description != "hello" {
		t.Fatalf("description corrupted: %q", cd.Description)
	}
	h, m := CacheStats()
	if h != 1 || m != 1 {
		t.Fatalf("hits=%d misses=%d, want 1/1", h, m)
	}
}

// TestCache_DisabledByEnv ensures GLEANN_MULTIMODAL_CACHE=0 bypasses the
// cache so users can debug stale results without nuking the directory.
func TestCache_DisabledByEnv(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	t.Setenv("GLEANN_MULTIMODAL_CACHE", "0")
	storeCached("xyz", cachedDescription{Description: "should-not-persist"})
	if _, ok := loadCached("xyz"); ok {
		t.Fatal("cache should be disabled by env")
	}
}

// TestDescriptionPrompt_Locale covers Bug #10 — Turkish locale must
// produce a Turkish prompt for each media type so VLM responses are in TR.
func TestDescriptionPrompt_Locale(t *testing.T) {
	en := &Processor{Lang: "en"}
	tr := &Processor{Lang: "tr"}

	cases := []MediaType{MediaTypeImage, MediaTypeAudio, MediaTypeVideo}
	for _, mt := range cases {
		eng := en.descriptionPrompt(mt, "f")
		trk := tr.descriptionPrompt(mt, "f")
		if eng == trk {
			t.Errorf("mt=%d: EN and TR prompts must differ", mt)
		}
		if !strings.Contains(strings.ToLower(trk), "t\u00fcrk\u00e7e") {
			t.Errorf("mt=%d: TR prompt must contain 'türkçe', got %q", mt, trk)
		}
	}
}

// TestNewProcessor_LangFromEnv: GLEANN_MULTIMODAL_LANG=tr should propagate.
func TestNewProcessor_LangFromEnv(t *testing.T) {
	t.Setenv("GLEANN_MULTIMODAL_LANG", "tr")
	p := NewProcessor("", "")
	if p.Lang != "tr" {
		t.Fatalf("Lang=%q, want tr", p.Lang)
	}
	t.Setenv("GLEANN_MULTIMODAL_LANG", "xx")
	p = NewProcessor("", "")
	if p.Lang != "en" {
		t.Fatalf("unknown lang must fall back to en, got %q", p.Lang)
	}
}
