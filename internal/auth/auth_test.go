package auth

import (
	"os"
	"strings"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admin, created, err := s.EnsureAdmin()
	if err != nil || !created {
		t.Fatalf("EnsureAdmin: %v created=%v", err, created)
	}
	again, created, _ := s.EnsureAdmin()
	if created || again != admin {
		t.Fatal("EnsureAdmin must be idempotent")
	}
	if sc, ok := s.Verify(admin); !ok || sc != ScopeAdmin {
		t.Fatalf("admin verify: %v %v", sc, ok)
	}
	q, err := s.Create("opencode", ScopeQuery)
	if err != nil {
		t.Fatal(err)
	}
	if sc, ok := s.Verify(q); !ok || sc != ScopeQuery {
		t.Fatalf("query verify: %v %v", sc, ok)
	}
	if _, ok := s.Verify("gl_nope"); ok {
		t.Fatal("bogus token accepted")
	}
	if _, ok := s.Verify(strings.TrimPrefix(q, "gl_")); ok {
		t.Fatal("token without prefix accepted")
	}
	// Only hashes are stored.
	data, _ := os.ReadFile(s.tokensPath())
	if strings.Contains(string(data), q) || strings.Contains(string(data), admin) {
		t.Fatal("raw token persisted in tokens.json")
	}
	if fi, _ := os.Stat(s.tokensPath()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("tokens.json mode %v", fi.Mode().Perm())
	}
	if err := s.Revoke("opencode"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Verify(q); ok {
		t.Fatal("revoked token still valid")
	}
	if err := s.Revoke("admin"); err == nil {
		t.Fatal("admin revoke must fail")
	}
	n, err := s.Rotate()
	if err != nil || n == admin {
		t.Fatalf("rotate: %v", err)
	}
	if _, ok := s.Verify(admin); ok {
		t.Fatal("old admin token valid after rotate")
	}
	if _, ok := s.Verify(n); !ok {
		t.Fatal("new admin token invalid")
	}
}
