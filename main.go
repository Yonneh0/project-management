package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	const defaultDir = `C:\Projects\AI`
	targetDir := defaultDir

	if len(os.Args) > 1 {
		targetDir = os.Args[1]
	}

	dbPath := filepath.Join(targetDir, ".mcp_file_index.db")

	// Create compile cache with 60-second TTL
	compileCache := newCompileCache(60 * time.Second)

	store, err := initDatabase(dbPath, compileCache)
	if err != nil {
		log.Fatalf("database initialization failed: %v", err)
	}

	// Register global store for watcher-based cache invalidation
	setGlobalStore(store)

	// Set up cache invalidation hook on file changes
	store.SetCacheInvalidator(func(path string) {
		store.InvalidateCompileCache(filepath.Dir(path))
	})

	watcher, err := newFileWatcher(targetDir, dbPath, store.upsertFile, store.deleteFile)
	if err != nil {
		log.Fatalf("file watcher setup failed: %v", err)
	}
	defer watcher.Close()

	mcpServer := server.NewMCPServer("project-management", "1.0.0")
	registerTools(mcpServer, store, targetDir)

	if err := server.ServeStdio(mcpServer); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
