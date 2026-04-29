// Package packs is gleann's knowledge pack registry.
//
// A "pack" is a self-contained, versioned bundle of curated reference data
// (e.g. crops in Türkiye, plant pests, beekeeping calendar). Packs live on
// disk under PacksDir/<pack_id>/ and consist of:
//
//   - pack.yaml    — manifest (id, version, locale, content_files, ...)
//   - <files>      — YAML/JSON content files declared by the manifest
//
// The registry watches the directory at startup, parses every pack.yaml it
// finds, and serves three concerns:
//
//  1. Discovery — list packs and fetch manifests (for app onboarding).
//  2. Bulk read — return the merged content (for client-side caching).
//  3. Lookup    — fetch a single item by slug (for ad-hoc reads).
//  4. Search    — substring/word search over indexed fields (semantic
//     embedding integration is intentionally a future concern; the manifest
//     advertises `search.semantic` so clients can fall back gracefully).
//
// The registry is read-only at runtime. Pack updates require a reload.
package packs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest is the parsed contents of pack.yaml.
type Manifest struct {
	ID            string     `yaml:"id" json:"id"`
	Version       string     `yaml:"version" json:"version"`
	SchemaVersion int        `yaml:"schema_version" json:"schema_version"`
	Locale        string     `yaml:"locale" json:"locale"`
	Title         string     `yaml:"title" json:"title"`
	Description   string     `yaml:"description" json:"description"`
	License       string     `yaml:"license,omitempty" json:"license,omitempty"`
	Tier          string     `yaml:"tier,omitempty" json:"tier,omitempty"` // free|pro
	ContentFiles  []string   `yaml:"content_files" json:"content_files"`
	Search        SearchSpec `yaml:"search,omitempty" json:"search,omitempty"`
	AppHints      []AppHint  `yaml:"app_hints,omitempty" json:"app_hints,omitempty"`
}

// SearchSpec declares which fields a pack's items should be searchable on.
type SearchSpec struct {
	Fields   []string `yaml:"fields,omitempty" json:"fields,omitempty"`
	Semantic bool     `yaml:"semantic,omitempty" json:"semantic,omitempty"`
}

// AppHint is metadata for client apps about how this pack should be loaded.
type AppHint struct {
	AppID    string `yaml:"app_id" json:"app_id"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
	AutoLoad bool   `yaml:"auto_load,omitempty" json:"auto_load,omitempty"`
}

// Pack is a fully-loaded pack: manifest + content + cached ETag.
type Pack struct {
	Manifest Manifest               `json:"manifest"`
	Items    []map[string]any       `json:"items"`
	ETag     string                 `json:"etag"`
	LoadedAt time.Time              `json:"loaded_at"`
	BySlug   map[string]int         `json:"-"`
	Raw      map[string]interface{} `json:"-"` // verbatim parsed top-level when single-file
}

// Errors.
var (
	ErrNotFound = errors.New("pack not found")
	ErrInvalid  = errors.New("invalid pack")
)

// Registry holds all packs loaded from disk.
type Registry struct {
	dir   string
	mu    sync.RWMutex
	packs map[string]*Pack
}

// New returns an empty registry rooted at dir. Call Reload to populate it.
// If dir is empty no packs will ever be discovered (registry stays empty
// and its handlers return 404 — useful for tests).
func New(dir string) *Registry {
	return &Registry{dir: dir, packs: map[string]*Pack{}}
}

// Dir is the on-disk root the registry scans.
func (r *Registry) Dir() string { return r.dir }

// Reload re-scans the packs directory and replaces the in-memory registry.
// Failures on individual packs are recorded in the returned error joined via
// errors.Join; valid packs are still loaded.
func (r *Registry) Reload() error {
	if r.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read packs dir: %w", err)
	}
	loaded := map[string]*Pack{}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := loadPack(filepath.Join(r.dir, e.Name()))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			continue
		}
		loaded[p.Manifest.ID] = p
	}
	r.mu.Lock()
	r.packs = loaded
	r.mu.Unlock()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// List returns all packs sorted by ID.
func (r *Registry) List() []*Pack {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Pack, 0, len(r.packs))
	for _, p := range r.packs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Manifest.ID < out[j].Manifest.ID })
	return out
}

// Get returns a pack by id, or ErrNotFound.
func (r *Registry) Get(id string) (*Pack, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.packs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return p, nil
}

// Item returns a single item by slug from a pack, or ErrNotFound.
// "slug" is matched against the item's `id`, `slug`, or `key` field.
func (r *Registry) Item(packID, slug string) (map[string]any, error) {
	p, err := r.Get(packID)
	if err != nil {
		return nil, err
	}
	idx, ok := p.BySlug[slug]
	if !ok {
		return nil, ErrNotFound
	}
	return p.Items[idx], nil
}

// Search performs a case-insensitive substring search over the fields
// declared in pack.Manifest.Search.Fields. Returns up to limit items.
// If query is empty, returns all items (capped). Semantic search is not
// implemented here — see Manifest.Search.Semantic.
func (r *Registry) Search(packID, query string, limit int) ([]map[string]any, error) {
	p, err := r.Get(packID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	q := strings.ToLower(strings.TrimSpace(query))
	fields := p.Manifest.Search.Fields
	out := make([]map[string]any, 0, limit)
	for _, item := range p.Items {
		if len(out) >= limit {
			break
		}
		if q == "" {
			out = append(out, item)
			continue
		}
		if matchItem(item, fields, q) {
			out = append(out, item)
		}
	}
	return out, nil
}

// matchItem returns true when q appears (case-insensitive substring) in any
// of the configured fields. If fields is empty, every string-valued field
// is considered.
func matchItem(item map[string]any, fields []string, q string) bool {
	if len(fields) == 0 {
		for _, v := range item {
			if s, ok := v.(string); ok {
				if strings.Contains(strings.ToLower(s), q) {
					return true
				}
			}
		}
		return false
	}
	for _, f := range fields {
		v, ok := item[f]
		if !ok {
			continue
		}
		if s, ok := v.(string); ok {
			if strings.Contains(strings.ToLower(s), q) {
				return true
			}
		}
	}
	return false
}

// loadPack reads pack.yaml and all content_files from a single pack dir.
func loadPack(dir string) (*Pack, error) {
	manifestPath := filepath.Join(dir, "pack.yaml")
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("%w: missing pack.yaml", ErrInvalid)
	}
	var mf Manifest
	if err := yaml.Unmarshal(mb, &mf); err != nil {
		return nil, fmt.Errorf("%w: pack.yaml: %v", ErrInvalid, err)
	}
	if mf.ID == "" || mf.Version == "" {
		return nil, fmt.Errorf("%w: id and version are required", ErrInvalid)
	}
	if len(mf.ContentFiles) == 0 {
		return nil, fmt.Errorf("%w: content_files is empty", ErrInvalid)
	}
	items := []map[string]any{}
	hasher := newETagHasher()
	hasher.Write(mb)
	for _, name := range mf.ContentFiles {
		// Reject path-traversal attempts.
		if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("%w: invalid content file %q", ErrInvalid, name)
		}
		cb, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("%w: content file %s: %v", ErrInvalid, name, err)
		}
		hasher.Write(cb)
		fileItems, err := decodeItems(cb)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
		}
		items = append(items, fileItems...)
	}
	bySlug := map[string]int{}
	for i, it := range items {
		for _, key := range []string{"id", "slug", "key"} {
			if v, ok := it[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					bySlug[s] = i
					break
				}
			}
		}
	}
	return &Pack{
		Manifest: mf,
		Items:    items,
		ETag:     hasher.String(),
		LoadedAt: time.Now().UTC(),
		BySlug:   bySlug,
	}, nil
}

// decodeItems accepts a YAML document that is either a top-level list or a
// top-level map containing a single list field. The list is normalised to
// []map[string]any.
func decodeItems(data []byte) ([]map[string]any, error) {
	// Try list first.
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	switch v := raw.(type) {
	case []any:
		return normalizeList(v)
	case map[string]any:
		// Find the only list field, otherwise wrap the map as a single item.
		var listKey string
		listCount := 0
		for k, vv := range v {
			if _, ok := vv.([]any); ok {
				listKey = k
				listCount++
			}
		}
		if listCount == 1 {
			return normalizeList(v[listKey].([]any))
		}
		return []map[string]any{v}, nil
	}
	return nil, fmt.Errorf("unsupported top-level YAML type %T", raw)
}

func normalizeList(list []any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(list))
	for i, el := range list {
		m, ok := el.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d is not a map (got %T)", i, el)
		}
		out = append(out, m)
	}
	return out, nil
}
