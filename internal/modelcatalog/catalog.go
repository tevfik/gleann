// Package modelcatalog lists the local GGUF models gleann can download
// (models.json, embedded) and downloads them into ~/.gleann/models. The web
// UI (through the settings.Catalog contract), `gleann setup` and
// `gleann models` all read this one list.
package modelcatalog

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tevfik/gleann/modules/settings"
)

//go:embed models.json
var modelsJSON []byte

// Kinds of models.
const (
	Embedding = "embedding"
	LLM       = "llm"
	Reranker  = "reranker"
)

// Entry is one downloadable model.
type Entry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	File   string `json:"file"`
	URL    string `json:"url"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// All returns every catalog entry.
func All() []Entry {
	var doc struct {
		Models []Entry `json:"models"`
	}
	if err := json.Unmarshal(modelsJSON, &doc); err != nil {
		panic("modelcatalog: models.json: " + err.Error())
	}
	return doc.Models
}

// OfKind returns the entries of one kind (Embedding, LLM or Reranker).
func OfKind(kind string) []Entry {
	var out []Entry
	for _, e := range All() {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry with the given id or file name (case-insensitive),
// or one whose file a path names.
func Find(name string) (Entry, bool) {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	for _, e := range All() {
		if strings.EqualFold(e.ID, name) || strings.ToLower(e.File) == base {
			return e, true
		}
	}
	return Entry{}, false
}

// Dir is where models are stored: ~/.gleann/models.
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gleann", "models")
}

// Path is where an entry's file is stored.
func (e Entry) Path() string { return filepath.Join(Dir(), e.File) }

// Installed reports whether the file is on disk with its catalog size.
func (e Entry) Installed() bool {
	fi, err := os.Stat(e.Path())
	return err == nil && (e.Bytes == 0 || fi.Size() == e.Bytes)
}

// client has no overall timeout (multi-GB files) but bounds connection setup.
var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	ResponseHeaderTimeout: 60 * time.Second,
	TLSHandshakeTimeout:   30 * time.Second,
}}

// Download fetches an entry into Dir, checking its size and sha256. It
// writes to a .part file first, so an interrupted download never leaves a
// truncated model under the real name.
func Download(ctx context.Context, e Entry, progress func(done, total int64)) error {
	if e.Installed() {
		return nil
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gleann")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", e.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d from %s", e.Name, resp.StatusCode, e.URL)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = e.Bytes
	}

	part := e.Path() + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	pw := &progressWriter{total: total, report: progress}
	_, err = io.Copy(io.MultiWriter(f, h, pw), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && e.Bytes > 0 && pw.done != e.Bytes {
		err = fmt.Errorf("got %d bytes, want %d", pw.done, e.Bytes)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); err == nil && e.SHA256 != "" && sum != e.SHA256 {
		err = fmt.Errorf("checksum mismatch (got %s, want %s)", sum, e.SHA256)
	}
	if err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("download %s: %w", e.Name, err)
	}
	return os.Rename(part, e.Path())
}

type progressWriter struct {
	done, total int64
	last        time.Time
	report      func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.report != nil && (time.Since(p.last) > 250*time.Millisecond || p.done == p.total) {
		p.last = time.Now()
		p.report(p.done, p.total)
	}
	return len(b), nil
}

var kindGroup = map[string]string{Embedding: "Embedding models", LLM: "LLMs", Reranker: "Rerankers"}

// Catalog exposes the entries under the settings.Catalog contract
// (GET/POST), for the web UI. A model's Value is its file name.
func Catalog() *settings.Catalog {
	return &settings.Catalog{
		List: func() []settings.Model {
			var out []settings.Model
			for _, e := range All() {
				out = append(out, settings.Model{
					ID: e.ID, Name: e.Name, Group: kindGroup[e.Kind], Size: settings.HumanSize(e.Bytes),
					Value: e.File, Installed: e.Installed(),
				})
			}
			return out
		},
		Download: func(ctx context.Context, id string, report func(settings.Progress)) error {
			e, ok := Find(id)
			if !ok {
				return fmt.Errorf("unknown model %q", id)
			}
			return Download(ctx, e, func(done, total int64) {
				report(settings.Progress{Done: done, Total: total})
			})
		},
	}
}
