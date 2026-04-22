package pkg

import (
	"crypto/md5"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
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

const (
	schemaSQL = `
		CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT UNIQUE NOT NULL,
			name TEXT NOT NULL,
			size INTEGER NOT NULL,
			mod_time TEXT NOT NULL,
			md5_hash TEXT NOT NULL,
			is_dir BOOLEAN NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_files_path ON files(path);
		CREATE INDEX IF NOT EXISTS idx_files_md5 ON files(md5_hash);
	`
)

// CompileCacheInvalidator provides a hook for invalidating the compile cache on file changes.
type CompileCacheInvalidator func(path string)

// DirExists checks if a directory exists at the given path.
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// FileStore is the main storage interface for indexed files.
type FileStore struct {
	db               *sql.DB
	CompileCache     *compileCache
	cacheInvalidator CompileCacheInvalidator
}

// InitDatabase initializes the file store and database at the given path.
func InitDatabase(path string, cache *compileCache) (*FileStore, error) {
	dbFilePath = path

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := conn.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	store := &FileStore{db: conn}
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

var dbFilePath string

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

// UpsertFile updates or inserts a file record into the database.
func (s *FileStore) UpsertFile(path string, isDir bool, size int64, modTime string, md5Hash string) error {
	return s.upsertFile(path, isDir, size, modTime, md5Hash)
}

func (s *FileStore) upsertFile(path string, isDir bool, size int64, modTime string, md5Hash string) error {
	name := filepath.Base(path)
	query := `INSERT INTO files (path, name, size, mod_time, md5_hash, is_dir) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(path) DO UPDATE SET name=excluded.name, size=excluded.size, mod_time=excluded.mod_time, md5_hash=excluded.md5_hash, is_dir=excluded.is_dir`

	_, err := s.db.Exec(query, path, name, size, modTime, md5Hash, isDir)
	return err
}

// DeleteFile deletes a file record from the database.
func (s *FileStore) DeleteFile(path string) error {
	return s.deleteFile(path)
}

func (s *FileStore) deleteFile(path string) error {
	_, err := s.db.Exec("DELETE FROM files WHERE path = ?", path)
	return err
}

// SearchFiles searches for files by pattern.
func (s *FileStore) SearchFiles(pattern string, limit int) (*SearchResult, error) {
	if limit <= 0 {
		limit = 1
	}
	query := "SELECT path, name, size, mod_time, md5_hash, is_dir FROM files WHERE path LIKE ? LIMIT ?"
	rows, err := s.db.Query(query, "%"+pattern+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FileInfo
	for rows.Next() {
		var f FileInfo
		if err := rows.Scan(&f.Path, &f.Name, &f.Size, &f.ModTime, &f.MD5, &f.IsDir); err != nil {
			return nil, err
		}
		results = append(results, f)
	}

	var total int
	s.db.QueryRow("SELECT COUNT(*) FROM files WHERE path LIKE ?", "%"+pattern+"%").Scan(&total)

	return &SearchResult{Files: results, Count: total}, nil
}

// FindDuplicates finds duplicate files by MD5 hash.
func (s *FileStore) FindDuplicates() ([]DuplicateGroup, error) {
	query := `SELECT md5_hash, path, name, size, mod_time FROM files WHERE is_dir = 0 GROUP BY md5_hash HAVING COUNT(*) > 1`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	groupsMap := make(map[string]*DuplicateGroup)
	for rows.Next() {
		var md5Hash, path, name, modTime string
		var size int64
		if err := rows.Scan(&md5Hash, &path, &name, &modTime, &size); err != nil {
			return nil, err
		}

		group, exists := groupsMap[md5Hash]
		if !exists {
			group = &DuplicateGroup{MD5: md5Hash}
			groupsMap[md5Hash] = group
		}
		group.Files = append(group.Files, FileInfo{Path: path, Name: name, Size: size, ModTime: modTime})
	}

	var result []DuplicateGroup
	for _, g := range groupsMap {
		result = append(result, *g)
	}
	return result, nil
}

func (s *FileStore) updateParentDirStats(path string) error {
	var size int64
	var name string
	var modTime string
	var md5Hash string
	var isDir bool
	err := s.db.QueryRow("SELECT size, name, mod_time, md5_hash, is_dir FROM files WHERE path = ?", path).Scan(&size, &name, &modTime, &md5Hash, &isDir)
	if err != nil {
		return fmt.Errorf("failed to retrieve current stats for %s: %w", path, err)
	}

	var totalSize int64 = 0
	var fileCount int = 0

	err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
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
