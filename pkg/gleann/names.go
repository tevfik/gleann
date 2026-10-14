package gleann

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxNameLength is the maximum length of an index or plugin name.
const MaxNameLength = 128

// ErrInvalidName is returned (wrapped) by ValidateName for unsafe names.
var ErrInvalidName = errors.New("invalid name")

// validNameRe allows ASCII letters, digits, '.', '_' and '-', and requires the
// first character to be alphanumeric (so names can never start with '-' and be
// parsed as a CLI flag, nor start with '.' and refer to hidden/parent dirs).
var validNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateName checks that name is safe to use as an index or plugin name.
//
// Index and plugin names are joined into filesystem paths (indexDir/name,
// ~/.gleann/plugins/name) and passed as positional arguments to the gleann
// binary, so they must be a single, non-hidden path component that cannot be
// mistaken for a flag. ValidateName rejects:
//   - empty names and names longer than MaxNameLength
//   - "." and ".."
//   - names containing path separators or other characters outside
//     [A-Za-z0-9._-]
//   - names not starting with an ASCII letter or digit (e.g. "-x", ".hidden")
//
// External entry points (HTTP handlers, MCP tools) should call this before
// touching the filesystem.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("%w: name exceeds %d characters", ErrInvalidName, MaxNameLength)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidName, name)
	}
	if !validNameRe.MatchString(name) {
		return fmt.Errorf("%w: %q (allowed: letters, digits, '.', '_', '-'; must start with a letter or digit)", ErrInvalidName, name)
	}
	return nil
}

// checkPathName is the defensive check used inside pkg/gleann wherever a
// name is joined into a filesystem path. It is deliberately more lenient than
// ValidateName so that indexes created by older versions with unusual (but
// harmless) names such as spaces remain usable from the CLI, while still
// making directory traversal impossible.
func checkPathName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name is empty", ErrInvalidName)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: %q contains a path separator", ErrInvalidName, name)
	}
	return nil
}
