package gleann

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// pluginHTTPClient is used for all plugin / model downloads. It bounds the
// connect, TLS-handshake and response-header phases but deliberately sets
// no overall timeout: release assets (models, binaries) can be hundreds of
// megabytes. Callers bound the whole transfer with a context.
var pluginHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	},
}

// httpStatusError is returned when a download responds with a non-200
// status.
type httpStatusError struct {
	URL        string
	StatusCode int
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP status %d", e.StatusCode)
}

// httpGet performs a context-bound GET and returns the response only for
// 200 OK; the caller must close the body.
func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := pluginHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &httpStatusError{URL: url, StatusCode: resp.StatusCode}
	}
	return resp, nil
}

// fetchJSON GETs url and decodes the JSON body into v.
func fetchJSON(ctx context.Context, url string, v any) error {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// fetchLatestRelease fetches the latest GitHub release metadata of
// owner/repo into v.
func fetchLatestRelease(ctx context.Context, owner, repo string, v any) error {
	return fetchJSON(ctx, fmt.Sprintf("%s/repos/%s/%s/releases/latest", githubAPIBase, owner, repo), v)
}

// fetchToFile downloads url to dest. The body is streamed into a temporary
// file in dest's directory which is renamed over dest only after the
// transfer completed, so an interrupted download never leaves a truncated
// dest behind.
func fetchToFile(ctx context.Context, url, dest string) error {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dest), "."+filepath.Base(dest)+".*.part")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	// Match the permissions os.Create would have produced (0666 &^ umask
	// is typically 0644); CreateTemp uses 0600.
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	written, err := io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		return err
	}
	if resp.ContentLength > 0 && written != resp.ContentLength {
		tmp.Close()
		return fmt.Errorf("incomplete download: got %d of %d bytes", written, resp.ContentLength)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// fetchToTemp downloads url into a new temporary file and returns its path;
// the caller must remove it.
func fetchToTemp(ctx context.Context, url string) (string, error) {
	f, err := os.CreateTemp("", "gleann-download-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	if err := fetchToFile(ctx, url, path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
