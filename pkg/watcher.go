package pkg

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

var ignoredPaths map[string]bool

type fileOp func(path string, isDir bool, size int64, modTime string, md5Hash string) error
type deleteOp func(path string) error

func NewFileWatcher(root string, dbPath string, upsert fileOp, del deleteOp) (*fsnotify.Watcher, error) {
	ignoredPaths = make(map[string]bool)
	if dbPath != "" {
		ignoredPaths[dbPath] = true
	}

	ignoredPatterns := []string{
		".mcp_file_index.db",
		".mcp_file_index.db-journal",
		".mcp_file_index.db-wal",
		".mcp_file_index.db-shm",
	}
	for _, pattern := range ignoredPatterns {
		ignoredPaths[pattern] = true
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			select {
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				handleWatchEvent(event, upsert, del)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				log.Printf("watcher error: %v", err)
			}
		}
	}()

	if err := w.Add(root); err != nil {
		return nil, fmt.Errorf("add watch for %s: %w", root, err)
	}
	log.Printf("file watcher active for: %s (ignoring %d paths)", root, len(ignoredPaths))
	return w, nil
}

var GlobalStore *FileStore

func SetGlobalStore(store *FileStore) {
	GlobalStore = store
}

func handleWatchEvent(event fsnotify.Event, upsert fileOp, del deleteOp) {
	path := event.Name

	if shouldIgnorePath(path) {
		return
	}

	if event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write {
		info, err := os.Stat(path)
		if err != nil {
			return
		}

		isDir := info.IsDir()
		modTime := info.ModTime().UTC().Format(time.RFC3339)
		size := info.Size()
		md5Hash := ""

		if !isDir {
			hash, hErr := computeMD5(path)
			if hErr == nil {
				md5Hash = hash
			}
		}

		if err := upsert(path, isDir, size, modTime, md5Hash); err != nil {
			log.Printf("upsert failed: %v", err)
		}

		if !isDir && GlobalStore != nil {
			GlobalStore.InvalidateCompileCache(filepath.Dir(path))
		}
	} else if event.Op&fsnotify.Remove == fsnotify.Remove || event.Op&fsnotify.Rename == fsnotify.Rename {
		if err := del(path); err != nil {
			log.Printf("delete failed: %v", err)
		}

		if GlobalStore != nil {
			GlobalStore.InvalidateCompileCache(filepath.Dir(path))
		}
	}
}

func shouldIgnorePath(eventPath string) bool {
	if ignoredPaths[eventPath] {
		return true
	}

	baseName := filepath.Base(eventPath)
	if ignoredPaths[baseName] {
		return true
	}

	return false
}
