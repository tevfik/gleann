// Package multimodal — end-to-end live test against gleann-plugin-vision.
//
// This file is gated behind `gleannvision_e2e` build tag so it only runs
// when the real binary is up. CI/dev runs:
//
//   go run github.com/tevfik/gleann-plugin-vision --serve --port 18767 &
//   go test -tags gleannvision_e2e ./internal/multimodal/...
//
//go:build gleannvision_e2e
// +build gleannvision_e2e

package multimodal

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRealPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			c := uint8((x*y + 17) % 255)
			img.Set(x, y, color.RGBA{c, c, c, 0xff})
		}
	}
	f, _ := os.Create(path)
	defer f.Close()
	_ = png.Encode(f, img)
}

// TestE2E_RealVisionPlugin — round-trips an image through the actual
// running binary and asserts the v2 schema fields populate.
func TestE2E_RealVisionPlugin(t *testing.T) {
	url := os.Getenv("GLEANN_VISION_PLUGIN_URL")
	if url == "" {
		url = "http://localhost:18767"
	}

	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get(url + "/health"); err != nil {
		t.Skipf("plugin not running at %s: %v", url, err)
	}

	dir := t.TempDir()
	p := filepath.Join(dir, "e2e.png")
	writeRealPNG(t, p)
	vr, err := CallVisionPlugin(url, p)
	if err != nil {
		t.Fatalf("CallVisionPlugin: %v", err)
	}
	if vr.SchemaVersion != 2 {
		t.Errorf("schema_version=%d, want 2", vr.SchemaVersion)
	}
	if len(vr.CLIPEmbedding) != 64 {
		t.Errorf("CLIP embedding len=%d, want 64", len(vr.CLIPEmbedding))
	}
	if vr.Width != 64 || vr.Height != 64 {
		t.Errorf("dims=%dx%d, want 64x64", vr.Width, vr.Height)
	}
}
