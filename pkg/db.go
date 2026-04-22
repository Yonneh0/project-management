package pkg

import (
	"crypto/md5"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// SearchResult holds the result of a file search operation.
type SearchResult struct {
	Files []FileInfo `json:"files"`
	Count int        `json:"count"`
}

// DuplicateGroup holds files that share the same MD5 hash.
type DuplicateGroup struct {
	MD5   string     `json:"md5"`
	Files []FileInfo `json:"files"`
}

// FileInfo represents a single indexed file record for JSON serialization and tool results.
type FileInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	MD5     string `json:"md5_hash"`
	IsDir   bool   `json:"is_dir"`
}

// DirExists checks if a directory exists at the given path.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CompileCacheInvalidator provides a hook for invalidating the compile cache on file changes.
type CompileCacheInvalidator func(path string)

// FileStore is the main storage interface for indexed files.
type FileStore struct {
	store            *storageFileStore
	CompileCache     *compileCache
	cacheInvalidator CompileCacheInvalidator
}

var dbFilePath string

// InitDatabase initializes the file store with in-memory storage and optional JSON persistence.
func InitDatabase(path string, cache *compileCache) (*FileStore, error) {
	dbFilePath = path

	store := &FileStore{}
	var err error
	store.store, err = NewStorageFileStore(path)
	if err != nil {
		return nil, fmt.Errorf("open storage: %w", err)
	}

	if err := store.scanDirectory(path); err != nil {
		return nil, fmt.Errorf("initial scan failed: %w", err)
	}

	return store, nil
}

// SetCompileCache sets the compile cache for this file store.
func (s *FileStore) SetCompileCache(cache *compileCache) {
	s.CompileCache = cache
}

// SetCacheInvalidator sets the cache invalidation hook.
func (s *FileStore) SetCacheInvalidator(invalidator CompileCacheInvalidator) {
	s.cacheInvalidator = invalidator
}

// InvalidateCompileCache invalidates compile cache entries for a given path.
func (s *FileStore) InvalidateCompileCache(path string) {
	if s.CompileCache != nil {
		s.CompileCache.Invalidate(path)
	}
	if s.cacheInvalidator != nil {
		s.cacheInvalidator(path)
	}
}

func (s *FileStore) scanDirectory(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if dbFilePath != "" && p == dbFilePath {
			return nil
		}

		if d.IsDir() {
			return s.upsertFile(p, true, 0, "", "")
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		hash, hErr := computeMD5(p)
		if hErr != nil {
			log.Printf("hash failed for %s: %v", p, hErr)
			return nil
		}

		return s.upsertFile(p, false, info.Size(), info.ModTime().UTC().Format(time.RFC3339), hash)
	})
}

// ComputeMD5 computes the MD5 hash of a file and returns it as a hex string.
func ComputeMD5(path string) (string, error) {
	return computeMD5(path)
}

func computeMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// UpsertFile updates or inserts a file record into the store.
func (s *FileStore) UpsertFile(path string, isDir bool, size int64, modTime string, md5Hash string) error {
	return s.upsertFile(path, isDir, size, modTime, md5Hash)
}

func (s *FileStore) upsertFile(path string, isDir bool, size int64, modTime string, md5Hash string) error {
	if err := s.store.UpsertFile(path, isDir, size, modTime, md5Hash); err != nil {
		return err
	}
	s.store.scheduleSave()
	return s.updateParentDirStats(path)
}

// DeleteFile deletes a file record from the store.
func (s *FileStore) DeleteFile(path string) error {
	return s.deleteFile(path)
}

func (s *FileStore) deleteFile(path string) error {
	if err := s.store.DeleteFile(path); err != nil {
		return err
	}
	s.store.scheduleSave()
	return nil
}

// SearchFiles searches for files by pattern.
func (s *FileStore) SearchFiles(pattern string, limit int) (*SearchResult, error) {
	return s.store.SearchFiles(pattern, limit)
}

// FindDuplicates finds duplicate files by MD5 hash.
func (s *FileStore) FindDuplicates() ([]DuplicateGroup, error) {
	return s.store.FindDuplicates()
}

func (s *FileStore) updateParentDirStats(path string) error {
	var modTime string
	var md5Hash string
	var isDir bool

	// Check if path exists in store.
	rec, ok := s.store.records[path]
	if !ok {
		return nil // directory may not be indexed yet
	}

	modTime = rec.ModTime
	md5Hash = rec.MD5Hash
	isDir = rec.IsDir

	var totalSize int64 = 0
	var fileCount int = 0

	err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			info, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			totalSize += info.Size()
			fileCount++
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk directory %s for stats: %w", path, err)
	}

	return s.upsertFile(path, isDir, totalSize, modTime, md5Hash)
}