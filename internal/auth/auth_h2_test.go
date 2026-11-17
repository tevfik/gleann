package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func unsetHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "") // plan9
}

func TestNoHomeDirectory(t *testing.T) {
	unsetHome(t)
	t.Setenv(EnvToken, "")
	if got := DefaultDir(); got != "" {
		t.Fatalf("DefaultDir without a home = %q, want empty", got)
	}
	if got := ClientToken(); got != "" {
		t.Fatalf("ClientToken without a home = %q, want empty", got)
	}
	if _, err := Open(DefaultDir()); err == nil {
		t.Fatal("Open without a home directory must fail")
	}
}

func TestOpenParentIsFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(file, "auth")); err == nil {
		t.Fatal("Open below a regular file must fail")
	}
}

func TestUnreadableTokenFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A directory where tokens.json should be: ReadFile fails with an error
	// other than ErrNotExist, which must surface instead of an empty store.
	if err := os.Mkdir(s.tokensPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List must report an unreadable token file")
	}
	if _, ok := s.Verify("gl_anything"); ok {
		t.Fatal("Verify must fail closed")
	}
}

func TestEnsureAdminRecreatesStaleEntry(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := s.EnsureAdmin()
	if err != nil || !created {
		t.Fatalf("EnsureAdmin = %v %v", created, err)
	}
	q, err := s.Create("ci", ScopeQuery)
	if err != nil {
		t.Fatal(err)
	}
	// admin.token no longer matches the stored hash: the admin entry is
	// replaced, the other tokens are kept.
	if err := os.WriteFile(s.adminPath(), []byte("gl_tampered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, created, err := s.EnsureAdmin()
	if err != nil || !created {
		t.Fatalf("EnsureAdmin after tamper = %v %v", created, err)
	}
	if second == first {
		t.Fatal("a fresh admin token was expected")
	}
	if _, ok := s.Verify(first); ok {
		t.Fatal("the stale admin token must be dropped")
	}
	if sc, ok := s.Verify(q); !ok || sc != ScopeQuery {
		t.Fatalf("query token lost: %v %v", sc, ok)
	}
	es, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	admins := 0
	for _, e := range es {
		if e.Name == "admin" {
			admins++
		}
	}
	if len(es) != 2 || admins != 1 {
		t.Fatalf("entries after re-create: %+v", es)
	}
}

func TestEnsureAdminWriteFailure(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty directory in place of admin.token makes the rename fail.
	if err := os.MkdirAll(filepath.Join(s.adminPath(), "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnsureAdmin(); err == nil {
		t.Fatal("EnsureAdmin must report that admin.token cannot be written")
	}
}

func TestRotateKeepsOtherTokens(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnsureAdmin(); err != nil {
		t.Fatal(err)
	}
	q, err := s.Create("reader", ScopeQuery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rotate(); err != nil {
		t.Fatal(err)
	}
	if sc, ok := s.Verify(q); !ok || sc != ScopeQuery {
		t.Fatalf("Rotate dropped a non-admin token: %v %v", sc, ok)
	}
}
