// Package store holds a tiny key/value abstraction used by the service layer.
package store

import "sort"

// Store is the persistence interface.
type Store interface {
	Get(key string) (string, bool)
	Put(key, value string)
	Keys() []string
}

// MemStore keeps everything in a map.
type MemStore struct {
	data map[string]string
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{data: map[string]string{}}
}

// Get returns the value stored under key.
func (m *MemStore) Get(key string) (string, bool) {
	v, ok := m.data[key]
	return v, ok
}

// Put stores value under key.
func (m *MemStore) Put(key, value string) {
	m.data[normalize(key)] = value
}

// Keys returns the sorted keys.
func (m *MemStore) Keys() []string {
	out := make([]string, 0, len(m.data))
	for k := range m.data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func normalize(key string) string {
	return key
}
