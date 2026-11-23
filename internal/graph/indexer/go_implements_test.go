package indexer

import (
	"path/filepath"
	"testing"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

func TestCollectGoImplementsEdges_InterfaceEmbedding(t *testing.T) {
	source := `package server

type Handler interface {
	ServeHTTP(w Writer, r *Request)
}

type Logger interface {
	Log(msg string)
}

// ExtendedHandler embeds Handler and Logger interfaces.
type ExtendedHandler interface {
	Handler
	Logger
	Extra()
}

// MyServer implements ExtendedHandler by embedding it.
type MyServer struct {
	ExtendedHandler
	name string
}
`
	chunks := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(source, filepath.FromSlash("server/server.go"))

	impls, refs, _ := collectGoImplementsEdges("github.com/test/proj", "server/server.go", source, chunks)

	// ExtendedHandler should IMPLEMENT Handler and Logger.
	foundHandlerImpl := false
	foundLoggerImpl := false
	foundStructEmbed := false

	for _, impl := range impls {
		t.Logf("IMPLEMENTS: %s → %s", impl.ImplFQN, impl.IfaceFQN)
		if impl.IfaceFQN == "github.com/test/proj/server.Handler" {
			foundHandlerImpl = true
		}
		if impl.IfaceFQN == "github.com/test/proj/server.Logger" {
			foundLoggerImpl = true
		}
	}

	// MyServer embeds ExtendedHandler (interface) → IMPLEMENTS.
	for _, impl := range impls {
		if impl.ImplFQN == "github.com/test/proj/server.MyServer" &&
			impl.IfaceFQN == "github.com/test/proj/server.ExtendedHandler" {
			foundStructEmbed = true
		}
	}

	if !foundHandlerImpl {
		t.Error("missing IMPLEMENTS: ExtendedHandler → Handler")
	}
	if !foundLoggerImpl {
		t.Error("missing IMPLEMENTS: ExtendedHandler → Logger")
	}
	if !foundStructEmbed {
		t.Error("missing IMPLEMENTS: MyServer → ExtendedHandler (struct embedding interface)")
	}

	_ = refs // references may be extracted for field types
}

func TestCollectGoImplementsEdges_StructFieldReferences(t *testing.T) {
	source := `package service

type Config struct {
	Name    string
	Timeout int
}

type Service struct {
	config Config
	db     Database
}

type Database struct {
	Host string
}

func NewService(cfg Config, db Database) *Service {
	return &Service{config: cfg, db: db}
}
`
	chunks := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(source, filepath.FromSlash("service/service.go"))

	_, refs, _ := collectGoImplementsEdges("github.com/test/proj", "service/service.go", source, chunks)

	// Service should REFERENCE Config and Database via fields.
	foundConfigRef := false
	foundDBRef := false
	for _, ref := range refs {
		t.Logf("REFERENCES: %s → %s", ref.RefererFQN, ref.RefereeFQN)
		if ref.RefereeFQN == "github.com/test/proj/service.Config" {
			foundConfigRef = true
		}
		if ref.RefereeFQN == "github.com/test/proj/service.Database" {
			foundDBRef = true
		}
	}

	if !foundConfigRef {
		t.Error("missing REFERENCES to Config")
	}
	if !foundDBRef {
		t.Error("missing REFERENCES to Database")
	}
}

func TestCollectGoImplementsEdges_FuncSignatureReferences(t *testing.T) {
	source := `package handler

type Request struct {
	Method string
}

type Response struct {
	Code int
}

func Handle(req Request) Response {
	return Response{Code: 200}
}
`
	chunks := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(source, filepath.FromSlash("handler/handler.go"))

	_, refs, _ := collectGoImplementsEdges("github.com/test/proj", "handler/handler.go", source, chunks)

	foundReqRef := false
	foundRespRef := false
	for _, ref := range refs {
		t.Logf("REFERENCES: %s → %s", ref.RefererFQN, ref.RefereeFQN)
		if ref.RefereeFQN == "github.com/test/proj/handler.Request" {
			foundReqRef = true
		}
		if ref.RefereeFQN == "github.com/test/proj/handler.Response" {
			foundRespRef = true
		}
	}

	if !foundReqRef {
		t.Error("Handle should REFERENCE Request via parameter")
	}
	if !foundRespRef {
		t.Error("Handle should REFERENCE Response via return type")
	}
}

func TestCollectGoImplementsEdges_CrossPackageReference(t *testing.T) {
	source := `package main

import "net/http"

func serve(w http.ResponseWriter, r *http.Request) {
}
`
	chunks := chunking.NewASTChunker(chunking.DefaultASTChunkerConfig()).ChunkCode(source, filepath.FromSlash("main.go"))

	_, refs, _ := collectGoImplementsEdges("github.com/test/proj", "main.go", source, chunks)

	// Cross-package references to net/http types should appear.
	foundHTTPRef := false
	for _, ref := range refs {
		t.Logf("REFERENCES: %s → %s", ref.RefererFQN, ref.RefereeFQN)
		if ref.RefereeFQN == "net/http.ResponseWriter" || ref.RefereeFQN == "net/http.Request" {
			foundHTTPRef = true
		}
	}

	if !foundHTTPRef {
		t.Error("should find cross-package reference to net/http types")
	}
}

// Helper to check the callee stub builder still works.
func TestCalleeStubHelper(t *testing.T) {
	sym := calleeStub("github.com/test/proj.MyFunc")
	if sym.FQN != "github.com/test/proj.MyFunc" {
		t.Errorf("unexpected FQN: %s", sym.FQN)
	}
	if sym.Kind != "function" {
		t.Errorf("unexpected kind: %s", sym.Kind)
	}
}

// Verify that the graph edge types are properly constructed.
func TestEdgeImplementsType(t *testing.T) {
	e := gtypes.EdgeImplements{
		ImplFQN:  "pkg.MyStruct",
		IfaceFQN: "pkg.MyInterface",
	}
	if e.ImplFQN != "pkg.MyStruct" {
		t.Error("ImplFQN mismatch")
	}
}

func TestEdgeReferencesType(t *testing.T) {
	e := gtypes.EdgeReferences{
		RefererFQN: "pkg.MyFunc",
		RefereeFQN: "pkg.Config",
		Confidence: "extracted",
	}
	if e.Confidence != "extracted" {
		t.Error("Confidence mismatch")
	}
}
