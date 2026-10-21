package gleann

import "testing"

// The asset names gleann-plugin-sound v1.0.0 publishes. A stub build runs but
// recognises nothing, so it must lose to every real build.
func TestSelectReleaseAssetSoundRelease(t *testing.T) {
	assets := []string{
		"checksums.txt",
		"gleann-plugin-sound-onnx-v1.0.0-linux-amd64.tar.gz",
		"gleann-plugin-sound-stub-v1.0.0-darwin-arm64.tar.gz",
		"gleann-plugin-sound-stub-v1.0.0-linux-amd64.tar.gz",
		"gleann-plugin-sound-stub-v1.0.0-windows-amd64.zip",
		"gleann-plugin-sound-v1.0.0-darwin-arm64.tar.gz",
		"gleann-plugin-sound-v1.0.0-linux-amd64.tar.gz",
		"gleann-plugin-sound-v1.0.0-windows-amd64.zip",
		"gleann-plugin-sound-whisper-onnx-v1.0.0-linux-amd64.tar.gz",
	}
	for _, c := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "gleann-plugin-sound-whisper-onnx-v1.0.0-linux-amd64.tar.gz"},
		{"darwin", "arm64", "gleann-plugin-sound-v1.0.0-darwin-arm64.tar.gz"},
		{"windows", "amd64", "gleann-plugin-sound-v1.0.0-windows-amd64.zip"},
	} {
		i, err := selectReleaseAsset(assets, c.goos, c.goarch)
		if err != nil || assets[i] != c.want {
			t.Errorf("%s/%s selected %v (%v), want %s", c.goos, c.goarch, i, err, c.want)
		}
	}
}
