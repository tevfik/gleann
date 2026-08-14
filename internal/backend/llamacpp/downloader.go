package llamacpp

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// DownloadModel fetches a GGUF model from Hugging Face if it doesn't already exist.
func DownloadModel(repoID, filename string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	destDir := filepath.Join(home, ".gleann", "models")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}

	destPath := filepath.Join(destDir, filename)

	// Check if already downloaded
	if info, err := os.Stat(destPath); err == nil && info.Size() > 0 {
		return destPath, nil
	}

	// Construct Hugging Face download URL
	// Example: https://huggingface.co/BARTOWSKI/bge-reranker-v2-m3-GGUF/resolve/main/bge-reranker-v2-m3-q4_k_m.gguf
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)

	fmt.Printf("Downloading model %s from Hugging Face...\n", filename)

	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to initiate download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("unexpected status code %d when downloading %s", resp.StatusCode, url)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer out.Close()

	// We can add a progress bar here later
	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("failed to save model to disk: %w", err)
	}

	fmt.Println("Download complete!")
	return destPath, nil
}
