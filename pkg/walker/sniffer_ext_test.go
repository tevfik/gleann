package walker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIsBinaryData_TruncatedMultibyteTextIsNotBinary(t *testing.T) {
	// Regression: a >1 KiB Cyrillic/CJK text whose 1 KiB sample ends in the
	// middle of a rune was classified as binary.
	cases := map[string]string{
		"cyrillic 2-byte": "Привет мир ",
		"cjk 3-byte":      "表格数据测试",
		"emoji 4-byte":    "😀🚀🎉",
	}
	for name, unit := range cases {
		t.Run(name, func(t *testing.T) {
			text := []byte(strings.Repeat(unit, 400))
			// Try every possible cut position around the 1 KiB sample boundary.
			for off := 0; off < utf8.UTFMax; off++ {
				data := append([]byte(strings.Repeat("a", off)), text...)
				if IsBinaryData(data) {
					t.Fatalf("offset %d: valid UTF-8 text classified as binary", off)
				}
				// Simulate a truncated file prefix shorter than the sample.
				prefix := data[:1023]
				if IsBinaryData(prefix) {
					t.Fatalf("offset %d: truncated UTF-8 prefix classified as binary", off)
				}
			}
		})
	}
}

func TestIsBinaryData_InvalidUTF8StillBinary(t *testing.T) {
	data := bytes.Repeat([]byte{0xC3, 0x28, 0xA0, 0xA1, 0xE2, 0x28, 0xA1}, 300)
	if !IsBinaryData(data) {
		t.Fatal("invalid UTF-8 high-byte data should be binary")
	}
	// A single dangling lead byte in a short, non-truncated sample is trimmed,
	// but invalid bytes elsewhere must still be detected.
	bad := append(bytes.Repeat([]byte("ж"), 100), 0xFF, 0xFE)
	if !IsBinaryData(bad) {
		t.Fatal("0xFF 0xFE tail is not an incomplete rune and should be binary")
	}
}

func TestTrimIncompleteRune(t *testing.T) {
	euro := []byte("€") // E2 82 AC
	tests := []struct {
		in, want []byte
	}{
		{nil, nil},
		{[]byte("abc"), []byte("abc")},
		{euro, euro},
		{euro[:1], []byte{}},
		{euro[:2], []byte{}},
		{append([]byte("a"), euro[:2]...), []byte("a")},
		{[]byte{0x80}, []byte{0x80}}, // lone continuation byte: not trimmed, still invalid
	}
	for _, tt := range tests {
		if got := trimIncompleteRune(tt.in); !bytes.Equal(got, tt.want) {
			t.Errorf("trimIncompleteRune(%x) = %x, want %x", tt.in, got, tt.want)
		}
	}
}

func TestSniffFile_DetectsMinifiedBundleByLongFirstLine(t *testing.T) {
	// Regression: SniffFile read only 1024 bytes, so the >2000-byte first
	// line heuristic could never fire for files on disk.
	dir := t.TempDir()
	p := filepath.Join(dir, "bundle.js")
	content := strings.Repeat("var a=1;function b(){return a}", 200) + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SniffFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsMinified {
		t.Fatalf("expected minified bundle to be detected from disk, got %+v", res)
	}
	if res.Language != "javascript" {
		t.Fatalf("language = %q", res.Language)
	}
}

func TestSniffBytes_LongFirstLineInProseIsNotMinified(t *testing.T) {
	long := strings.Repeat("word ", 600) // 3000 bytes, one line
	for _, name := range []string{"README.md", "notes.txt", "data.csv", "rows.tsv"} {
		if res := SniffBytes(name, []byte(long)); res.IsMinified {
			t.Errorf("%s: soft-wrapped prose/data must not be flagged minified: %+v", name, res)
		}
	}
	if res := SniffBytes("app.min.md", []byte("x")); !res.IsMinified {
		t.Error(".min. name should still mark minified regardless of category")
	}
	for _, name := range []string{"app.js", "style.json", "noext"} {
		if res := SniffBytes(name, []byte(long)); !res.IsMinified {
			t.Errorf("%s: long single line should be flagged minified", name)
		}
	}
}

func TestSniffBytes_ShebangVariants(t *testing.T) {
	tests := []struct {
		line string
		lang string
	}{
		{"#!/usr/bin/python3", "python"},
		{"#!/usr/local/bin/python3.12", "python"},
		{"#!/usr/bin/env python3", "python"},
		{"#! /usr/bin/env python", "python"},
		{"#!/usr/bin/env -S python3 -u", "python"},
		{"#!/usr/bin/env PYTHONPATH=. python3", "python"},
		{"#!/opt/homebrew/bin/bash", "bash"},
		{"#!/bin/sh -e", "bash"},
		{"#!/usr/bin/env zsh", "bash"},
		{"#!/usr/bin/env -S node --experimental-modules", "javascript"},
		{"#!/usr/bin/env deno run", "javascript"},
		{"#!/usr/bin/perl -w", "perl"},
		{"#!/usr/bin/env ruby", "ruby"},
		{"#!/usr/bin/php", "php"},
		{"#!/usr/bin/env", ""},
		{"#!", ""},
		{"#!/usr/bin/awk -f", ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			res := SniffBytes("script", []byte(tt.line+"\necho hi\n"))
			if res.Language != tt.lang {
				t.Fatalf("language = %q, want %q (%+v)", res.Language, tt.lang, res)
			}
			if tt.lang != "" && res.Category != CategoryCode {
				t.Fatalf("category = %q, want code", res.Category)
			}
		})
	}
}

func TestSniffFile_ShortAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SniffFile(empty)
	if err != nil || res.IsBinary || res.Category != CategoryCode {
		t.Fatalf("empty .go: %+v err=%v", res, err)
	}
	if _, err := SniffFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// FuzzSniffBytes checks SniffBytes never panics and keeps its result internally
// consistent; it also checks that valid UTF-8 text is never reported binary.
func FuzzSniffBytes(f *testing.F) {
	f.Add("a.go", []byte("package main\n"))
	f.Add("x", []byte("#!/usr/bin/env -S node\n"))
	f.Add("b.bin", []byte{0x00, 0x01, 0x02})
	f.Add("c.txt", []byte(strings.Repeat("Привет ", 300)))
	f.Add("d.js", []byte(strings.Repeat("a", 2500)))
	f.Add("e", []byte("#!"))
	f.Fuzz(func(t *testing.T, name string, data []byte) {
		res := SniffBytes(name, data)
		if res.IsBinary != (res.Category == CategoryBinary) {
			t.Fatalf("IsBinary/Category inconsistent: %+v", res)
		}
		if res.IsBinary && (res.IsMinified || res.Language != "") {
			t.Fatalf("binary result carries text attributes: %+v", res)
		}
		if res.Category == "" {
			t.Fatalf("empty category for %q", name)
		}
		// Printable, valid UTF-8 without control characters must be text.
		if utf8.Valid(data) && !bytes.ContainsFunc(data, func(r rune) bool {
			return r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f'
		}) && res.IsBinary {
			t.Fatalf("valid control-free UTF-8 classified binary: %q", data)
		}
	})
}
