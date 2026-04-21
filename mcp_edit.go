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

	oldText, hasOldText := extractOptionalString(req, "oldText")
	newText, hasNewText := extractOptionalString(req, "newText")
	countArg, _ := extractOptionalInt(req, "count")

	editFormat := "text"
	if v, ok := extractOptionalString(req, "format"); ok {
		editFormat = v
	}
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
		return handleEditFile(pathStr, oldText, hasOldText, newText, hasNewText, countArg, editFormat, rootDir)
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

func handleEditFile(pathStr, oldText string, hasOldText bool, newText string, hasNewText bool, countArg int, editFormat string, rootDir string) (*mcp.CallToolResult, error) {
	resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	contentBytes, err := os.ReadFile(resolvedPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	// Handle hex format for binary manipulation
	if editFormat == "hex" {
		return handleEditHex(resolvedPath, oldText, hasOldText, newText, hasNewText, countArg, contentBytes)
	}

	// Text mode (original behavior)
	matchCount := strings.Count(string(contentBytes), oldText)
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

	newContent := replaceN(string(contentBytes), oldText, newText, replacements)
	replacedCount := matchCount - strings.Count(newContent, oldText)

	if newContent == string(contentBytes) {
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

// handleEditHex handles hex-encoded binary edit operations.
func handleEditHex(filePath string, oldText string, hasOldText bool, newText string, hasNewText bool, countArg int, contentBytes []byte) (*mcp.CallToolResult, error) {
	var oldData, newData []byte
	var err error

	// Decode oldText from hex if provided
	if hasOldText && oldText != "" {
		oldData, err = fromHexDump(oldText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to decode oldText as hex: %v", err)), nil
		}
		if len(oldData) == 0 {
			return mcp.NewToolResultError("oldText decoded to empty byte sequence"), nil
		}
	}

	// Decode newText from hex if provided
	if hasNewText && newText != "" {
		newData, err = fromHexDump(newText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to decode newText as hex: %v", err)), nil
		}
	}

	// Find occurrences of oldData in content
	var positions []int
	if hasOldText && oldText != "" {
		positions = binaryFindBytes(contentBytes, oldData)
		if len(positions) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No occurrences of hex pattern (%d bytes) found in %s", len(oldData), filepath.Base(filePath))), nil
		}

		// Determine how many replacements to make
		replacements := countArg
		if replacements == 0 || replacements > len(positions) {
			replacements = len(positions)
		}

		// Perform the replacement
		newContent, replacedCount := binaryReplaceN(contentBytes, oldData, newData, replacements)

		if newContent == nil {
			newContent = contentBytes
		}

		if string(newContent) == string(contentBytes) {
			return mcp.NewToolResultText(fmt.Sprintf("No changes made (content identical) in %s", filepath.Base(filePath))), nil
		}

		if err := os.WriteFile(filePath, newContent, 0644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
		}

		pctx := GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "edit", filePath)
		}

		resultMsg := fmt.Sprintf("Replaced %d occurrence(s) of hex pattern in %s\n", replacedCount, filepath.Base(filePath))
		resultMsg += fmt.Sprintf("Pattern size: %d bytes | Replacement size: %d bytes\n", len(oldData), len(newData))

		// Show a preview of the hex dump around first match if oldText was provided
		if hasOldText && len(positions) > 0 {
			resultMsg += fmt.Sprintf("\nHex dump (first 64 bytes):\n")
			end := 64
			if end > len(newContent) {
				end = len(newContent)
			}
			resultMsg += toHexDump(newContent[:end])
		}

		return mcp.NewToolResultText(resultMsg), nil
	}

	// If only newText is provided (no oldText), append newData at the end of file
	if hasNewText && !hasOldText {
		newContent := append(contentBytes, newData...)
		if err := os.WriteFile(filePath, newContent, 0644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
		}

		pctx := GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "edit", filePath)
		}

		return mcp.NewToolResultText(fmt.Sprintf("Appended %d bytes to %s\nSize: %d → %d bytes", len(newData), filepath.Base(filePath), len(contentBytes), len(newContent))), nil
	}

	// No oldText or newText provided
	if !hasOldText && !hasNewText {
		return mcp.NewToolResultError("provide at least one of oldText or newText for hex edit"), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Hex edit complete in %s", filepath.Base(filePath))), nil
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

	// Sanitize the entry path to prevent directory traversal escapes
	sanitizedEntry, sanitizeErr := sanitizeArchiveEntryPath(entryPath)
	if sanitizeErr != nil {
		return mcp.NewToolResultText(fmt.Sprintf("Entry path rejected (sandbox escape): %s - %v", entryPath, sanitizeErr)), nil
	}

	resultMsg, err := extractFromArchive(archiveFile, sanitizedEntry, resolvedDest)
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
