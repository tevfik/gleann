package service

import (
	"testing"

	"example.com/fixture/store"
)

func TestSaveLoad(t *testing.T) {
	s := New(store.NewMemStore())
	if err := s.Save("a", "1"); err != nil {
		t.Fatal(err)
	}
	if s.Load("a") != "1" {
		t.Fatal("lost value")
	}
}

func TestReport(t *testing.T) {
	s := New(store.NewMemStore())
	_ = s.Save("k", "v")
	if s.Report() == "" {
		t.Fatal("empty report")
	}
}
