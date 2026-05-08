package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleEditItem processes the EditItem tool request.
func handleEditItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathStr, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'path': %v", err)), nil
	}

	action := extractArgsDefault(req, "action", "edit")

	startLineArg := extractArgsDefault[*int](req, "startLine", nil)
	endLineArg := extractArgsDefault[*int](req, "endLine", nil)
	replacement := extractArgsDefault(req, "replacement", "")

	recursive := extractArgsDefault(req, "recursive", false)
	ignoreMissing := extractArgsDefault(req, "ignoreMissing", false)
	overwrite := extractArgsDefault(req, "overwrite", false)
	content := extractArgsDefault(req, "content", "")
	isFolder := extractArgsDefault(req, "isFolder", false)

	encodingStr := extractArgsDefault(req, "encoding", "utf-8")
	encoding := encodingFromString(encodingStr)

	showProgress := extractArgsDefault(req, "showProgress", false)

	pctx := GetGlobalProjectSnapshot()
	if pctx.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	rootDir := GetGlobalRootDir()

	switch action {
	case "create":
		return handleCreateFile(pathStr, content, content != "", isFolder, overwrite, ignoreMissing, recursive, rootDir, encoding, showProgress)
	default:
		// default to "edit" — handles line-based replacement on existing files.
		// When startLine/endLine are omitted and no content/replacement provided, replace the entire file (not just line 1).
		useReplacement := replacement
		if useReplacement == "" && content != "" {
			useReplacement = content
		}

		startLineVal := 0
		endLineVal := 0

		if startLineArg != nil || endLineArg != nil {
			// Explicit line range provided.
			startLineVal = extractArgsDefault(req, "startLine", 1)
			endLineVal = extractArgsDefault(req, "endLine", 0)
		} else if useReplacement == "" && content == "" {
			// No explicit line range AND no content — replace entire file.
			startLineVal = 1
			endLineVal = -1 // sentinel for "all remaining lines".
		}

		return handleEditFile(pathStr, &startLineVal, &endLineVal, useReplacement, rootDir, encoding, showProgress)
	}
}

// escapeNonPrintable replaces non-printable ASCII characters with their escaped form.
// Standard whitespace (\t, \n, \r) are preserved as-is; other control chars become \uXXXX.
func escapeNonPrintable(s string) string {
	var sb strings.Builder
	for _, c := range s {
		switch c {
		case '\t':
			sb.WriteString("\\t")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		default:
			if (c >= 0x20 && c <= 0x7E) || c > 0xFF {
				sb.WriteRune(c)
			} else {
				sb.WriteString(fmt.Sprintf("\\u%04X", uint16(c)))
			}
		}
	}
	return sb.String()
}

// handleEditFile replaces lines in a file by 1-based line number range.
// Preserves trailing newline; supports encoding and progress indicators.
func handleEditFile(pathStr string, startLine *int, endLine *int, replacement string, rootDir string, encoding TextEncoding, showProgress bool) (*mcp.CallToolResult, error) {
	resolvedPath, err := ResolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	// Get file info
	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to stat file: %v", err)), nil
	}

	// Read file (before edit for diff generation)
	var contentBytes []byte
	if showProgress && fileInfo.Size() >= ProgressThreshold {
		contentBytes, err = readFileWithProgress(resolvedPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
		}
	} else {
		contentBytes, err = os.ReadFile(resolvedPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
		}
	}

	startLineVal := 1 // default to line 1 if not provided
	if startLine != nil {
		startLineVal = *startLine
	}

	lines := strings.Split(string(contentBytes), "\n")

	hasTrailingNewline := len(contentBytes) > 0 && contentBytes[len(contentBytes)-1] == '\n'
	totalLines := len(lines)
	if hasTrailingNewline && totalLines > 0 && lines[totalLines-1] == "" {
		totalLines--
		lines = lines[:totalLines]
	}

	var endLineVal int
	// Handle sentinel value: -1 means "replace all lines to end".
	if endLine != nil && *endLine == -1 {
		endLineVal = totalLines
	} else if endLine != nil && *endLine >= startLineVal {
		endLineVal = *endLine
	} else if endLine == nil {
		endLineVal = startLineVal // no explicit endline — replace exactly one line
	}
	if endLineVal > totalLines {
		endLineVal = totalLines
	}

	var newLines []string
	if startLineVal > 1 {
		newLines = append(newLines, lines[:startLineVal-1]...)
	}
	if replacement != "" {
		newLines = append(newLines, strings.Split(replacement, "\n")...)
	}
	if endLineVal < totalLines {
		newLines = append(newLines, lines[endLineVal:]...)
	}

	newContent := strings.Join(newLines, "\n")
	if hasTrailingNewline {
		newContent += "\n"
	}

	if newContent == string(contentBytes) {
		return mcp.NewToolResultText(fmt.Sprintf("No changes made (content identical) in %s", filepath.Base(resolvedPath))), nil
	}

	// Generate diff for the user
	var replacementLines []string
	if replacement != "" {
		replacementLines = strings.Split(replacement, "\n")
	}

	// Check if only the target lines are identical (substantive vs cosmetic change).
	linesIdentical := true
	if len(lines[startLineVal-1:endLineVal]) == len(replacementLines) && replacement != "" {
		for i := range lines[startLineVal-1 : endLineVal] {
			if lines[startLineVal-1 : endLineVal][i] != replacementLines[i] {
				linesIdentical = false
				break
			}
		}
	}

	fileDiff := generateFileDiff(filepath.Base(resolvedPath), lines[startLineVal-1:endLineVal], replacementLines, startLineVal, endLineVal)

	// Encode content
	writableContent, encodeErr := encodeToBytes(newContent, encoding)
	if encodeErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to encode content: %v", encodeErr)), nil
	}

	// Write file (even if no substantive changes — still writes to normalize line endings).
	if err := writeFileConditionally(resolvedPath, writableContent, showProgress, fileInfo.Size()); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	// Return appropriate message based on whether substantive changes were made.
	message := fmt.Sprintf("Replaced line(s) %d-%d in %s%s", startLineVal, endLineVal, filepath.Base(resolvedPath), fileDiff)
	if linesIdentical {
		// Determine if the replacement is substantive (actually changed content).
		hasMeaningfulReplacement := false

		// Case 1: non-empty text replacing existing content.
		if replacement != "" && len(lines[startLineVal-1:endLineVal]) == len(replacementLines) {
			for _, line := range replacementLines {
				if strings.TrimSpace(line) != "" {
					hasMeaningfulReplacement = true
					break
				}
			}
		}

		// Case 2: empty string replacing existing content (e.g., removing a line).
		if replacement == "" && len(lines[startLineVal-1:endLineVal]) > 0 {
			hasMeaningfulReplacement = true
		}

		if !hasMeaningfulReplacement {
			message = fmt.Sprintf("No substantive changes — replacement text matches existing content (line endings normalized) in %s%s", filepath.Base(resolvedPath), fileDiff)
		}
	}

	return mcp.NewToolResultText(message), nil
}

// buildEditMetadataJSON creates structured JSON metadata for edit operations.
func buildEditMetadataJSON(operation string, affectedPath string, startLine int, endLine int, encodingUsed TextEncoding) string {
	metadata := map[string]interface{}{
		"operation":     operation,
		"affected_path": affectedPath,
	}

	if startLine > 0 && endLine >= startLine {
		metadata["lines_changed"] = map[string]int{
			"start": startLine,
			"end":   endLine,
			"count": endLine - startLine + 1,
		}
	}

	metadata["encoding_used"] = encodingToString(encodingUsed)

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return ""
	}

	return fmt.Sprintf("\n\n=== JSON Metadata ===\n%s", string(data))
}

// handleCreateFile creates a new file or directory.
func handleCreateFile(pathStr string, content string, hasContent bool, isFolder bool, overwrite bool, ignoreMissing bool, recursive bool, rootDir string, encoding TextEncoding, showProgress bool) (*mcp.CallToolResult, error) {
	resolvedPath, err := ResolvePathWithBoundaryCheck(rootDir, pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	if existing, statErr := os.Stat(resolvedPath); statErr == nil {
		if isFolder && existing.IsDir() {
			if ignoreMissing || !overwrite {
				msg := fmt.Sprintf("Item found: %s", filepath.Base(resolvedPath))
				if ignoreMissing {
					msg += " — no changes made (ignoreMissing=true)"
				} else if !overwrite {
					msg += " (folder exists, overwrite=false)"
				}
				return mcp.NewToolResultText(msg), nil
			}
			// overwrite=true: clear directory contents recursively or remove entirely
			if recursive {
				entries, err := os.ReadDir(resolvedPath)
				if err == nil && len(entries) > 0 {
					for _, entry := range entries {
						entryPath := filepath.Join(resolvedPath, entry.Name())
						if entry.IsDir() {
							if rErr := os.RemoveAll(entryPath); rErr != nil {
								return mcp.NewToolResultError(fmt.Sprintf("failed to remove directory content: %v", rErr)), nil
							}
						} else {
							if rErr := os.Remove(entryPath); rErr != nil {
								return mcp.NewToolResultError(fmt.Sprintf("failed to remove file: %v", rErr)), nil
							}
						}
					}
				}
			} else {
				entries, err := os.ReadDir(resolvedPath)
				if err == nil && len(entries) > 0 {
					return mcp.NewToolResultError(fmt.Sprintf("directory is not empty: %s\nSet recursive=true to clear contents.", filepath.Base(resolvedPath))), nil
				}
			}
		} else if !overwrite {
			return mcp.NewToolResultText(fmt.Sprintf("Item found: %s — no changes made", filepath.Base(resolvedPath))), nil
		}
		if existing.IsDir() && overwrite {
			if rErr := os.RemoveAll(resolvedPath); rErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to overwrite directory: %v", rErr)), nil
			}
		}
	}

	if isFolder {
		// Ensure empty directories are persisted for reliable path resolution.
		if content == "" && !hasContent {
		result := fmt.Sprintf("Directory created (empty): %s", resolvedPath)

			// MkdirAll creates parent dirs AND the target directory itself.
			if err := os.MkdirAll(resolvedPath, 0755); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to create empty directory: %v", err)), nil
			}

			metadata := buildEditMetadataJSON("create_directory (empty)", resolvedPath, 0, 0, encoding)
			return mcp.NewToolResultText(result + metadata), nil
		}

		result := fmt.Sprintf("Directory created: %s", resolvedPath)
		if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create directory: %v", err)), nil
		}

		metadata := buildEditMetadataJSON("create_directory", resolvedPath, 0, 0, encoding)
		return mcp.NewToolResultText(result + metadata), nil
	}

	parentDir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create parent directory: %v", err)), nil
	}

	var fileContent []byte
	if hasContent {
		contentLen := len(content)
		if int64(contentLen) > MaxFileSize {
			return mcp.NewToolResultError(fmt.Sprintf("content size (%d bytes) exceeds maximum allowed file size (%d bytes)", contentLen, MaxFileSize)), nil
		}

		// Encode content to the specified encoding
		fileContent, err = encodeToBytes(content, encoding)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to encode content: %v", err)), nil
		}
	} else {
		fileContent = []byte{}
	}

	// Write file with progress for large files
	if err := writeFileConditionally(resolvedPath, fileContent, showProgress, int64(len(fileContent))); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create file: %v", err)), nil
	}

	metadata := buildEditMetadataJSON("create_file", resolvedPath, 0, 0, encoding)
	msg := "File created"
	if _, statErr := os.Stat(resolvedPath); statErr == nil {
		msg = "Overwritten"
	}
	return mcp.NewToolResultText(fmt.Sprintf("%s: %s%s", msg, filepath.Base(resolvedPath), metadata)), nil
}

// encodingToString converts a TextEncoding constant to its string representation.
func encodingToString(enc TextEncoding) string {
	switch enc {
	case EncodingUTF8:
		return "utf-8"
	case EncodingUTF8BOM:
		return "utf-8-bom"
	case EncodingUTF16LE:
		return "utf-16le"
	case EncodingUTF16BE:
		return "utf-16be"
	case EncodingCP1252:
		return "cp1252"
	case EncodingASCII:
		return "ascii"
	default:
		return "utf-8"
	}
}

// readFileWithProgress reads a file and prints progress updates for large files.
func readFileWithProgress(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	totalSize := fileInfo.Size()
	buffer := make([]byte, 0, totalSize)
	chunk := make([]byte, 1024*1024) // 1MB chunks
	lastPercent := 0

	for {
		n, err := file.Read(chunk)
		if n > 0 {
			buffer = append(buffer, chunk[:n]...)

			// Update progress every 5%
			percent := int((int64(len(buffer)) * 100) / totalSize)
			if percent-lastPercent >= 5 {
				fmt.Printf("\r[Progress] Reading file: %d%% (%s / %s)", percent, humanReadableSize(int64(len(buffer))), humanReadableSize(totalSize))
				lastPercent = percent
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read: %w", err)
		}
	}

	fmt.Println() // Newline after progress
	return buffer, nil
}

// writeFileWithProgress writes data to a file and prints progress updates.
func writeFileWithProgress(path string, data []byte) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	totalSize := int64(len(data))
	chunkSize := int64(1024 * 1024) // 1MB chunks
	lastPercent := 0

	for written := int64(0); written < totalSize; {
		toWrite := chunkSize
		if int64(len(data))-written < chunkSize {
			toWrite = int64(len(data)) - written
		}

		endIdx := written + toWrite
		if int(endIdx) > len(data) {
			endIdx = int64(len(data))
		}
		n, err := file.Write(data[written:endIdx])
		if err != nil {
			return fmt.Errorf("failed to write: %w", err)
		}

		written += int64(n)
		percent := int((written * 100) / totalSize)
		if percent-lastPercent >= 5 {
			fmt.Printf("\r[Progress] Writing file: %d%% (%s / %s)", percent, humanReadableSize(written), humanReadableSize(totalSize))
			lastPercent = percent
		}
	}

	fmt.Println() // Newline after progress
	return nil
}

// generateFileDiff creates a unified-style diff showing the changed lines.
func generateFileDiff(filename string, oldLines []string, newLines []string, startLine, endLine int) string {
	if len(oldLines) == 0 && len(newLines) > 0 {
		return fmt.Sprintf("\n\n=== Diff ===\n--- %s (before)\n+++ %s (after)\n@@ -%d,%d +0,%d @@\n%s", filename, filename, startLine-1, endLine-startLine+1, len(newLines), formatAddedLines(newLines))
	}

	var sb strings.Builder
	sb.WriteString("\n\n=== Diff ===\n")
	sb.WriteString(fmt.Sprintf("--- %s (before)\n", filename))
	sb.WriteString(fmt.Sprintf("+++ %s (after)\n", filename))
	sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", startLine, endLine-startLine+1, startLine, len(newLines)))

	if len(oldLines) > 0 {
		for _, line := range oldLines {
			sb.WriteString("- " + line + "\n")
		}
	}
	if len(newLines) > 0 {
		for _, line := range newLines {
			sb.WriteString("+ " + line + "\n")
		}
	}

	sb.WriteString("=== End Diff ===\n")
	return sb.String()
}

// formatAddedLines formats lines as added lines (with + prefix) for context-only diffs.
func formatAddedLines(lines []string) string {
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString("+ " + line + "\n")
	}
	return sb.String()
}

// extractArgsDefault extracts an argument from MCP tool request with a default fallback value.
// When extraction fails (missing or type mismatch), returns defaultVal instead of the zero value.
func extractArgsDefault[T any](req mcp.CallToolRequest, key string, defaultVal T) T {
	result, err := extractArg[T](req, key)
	if err != nil {
		return defaultVal
	}
	return result
}

// FileInfo represents a single indexed file record for JSON serialization and tool results.
type FileInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
}

// DirEntryInfo holds information about a directory entry.
type DirEntryInfo struct {
	Name          string
	Path          string
	IsDir         bool
	Info          os.FileInfo
	RelPath       string
	IsSymlink     bool
	SymlinkTarget string
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

// ProjectContext holds the currently active project state.
type ProjectContext struct {
	RootDir string `json:"root_dir"`
	Path    string `json:"path"`
}

// globalProjectMu guards GlobalProject.
var globalProjectMu sync.RWMutex

// GlobalProject is the singleton project context (guarded by globalProjectMu).
var GlobalProject *ProjectContext = nil

// SetGlobalProject safely sets the global project context.
func SetGlobalProject(ctx *ProjectContext) {
	globalProjectMu.Lock()
	defer globalProjectMu.Unlock()
	GlobalProject = ctx
}

// CloseProject resets the global project context.
func CloseProject() {
	globalProjectMu.Lock()
	defer globalProjectMu.Unlock()
	GlobalProject = nil
}

// GetGlobalProject returns the current project context (thread-safe).
func GetGlobalProject() *ProjectContext {
	globalProjectMu.RLock()
	defer globalProjectMu.RUnlock()
	return GlobalProject
}

// ProjectContextSnapshot is a thread-safe copy of the project context.
type ProjectContextSnapshot struct {
	RootDir string
	Path    string
}

// GetGlobalProjectSnapshot returns a thread-safe snapshot of the current project context.
func GetGlobalProjectSnapshot() ProjectContextSnapshot {
	globalProjectMu.RLock()
	defer globalProjectMu.RUnlock()
	if GlobalProject == nil {
		return ProjectContextSnapshot{}
	}
	snap := ProjectContextSnapshot{
		RootDir: GlobalProject.RootDir,
		Path:    GlobalProject.Path,
	}
	return snap
}

// globalRootDir is the root directory used for all project operations, set once at startup.
var globalRootDir string

// globalRootDirMu guards globalRootDir.
var globalRootDirMu sync.RWMutex

func SetGlobalRootDir(rootDir string) {
	globalRootDirMu.Lock()
	defer globalRootDirMu.Unlock()
	globalRootDir = rootDir
}

// GetGlobalRootDir returns the global root directory (thread-safe).
// This is independent of whether a project is currently open.
func GetGlobalRootDir() string {
	globalRootDirMu.RLock()
	defer globalRootDirMu.RUnlock()
	return globalRootDir
}

// IsWithinProject checks if a resolved absolute path is within the current project root.
// This is a security check to prevent file operations outside the active project.
// Handles Windows drive-letter case sensitivity (e.g., "C:\" vs "c:\").
// Thread-safe: uses GetGlobalProjectSnapshot() to avoid mutex violations.
func IsWithinProject(path string) bool {
	snap := GetGlobalProjectSnapshot()
	if snap.Path == "" {
		return true
	}

	cleanPath := filepath.Clean(path)
	cleanProject := filepath.Clean(snap.Path)

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

// ResolveRootPath resolves a path relative to the root directory (used during OpenProject).
// If path is absolute, returns it as-is (cleaned). If relative, joins with rootDir.
func ResolveRootPath(rootDir, path string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	return filepath.Clean(path), nil
}

// ResolvePath resolves a path relative to the current project root.
// If a project is open, resolves relative to it. Otherwise, resolves relative to rootDir.
// If path is already absolute, returns it cleaned.
// Thread-safe: uses GetGlobalProjectSnapshot() to avoid mutex violations.
func ResolvePath(rootDir, path string) (string, error) {
	snap := GetGlobalProjectSnapshot()
	if snap.Path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(snap.Path, path)
		}
		return filepath.Clean(path), nil
	}
	return ResolveRootPath(rootDir, path)
}

// ResolvePathWithBoundaryCheck resolves a path and validates it's within the project.
// Returns an error if the resolved path is outside the current project root.
// This is the primary safety check used by all file-based tools.
// Thread-safe: uses GetGlobalProjectSnapshot() to avoid mutex violations.
func ResolvePathWithBoundaryCheck(rootDir, path string) (string, error) {
	resolved, err := ResolvePath(rootDir, path)
	if err != nil {
		return "", err
	}
	snap := GetGlobalProjectSnapshot()
	if snap.Path == "" {
		return resolved, nil
	}
	if !IsWithinProject(resolved) {
		return "", fmt.Errorf("path '%s' is outside the open project '%s'", path, snap.Path)
	}
	return resolved, nil
}

// FileOperationHint provides hints for large file operations.
type FileOperationHint struct {
	Operation      string `json:"operation"`
	AffectedPath   string `json:"affected_path"`
	FileSize       int64  `json:"file_size,omitempty"`
	ThresholdHint  string `json:"threshold_hint,omitempty"`
	SuggestChunked bool   `json:"suggest_chunked,omitempty"`
}

// ToolResponse is a standardized response format for all tool operations.
type ToolResponse struct {
	Operation    string                 `json:"operation"`
	AffectedPath string                 `json:"affected_path,omitempty"`
	Hint         *FileOperationHint     `json:"hint,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// BuildStandardResponse creates a standardized JSON response string.
func BuildStandardResponse(operation, affectedPath string, hint *FileOperationHint, metadata map[string]interface{}) string {
	resp := ToolResponse{
		Operation:    operation,
		AffectedPath: affectedPath,
		Hint:         hint,
		Metadata:     metadata,
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Sprintf("operation=%s", operation)
	}

	return string(data)
}

// writeFileConditionally writes data to a file with progress indicators if showProgress is true and the file size exceeds ProgressThreshold.
func writeFileConditionally(path string, data []byte, showProgress bool, originalSize int64) error {
	// Use progress write for large files when enabled
	if showProgress && originalSize >= ProgressThreshold {
		return writeFileWithProgress(path, data)
	}
	return os.WriteFile(path, data, 0644)
}

// BuildFileHint creates a FileOperationHint for large file operations.
func BuildFileHint(operation, affectedPath string, fileSize int64) *FileOperationHint {
	hint := &FileOperationHint{
		Operation:    operation,
		AffectedPath: affectedPath,
		FileSize:     fileSize,
	}

	// Suggest chunked reading for large files.
	if fileSize >= LargeFileThreshold {
		hint.SuggestChunked = true
		hint.ThresholdHint = fmt.Sprintf("File size (%d bytes) exceeds 5MB threshold. Consider using offset/length parameters to read in chunks.", fileSize)
	}

	return hint
}

func execTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd.Stdout = nil
	cmd.Stderr = nil

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		ch <- result{out: out, err: err}
	}()

	select {
	case r := <-ch:
		return r.out, r.err == nil
	case <-ctx.Done():
		cmd.Process.Kill() // nolint:errcheck
		return nil, true   // timeout is treated as success (no error)
	}
}

// MEMORYSTATUSEX matches the Windows API struct (56 bytes: 2×uint32 + 6×uint64).
type MEMORYSTATUSEX struct {
	DwLength         uint32
	DwMemoryLoad     uint32
	UllTotalPhys     uint64
	UllAvailPhys     uint64
	UllTotalPageFile uint64
	UllAvailPageFile uint64
	UllTotalVirtual  uint64
	UllAvailVirtual  uint64
}
