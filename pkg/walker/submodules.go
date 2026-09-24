package walker

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// ParseGitmodules parses a .gitmodules file and returns the list of submodule paths.
// The paths returned are normalized with forward slashes and relative to the repository root.
func ParseGitmodules(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var submodules []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Look for 'path = <relpath>'
		if strings.HasPrefix(line, "path") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				subPath := strings.TrimSpace(parts[1])
				// Clean quotes if any
				subPath = strings.Trim(subPath, "\"'")
				subPath = filepath.ToSlash(filepath.Clean(subPath))
				if subPath != "" && subPath != "." {
					submodules = append(submodules, subPath)
				}
			}
		}
	}

	return submodules, scanner.Err()
}
