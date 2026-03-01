package gleann

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// PassageManager handles storage and retrieval of text passages.
// It uses JSONL format for passages and a binary offset index for O(1) random access.
//
// Files:
//   - {name}.passages.jsonl — one JSON object per line
//   - {name}.passages.idx  — binary offset map (int64 per passage)
//
// This mirrors Python LEANN's PassageManager.
type PassageManager struct {
	mu       sync.RWMutex
	basePath string // path without extension
	passages []Passage
	offsets  []int64 // byte offsets into JSONL file
	loaded   bool
}

// NewPassageManager creates a new PassageManager.
func NewPassageManager(basePath string) *PassageManager {
	return &PassageManager{
		basePath: basePath,
	}
}

// jsonlPath returns the path to the JSONL file.
func (pm *PassageManager) jsonlPath() string {
	return pm.basePath + ".passages.jsonl"
}

// idxPath returns the path to the offset index file.
func (pm *PassageManager) idxPath() string {
	return pm.basePath + ".passages.idx"
}

// Add adds passages to the manager and writes them to disk.
func (pm *PassageManager) Add(items []Item) ([]int64, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Ensure directory exists.
	dir := filepath.Dir(pm.basePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	// Open JSONL file for append.
	f, err := os.OpenFile(pm.jsonlPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open JSONL file: %w", err)
	}
	defer f.Close()

	// Get current file position.
	pos, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("seek to end: %w", err)
	}

	startID := int64(len(pm.passages))
	ids := make([]int64, len(items))
	newOffsets := make([]int64, len(items))
	newPassages := make([]Passage, len(items))

	w := bufio.NewWriter(f)
	for i, item := range items {
		id := startID + int64(i)
		ids[i] = id
		newOffsets[i] = pos

		passage := Passage{
			ID:       id,
			Text:     item.Text,
			Metadata: item.Metadata,
		}
		newPassages[i] = passage

		data, err := json.Marshal(passage)
		if err != nil {
			return nil, fmt.Errorf("marshal passage %d: %w", id, err)
		}
		data = append(data, '\n')
		n, err := w.Write(data)
		if err != nil {
			return nil, fmt.Errorf("write passage %d: %w", id, err)
		}
		pos += int64(n)
	}
	if err := w.Flush(); err != nil {
		return nil, fmt.Errorf("flush JSONL: %w", err)
	}

	pm.passages = append(pm.passages, newPassages...)
	pm.offsets = append(pm.offsets, newOffsets...)
	pm.loaded = true

	// Write offset index.
	if err := pm.writeOffsets(); err != nil {
		return nil, fmt.Errorf("write offsets: %w", err)
	}

	return ids, nil
}

// Get retrieves a passage by its ID using the offset index for O(1) access.
func (pm *PassageManager) Get(id int64) (Passage, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// If in-memory, return directly.
	if pm.loaded && id >= 0 && id < int64(len(pm.passages)) {
		return pm.passages[id], nil
	}

	// Otherwise, use offset index for random access from disk.
	return pm.readFromDisk(id)
}

// GetBatch retrieves multiple passages by their IDs.
func (pm *PassageManager) GetBatch(ids []int64) ([]Passage, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	passages := make([]Passage, len(ids))
	for i, id := range ids {
		if pm.loaded && id >= 0 && id < int64(len(pm.passages)) {
			passages[i] = pm.passages[id]
		} else {
			p, err := pm.readFromDisk(id)
			if err != nil {
				return nil, fmt.Errorf("read passage %d: %w", id, err)
			}
			passages[i] = p
		}
	}
	return passages, nil
}

// GetTexts retrieves text content for the given IDs.
func (pm *PassageManager) GetTexts(ids []int64) ([]string, error) {
	passages, err := pm.GetBatch(ids)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(passages))
	for i, p := range passages {
		texts[i] = p.Text
	}
	return texts, nil
}

// All returns all passages in memory.
func (pm *PassageManager) All() []Passage {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.passages
}

// Count returns the number of passages.
func (pm *PassageManager) Count() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if len(pm.passages) > 0 {
		return len(pm.passages)
	}
	return len(pm.offsets)
}

// Load loads passages and offsets from disk.
// By default it uses lazy loading: only the offset index is read (typically <1 KB),
// and individual passages are fetched on demand via readFromDisk.
func (pm *PassageManager) Load() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Try loading just the offset index (tiny file).
	if err := pm.loadOffsets(); err != nil {
		// Offsets file doesn't exist — build it from JSONL.
		if err := pm.buildOffsets(); err != nil {
			return err
		}
		if err := pm.writeOffsets(); err != nil {
			return fmt.Errorf("write offsets: %w", err)
		}
	}

	// Don't load full JSONL into memory. Passages are loaded on demand.
	pm.loaded = false
	return nil
}

// LoadAll loads all passages into memory (needed for BM25 scoring).
func (pm *PassageManager) LoadAll() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.loadFromJSONL()
}

// buildOffsets scans the JSONL file to build the offset index
// without parsing each line's JSON content.
func (pm *PassageManager) buildOffsets() error {
	f, err := os.Open(pm.jsonlPath())
	if err != nil {
		if os.IsNotExist(err) {
			pm.offsets = nil
			pm.loaded = true
			return nil
		}
		return fmt.Errorf("open JSONL: %w", err)
	}
	defer f.Close()

	pm.offsets = nil
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var pos int64
	for scanner.Scan() {
		pm.offsets = append(pm.offsets, pos)
		pos += int64(len(scanner.Bytes())) + 1 // +1 for newline
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan JSONL for offsets: %w", err)
	}
	return nil
}

// loadFromJSONL loads all passages from the JSONL file.
func (pm *PassageManager) loadFromJSONL() error {
	f, err := os.Open(pm.jsonlPath())
	if err != nil {
		if os.IsNotExist(err) {
			pm.passages = nil
			pm.offsets = nil
			pm.loaded = true
			return nil
		}
		return fmt.Errorf("open JSONL: %w", err)
	}
	defer f.Close()

	pm.passages = nil
	pm.offsets = nil

	scanner := bufio.NewScanner(f)
	// Increase buffer for long lines.
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)

	var pos int64
	for scanner.Scan() {
		pm.offsets = append(pm.offsets, pos)

		line := scanner.Bytes()
		var passage Passage
		if err := json.Unmarshal(line, &passage); err != nil {
			return fmt.Errorf("unmarshal passage at offset %d: %w", pos, err)
		}
		pm.passages = append(pm.passages, passage)
		pos += int64(len(line)) + 1 // +1 for newline
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan JSONL: %w", err)
	}

	pm.loaded = true
	return nil
}

// readFromDisk reads a single passage from disk using the offset index.
func (pm *PassageManager) readFromDisk(id int64) (Passage, error) {
	if id < 0 || id >= int64(len(pm.offsets)) {
		return Passage{}, fmt.Errorf("passage ID %d out of range [0, %d)", id, len(pm.offsets))
	}

	f, err := os.Open(pm.jsonlPath())
	if err != nil {
		return Passage{}, fmt.Errorf("open JSONL: %w", err)
	}
	defer f.Close()

	offset := pm.offsets[id]
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return Passage{}, fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	reader := bufio.NewReader(f)
	line, err := reader.ReadBytes('\n')
	if err != nil && err != io.EOF {
		return Passage{}, fmt.Errorf("read line: %w", err)
	}

	var passage Passage
	if err := json.Unmarshal(line, &passage); err != nil {
		return Passage{}, fmt.Errorf("unmarshal passage: %w", err)
	}

	return passage, nil
}

// writeOffsets writes the offset index to disk.
func (pm *PassageManager) writeOffsets() error {
	f, err := os.Create(pm.idxPath())
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, offset := range pm.offsets {
		if err := binary.Write(w, binary.LittleEndian, offset); err != nil {
			return fmt.Errorf("write offset: %w", err)
		}
	}
	return w.Flush()
}

// loadOffsets loads the offset index from disk.
func (pm *PassageManager) loadOffsets() error {
	f, err := os.Open(pm.idxPath())
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	numOffsets := stat.Size() / 8 // int64 = 8 bytes
	pm.offsets = make([]int64, numOffsets)
	return binary.Read(f, binary.LittleEndian, pm.offsets)
}

// Delete removes all files associated with this passage manager.
func (pm *PassageManager) Delete() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.passages = nil
	pm.offsets = nil
	pm.loaded = false

	os.Remove(pm.jsonlPath())
	os.Remove(pm.idxPath())
	return nil
}
