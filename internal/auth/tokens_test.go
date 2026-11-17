package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultDirIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := DefaultDir(), filepath.Join(home, ".gleann", "auth"); got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") must fail")
	}
}

func TestListRevokeRotate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldAdmin, _, err := s.EnsureAdmin()
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Create("ci", ScopeQuery)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("ci", ScopeQuery); err == nil {
		t.Fatal("duplicate name must be rejected")
	}
	if _, err := s.Create("  ", ScopeQuery); err == nil {
		t.Fatal("blank name must be rejected")
	}
	if _, err := s.Create("x", Scope("root")); err == nil {
		t.Fatal("unknown scope must be rejected")
	}

	es, err := s.List()
	if err != nil || len(es) != 2 {
		t.Fatalf("List = %v, %v; want 2 entries", es, err)
	}
	var ciID string
	for _, e := range es {
		if e.Name == "ci" {
			ciID = e.ID
		}
	}

	if err := s.Revoke("admin"); err == nil || !strings.Contains(err.Error(), "cannot be revoked") {
		t.Fatalf("revoking admin: %v", err)
	}
	if err := s.Revoke("missing"); err == nil {
		t.Fatal("revoking unknown token must fail")
	}
	if err := s.Revoke(ciID); err != nil { // by id
		t.Fatal(err)
	}
	if _, ok := s.Verify(q); ok {
		t.Fatal("revoked token still verifies")
	}

	newAdmin, err := s.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if newAdmin == oldAdmin {
		t.Fatal("rotate must produce a new token")
	}
	if _, ok := s.Verify(oldAdmin); ok {
		t.Fatal("old admin token still valid after rotate")
	}
	if sc, ok := s.Verify(newAdmin); !ok || sc != ScopeAdmin {
		t.Fatalf("new admin verify = %v %v", sc, ok)
	}
}

func TestCorruptTokenFileIsAnError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.tokensPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List on corrupt file must fail")
	}
	if _, err := s.Create("a", ScopeQuery); err == nil {
		t.Fatal("Create on corrupt file must fail")
	}
	if _, _, err := s.EnsureAdmin(); err == nil {
		t.Fatal("EnsureAdmin on corrupt file must fail")
	}
	if err := s.Revoke("a"); err == nil {
		t.Fatal("Revoke on corrupt file must fail")
	}
	if _, err := s.Rotate(); err == nil {
		t.Fatal("Rotate on corrupt file must fail")
	}
	if _, ok := s.Verify("gl_x"); ok {
		t.Fatal("Verify on corrupt file must fail closed")
	}
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	err := writeFileAtomic(filepath.Join(t.TempDir(), "nope", "f"), []byte("x"), 0o600)
	if err == nil {
		t.Fatal("expected error when parent dir is missing")
	}
}

func TestClientTokenPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvToken, "")
	if got := ClientToken(); got != "" {
		t.Fatalf("no env, no file: got %q", got)
	}
	dir := filepath.Join(home, ".gleann", "auth")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "admin.token"), []byte("gl_fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ClientToken(); got != "gl_fromfile" {
		t.Fatalf("file token = %q", got)
	}
	t.Setenv(EnvToken, "  gl_fromenv ")
	if got := ClientToken(); got != "gl_fromenv" {
		t.Fatalf("env must win and be trimmed, got %q", got)
	}
}
