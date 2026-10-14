package indexing

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/multimodal"
	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/walker"
)

// FileEntry is a file eligible for indexing.
type FileEntry struct {
	Path string
	Info os.FileInfo
}

// CollectOptions configures CollectEligibleFiles. All extractor fields are
// optional; they widen the set of binary extensions that are accepted.
type CollectOptions struct {
	Mode              Mode
	IncludeSubmodules bool
	PluginManager     *gleann.PluginManager
	NativeExtractor   *gleann.NativeExtractor
	Multimodal        *multimodal.Processor
}

// CollectEligibleFiles walks dir and returns files eligible for indexing,
// respecting .gitignore/.gleannignore, submodules, hidden dirs, binary
// extensions and the mode filter. The walk stops early when ctx is done.
func CollectEligibleFiles(ctx context.Context, dir string, co CollectOptions) ([]FileEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts := walker.Options{
		IncludeSubmodules: co.IncludeSubmodules,
		FollowSymlinks:    true,
	}
	pluginManager, nativeExtractor, mmProcessor, mode := co.PluginManager, co.NativeExtractor, co.Multimodal, co.Mode

	var files []FileEntry
	err := walker.Walk(dir, opts, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))

		// Skip binary extensions unless a plugin, native extractor, or multimodal can handle them
		canHandle := (pluginManager != nil && pluginManager.FindDocumentExtractor(ext) != nil) ||
			(nativeExtractor != nil && nativeExtractor.CanHandle(ext)) ||
			(mmProcessor != nil && mmProcessor.CanProcess(path))
		if binaryExts[ext] && !canHandle {
			return nil
		}

		// Fast path: skip minified bundles
		if strings.Contains(strings.ToLower(filepath.Base(path)), ".min.") {
			return nil
		}

		isStdCode := IsCodeExtension(ext)
		isStdDoc := IsDocumentationExtension(ext)
		isStdOffice := IsOfficeDocExtension(ext)

		var sniffResult *walker.SniffResult
		getSniff := func() walker.SniffResult {
			if sniffResult == nil {
				res, _ := walker.SniffFile(path)
				sniffResult = &res
			}
			return *sniffResult
		}

		switch mode {
		case ModeCode:
			// Code mode: source code, configs, and documentation text.
			// Office docs, binary files, and massive data dumps (>1MB) are skipped.
			if isStdOffice || info.Size() > 1<<20 {
				return nil
			}
			if !isStdCode && !isStdDoc {
				s := getSniff()
				if s.IsBinary || s.IsMinified {
					return nil
				}
				if s.Category != walker.CategoryCode && s.Category != walker.CategoryConfig && s.Category != walker.CategoryDoc {
					return nil
				}
			} else {
				// Verify text content for small files with code/doc extensions (protect against disguised binaries)
				if info.Size() > 0 && info.Size() < 2048 {
					s := getSniff()
					if s.IsBinary || s.IsMinified {
						return nil
					}
				}
			}
		case ModeDocs:
			// Docs mode: office docs, PDFs, and documentation text. Pure code files are skipped.
			if (!isStdOffice && !isStdDoc && !canHandle) || info.Size() > 100<<20 {
				return nil
			}
			if !isStdOffice && !canHandle {
				s := getSniff()
				if s.IsBinary || s.IsMinified {
					return nil
				}
			}
		case ModeAll, "":
			hasMultimodal := mmProcessor != nil && mmProcessor.CanProcess(path)
			if !isStdOffice && !hasMultimodal && !canHandle {
				if info.Size() > 100<<20 {
					return nil
				}
				s := getSniff()
				if s.IsBinary || s.IsMinified {
					return nil
				}
			}
		}

		files = append(files, FileEntry{Path: path, Info: info})
		return nil
	})
	return files, err
}
