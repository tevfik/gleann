// Package multimodal — file metadata extractor (Bug #9).
//
// Provides format-aware metadata that complements the VLM description so
// downstream consumers (graph indexer, search filters) can query by
// dimensions, timestamp, or exiftool-derived camera fields.
//
// The pure-Go path uses image.DecodeConfig (registered with all stdlib
// image formats) to obtain dimensions cheaply without decoding the full
// pixel buffer. When `exiftool` is on PATH and `GLEANN_EXIFTOOL=1` is set
// the extractor additionally collects camera/GPS/datetime tags.
package multimodal

import (
	"encoding/json"
	"image"
	_ "image/gif"  // register decoder for DecodeConfig
	_ "image/jpeg" // register decoder for DecodeConfig
	_ "image/png"  // register decoder for DecodeConfig
	"os"
	"os/exec"
	"strings"
	"time"
)

// FileMetadata captures lightweight, format-aware facts about a media file.
type FileMetadata struct {
	Width     int               `json:"width,omitempty"`
	Height    int               `json:"height,omitempty"`
	Format    string            `json:"format,omitempty"`
	SizeBytes int64             `json:"size_bytes,omitempty"`
	ModTime   time.Time         `json:"mod_time,omitempty"`
	Exif      map[string]string `json:"exif,omitempty"` // optional, exiftool when enabled
}

// ExtractFileMetadata returns metadata for the given file. The function
// never returns an error for missing optional data; instead it populates
// only the fields it could read. A missing file or unreadable header is
// reported via the returned error.
func ExtractFileMetadata(path string) (FileMetadata, error) {
	var md FileMetadata
	info, err := os.Stat(path)
	if err != nil {
		return md, err
	}
	md.SizeBytes = info.Size()
	md.ModTime = info.ModTime()

	// Best-effort image dimensions via stdlib (covers png/jpg/gif).
	if f, ferr := os.Open(path); ferr == nil {
		defer f.Close()
		if cfg, fmtName, derr := image.DecodeConfig(f); derr == nil {
			md.Width = cfg.Width
			md.Height = cfg.Height
			md.Format = fmtName
		}
	}

	// Optional exiftool enrichment.
	if os.Getenv("GLEANN_EXIFTOOL") == "1" {
		if exif := extractExifWithTool(path); len(exif) > 0 {
			md.Exif = exif
		}
	}
	return md, nil
}

// extractExifWithTool shells out to `exiftool -j` and returns a flat
// string map of the most useful tags. Returns nil on any failure so
// callers can treat it as best-effort enrichment.
func extractExifWithTool(path string) map[string]string {
	if _, err := exec.LookPath("exiftool"); err != nil {
		return nil
	}
	out, err := exec.Command("exiftool", "-j", "-n", "-q",
		"-Make", "-Model", "-DateTimeOriginal", "-CreateDate",
		"-GPSLatitude", "-GPSLongitude", "-ImageWidth", "-ImageHeight",
		path).Output()
	if err != nil {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err != nil || len(arr) == 0 {
		return nil
	}
	flat := make(map[string]string, len(arr[0]))
	for k, v := range arr[0] {
		if k == "SourceFile" {
			continue
		}
		switch tv := v.(type) {
		case string:
			s := strings.TrimSpace(tv)
			if s != "" {
				flat[k] = s
			}
		case float64:
			flat[k] = strings.TrimRight(strings.TrimRight(
				stringFromFloat(tv), "0"), ".")
		default:
			data, _ := json.Marshal(v)
			flat[k] = string(data)
		}
	}
	return flat
}

// stringFromFloat is a small helper that avoids importing strconv just for
// the format specifier; precision is intentionally generous because EXIF
// GPS values commonly need 6+ decimals.
func stringFromFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
