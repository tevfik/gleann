package indexing

import "strings"

// Mode selects which kinds of files an index build or sync considers.
type Mode string

const (
	// ModeAll indexes everything (default).
	ModeAll Mode = "all"
	// ModeCode indexes source code, configs and plain-text documentation;
	// office documents are skipped and external plugins are disabled.
	ModeCode Mode = "code"
	// ModeDocs indexes office documents, PDFs and documentation text only.
	ModeDocs Mode = "docs"
)

// ParseMode normalises a user-supplied mode string. An empty string yields
// ModeAll; other values are lower-cased and returned as-is, matching the
// historical CLI behaviour.
func ParseMode(s string) Mode {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ModeAll
	}
	return Mode(s)
}

// binaryExts lists file extensions that should be skipped unless a plugin or
// native extractor can handle them.
var binaryExts = map[string]bool{
	".pdf": true, ".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
	".exe": true, ".bin": true, ".dll": true, ".so": true, ".dylib": true, ".o": true, ".a": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true, ".svg": true, ".webp": true,
	".mp3": true, ".mp4": true, ".avi": true, ".mov": true, ".mkv": true, ".flv": true, ".wav": true, ".flac": true, ".ogg": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".db": true, ".sqlite": true, ".sqlite3": true,
	".pyc": true, ".class": true, ".jar": true, ".war": true,
	".iso": true, ".img": true, ".dmg": true, ".deb": true, ".rpm": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
}

var codeExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".swift": true, ".kt": true, ".kts": true,
	".cs": true, ".m": true, ".mm": true, ".lua": true, ".r": true,
	".scala": true, ".sc": true, ".clj": true, ".ex": true, ".exs": true, ".erl": true, ".hs": true,
	".ml": true, ".pl": true, ".sh": true, ".bash": true, ".zsh": true,
	".fish": true, ".ps1": true, ".bat": true, ".cmd": true,
	".sql": true, ".graphql": true, ".proto": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".xml": true, ".html": true, ".css": true, ".scss": true, ".less": true,
	".vue": true, ".svelte": true, ".astro": true,
	".tf": true, ".hcl": true, ".nix": true,
	".zig": true, ".nim": true, ".v": true, ".d": true,
	".dart": true, ".groovy": true, ".gradle": true,
}

var officeExts = map[string]bool{
	".pdf": true, ".docx": true, ".doc": true,
	".xlsx": true, ".xls": true, ".pptx": true, ".ppt": true,
	".csv": true, ".tsv": true, ".odt": true, ".ods": true, ".odp": true,
}

var documentationExts = map[string]bool{
	".md": true, ".markdown": true, ".mdown": true,
	".txt": true, ".rst": true, ".adoc": true,
}

// IsCodeExtension reports whether ext (e.g. ".go") is a recognised source
// code or config format. The comparison is case-insensitive.
func IsCodeExtension(ext string) bool { return codeExts[strings.ToLower(ext)] }

// IsOfficeDocExtension reports whether ext is an office/document format.
func IsOfficeDocExtension(ext string) bool { return officeExts[strings.ToLower(ext)] }

// IsDocumentationExtension reports whether ext is a text/markdown
// documentation format.
func IsDocumentationExtension(ext string) bool { return documentationExts[strings.ToLower(ext)] }
