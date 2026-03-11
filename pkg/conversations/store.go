// Package conversations provides persistent conversation management with
// SHA-1 identifiers, titles, and continuation support (inspired by mods).
package conversations

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// Conversation represents a saved conversation with metadata.
type Conversation struct {
	ID        string    `json:"id"`      // SHA-1 hash (first 8 chars displayed)
	Title     string    `json:"title"`   // user-assigned or auto-generated
	Indexes   []string  `json:"indexes"` // gleann index names (one or more)
	Model     string    `json:"model"`   // LLM model used
	Messages  []Message `json:"messages"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IndexLabel returns a display-friendly label for the conversation's indexes.
// Single index: "docs" — Multiple: "docs,code"
func (c *Conversation) IndexLabel() string {
	if len(c.Indexes) == 0 {
		return ""
	}
	return strings.Join(c.Indexes, ",")
}

// Store manages persistent conversations on disk.
type Store struct {
	dir string
}

// NewStore creates a conversation store at the given directory.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// DefaultStore returns a store at ~/.gleann/conversations/.
func DefaultStore() *Store {
	home, _ := os.UserHomeDir()
	return NewStore(filepath.Join(home, ".gleann", "conversations"))
}

// Save persists a conversation to disk.
func (s *Store) Save(conv *Conversation) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Generate ID from content hash if empty.
	if conv.ID == "" {
		conv.ID = generateID(conv)
	}
	if conv.CreatedAt.IsZero() {
		conv.CreatedAt = time.Now()
	}
	conv.UpdatedAt = time.Now()

	// Auto-generate title from first user message if empty.
	if conv.Title == "" {
		conv.Title = autoTitle(conv)
	}

	data, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(conv.ID), data, 0o644)
}

// Load reads a conversation by ID or title.
func (s *Store) Load(idOrTitle string) (*Conversation, error) {
	// Try direct ID first.
	if conv, err := s.loadByID(idOrTitle); err == nil {
		return conv, nil
	}

	// Try prefix match on ID.
	convs, err := s.List()
	if err != nil {
		return nil, err
	}

	for _, c := range convs {
		if strings.HasPrefix(c.ID, idOrTitle) {
			return s.loadByID(c.ID)
		}
	}

	// Try title match (exact, case-insensitive).
	for _, c := range convs {
		if strings.EqualFold(c.Title, idOrTitle) {
			return s.loadByID(c.ID)
		}
	}

	return nil, fmt.Errorf("conversation not found: %s", idOrTitle)
}

// List returns all conversations sorted by update time (newest first).
func (s *Store) List() ([]Conversation, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var convs []Conversation
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var conv Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}
		convs = append(convs, conv)
	}

	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt.After(convs[j].UpdatedAt)
	})
	return convs, nil
}

// Delete removes a conversation by ID or title.
func (s *Store) Delete(idOrTitle string) error {
	conv, err := s.Load(idOrTitle)
	if err != nil {
		return err
	}
	return os.Remove(s.path(conv.ID))
}

// DeleteOlderThan removes conversations older than the given duration.
func (s *Store) DeleteOlderThan(d time.Duration) (int, error) {
	convs, err := s.List()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-d)
	count := 0
	for _, c := range convs {
		if c.UpdatedAt.Before(cutoff) {
			if err := os.Remove(s.path(c.ID)); err == nil {
				count++
			}
		}
	}
	return count, nil
}

// Latest returns the most recently updated conversation, or nil if none.
func (s *Store) Latest() (*Conversation, error) {
	convs, err := s.List()
	if err != nil {
		return nil, err
	}
	if len(convs) == 0 {
		return nil, nil
	}
	return s.loadByID(convs[0].ID)
}

func (s *Store) loadByID(id string) (*Conversation, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func generateID(conv *Conversation) string {
	h := sha1.New()
	for _, idx := range conv.Indexes {
		h.Write([]byte(idx))
	}
	h.Write([]byte(conv.Title))
	for _, m := range conv.Messages {
		h.Write([]byte(m.Role))
		h.Write([]byte(m.Content))
	}
	h.Write([]byte(time.Now().String()))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func autoTitle(conv *Conversation) string {
	for _, m := range conv.Messages {
		if m.Role == "user" {
			title := m.Content
			if len(title) > 60 {
				title = title[:57] + "..."
			}
			return title
		}
	}
	return "untitled"
}

// ShortID returns the first 8 characters of the conversation ID.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// MessageCount returns the number of user+assistant message pairs.
func (c *Conversation) MessageCount() int {
	count := 0
	for _, m := range c.Messages {
		if m.Role == "user" {
			count++
		}
	}
	return count
}
