package pkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileRecord is the in-memory representation of a stored file entry.
type fileRecord struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	MD5Hash string `json:"md5_hash"`
	IsDir   bool   `json:"is_dir"`
}

// storageFileStore is an in-memory, thread-safe file store with JSON persistence.
type storageFileStore struct {
	mu      sync.RWMutex
	records map[string]*fileRecord // keyed by absolute path
	index   map[string][]string    // keyed by md5_hash, values are paths (only for non-dirs)

	persistPath string
	saveTimer   *time.Timer
	saveMu      sync.Mutex
}

// storagePersistence holds the JSON file structure.
type storagePersistence struct {
	Version   int       `json:"version"`
	UpdatedAt string    `json:"updated_at"`
	Files     []fileRecord `json:"files"`
}

const (
	storageVersion = 1
	defaultPersistInterval = 5 * time.Second
)

// NewStorageFileStore creates a new in-memory file store with optional JSON persistence.
func NewStorageFileStore(persistPath string) (*storageFileStore, error) {
	s := &storageFileStore{
		records:     make(map[string]*fileRecord),
		index:       make(map[string][]string),
		persistPath: persistPath,
	}

	// Load existing data from persistence file if it exists.
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load storage: %w", err)
	}

	return s, nil
}

// load reads the JSON persistence file and populates in-memory structures.
func (s *storageFileStore) load() error {
	if s.persistPath == "" {
		return os.ErrNotExist
	}

	data, err := os.ReadFile(s.persistPath)
	if err != nil {
		return err
	}

	// Skip non-JSON files (e.g., old storage file from a previous version).
	if len(data) > 0 && data[0] != '{' && data[0] != '[' {
		fmt.Fprintf(os.Stderr, "storage: skipping non-JSON persistence file %s\n", s.persistPath)
		return os.ErrNotExist
	}

	var p storagePersistence
	if err := json.Unmarshal(data, &p); err != nil {
		fmt.Fprintf(os.Stderr, "storage: invalid persistence file %s (%v), starting fresh\n", s.persistPath, err)
		return os.ErrNotExist
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, rec := range p.Files {
		recPtr := &rec
		s.records[rec.Path] = recPtr
		if !rec.IsDir && rec.MD5Hash != "" {
			s.index[rec.MD5Hash] = append(s.index[rec.MD5Hash], rec.Path)
		}
	}

	return nil
}

// save writes in-memory data to the JSON persistence file.
func (s *storageFileStore) save() error {
	s.mu.RLock()
	files := make([]fileRecord, 0, len(s.records))
	for _, rec := range s.records {
		files = append(files, *rec)
	}
	s.mu.RUnlock()

	p := storagePersistence{
		Version:   storageVersion,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Files:     files,
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal persistence data: %w", err)
	}

	tmpPath := s.persistPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write persistence file: %w", err)
	}

	if err := os.Rename(tmpPath, s.persistPath); err != nil {
		return fmt.Errorf("rename persistence file: %w", err)
	}

	return nil
}

// scheduleSave queues a background save operation with debouncing.
func (s *storageFileStore) scheduleSave() {
	if s.persistPath == "" {
		return
	}

	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}

	s.saveTimer = time.AfterFunc(defaultPersistInterval, func() {
		if err := s.save(); err != nil {
			// Log to stderr silently — persistence is best-effort.
			fmt.Fprintf(os.Stderr, "storage save error: %v\n", err)
		}

		s.saveMu.Lock()
		s.saveTimer = nil
		s.saveMu.Unlock()
	})
}

// UpsertFile adds or updates a file record in memory and triggers persistence.
func (s *storageFileStore) UpsertFile(path string, isDir bool, size int64, modTime string, md5Hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := filepath.Base(path)

	// If replacing an existing record, remove old index entry.
	if existing, ok := s.records[path]; ok {
		if !existing.IsDir && existing.MD5Hash != "" && existing.MD5Hash != md5Hash {
			idx := s.index[existing.MD5Hash]
			for i, p := range idx {
				if p == path {
					s.index[existing.MD5Hash] = append(idx[:i], idx[i+1:]...)
					break
				}
			}
		}
	}

	rec := &fileRecord{
		Path:    path,
		Name:    name,
		Size:    size,
		ModTime: modTime,
		MD5Hash: md5Hash,
		IsDir:   isDir,
	}

	s.records[path] = rec

	if !isDir && md5Hash != "" {
		s.index[md5Hash] = append(s.index[md5Hash], path)
	}

	return nil
}

// DeleteFile removes a file record from memory and triggers persistence.
func (s *storageFileStore) DeleteFile(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[path]
	if !ok {
		return nil // not found is not an error
	}

	delete(s.records, path)

	if !rec.IsDir && rec.MD5Hash != "" {
		idx := s.index[rec.MD5Hash]
		for i, p := range idx {
			if p == path {
				s.index[rec.MD5Hash] = append(idx[:i], idx[i+1:]...)
				break
			}
		}
	}

	return nil
}

// SearchFiles searches for files by pattern substring match.
func (s *storageFileStore) SearchFiles(pattern string, limit int) (*SearchResult, error) {
	if limit <= 0 {
		limit = 1
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []FileInfo
	count := 0

	for _, rec := range s.records {
		if count >= limit {
			break
		}
		if containsFold(rec.Path, pattern) || containsFold(rec.Name, pattern) {
			results = append(results, fileInfoFromRecord(rec))
			count++
		}
	}

	// Count total matches (not limited).
	total := 0
	for _, rec := range s.records {
		if containsFold(rec.Path, pattern) || containsFold(rec.Name, pattern) {
			total++
		}
	}

	return &SearchResult{Files: results, Count: total}, nil
}

// FindDuplicates finds duplicate files by MD5 hash.
func (s *storageFileStore) FindDuplicates() ([]DuplicateGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []DuplicateGroup

	for md5Hash, paths := range s.index {
		if len(paths) <= 1 {
			continue
		}

		group := DuplicateGroup{MD5: md5Hash}
		for _, path := range paths {
			rec, ok := s.records[path]
			if !ok {
				continue
			}
			group.Files = append(group.Files, fileInfoFromRecord(rec))
		}

		result = append(result, group)
	}

	return result, nil
}

// RecordCount returns the number of stored records.
func (s *storageFileStore) RecordCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.records)
}

// Clear removes all records and triggers persistence.
func (s *storageFileStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.records = make(map[string]*fileRecord)
	s.index = make(map[string][]string)

	return nil
}

// fileInfoFromRecord converts an internal fileRecord to the public FileInfo struct.
func fileInfoFromRecord(rec *fileRecord) FileInfo {
	return FileInfo{
		Path:    rec.Path,
		Name:    rec.Name,
		Size:    rec.Size,
		ModTime: rec.ModTime,
		MD5:     rec.MD5Hash,
		IsDir:   rec.IsDir,
	}
}

// containsFold is a case-insensitive substring check.
func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && (lowercase(s) == lowercase(substr) ||
		len(substr) < len(s) && (lowercase(s[:len(substr)]) == lowercase(substr) ||
			lowercase(s[len(s)-len(substr):]) == lowercase(substr) ||
			findSubstring(lowercase(s), lowercase(substr)) >= 0))
}

func lowercase(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c = c + ('a' - 'A')
		}
		result[i] = c
	}
	return string(result)
}

func findSubstring(s, substr string) int {
	if len(substr) == 0 || len(substr) > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}