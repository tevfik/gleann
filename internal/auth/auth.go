// Package auth implements gleann's bearer-token store.
//
// Tokens are random 256-bit values shown to the user once; only their SHA-256
// hashes are persisted (~/.gleann/auth/tokens.json, mode 0600). On first use an
// admin token is also written in clear text to ~/.gleann/auth/admin.token
// (mode 0600) so local clients (CLI, stdio MCP) can authenticate without any
// configuration.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scope limits what a token may do.
type Scope string

const (
	ScopeQuery Scope = "query" // search, ask, chat, A2A, memory read
	ScopeAdmin Scope = "admin" // everything else
)

// EnvToken lets a client supply its token without touching the token file.
const EnvToken = "GLEANN_API_TOKEN"

const tokenPrefix = "gl_"

// Entry is one persisted token (hash only).
type Entry struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Scope    Scope     `json:"scope"`
	Hash     string    `json:"sha256"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitempty"`
}

// Store is a file-backed token store. It is safe for concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

// DefaultDir returns ~/.gleann/auth.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gleann", "auth")
}

// Open returns a store rooted at dir (created with mode 0700 if missing).
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("auth: no home directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func (s *Store) tokensPath() string { return filepath.Join(s.dir, "tokens.json") }
func (s *Store) adminPath() string  { return filepath.Join(s.dir, "admin.token") }

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func newRaw() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) load() ([]Entry, error) {
	data, err := os.ReadFile(s.tokensPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var es []Entry
	if err := json.Unmarshal(data, &es); err != nil {
		return nil, fmt.Errorf("auth: %s is corrupt: %w", s.tokensPath(), err)
	}
	return es, nil
}

func (s *Store) save(es []Entry) error {
	data, err := json.MarshalIndent(es, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.tokensPath(), data, 0o600)
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Create adds a token and returns the raw value (shown only once).
func (s *Store) Create(name string, scope Scope) (string, error) {
	if scope != ScopeQuery && scope != ScopeAdmin {
		return "", fmt.Errorf("auth: unknown scope %q (want query|admin)", scope)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("auth: token name required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.load()
	if err != nil {
		return "", err
	}
	for _, e := range es {
		if e.Name == name {
			return "", fmt.Errorf("auth: a token named %q already exists", name)
		}
	}
	raw, err := newRaw()
	if err != nil {
		return "", err
	}
	idb := make([]byte, 4)
	_, _ = rand.Read(idb)
	es = append(es, Entry{ID: hex.EncodeToString(idb), Name: name, Scope: scope, Hash: hashToken(raw), Created: time.Now().UTC()})
	return raw, s.save(es)
}

// EnsureAdmin makes sure an admin token exists and its raw value is readable
// from admin.token. It returns the raw token and whether it was just created.
func (s *Store) EnsureAdmin() (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.load()
	if err != nil {
		return "", false, err
	}
	if raw, err := os.ReadFile(s.adminPath()); err == nil {
		r := strings.TrimSpace(string(raw))
		for _, e := range es {
			if e.Name == "admin" && e.Scope == ScopeAdmin && constEq(e.Hash, hashToken(r)) {
				return r, false, nil
			}
		}
	}
	// (Re)create: drop any stale "admin" entry.
	kept := es[:0]
	for _, e := range es {
		if e.Name != "admin" {
			kept = append(kept, e)
		}
	}
	raw, err := newRaw()
	if err != nil {
		return "", false, err
	}
	kept = append(kept, Entry{ID: "admin", Name: "admin", Scope: ScopeAdmin, Hash: hashToken(raw), Created: time.Now().UTC()})
	if err := writeFileAtomic(s.adminPath(), []byte(raw+"\n"), 0o600); err != nil {
		return "", false, err
	}
	return raw, true, s.save(kept)
}

func constEq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Verify checks a raw token and returns its scope.
func (s *Store) Verify(raw string) (Scope, bool) {
	if !strings.HasPrefix(raw, tokenPrefix) {
		return "", false
	}
	h := hashToken(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.load()
	if err != nil {
		return "", false
	}
	for _, e := range es {
		if constEq(e.Hash, h) {
			return e.Scope, true
		}
	}
	return "", false
}

// List returns all entries (hashes only).
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Revoke removes a token by name or id. The admin token cannot be revoked
// (use Rotate).
func (s *Store) Revoke(nameOrID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	es, err := s.load()
	if err != nil {
		return err
	}
	out := es[:0]
	found := false
	for _, e := range es {
		if e.Name == nameOrID || e.ID == nameOrID {
			if e.Name == "admin" {
				return errors.New("auth: the admin token cannot be revoked; use `gleann token rotate`")
			}
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return fmt.Errorf("auth: no token %q", nameOrID)
	}
	return s.save(out)
}

// Rotate replaces the admin token with a fresh one.
func (s *Store) Rotate() (string, error) {
	s.mu.Lock()
	es, err := s.load()
	if err == nil {
		kept := es[:0]
		for _, e := range es {
			if e.Name != "admin" {
				kept = append(kept, e)
			}
		}
		err = s.save(kept)
	}
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	_ = os.Remove(s.adminPath())
	raw, _, err := s.EnsureAdmin()
	return raw, err
}

// ClientToken returns the token a local client should send: GLEANN_API_TOKEN,
// else the contents of ~/.gleann/auth/admin.token, else "".
func ClientToken() string {
	if t := strings.TrimSpace(os.Getenv(EnvToken)); t != "" {
		return t
	}
	dir := DefaultDir()
	if dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(dir, "admin.token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
