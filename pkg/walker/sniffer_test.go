package walker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSniffBytes_BinaryData(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		filename string
		isBinary bool
	}{
		{
			name:     "clean ascii text",
			data:     []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"),
			filename: "main.go",
			isBinary: false,
		},
		{
			name:     "utf-8 turkish text",
			data:     []byte("Türkçe karakterler: ç, ğ, ı, ö, ş, ü. Merhaba dünya!\n"),
			filename: "notlar.txt",
			isBinary: false,
		},
		{
			name:     "null byte present",
			data:     []byte("hello world\x00this is binary\x01\x02"),
			filename: "unknown.dat",
			isBinary: true,
		},
		{
			name:     "elf binary header",
			data:     []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
			filename: "tool",
			isBinary: true,
		},
		{
			name:     "png header disguised as txt",
			data:     append([]byte("\x89PNG\r\n\x1a\n"), []byte(strings.Repeat("\x00", 20))...),
			filename: "image.txt",
			isBinary: true,
		},
		{
			name:     "control characters exceeding ratio",
			data:     []byte("\x01\x02\x03\x04\x05\x06\x07\x08abc"),
			filename: "blob.bin",
			isBinary: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := SniffBytes(tt.filename, tt.data)
			if res.IsBinary != tt.isBinary {
				t.Errorf("SniffBytes(%s).IsBinary = %v, want %v", tt.name, res.IsBinary, tt.isBinary)
			}
			if tt.isBinary && res.Category != CategoryBinary {
				t.Errorf("expected CategoryBinary for %s, got %s", tt.name, res.Category)
			}
		})
	}
}

func TestSniffBytes_ShebangAndExtensionless(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		data         []byte
		wantLang     string
		wantCategory FileCategory
	}{
		{
			name:         "bash shebang",
			filename:     "deploy",
			data:         []byte("#!/bin/bash\nset -euo pipefail\necho 'Deploying...'\n"),
			wantLang:     "bash",
			wantCategory: CategoryCode,
		},
		{
			name:         "env python3 shebang",
			filename:     "generate_report",
			data:         []byte("#!/usr/bin/env python3\nimport sys\nprint('Report')\n"),
			wantLang:     "python",
			wantCategory: CategoryCode,
		},
		{
			name:         "env node shebang",
			filename:     "cli",
			data:         []byte("#!/usr/bin/env node\nconsole.log('CLI');\n"),
			wantLang:     "javascript",
			wantCategory: CategoryCode,
		},
		{
			name:         "dockerfile exact name",
			filename:     "Dockerfile",
			data:         []byte("FROM golang:1.24-alpine\nWORKDIR /app\nCOPY . .\n"),
			wantLang:     "dockerfile",
			wantCategory: CategoryConfig,
		},
		{
			name:         "makefile exact name",
			filename:     "Makefile",
			data:         []byte("all:\n\tgo build ./...\n"),
			wantLang:     "makefile",
			wantCategory: CategoryConfig,
		},
		{
			name:         "dockerfile extension",
			filename:     "Dockerfile.prod",
			data:         []byte("FROM alpine:latest\nRUN apk add curl\n"),
			wantLang:     "dockerfile",
			wantCategory: CategoryConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := SniffBytes(tt.filename, tt.data)
			if res.Language != tt.wantLang {
				t.Errorf("Language = %q, want %q", res.Language, tt.wantLang)
			}
			if res.Category != tt.wantCategory {
				t.Errorf("Category = %q, want %q", res.Category, tt.wantCategory)
			}
		})
	}
}

func TestSniffBytes_MinifiedDetection(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		data         []byte
		wantMinified bool
	}{
		{
			name:         "min.js file name",
			filename:     "bundle.min.js",
			data:         []byte("function a(){return 1;}"),
			wantMinified: true,
		},
		{
			name:         "very long single line (>2000 chars)",
			filename:     "generated.js",
			data:         []byte("var x=" + strings.Repeat("a+", 1500) + "b;"),
			wantMinified: true,
		},
		{
			name:         "normal formatted code",
			filename:     "app.js",
			data:         []byte("function app() {\n  const x = 1;\n  return x;\n}\n"),
			wantMinified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := SniffBytes(tt.filename, tt.data)
			if res.IsMinified != tt.wantMinified {
				t.Errorf("IsMinified = %v, want %v", res.IsMinified, tt.wantMinified)
			}
		})
	}
}

func TestSniffFile(t *testing.T) {
	tmpDir := t.TempDir()

	pyScript := filepath.Join(tmpDir, "my_script")
	_ = os.WriteFile(pyScript, []byte("#!/usr/bin/env python3\nprint('hello world')\n"), 0755)

	res, err := SniffFile(pyScript)
	if err != nil {
		t.Fatalf("SniffFile failed: %v", err)
	}
	if res.IsBinary {
		t.Errorf("expected not binary")
	}
	if res.Language != "python" {
		t.Errorf("expected language python, got %q", res.Language)
	}
	if res.Category != CategoryCode {
		t.Errorf("expected CategoryCode, got %s", res.Category)
	}
}
