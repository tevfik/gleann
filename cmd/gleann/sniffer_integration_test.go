package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIntegration_CollectEligibleFiles_Sniffer(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Extensionless Dockerfile
	_ = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte("FROM alpine:3.19\nCMD [\"echo\", \"hello\"]\n"), 0644)

	// 2. Extensionless Bash script
	_ = os.WriteFile(filepath.Join(tmpDir, "deploy_script"), []byte("#!/bin/bash\nset -e\necho 'Deploying cluster'\n"), 0755)

	// 3. Extensionless Python script
	_ = os.WriteFile(filepath.Join(tmpDir, "data_pipeline"), []byte("#!/usr/bin/env python3\nimport sys\nsys.exit(0)\n"), 0755)

	// 4. Standard Go file
	_ = os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644)

	// 5. Minified JS file (should be skipped)
	_ = os.WriteFile(filepath.Join(tmpDir, "bundle.min.js"), []byte("function a(){return 1;}"), 0644)

	// 6. Fake .txt binary file (should be skipped by binary check)
	_ = os.WriteFile(filepath.Join(tmpDir, "fake_text.txt"), []byte("Hello text\x00\x00\x01\x02binary payload"), 0644)

	// 7. Legitimate markdown doc
	_ = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# Project\nThis is docs.\n"), 0644)

	t.Run("IndexModeCode_IncludesExtensionless_ExcludesBinariesAndMinified", func(t *testing.T) {
		files, err := collectEligibleFiles(tmpDir, nil, nil, nil, IndexModeCode, false)
		if err != nil {
			t.Fatalf("collectEligibleFiles failed: %v", err)
		}

		fileMap := make(map[string]bool)
		for _, f := range files {
			fileMap[filepath.Base(f.path)] = true
		}

		// Expected included
		if !fileMap["Dockerfile"] {
			t.Errorf("expected Dockerfile to be included in IndexModeCode")
		}
		if !fileMap["deploy_script"] {
			t.Errorf("expected deploy_script (bash shebang) to be included in IndexModeCode")
		}
		if !fileMap["data_pipeline"] {
			t.Errorf("expected data_pipeline (python shebang) to be included in IndexModeCode")
		}
		if !fileMap["main.go"] {
			t.Errorf("expected main.go to be included in IndexModeCode")
		}
		if !fileMap["README.md"] {
			t.Errorf("expected README.md to be included in IndexModeCode")
		}

		// Expected excluded
		if fileMap["bundle.min.js"] {
			t.Errorf("expected bundle.min.js to be EXCLUDED in IndexModeCode")
		}
		if fileMap["fake_text.txt"] {
			t.Errorf("expected fake_text.txt (null bytes) to be EXCLUDED in IndexModeCode")
		}
	})

	t.Run("IndexModeAll_IncludesShebangAndDocs_ExcludesBinaries", func(t *testing.T) {
		files, err := collectEligibleFiles(tmpDir, nil, nil, nil, IndexModeAll, false)
		if err != nil {
			t.Fatalf("collectEligibleFiles failed: %v", err)
		}

		fileMap := make(map[string]bool)
		for _, f := range files {
			fileMap[filepath.Base(f.path)] = true
		}

		if !fileMap["deploy_script"] {
			t.Errorf("expected deploy_script to be included in IndexModeAll")
		}
		if !fileMap["Dockerfile"] {
			t.Errorf("expected Dockerfile to be included in IndexModeAll")
		}
		if fileMap["fake_text.txt"] {
			t.Errorf("expected fake_text.txt to be EXCLUDED in IndexModeAll")
		}
		if fileMap["bundle.min.js"] {
			t.Errorf("expected bundle.min.js to be EXCLUDED in IndexModeAll")
		}
	})
}
