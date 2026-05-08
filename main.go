package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
)

func main() {
	// Set up signal handling for graceful shutdown.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Parse command-line flags
	targetDir := flag.String("target-dir", "", "Target directory for project operations (defaults to current working directory)")
	flag.Parse()

	// Determine target directory
	if *targetDir == "" {
		var err error
		*targetDir, err = os.Getwd()
		if err != nil {
			log.Fatalf("failed to get current directory: %v", err)
		}
	}

	// Set global root dir early so all tools can access it via GetGlobalRootDir().
	SetGlobalRootDir(*targetDir)

	mcpServer := server.NewMCPServer("project-management", "1.0.0")
	RegisterTools(mcpServer, *targetDir)

	log.Println("MCP server starting...")

	if err := server.ServeStdio(mcpServer); err != nil {
		errStr := err.Error()
		if isClientDisconnectError(errStr) {
			log.Println("Client disconnected. This is expected when the MCP host closes the connection.")
			return
		}
		log.Fatalf("server failed: %v", err)
	}
}

// isClientDisconnectError checks if the error string indicates a client-side disconnect.
func isClientDisconnectError(errStr string) bool {
	lower := strings.ToLower(errStr)
	indicators := []string{
		"broken pipe",
		"read error",
		"write error",
		"connection reset",
		"connection closed",
		"connection refused",
		"abort",
		"eof",
		"i/o timeout",
	}
	for _, indicator := range indicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}
