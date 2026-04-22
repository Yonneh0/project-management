package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"project-management/pkg"

	"github.com/mark3labs/mcp-go/mcp"
)

func handleCreateItem(_ context.Context, req mcp.CallToolRequest, _ *pkg.FileStore, rootDir string) (*mcp.CallToolResult, error) {
	pathStr, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'path': %v", err)), nil
	}

	isFolder, _ := extractOptionalBool(req, "isFolder")
	overwrite, _ := extractOptionalBool(req, "overwrite")
	content, _ := extractOptionalString(req, "content")

	resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	baseResolved := filepath.Base(resolvedPath)
	isArchiveBase := pkg.IsArchiveFile(baseResolved)
	hasZipSlash := strings.Contains(pathStr, ".zip/")
	hasTarSlash := strings.Contains(pathStr, ".tar/") || strings.Contains(pathStr, ".tar.gz/") || strings.Contains(pathStr, ".tgz/")
	if isArchiveBase || hasZipSlash || hasTarSlash {
		return handleCreateInArchive(resolvedPath, content, isFolder, overwrite)
	}

	if isFolder {
		exists := dirExists(resolvedPath)
		if exists {
			if overwrite {
				return mcp.NewToolResultText(fmt.Sprintf("Folder already exists (overwrite=true): %s", resolvedPath)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("folder already exists: %s", resolvedPath)), nil
		}

		if err := os.MkdirAll(resolvedPath, 0755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create folder: %v", err)), nil
		}

		pctx := pkg.GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "create", resolvedPath)
		}

		return mcp.NewToolResultText(fmt.Sprintf("Folder created successfully: %s", resolvedPath)), nil
	}

	parentDir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create parent directories: %v", err)), nil
	}

	exists := fileExists(resolvedPath)
	if exists {
		if overwrite {
			if err := os.Remove(resolvedPath); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to remove existing file: %v", err)), nil
			}
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("file already exists (overwrite=false): %s", resolvedPath)), nil
		}
	}

	if content == "" && !isFolder {
		content = ""
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create file: %v", err)), nil
	}

	pctx := pkg.GetGlobalProject()
	if pctx != nil && pctx.Path != "" {
		autoCommit(pctx.Path, "create", resolvedPath)
	}

	return mcp.NewToolResultText(fmt.Sprintf("File created successfully: %s", resolvedPath)), nil
}

func handleCreateInArchive(archivePath, content string, isFolder bool, overwrite bool) (*mcp.CallToolResult, error) {
	parts := strings.SplitN(archivePath, "/", 2)
	if len(parts) < 2 {
		return mcp.NewToolResultError("invalid archive path format: use 'archive.zip/path/to/entry'"), nil
	}

	archFile := parts[0]
	entryPath := parts[1]

	sanitizedName, sanitizeErr := sanitizeArchiveEntryPath(entryPath)
	if sanitizeErr != nil {
		return mcp.NewToolResultText(fmt.Sprintf("Entry path rejected (sandbox escape): %s - %v", entryPath, sanitizeErr)), nil
	}
	entryPath = sanitizedName

	var archInfo *pkg.ArchiveInfo
	if existing, err := openArchive(archFile); err == nil {
		archInfo = existing
	} else {
		format := pkg.GetArchiveFormat(archFile)
		if format == "" {
			return mcp.NewToolResultError(fmt.Sprintf("unsupported archive format: %s", archFile)), nil
		}
		archInfo = &pkg.ArchiveInfo{
			Path:    archFile,
			Entries: make(map[string]pkg.ArchiveEntry),
			Format:  format,
			IsOpen:  true,
		}
	}

	if isFolder {
		if !strings.HasSuffix(entryPath, "/") {
			entryPath = entryPath + "/"
		}
		archInfo.Entries[entryPath] = pkg.ArchiveEntry{
			Name:    entryPath,
			IsDir:   true,
			ModTime: time.Now(),
		}
	} else {
		if !overwrite {
			if _, exists := archInfo.Entries[entryPath]; exists {
				return mcp.NewToolResultError(fmt.Sprintf("entry already exists in archive (overwrite=false): %s", entryPath)), nil
			}
		}
		archInfo.Entries[entryPath] = pkg.ArchiveEntry{
			Name:    entryPath,
			Content: []byte(content),
			IsDir:   false,
			ModTime: time.Now(),
		}
	}

	if err := saveArchive(archInfo); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to update archive: %v", err)), nil
	}

	pctx := pkg.GetGlobalProject()
	if pctx != nil && pctx.Path != "" {
		autoCommit(pctx.Path, "create", archFile)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Entry created in archive: %s → %s", filepath.Base(archFile), entryPath)), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
