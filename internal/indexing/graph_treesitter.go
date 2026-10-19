//go:build treesitter

package indexing

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graph/indexer"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/internal/graph/report"
)

// GraphSupported reports whether this binary was built with graph
// indexing support (-tags treesitter).
const GraphSupported = true

// buildGraphIndex builds the AST graph index for the given directory and
// writes document graph nodes/edges for plugin-extracted documents.
//
// If ChangedFiles is non-empty, only those files are re-indexed
// (incremental mode); otherwise the whole directory is re-indexed.
func buildGraphIndex(opts GraphOptions, p *printer) error {
	// Resolve docsDir to absolute path to avoid cwd-dependent issues.
	absDocsDir, err := filepath.Abs(opts.DocsDir)
	if err != nil {
		absDocsDir = opts.DocsDir
	}
	if realPath, err := filepath.EvalSymlinks(absDocsDir); err == nil {
		absDocsDir = realPath
	}

	p.Printf("🕸️  Building API Graph Index from %s...\n", absDocsDir)
	graphStart := time.Now()

	dbPath := filepath.Join(opts.IndexDir, opts.Name+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		if errors.Is(err, kgraph.ErrOpenFailed) {
			return fmt.Errorf("graph database %s could not be opened — another process (is `gleann serve` running?) may be holding it; "+
				"stop it and retry, or let the server do the update: %w", dbPath, err)
		}
		return fmt.Errorf("could not initialize kuzu graph db: %w", err)
	}
	defer db.Close()

	// Detect Go module name from go.mod in the docs directory.
	module := indexer.DetectGoModule(absDocsDir)

	// 1. AST code indexing.
	idx := indexer.New(db, module, absDocsDir).WithIncludeSubmodules(opts.IncludeSubmodules)

	// Attach a content-hash store so incremental updates skip files whose
	// on-disk content matches the previously indexed version. The store
	// lives next to the KuzuDB directory so a single "delete index" wipes
	// both in one go.
	hashStore, hashErr := indexer.NewFileHashStore(indexer.DefaultHashStorePath(opts.IndexDir, opts.Name))
	if hashErr != nil {
		p.Warnf("warning: file hash store unavailable, falling back to always re-parse: %v\n", hashErr)
	} else {
		idx.WithHashStore(hashStore)
		defer idx.CloseHashStore()
	}
	if len(opts.ChangedFiles) > 0 {
		p.Printf("🔄 Incremental graph update: %d changed files\n", len(opts.ChangedFiles))
		if err := idx.IndexFiles(opts.ChangedFiles); err != nil {
			return fmt.Errorf("incremental graph indexing failed: %w", err)
		}
		p.Printf("✅ Incremental Graph Index updated in %s\n", time.Since(graphStart).Round(time.Millisecond))
	} else {
		if err := idx.IndexDir(absDocsDir); err != nil {
			return fmt.Errorf("graph indexing failed: %w", err)
		}
		p.Printf("✅ Code Graph Index built in %s\n", time.Since(graphStart).Round(time.Millisecond))
	}

	// 2. Document graph indexing (plugin-extracted documents).
	if len(opts.PluginDocs) > 0 {
		docStart := time.Now()
		docIdx := indexer.NewDocIndexer(db, 512, 64)

		// Batch all documents into a single DB transaction for performance.
		inputs := make([]*indexer.DocGraphInput, len(opts.PluginDocs))
		for i, pd := range opts.PluginDocs {
			inputs[i] = &indexer.DocGraphInput{
				Result:     pd.Result,
				SourcePath: pd.SourcePath,
			}
		}
		if err := docIdx.WriteGraphBatch(inputs); err != nil {
			return fmt.Errorf("doc graph batch indexing failed: %w", err)
		}
		p.Printf("📄 Document Graph: %d documents indexed in %s\n", len(opts.PluginDocs), time.Since(docStart).Round(time.Millisecond))
	}
	return nil
}

// GenerateGraphReport writes a Markdown graph report (GRAPH_REPORT.md
// format) for an index to outputPath.
func GenerateGraphReport(indexName, indexDir, docsDir, outputPath string) error {
	dbPath := filepath.Join(indexDir, indexName+"_graph")
	db, err := kgraph.Open(dbPath)
	if err != nil {
		return fmt.Errorf("error opening graph db: %w", err)
	}
	defer db.Close()

	result, err := community.FromKuzu(db, 5, 20)
	if err != nil {
		return fmt.Errorf("running community detection: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating file: %w", err)
	}
	defer f.Close()

	opts := report.Options{
		IndexName: indexName,
		DocsDir:   docsDir,
	}
	return report.WriteMarkdown(f, result, opts)
}
