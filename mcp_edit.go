package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

func handleEditItem(ctx context.Context, req mcp.CallToolRequest, store *fileStore, rootDir string) (*mcp.CallToolResult, error) {
	pathStr, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'path': %v", err)), nil
	}

	action, _ := extractOptionalString(req, "action")
	if action == "" {
		action = "edit"
	}

	oldText, _ := extractOptionalString(req, "oldText")
	newText, _ := extractOptionalString(req, "newText")
	countArg, _ := extractOptionalInt(req, "count")
	compressDest, _ := extractOptionalString(req, "compressToArchive")
	deleteOriginal, _ := extractOptionalBool(req, "deleteOriginalAfterCompress")
	extractSrc, _ := extractOptionalString(req, "extractFromArchive")
	recursive, _ := extractOptionalBool(req, "recursive")
	ignoreMissing, _ := extractOptionalBool(req, "ignoreMissing")

	pctx := GetGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	switch action {
	case "edit":
		return handleEditFile(pathStr, oldText, newText, countArg, rootDir)
	case "delete":
		return handleDeleteInEdit(pathStr, recursive, ignoreMissing, pctx.Path)
	case "compress":
		return handleCompressItem(pathStr, compressDest, deleteOriginal, pctx.Path)
	case "extract":
		return handleExtractItem(pathStr, extractSrc, rootDir)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (valid: edit, delete, compress, extract)", action)), nil
	}
}

func handleEditFile(pathStr, oldText, newText string, countArg int, rootDir string) (*mcp.CallToolResult, error) {
	resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	contentBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	content := string(contentBytes)

	matchCount := strings.Count(content, oldText)
	if matchCount == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No occurrences of '%s' found in %s", truncate(oldText, 50), filepath.Base(resolvedPath))), nil
	}

	replacements := countArg
	if replacements == 0 {
		replacements = matchCount
	}
	if replacements > matchCount {
		replacements = matchCount
	}

	newContent := replaceN(content, oldText, newText, replacements)
	replacedCount := matchCount - strings.Count(newContent, oldText)

	if newContent == content {
		return mcp.NewToolResultText(fmt.Sprintf("No changes made (content identical) in %s", filepath.Base(resolvedPath))), nil
	}

	if err := os.WriteFile(resolvedPath, []byte(newContent), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	pctx := GetGlobalProject()
	if pctx != nil && pctx.Path != "" {
		autoCommit(pctx.Path, "edit", resolvedPath)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Replaced %d occurrence(s) in %s", replacedCount, filepath.Base(resolvedPath))), nil
}

func replaceN(s, old, new string, n int) string {
	for i := 0; i < n; i++ {
		idx := strings.Index(s, old)
		if idx == -1 {
			break
		}
		s = s[:idx] + new + s[idx+len(old):]
	}
	return s
}

func handleDeleteInEdit(pathStr string, recursive, ignoreMissing bool, projectPath string) (*mcp.CallToolResult, error) {
	resolvedPath, err := resolvePathWithBoundaryCheck(projectPath, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		if ignoreMissing {
			return mcp.NewToolResultText(fmt.Sprintf("Item not found (ignoreMissing=true): %s", filepath.Base(resolvedPath))), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("item not found: %v", err)), nil
	}

	if info.IsDir() {
		if recursive {
			if err := os.RemoveAll(resolvedPath); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to delete directory recursively: %v", err)), nil
			}
		} else {
			entries, _ := os.ReadDir(resolvedPath)
			if len(entries) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf("directory not empty (use recursive=true): %s", filepath.Base(resolvedPath))), nil
			}
			if err := os.Remove(resolvedPath); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to delete empty directory: %v", err)), nil
			}
		}
	} else {
		if err := os.Remove(resolvedPath); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete file: %v", err)), nil
		}
	}

	autoCommit(projectPath, "delete", resolvedPath)

	return mcp.NewToolResultText(fmt.Sprintf("Deleted: %s", filepath.Base(resolvedPath))), nil
}

func handleCompressItem(pathStr, destArchive string, deleteOriginal bool, projectPath string) (*mcp.CallToolResult, error) {
	resolvedPath, err := resolvePathWithBoundaryCheck(projectPath, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	var archiveDest string
	if destArchive != "" {
		archiveDest, err = resolvePathWithBoundaryCheck(projectPath, destArchive)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("archive path resolution failed: %v", err)), nil
		}
	} else {
		return mcp.NewToolResultError("compressToArchive is required for action=compress"), nil
	}

	resultMsg, err := compressToArchive(resolvedPath, archiveDest, deleteOriginal)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("compression failed: %v", err)), nil
	}

	autoCommit(projectPath, "compress", resolvedPath)

	return mcp.NewToolResultText(resultMsg), nil
}

func handleExtractItem(pathStr, extractSrc string, rootDir string) (*mcp.CallToolResult, error) {
	var archiveFile, entryPath string

	if extractSrc != "" {
		parts := strings.SplitN(extractSrc, "/", 2)
		if len(parts) < 2 {
			return mcp.NewToolResultError("invalid extractFromArchive format: use 'archive.zip/path/to/entry'"), nil
		}
		archiveFile = parts[0]
		entryPath = parts[1]

		resolvedArch, err := resolvePathWithBoundaryCheck(rootDir, archiveFile)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("archive path resolution failed: %v", err)), nil
		}
		archiveFile = resolvedArch
	} else {
		var ok bool
		archiveFile, entryPath, ok = resolveArchivePath(pathStr)
		if !ok {
			return mcp.NewToolResultError("either provide path as 'archive.zip/entry' or use extractFromArchive parameter"), nil
		}
	}

	resolvedDest, err := resolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("destination path resolution failed: %v", err)), nil
	}

	resultMsg, err := extractFromArchive(archiveFile, entryPath, resolvedDest)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("extraction failed: %v", err)), nil
	}

	pctx := GetGlobalProject()
	if pctx != nil && pctx.Path != "" {
		autoCommit(pctx.Path, "extract", resolvedDest)
	}

	return mcp.NewToolResultText(resultMsg), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
