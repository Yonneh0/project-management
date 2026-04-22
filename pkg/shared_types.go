package pkg

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DirEntryInfo holds information about a directory entry.
type DirEntryInfo struct {
	Name    string
	Path    string
	IsDir   bool
	Info    os.FileInfo
	RelPath string
}

// SortEntries sorts entries by the given criteria.
func SortEntries(entries []DirEntryInfo, sortBy string) {
	sort.SliceStable(entries, func(i, j int) bool {
		switch sortBy {
		case "size":
			sizeI := int64(0)
			sizeJ := int64(0)
			if entries[i].Info != nil && !entries[i].IsDir {
				sizeI = entries[i].Info.Size()
			}
			if entries[j].Info != nil && !entries[j].IsDir {
				sizeJ = entries[j].Info.Size()
			}
			return sizeI < sizeJ
		case "date":
			timeI := time.Time{}
			timeJ := time.Time{}
			if entries[i].Info != nil {
				timeI = entries[i].Info.ModTime()
			}
			if entries[j].Info != nil {
				timeJ = entries[j].Info.ModTime()
			}
			return timeI.Before(timeJ)
		case "type":
			if entries[i].IsDir != entries[j].IsDir {
				return !entries[i].IsDir
			}
			return entries[i].Name < entries[j].Name
		default:
			return entries[i].Name < entries[j].Name
		}
	})
}

// LanguageStatus holds the build status for a language.
type LanguageStatus struct {
	Language    string   `json:"language"`
	Detected    bool     `json:"detected"`
	Runtime     string   `json:"runtime,omitempty"`
	BuildStatus string   `json:"build_status,omitempty"`
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

// NewCompileCache creates a new compile cache with the given TTL.
func NewCompileCache(ttl time.Duration) *compileCache {
	return &compileCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// GenerateKey creates a cache key from the compile parameters.
func (c *compileCache) GenerateKey(path string, severity string, languages []string) string {
	key := path + ":" + severity
	if len(languages) > 0 {
		sort.Strings(languages)
		for _, lang := range languages {
			key += ":" + lang
		}
	}
	return key
}

// Get retrieves a cached result if it exists and hasn't expired.
func (c *compileCache) Get(key string) (*CompileResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}

	if time.Since(entry.createdAt) > c.ttl {
		delete(c.entries, key)
		return nil, false
	}

	return entry.result, true
}

// Set stores a result in the cache.
func (c *compileCache) Set(key string, result *CompileResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		result:    result,
		createdAt: time.Now(),
		key:       key,
	}
}

// Invalidate removes all cache entries matching a path prefix.
func (c *compileCache) Invalidate(path string) {
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

// InvalidateAll removes all cache entries.
func (c *compileCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// ProjectContext holds the currently active project state.
type ProjectContext struct {
	Path     string // absolute path to the opened project root
	NameHint string // human-readable hint (e.g., YYYYMMDD auto-generated name)
	GitInit  bool   // whether git has been initialized in this project
	IsNew    bool   // whether this was just created (not previously existing)
}

// GlobalProject is the singleton project context.
var GlobalProject *ProjectContext = nil

// CloseProject resets the global project context.
func CloseProject() {
	GlobalProject = nil
}

// GetGlobalProject returns the current project context.
func GetGlobalProject() *ProjectContext {
	return GlobalProject
}

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

// ArchiveCache stores open archives by their path.
var ArchiveCache = make(map[string]*ArchiveInfo)

// ArchiveCacheMu protects ArchiveCache.
var ArchiveCacheMu sync.RWMutex

// IsWithinProject checks if a resolved absolute path is within the current project root.
func IsWithinProject(path string) bool {
	if GlobalProject == nil || GlobalProject.Path == "" {
		return true
	}

	cleanPath := filepath.Clean(path)
	cleanProject := filepath.Clean(GlobalProject.Path)

	if filepath.IsAbs(cleanPath) && filepath.IsAbs(cleanProject) {
		comparePath := cleanPath
		compareProj := cleanProject
		if len(comparePath) >= 2 && comparePath[1] == ':' {
			comparePath = strings.ToLower(comparePath)
		}
		if len(compareProj) >= 2 && compareProj[1] == ':' {
			compareProj = strings.ToLower(compareProj)
		}

		if comparePath == compareProj {
			return true
		}

		if len(comparePath) > len(compareProj) && comparePath[len(compareProj)] == filepath.Separator {
			return strings.HasPrefix(comparePath, compareProj+string(filepath.Separator))
		}

		return false
	}

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
	if GlobalProject == nil || GlobalProject.Path == "" {
		return "", fmt.Errorf("no project open")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(GlobalProject.Path, path)
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
		return detectArchiveFormatByMagicBytes(path)
	}
}

// detectArchiveFormatByMagicBytes detects archive format using file signature bytes.
func detectArchiveFormatByMagicBytes(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 260)
	n, err := f.Read(buf)
	if err != nil || n < 4 {
		return ""
	}
	buf = buf[:n]

	if buf[0] == 0x50 && buf[1] == 0x4B && buf[2] == 0x03 && buf[3] == 0x04 {
		return "zip"
	}

	if buf[0] == 0x1F && buf[1] == 0x8B {
		return "tar.gz"
	}

	if n >= 263 && string(buf[257:262]) == "ustar" {
		return "tar"
	}

	return ""
}
