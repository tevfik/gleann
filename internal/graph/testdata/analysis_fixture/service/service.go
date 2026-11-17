// Package service implements the business logic on top of store.
package service

import (
	"strings"

	"example.com/fixture/store"
)

// Service wraps a Store.
type Service struct {
	st store.Store
}

// New builds a Service.
func New(st store.Store) *Service {
	return &Service{st: st}
}

// Save validates and stores a record.
func (s *Service) Save(key, value string) error {
	if err := validate(key); err != nil {
		return err
	}
	s.st.Put(key, value)
	return nil
}

// Load returns a record or the empty string.
func (s *Service) Load(key string) string {
	v, _ := s.st.Get(key)
	return v
}

// Report lists all records.
func (s *Service) Report() string {
	var sb strings.Builder
	for _, k := range s.st.Keys() {
		sb.WriteString(k + "=" + s.Load(k) + "\n")
	}
	return sb.String()
}

func validate(key string) error {
	if key == "" {
		return errEmpty
	}
	return nil
}

type emptyError struct{}

func (emptyError) Error() string { return "empty key" }

var errEmpty error = emptyError{}
