package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// dirEntryInfo holds information about a directory entry.
type dirEntryInfo struct {
	name    string
	path    string
	isDir   bool
	info    os.FileInfo
	relPath string
}

func sortEntries(entries []dirEntryInfo, sortBy string) {
	sort.SliceStable(entries, func(i, j int) bool {
		switch sortBy {
		case "size":
			sizeI := int64(0)
			sizeJ := int64(0)
			if entries[i].info != nil && !entries[i].isDir {
				sizeI = entries[i].info.Size()
			}
			if entries[j].info != nil && !entries[j].isDir {
				sizeJ = entries[j].info.Size()
			}
			return sizeI < sizeJ
		case "date":
			timeI := time.Time{}
			timeJ := time.Time{}
			if entries[i].info != nil {
				timeI = entries[i].info.ModTime()
			}
			if entries[j].info != nil {
				timeJ = entries[j].info.ModTime()
			}
			return timeI.Before(timeJ)
		case "type":
			if entries[i].isDir != entries[j].isDir {
				return !entries[i].isDir
			}
			return entries[i].name < entries[j].name
		default: // name
			return entries[i].name < entries[j].name
		}
	})
}

// ==================== Compile Cache Types ====================

// LanguageStatus holds the build status for a language.
type LanguageStatus struct {
	Language    string   `json:"language"`
	Detected    bool     `json:"detected"`
	Runtime     string   `json:"runtime,omitempty"`
	BuildStatus string   `json:"build_status,omitempty"` // "success", "failed", "skipped"
	Errors      []string `json:"errors,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// CompileResult holds the result of a compile status check.
type CompileResult struct {
	Path             string           `json:"path"`
	Timestamp        time.Time        `json:"timestamp"`
	Cached           bool             `json:"cached,omitempty"`
	LanguageStatuses []LanguageStatus `json:"language_statuses"`
}

// compileCache is an in-memory cache for CompileStatus results.
type compileCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	result    *CompileResult
	createdAt time.Time
	key       string
}

// newCompileCache creates a new compile cache with the given TTL.
func newCompileCache(ttl time.Duration) *compileCache {
	return &compileCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// generateKey creates a cache key from the compile parameters.
func (c *compileCache) generateKey(path string, severity string, languages []string) string {
	key := path + ":" + severity
	if len(languages) > 0 {
		sort.Strings(languages)
		for _, lang := range languages {
			key += ":" + lang
		}
	}
	return key
}

// get retrieves a cached result if it exists and hasn't expired.
func (c *compileCache) get(key string) (*CompileResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	// Check TTL expiration
	if time.Since(entry.createdAt) > c.ttl {
		delete(c.entries, key)
		return nil, false
	}

	return entry.result, true
}

// set stores a result in the cache.
func (c *compileCache) set(key string, result *CompileResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		result:    result,
		createdAt: time.Now(),
		key:       key,
	}
}

// invalidate removes all cache entries matching a path prefix.
func (c *compileCache) invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if entry.result != nil && len(entry.result.Path) > 0 {
			if entry.result.Path == path || len(entry.result.Path) >= len(path) && entry.result.Path[:len(path)] == path {
				delete(c.entries, key)
			}
		} else {
			if len(key) >= len(path) && key[:len(path)] == path {
				delete(c.entries, key)
			}
		}
	}
}

// invalidateAll removes all cache entries.
func (c *compileCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// ==================== Project Context Types ====================

// ProjectContext holds the currently active project state.
type ProjectContext struct {
	Path     string // absolute path to the opened project root
	NameHint string // human-readable hint (e.g., YYYYMMDD auto-generated name)
	GitInit  bool   // whether git has been initialized in this project
	IsNew    bool   // whether this was just created (not previously existing)
}

// globalProject is the singleton project context.
var globalProject *ProjectContext = nil

// ==================== Archive Types ====================

// ArchiveEntry represents a single entry within an archive.
type ArchiveEntry struct {
	Name    string
	Content []byte
	IsDir   bool
	ModTime time.Time
}

// ArchiveInfo holds metadata about an open/mounted archive.
type ArchiveInfo struct {
	Path    string // absolute path to the archive file
	Entries map[string]ArchiveEntry
	Format  string // "zip", "tar", "tar.gz"
	IsOpen  bool   // whether the archive is currently loaded in memory
}

// archiveCache stores open archives by their path.
var archiveCache = make(map[string]*ArchiveInfo)
var archiveMu sync.RWMutex

// ==================== Project Boundary Check ====================

// IsWithinProject checks if a resolved absolute path is within the current project root.
func IsWithinProject(path string) bool {
	if globalProject == nil || globalProject.Path == "" {
		return true // no project open, allow all paths
	}

	// Clean both paths to normalize (removes trailing separators, resolves . and ..)
	cleanPath := filepath.Clean(path)
	cleanProject := filepath.Clean(globalProject.Path)

	// If already absolute, compare directly after cleaning
	if filepath.IsAbs(cleanPath) && filepath.IsAbs(cleanProject) {
		// Case-insensitive drive letter comparison for Windows (C: vs c:)
		comparePath := cleanPath
		compareProj := cleanProject
		if len(comparePath) >= 2 && comparePath[1] == ':' {
			comparePath = strings.ToLower(comparePath)
		}
		if len(compareProj) >= 2 && compareProj[1] == ':' {
			compareProj = strings.ToLower(compareProj)
		}

		// Direct equality - path equals project root exactly
		if comparePath == compareProj {
			return true
		}

		// Check if path is a subdirectory of the project
		// Must have: path starts with project + separator (not just prefix)
		if len(comparePath) > len(compareProj) && comparePath[len(compareProj)] == filepath.Separator {
			return strings.HasPrefix(comparePath, compareProj+string(filepath.Separator))
		}

		return false
	}

	// For relative paths, make them absolute first
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return false
	}
	absProject, err := filepath.Abs(cleanProject)
	if err != nil {
		return false
	}

	absPath = filepath.Clean(absPath)
	absProject = filepath.Clean(absProject)

	// Case-insensitive drive letter comparison for Windows (C: vs c:)
	compareAbs := strings.ToLower(absPath)
	compareProjAbs := strings.ToLower(absProject)

	if compareAbs == compareProjAbs {
		return true
	}

	if len(compareAbs) > len(compareProjAbs) && compareAbs[len(compareProjAbs)] == filepath.Separator {
		return strings.HasPrefix(compareAbs, compareProjAbs+string(filepath.Separator))
	}

	return false
}

// ResolveProjectPath resolves a path relative to the current project.
func ResolveProjectPath(path string) (string, error) {
	if globalProject == nil || globalProject.Path == "" {
		return "", fmt.Errorf("no project open")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(globalProject.Path, path)
	}
	return filepath.Clean(path), nil
}

// ResolveRootPath resolves a path relative to the root directory (for OpenProject).
func ResolveRootPath(rootDir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	return filepath.Clean(path), nil
}

// ==================== Archive Helpers ====================

// IsArchiveFile checks if a file is an archive based on extension.
func IsArchiveFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".zip", ".tar", ".gz", ".tgz", ".rar", ".7z":
		return true
	default:
		return false
	}
}

// GetArchiveFormat returns the archive format from extension.
func GetArchiveFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".zip":
		return "zip"
	case ".tar":
		return "tar"
	case ".gz", ".tgz":
		return "tar.gz"
	case ".rar":
		return "rar"
	case ".7z":
		return "7z"
	default:
		return ""
	}
}
