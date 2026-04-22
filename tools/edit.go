package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"project-management/core"
	"project-management/pkg"

	"github.com/mark3labs/mcp-go/mcp"
)

func handleEditItem(_ context.Context, req mcp.CallToolRequest, _ *pkg.FileStore, rootDir string) (*mcp.CallToolResult, error) {
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

	pctx := core.GetGlobalProject()
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
	case "copy", "move":
		destPath, _ := extractOptionalString(req, "destination")
		overwrite, _ := extractOptionalBool(req, "overwrite")
		if action == "copy" {
			return handleCopyInEdit(pathStr, pctx.Path, overwrite, destPath)
		}
		return handleMoveInEdit(pathStr, pctx.Path, overwrite, destPath)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (valid: edit, delete, compress, extract, copy, move)", action)), nil
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

	if editFormat == "hex" {
		return handleEditHex(resolvedPath, oldText, hasOldText, newText, hasNewText, countArg, contentBytes)
	}

	if oldText == "" {
		return mcp.NewToolResultError("oldText cannot be empty for text edit"), nil
	}
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

	pctx := core.GetGlobalProject()
	if pctx != nil && pctx.Path != "" {
		autoCommit(pctx.Path, "edit", resolvedPath)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Replaced %d occurrence(s) in %s", replacedCount, filepath.Base(resolvedPath))), nil
}

func handleEditHex(filePath string, oldText string, hasOldText bool, newText string, hasNewText bool, countArg int, contentBytes []byte) (*mcp.CallToolResult, error) {
	var oldData, newData []byte
	var err error

	if hasOldText && oldText != "" {
		oldData, err = fromHexDump(oldText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to decode oldText as hex: %v", err)), nil
		}
		if len(oldData) == 0 {
			return mcp.NewToolResultError("oldText decoded to empty byte sequence"), nil
		}
	}

	if hasNewText && newText != "" {
		newData, err = fromHexDump(newText)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to decode newText as hex: %v", err)), nil
		}
	}

	var positions []int
	if hasOldText && oldText != "" {
		positions = binaryFindBytes(contentBytes, oldData)
		if len(positions) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No occurrences of hex pattern (%d bytes) found in %s", len(oldData), filepath.Base(filePath))), nil
		}

		replacements := countArg
		if replacements == 0 || replacements > len(positions) {
			replacements = len(positions)
		}

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

		pctx := core.GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "edit", filePath)
		}

		resultMsg := fmt.Sprintf("Replaced %d occurrence(s) of hex pattern in %s\n", replacedCount, filepath.Base(filePath))
		resultMsg += fmt.Sprintf("Pattern size: %d bytes | Replacement size: %d bytes\n", len(oldData), len(newData))

		if hasOldText && len(positions) > 0 {
			resultMsg += "\nHex dump (first 64 bytes):\n"
			end := 64
			if end > len(newContent) {
				end = len(newContent)
			}
			resultMsg += toHexDump(newContent[:end])
		}

		return mcp.NewToolResultText(resultMsg), nil
	}

	if hasNewText && !hasOldText {
		newContent := append(contentBytes, newData...)
		if err := os.WriteFile(filePath, newContent, 0644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
		}

		pctx := core.GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "edit", filePath)
		}

		return mcp.NewToolResultText(fmt.Sprintf("Appended %d bytes to %s\nSize: %d → %d bytes", len(newData), filepath.Base(filePath), len(contentBytes), len(newContent))), nil
	}

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

	sanitizedEntry, sanitizeErr := sanitizeArchiveEntryPath(entryPath)
	if sanitizeErr != nil {
		return mcp.NewToolResultText(fmt.Sprintf("Entry path rejected (sandbox escape): %s - %v", entryPath, sanitizeErr)), nil
	}

	resultMsg, err := extractFromArchive(archiveFile, sanitizedEntry, resolvedDest)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("extraction failed: %v", err)), nil
	}

	pctx := core.GetGlobalProject()
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

func handleCopyInEdit(pathStr string, projectPath string, overwrite bool, destPath string) (*mcp.CallToolResult, error) {
	resolvedSource, err := resolvePathWithBoundaryCheck(projectPath, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("source path resolution failed: %v", err)), nil
	}

	if destPath == "" {
		return mcp.NewToolResultError("destination is required for action=copy (use batch mode with destination field)"), nil
	}

	resolvedDest, err := resolvePathWithBoundaryCheck(projectPath, destPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("destination path resolution failed: %v", err)), nil
	}

	srcInfo, err := os.Stat(resolvedSource)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(fmt.Sprintf("Source not found: %s", resolvedSource)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
	}

	if _, err := os.Stat(resolvedDest); err == nil && !overwrite {
		return mcp.NewToolResultText(fmt.Sprintf("Destination already exists: %s\nSet overwrite=true to replace.", resolvedDest)), nil
	}

	var bytesCopied int64
	if srcInfo.IsDir() {
		bytesCopied, err = copyDirectoryRecursive(resolvedSource, resolvedDest)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("copy directory failed: %v", err)), nil
		}
	} else {
		srcFile, openErr := os.Open(resolvedSource)
		if openErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to open source: %v", openErr)), nil
		}
		defer srcFile.Close()

		destDir := filepath.Dir(resolvedDest)
		os.MkdirAll(destDir, 0755)

		destFile, createErr := os.Create(resolvedDest)
		if createErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create destination: %v", createErr)), nil
		}

		bytesCopied, err = io.Copy(destFile, srcFile)
		destFile.Close()
		if err != nil {
			os.Remove(resolvedDest)
			return mcp.NewToolResultError(fmt.Sprintf("copy failed: %v", err)), nil
		}
	}

	autoCommit(projectPath, "copy", resolvedSource)

	return mcp.NewToolResultText(fmt.Sprintf("Copied: %s -> %s\nSize: %s (%d bytes)\nAction: copy", resolvedSource, resolvedDest, humanReadableSize(bytesCopied), bytesCopied)), nil
}

func handleMoveInEdit(pathStr string, projectPath string, overwrite bool, destPath string) (*mcp.CallToolResult, error) {
	resolvedSource, err := resolvePathWithBoundaryCheck(projectPath, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("source path resolution failed: %v", err)), nil
	}

	if destPath == "" {
		return mcp.NewToolResultError("destination is required for action=move (use batch mode with destination field)"), nil
	}

	resolvedDest, err := resolvePathWithBoundaryCheck(projectPath, destPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("destination path resolution failed: %v", err)), nil
	}

	srcInfo, err := os.Stat(resolvedSource)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(fmt.Sprintf("Source not found: %s", resolvedSource)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
	}

	srcSize := srcInfo.Size()
	srcModTime := srcInfo.ModTime().UTC().Format(time.RFC3339)
	srcMD5, _ := pkg.ComputeMD5(resolvedSource)

	if _, err := os.Stat(resolvedDest); err == nil && !overwrite {
		return mcp.NewToolResultText(fmt.Sprintf("Destination exists: %s\nSet overwrite=true to replace.", resolvedDest)), nil
	}

	isRename := filepath.Dir(filepath.Clean(resolvedSource)) == filepath.Dir(filepath.Clean(resolvedDest))
	moveType := "Moved"
	if isRename {
		moveType = "Renamed"
	}

	err = os.Rename(resolvedSource, resolvedDest)
	if err != nil {
		if srcInfo.IsDir() {
			_, copyErr := copyDirectoryRecursive(resolvedSource, resolvedDest)
			if copyErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to fallback-copy directory: %v", copyErr)), nil
			}
			os.RemoveAll(resolvedSource)
		} else {
			srcFile, openErr := os.Open(resolvedSource)
			if openErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to move (rename error: %v, fallback open error: %v)", err, openErr)), nil
			}
			defer srcFile.Close()

			destDir := filepath.Dir(resolvedDest)
			os.MkdirAll(destDir, 0755)

			destFile, createErr := os.Create(resolvedDest)
			if createErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to move (rename error: %v, create error: %v)", err, createErr)), nil
			}

			io.Copy(destFile, srcFile)
			destFile.Close()
			os.Remove(resolvedSource)
		}
	}

	autoCommit(projectPath, "move", resolvedSource)

	return mcp.NewToolResultText(fmt.Sprintf("%s: %s -> %s\nType: %s | Size: %s (%d bytes)\nOriginal modified: %s | MD5: %s", moveType, resolvedSource, resolvedDest, moveType, humanReadableSize(srcSize), srcSize, srcModTime, srcMD5)), nil
}
