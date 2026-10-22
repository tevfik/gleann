package benchmark

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"hash/fnv"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// GenOptions controls GenerateGoTasks.
type GenOptions struct {
	Count    int    // number of tasks to produce (default 100)
	MinWords int    // minimum words in the doc sentence used as the query (default 6)
	Kind     string // "doc" (natural-language doc sentence, default) or "symbol" (identifier only)
}

// GenerateGoTasks derives retrieval tasks from a Go source tree, so the golden
// set grows with the code instead of being written by hand.
//
// Kind "doc": the query is the first sentence of an exported declaration's doc
// comment with the declaration's own name removed, and the gold file is the
// file that declares it. Removing the name keeps the task from being solved by
// exact-token matching alone, which is how a person who does not know the code
// asks a question.
// Kind "symbol": the query is the bare identifier (what an agent or a grep
// habit produces); the gold file is its declaration.
//
// Output is deterministic for a given tree. Test files are skipped.
func GenerateGoTasks(root string, opts GenOptions) ([]Task, error) {
	if opts.Count <= 0 {
		opts.Count = 100
	}
	if opts.MinWords <= 0 {
		opts.MinWords = 6
	}
	if opts.Kind == "" {
		opts.Kind = "doc"
	}
	if opts.Kind != "doc" && opts.Kind != "symbol" {
		return nil, fmt.Errorf("unknown task kind %q (want doc or symbol)", opts.Kind)
	}

	type cand struct {
		file, name, query string
	}
	var cands []cand
	nameCount := map[string]int{}

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "deps", "ext", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return nil // unparsable file: skip, not fatal
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		add := func(name string, doc *ast.CommentGroup) {
			if !ast.IsExported(name) {
				return
			}
			nameCount[name]++
			q := name
			if opts.Kind == "doc" {
				q = docQuery(doc, name)
				if len(strings.Fields(q)) < opts.MinWords {
					return
				}
			}
			cands = append(cands, cand{file: rel, name: name, query: q})
		}
		for _, decl := range f.Decls {
			switch dd := decl.(type) {
			case *ast.FuncDecl:
				add(dd.Name.Name, dd.Doc)
			case *ast.GenDecl:
				for _, sp := range dd.Specs {
					if ts, ok := sp.(*ast.TypeSpec); ok {
						doc := ts.Doc
						if doc == nil {
							doc = dd.Doc
						}
						add(ts.Name.Name, doc)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Names declared in several places (String, New, Close...) have no single
	// right answer; drop them.
	uniq := cands[:0]
	for _, c := range cands {
		if nameCount[c.name] == 1 {
			uniq = append(uniq, c)
		}
	}
	cands = uniq

	// Deterministic pseudo-random order so the sample spreads over the tree
	// instead of clustering in the first directories.
	sort.Slice(cands, func(i, j int) bool {
		hi, hj := hash(cands[i].file+"#"+cands[i].name), hash(cands[j].file+"#"+cands[j].name)
		if hi != hj {
			return hi < hj
		}
		return cands[i].file+cands[i].name < cands[j].file+cands[j].name
	})
	if len(cands) > opts.Count {
		cands = cands[:opts.Count]
	}

	tasks := make([]Task, 0, len(cands))
	for i, c := range cands {
		tasks = append(tasks, Task{
			ID:          fmt.Sprintf("gen-%s-%03d", opts.Kind, i+1),
			Description: c.name,
			Query:       c.query,
			GoldFiles:   []string{c.file},
		})
	}
	return tasks, nil
}

func hash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

// docQuery returns the first sentence of a doc comment with name removed.
func docQuery(doc *ast.CommentGroup, name string) string {
	if doc == nil {
		return ""
	}
	text := strings.Join(strings.Fields(doc.Text()), " ")
	if loc := sentenceEnd.FindStringIndex(text); loc != nil {
		text = text[:loc[0]]
	}
	// Drop the identifier itself, plain and as a Go-doc lead word.
	text = regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\b`).ReplaceAllString(text, "")
	text = strings.TrimFunc(strings.Join(strings.Fields(text), " "), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ':'
	})
	return text
}
