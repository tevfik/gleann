// Package roles provides custom system role management for LLM conversations.
// Roles are named system prompts stored in ~/.gleann/config.json that can be
// activated via --role flag. Inspired by charmbracelet/mods.
package roles

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
)

// Role is a named list of system prompt segments.
// Each segment can be a plain string, a file:// path, or an https:// URL.
type Role struct {
	Name     string   `json:"name"`
	Messages []string `json:"messages"`
}

// Registry holds all available roles.
type Registry struct {
	roles map[string][]string
}

// NewRegistry creates a role registry from a raw config map.
func NewRegistry(roles map[string][]string) *Registry {
	if roles == nil {
		roles = make(map[string][]string)
	}
	return &Registry{roles: roles}
}

// DefaultRegistry returns a registry with built-in roles.
func DefaultRegistry() *Registry {
	return NewRegistry(map[string][]string{
		"default": {},
		"reviewer": {
			"You are a senior code reviewer. Analyze code for bugs, security issues, " +
				"performance problems, and style violations. Be specific and actionable.",
		},
		"explain": {
			"You are a patient teacher. Explain concepts clearly with examples. " +
				"Use analogies when helpful. Assume the reader is a developer.",
		},
		"summarize": {
			"You are a concise summarizer. Extract the key points and present them " +
				"as a brief, well-structured summary. Use bullet points.",
		},
		"shell": {
			"You are a shell expert. You only output one-liners to solve problems.",
			"You do not explain anything. You simply output the command.",
		},
	})
}

// Get returns the system prompt messages for a role name.
func (r *Registry) Get(name string) ([]string, error) {
	msgs, ok := r.roles[name]
	if !ok {
		return nil, fmt.Errorf("role %q not found (available: %s)", name, strings.Join(r.List(), ", "))
	}
	return msgs, nil
}

// Resolve loads the actual text for a role, resolving file:// and https:// prefixes.
func (r *Registry) Resolve(name string) ([]string, error) {
	msgs, err := r.Get(name)
	if err != nil {
		return nil, err
	}

	resolved := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		content, err := LoadMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("role %q: %w", name, err)
		}
		resolved = append(resolved, content)
	}
	return resolved, nil
}

// SystemPrompt returns the combined system prompt for a role.
func (r *Registry) SystemPrompt(name string) (string, error) {
	msgs, err := r.Resolve(name)
	if err != nil {
		return "", err
	}
	return strings.Join(msgs, "\n\n"), nil
}

// List returns sorted role names.
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.roles))
	for name := range r.roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Has checks if a role exists.
func (r *Registry) Has(name string) bool {
	_, ok := r.roles[name]
	return ok
}

// Add registers a new role.
func (r *Registry) Add(name string, messages []string) {
	r.roles[name] = messages
}

// LoadMessage resolves a message string to its actual content.
// Supports: plain text, file:// paths, https:// and http:// URLs.
func LoadMessage(msg string) (string, error) {
	if strings.HasPrefix(msg, "https://") || strings.HasPrefix(msg, "http://") {
		resp, err := http.Get(msg) //nolint:gosec,noctx
		if err != nil {
			return "", fmt.Errorf("fetch %s: %w", msg, err)
		}
		defer resp.Body.Close()
		bts, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", msg, err)
		}
		return string(bts), nil
	}

	if strings.HasPrefix(msg, "file://") {
		bts, err := os.ReadFile(strings.TrimPrefix(msg, "file://"))
		if err != nil {
			return "", fmt.Errorf("read file: %w", err)
		}
		return string(bts), nil
	}

	return msg, nil
}
