package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== Batch Operation Results ====================

type batchResult struct {
	Batch      bool                     `json:"batch"`
	Total      int                      `json:"total"`
	Successful int                      `json:"successful"`
	Failed     int                      `json:"failed"`
	Results    []map[string]interface{} `json:"results"`
}

func newBatchResult(total int) *batchResult {
	return &batchResult{
		Batch:   true,
		Total:   total,
		Results: make([]map[string]interface{}, 0, total),
	}
}

func (br *batchResult) addSuccess(result map[string]interface{}) {
	br.Successful++
	result["status"] = "success"
	br.Results = append(br.Results, result)
}

func (br *batchResult) addError(index int, message string) {
	br.Failed++
	br.Results = append(br.Results, map[string]interface{}{
		"index":   index,
		"status":  "error",
		"message": message,
	})
}

func (br *batchResult) String() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Batch operation complete: %d total, %d successful, %d failed\n\n", br.Total, br.Successful, br.Failed))
	for _, result := range br.Results {
		status, _ := result["status"].(string)
		if status == "error" {
			msg, _ := result["message"].(string)
			path, _ := result["path"].(string)
			sb.WriteString(fmt.Sprintf("  FAILED: %s - %s\n", path, msg))
		} else {
			action, _ := result["action"].(string)
			path, _ := result["path"].(string)
			sb.WriteString(fmt.Sprintf("  OK: %s - %s\n", action, filepath.Base(path)))
		}
	}
	return sb.String()
}

// ==================== Batch Create Handler ====================

func handleBatchCreate(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	items, ok := extractOptionalBatchItems(req, "items")
	if !ok || len(items) == 0 {
		return nil, false // Not a batch operation
	}

	br := newBatchResult(len(items))

	for i, item := range items {
		resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, item.Path)
		if err != nil {
			br.addError(i, fmt.Sprintf("path resolution failed: %v", err))
			continue
		}

		if item.IsFolder {
			exists := dirExists(resolvedPath)
			if exists {
				if !item.Overwrite {
					br.addError(i, fmt.Sprintf("folder already exists: %s", resolvedPath))
					continue
				}
			} else if err := os.MkdirAll(resolvedPath, 0755); err != nil {
				br.addError(i, fmt.Sprintf("failed to create folder: %v", err))
				continue
			}

			pctx := GetGlobalProject()
			if pctx != nil && pctx.Path != "" {
				autoCommit(pctx.Path, "create", resolvedPath)
			}

			br.addSuccess(map[string]interface{}{
				"index":  i,
				"path":   resolvedPath,
				"action": "Created folder",
			})
		} else {
			parentDir := filepath.Dir(resolvedPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				br.addError(i, fmt.Sprintf("failed to create parent directories: %v", err))
				continue
			}

			exists := fileExists(resolvedPath)
			if exists && !item.Overwrite {
				br.addError(i, fmt.Sprintf("file already exists (overwrite=false): %s", resolvedPath))
				continue
			}
			if exists {
				os.Remove(resolvedPath)
			}

			if err := os.WriteFile(resolvedPath, []byte(item.Content), 0644); err != nil {
				br.addError(i, fmt.Sprintf("failed to create file: %v", err))
				continue
			}

			pctx := GetGlobalProject()
			if pctx != nil && pctx.Path != "" {
				autoCommit(pctx.Path, "create", resolvedPath)
			}

			br.addSuccess(map[string]interface{}{
				"index":  i,
				"path":   resolvedPath,
				"action": "Created file",
			})
		}
	}

	return mcp.NewToolResultText(br.String()), true
}

// ==================== Batch Edit Handler ====================

func handleBatchEdit(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	edits, ok := extractOptionalBatchEdits(req, "edits")
	if !ok || len(edits) == 0 {
		return nil, false // Not a batch operation
	}

	br := newBatchResult(len(edits))

	for i, edit := range edits {
		resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, edit.Path)
		if err != nil {
			br.addError(i, fmt.Sprintf("path resolution failed: %v", err))
			continue
		}

		action := edit.Action
		if action == "" {
			action = "edit"
		}

		switch action {
		case "edit":
			contentBytes, readErr := os.ReadFile(resolvedPath)
			if readErr != nil {
				br.addError(i, fmt.Sprintf("failed to read file: %v", readErr))
				continue
			}
			oldText := edit.OldText
			newText := edit.NewText
			countArg := edit.Count
			editFormat := edit.Format
			if editFormat == "" {
				editFormat = "text"
			}

			if editFormat == "hex" {
				oldData, _ := fromHexDump(oldText)
				newData, _ := fromHexDump(newText)
				if len(oldData) > 0 {
					positions := binaryFindBytes(contentBytes, oldData)
					if len(positions) == 0 {
						br.addError(i, "no occurrences found")
						continue
					}
					repl := countArg
					if repl == 0 || repl > len(positions) {
						repl = len(positions)
					}
					newContent, replacedCount := binaryReplaceN(contentBytes, oldData, newData, repl)
					os.WriteFile(resolvedPath, newContent, 0644)
					pctx := GetGlobalProject()
					if pctx != nil && pctx.Path != "" {
						autoCommit(pctx.Path, "edit", resolvedPath)
					}
					br.addSuccess(map[string]interface{}{
						"index":        i,
						"path":         resolvedPath,
						"action":       "Edited (hex)",
						"replacements": replacedCount,
					})
				} else {
					matchCount := strings.Count(string(contentBytes), oldText)
					if matchCount == 0 {
						br.addError(i, "no occurrences found")
						continue
					}
					repl := countArg
					if repl == 0 || repl > matchCount {
						repl = matchCount
					}
					newContent := replaceN(string(contentBytes), oldText, newText, repl)
					os.WriteFile(resolvedPath, []byte(newContent), 0644)
					pctx := GetGlobalProject()
					if pctx != nil && pctx.Path != "" {
						autoCommit(pctx.Path, "edit", resolvedPath)
					}
					br.addSuccess(map[string]interface{}{
						"index":        i,
						"path":         resolvedPath,
						"action":       "Edited (text)",
						"replacements": repl,
					})
				}
			} else {
				matchCount := strings.Count(string(contentBytes), oldText)
				if matchCount == 0 {
					br.addError(i, "no occurrences found")
					continue
				}
				repl := countArg
				if repl == 0 || repl > matchCount {
					repl = matchCount
				}
				newContent := replaceN(string(contentBytes), oldText, newText, repl)
				os.WriteFile(resolvedPath, []byte(newContent), 0644)
				pctx := GetGlobalProject()
				if pctx != nil && pctx.Path != "" {
					autoCommit(pctx.Path, "edit", resolvedPath)
				}
				br.addSuccess(map[string]interface{}{
					"index":        i,
					"path":         resolvedPath,
					"action":       "Edited (text)",
					"replacements": repl,
				})
			}

		case "delete":
			if err := os.Remove(resolvedPath); err != nil {
				if edit.IgnoreMissing && os.IsNotExist(err) {
					br.addSuccess(map[string]interface{}{
						"index":  i,
						"path":   resolvedPath,
						"action": "Deleted (or already missing)",
					})
				} else {
					br.addError(i, fmt.Sprintf("delete failed: %v", err))
				}
			} else {
				pctx := GetGlobalProject()
				if pctx != nil && pctx.Path != "" {
					autoCommit(pctx.Path, "delete", resolvedPath)
				}
				br.addSuccess(map[string]interface{}{
					"index":  i,
					"path":   resolvedPath,
					"action": "Deleted",
				})
			}

		case "compress":
			compressResultMsg, err := compressToArchive(resolvedPath, edit.CompressToArchive, edit.DeleteOriginalAfterCompress)
			if err != nil {
				br.addError(i, fmt.Sprintf("compress failed: %v", err))
				continue
			}
			pctx := GetGlobalProject()
			if pctx != nil && pctx.Path != "" {
				autoCommit(pctx.Path, "compress", resolvedPath)
			}
			br.addSuccess(map[string]interface{}{
				"index":   i,
				"path":    resolvedPath,
				"action":  "Compressed",
				"message": compressResultMsg,
			})

		case "extract":
			entryName := edit.ExtractEntryName
			if entryName == "" {
				parts := strings.SplitN(edit.ExtractFromArchive, "/", 2)
				if len(parts) >= 2 {
					entryName = parts[1]
				} else {
					entryName = edit.ExtractFromArchive
				}
			}
			extractResultMsg, extractErr := extractFromArchive(edit.ExtractFromArchive, entryName, resolvedPath)
			if extractErr != nil {
				br.addError(i, fmt.Sprintf("extract failed: %v", extractErr))
				continue
			}
			pctx := GetGlobalProject()
			if pctx != nil && pctx.Path != "" {
				autoCommit(pctx.Path, "extract", resolvedPath)
			}
			br.addSuccess(map[string]interface{}{
				"index":   i,
				"path":    resolvedPath,
				"message": extractResultMsg,
				"action":  "Extracted",
			})

		default:
			br.addError(i, fmt.Sprintf("unknown edit action: %s", action))
		}
	}

	return mcp.NewToolResultText(br.String()), true
}

// ==================== Batch Copy Handler ====================

func handleBatchCopy(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	copies, ok := extractOptionalBatchCopies(req, "copies")
	if !ok || len(copies) == 0 {
		return nil, false // Not a batch operation
	}

	br := newBatchResult(len(copies))

	for i, copy := range copies {
		srcResolved, err := resolvePathWithBoundaryCheck(rootDir, copy.Source)
		if err != nil {
			br.addError(i, fmt.Sprintf("source path resolution failed: %v", err))
			continue
		}

		dstResolved, err := resolvePathWithBoundaryCheck(rootDir, copy.Destination)
		if err != nil {
			br.addError(i, fmt.Sprintf("destination path resolution failed: %v", err))
			continue
		}

		srcInfo, err := os.Stat(srcResolved)
		if err != nil {
			br.addError(i, fmt.Sprintf("source not found: %s", copy.Source))
			continue
		}

		var bytesCopied int64
		if srcInfo.IsDir() {
			copied, err := copyDirectoryRecursive(srcResolved, dstResolved)
			if err != nil {
				br.addError(i, fmt.Sprintf("copy directory failed: %v", err))
				continue
			}
			bytesCopied = copied
		} else {
			srcFile, err := os.Open(srcResolved)
			if err != nil {
				br.addError(i, fmt.Sprintf("failed to open source: %v", err))
				continue
			}

			dstDir := filepath.Dir(dstResolved)
			os.MkdirAll(dstDir, 0755)

			destFile, err := os.Create(dstResolved)
			if err != nil {
				srcFile.Close()
				br.addError(i, fmt.Sprintf("failed to create destination: %v", err))
				continue
			}

			bytesCopied, err = io.Copy(destFile, srcFile)
			srcFile.Close()
			destFile.Close()
			if err != nil {
				os.Remove(dstResolved)
				br.addError(i, fmt.Sprintf("copy failed: %v", err))
				continue
			}
		}

		pctx := GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "copy", srcResolved)
		}

		br.addSuccess(map[string]interface{}{
			"index":  i,
			"path":   dstResolved,
			"action": "Copied",
			"bytes":  bytesCopied,
		})
	}

	return mcp.NewToolResultText(br.String()), true
}

// ==================== Batch Move Handler ====================

func handleBatchMove(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	moves, ok := extractOptionalBatchMoves(req, "moves")
	if !ok || len(moves) == 0 {
		return nil, false // Not a batch operation
	}

	br := newBatchResult(len(moves))

	for i, move := range moves {
		srcResolved, err := resolvePathWithBoundaryCheck(rootDir, move.Source)
		if err != nil {
			br.addError(i, fmt.Sprintf("source path resolution failed: %v", err))
			continue
		}

		dstResolved, err := resolvePathWithBoundaryCheck(rootDir, move.Destination)
		if err != nil {
			br.addError(i, fmt.Sprintf("destination path resolution failed: %v", err))
			continue
		}

		srcInfo, err := os.Stat(srcResolved)
		if err != nil {
			br.addError(i, fmt.Sprintf("source not found: %s", move.Source))
			continue
		}

		var movedSize int64
		if srcInfo.IsDir() {
			_, copyErr := copyDirectoryRecursive(srcResolved, dstResolved)
			if copyErr != nil {
				br.addError(i, fmt.Sprintf("failed to fallback-copy directory: %v", copyErr))
				continue
			}
			os.RemoveAll(srcResolved)
			movedSize = srcInfo.Size()
		} else {
			err = os.Rename(srcResolved, dstResolved)
			if err != nil {
				srcFile, openErr := os.Open(srcResolved)
				if openErr != nil {
					br.addError(i, fmt.Sprintf("failed to move: %v", err))
					continue
				}
				defer srcFile.Close()

				destDir := filepath.Dir(dstResolved)
				os.MkdirAll(destDir, 0755)

				destFile, createErr := os.Create(dstResolved)
				if createErr != nil {
					br.addError(i, fmt.Sprintf("failed to move: %v", err))
					continue
				}

				io.Copy(destFile, srcFile)
				destFile.Close()
				os.Remove(srcResolved)
				movedSize = srcInfo.Size()
			} else {
				movedSize = srcInfo.Size()
			}
		}

		pctx := GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			autoCommit(pctx.Path, "move", srcResolved)
		}

		br.addSuccess(map[string]interface{}{
			"index":  i,
			"path":   dstResolved,
			"action": "Moved",
			"bytes":  movedSize,
		})
	}

	return mcp.NewToolResultText(br.String()), true
}

// ==================== Batch Get Handler ====================

func handleBatchGet(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	paths, ok := extractOptionalStringArray(req, "paths")
	if !ok || len(paths) == 0 {
		return nil, false // Not a batch operation
	}

	action, _ := extractOptionalString(req, "action")
	if action == "" {
		action = "auto"
	}

	br := newBatchResult(len(paths))

	for i, path := range paths {
		resolvedPath, err := resolvePathWithBoundaryCheck(rootDir, path)
		if err != nil {
			br.addError(i, fmt.Sprintf("path resolution failed: %v", err))
			continue
		}

		info, statErr := os.Stat(resolvedPath)
		if statErr != nil {
			br.addError(i, fmt.Sprintf("stat failed: %v", statErr))
			continue
		}

		var resultData string
		var detectedAction string

		switch action {
		case "auto":
			if info.IsDir() {
				detectedAction = "list"
			} else {
				detectedAction = "read"
			}
		default:
			detectedAction = action
		}

		switch detectedAction {
		case "read":
			content, readErr := os.ReadFile(resolvedPath)
			if readErr != nil {
				br.addError(i, fmt.Sprintf("read failed: %v", readErr))
				continue
			}
			resultData = string(content)

		case "list":
			entries, listErr := os.ReadDir(resolvedPath)
			if listErr != nil {
				br.addError(i, fmt.Sprintf("list failed: %v", listErr))
				continue
			}
			var sb strings.Builder
			for _, entry := range entries {
				sb.WriteString(fmt.Sprintf("%s\n", entry.Name()))
			}
			resultData = sb.String()

		case "info":
			resultData = fmt.Sprintf("Name: %s | Size: %d | Type: %s", info.Name(), info.Size(), map[bool]string{true: "dir", false: "file"}[!info.IsDir()])

		default:
			br.addError(i, fmt.Sprintf("unknown action: %s", detectedAction))
			continue
		}

		br.addSuccess(map[string]interface{}{
			"index":  i,
			"path":   resolvedPath,
			"action": detectedAction,
			"data":   resultData,
		})
	}

	return mcp.NewToolResultText(br.String()), true
}

// ==================== Multi-Root Search Handler ====================

func handleMultiSearch(req mcp.CallToolRequest, rootDir string) (*mcp.CallToolResult, bool) {
	paths, ok := extractOptionalStringArray(req, "paths")
	if !ok || len(paths) == 0 {
		return nil, false // Not a multi-search operation
	}

	pattern, err := extractArg[string](req, "pattern")
	if err != nil {
		return mcp.NewToolResultError("missing required argument 'pattern'"), true
	}

	mode, _ := extractOptionalString(req, "mode")
	if mode == "" {
		mode = "name"
	}

	limit := 50
	if l, ok := extractOptionalInt(req, "limit"); ok && l > 0 {
		limit = l
	}

	includeHidden, _ := extractOptionalBool(req, "includeHidden")
	fileOnly, _ := extractOptionalBool(req, "fileOnly")
	dirOnly, _ := extractOptionalBool(req, "dirOnly")
	extensionsStr, _ := extractOptionalString(req, "extensions")
	contextLines := 0
	if cl, ok := extractOptionalInt(req, "contextLines"); ok && cl >= 0 {
		contextLines = cl
	}

	var extensions []string
	if extensionsStr != "" {
		for _, ext := range strings.Split(extensionsStr, ",") {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				extensions = append(extensions, ext)
			}
		}
	}

	var allResults []map[string]interface{}
	totalLimit := limit * len(paths) // Scale limit per root

	for _, searchRoot := range paths {
		resolvedRoot, err := resolvePathWithBoundaryCheck(rootDir, searchRoot)
		if err != nil {
			continue // Skip invalid roots
		}

		searchResults, err := doSearch(resolvedRoot, pattern, mode, totalLimit, includeHidden, fileOnly, dirOnly, extensions, contextLines)
		if err != nil {
			continue // Skip roots with errors
		}

		for _, r := range searchResults {
			r["root"] = searchRoot
			allResults = append(allResults, r)
			if len(allResults) >= limit {
				break
			}
		}
		if len(allResults) >= limit {
			break
		}
	}

	return mcp.NewToolResultText(formatMultiSearchResults(allResults)), true
}

// ==================== Search Helpers (used by multi-search) ====================

func doSearch(searchPath, pattern string, mode string, limit int, includeHidden bool, fileOnly bool, dirOnly bool, extensions []string, contextLines int) ([]map[string]interface{}, error) {
	var results []map[string]interface{}

	if mode == "grep" {
		err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || len(results) >= limit {
				return filepath.SkipAll
			}
			if d.IsDir() {
				name := d.Name()
				if !includeHidden && strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if fileOnly {
				return filepath.SkipDir
			}
			if len(extensions) > 0 {
				ext := filepath.Ext(d.Name())
				found := false
				for _, e := range extensions {
					if ext == e {
						found = true
						break
					}
				}
				if !found {
					return nil
				}
			}
			contentType := detectMIMEType(path)
			isBinary := strings.HasPrefix(contentType, "image/") || (strings.HasPrefix(contentType, "application/") && !strings.Contains(contentType, "json") && !strings.Contains(contentType, "xml") && contentType != "unknown")
			if isBinary {
				return nil
			}
			fileContent, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(fileContent), "\n")
			for i, line := range lines {
				if strings.Contains(line, pattern) && len(results) < limit {
					resultLine := line
					if contextLines > 0 {
						start := i - contextLines
						if start < 0 {
							start = 0
						}
						end := i + contextLines
						if end >= len(lines) {
							end = len(lines) - 1
						}
						var ctx strings.Builder
						for j := start; j <= end; j++ {
							ctx.WriteString(fmt.Sprintf("%d: %s\n", j+1, lines[j]))
						}
						resultLine = ctx.String()
					}
					results = append(results, map[string]interface{}{
						"path":    path,
						"line":    i + 1,
						"content": resultLine,
						"type":    "file",
					})
				}
			}
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "SkipAll") {
			return results, err
		}
	} else if mode == "regex" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return results, fmt.Errorf("invalid regex: %v", err)
		}
		err = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || len(results) >= limit {
				return filepath.SkipAll
			}
			name := d.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				return nil
			}
			if fileOnly && d.IsDir() {
				return nil
			}
			if dirOnly && !d.IsDir() {
				return nil
			}
			if len(extensions) > 0 && !d.IsDir() {
				ext := filepath.Ext(name)
				found := false
				for _, e := range extensions {
					if ext == e {
						found = true
						break
					}
				}
				if !found {
					return nil
				}
			}
			matched := re.MatchString(name) || re.MatchString(path)
			if matched {
				results = append(results, map[string]interface{}{
					"path": path,
					"type": map[bool]string{true: "dir", false: "file"}[d.IsDir()],
				})
			}
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "SkipAll") {
			return results, err
		}
	} else {
		err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil || len(results) >= limit {
				return filepath.SkipAll
			}
			name := d.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				return nil
			}
			match := strings.Contains(name, pattern) || strings.EqualFold(name, pattern)
			if !match {
				return nil
			}
			if fileOnly && d.IsDir() {
				return nil
			}
			if dirOnly && !d.IsDir() {
				return nil
			}
			if len(extensions) > 0 && !d.IsDir() {
				ext := filepath.Ext(name)
				found := false
				for _, e := range extensions {
					if ext == e {
						found = true
						break
					}
				}
				if !found {
					return nil
				}
			}
			results = append(results, map[string]interface{}{
				"path": path,
				"type": map[bool]string{true: "dir", false: "file"}[d.IsDir()],
			})
			return nil
		})
		if err != nil && !strings.Contains(err.Error(), "SkipAll") {
			return results, err
		}
	}

	return results, nil
}

func formatMultiSearchResults(results []map[string]interface{}) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Multi-Root Search Results (%d matches) ===\n\n", len(results)))
	for _, r := range results {
		path, _ := r["path"].(string)
		rType, _ := r["type"].(string)
		root, _ := r["root"].(string)
		lineNum, _ := r["line"].(float64)
		content, _ := r["content"].(string)

		sb.WriteString(fmt.Sprintf("[%s] %s (root: %s)", rType, path, root))
		if lineNum > 0 {
			sb.WriteString(fmt.Sprintf(":%d", int(lineNum)))
		}
		sb.WriteString("\n")
		if content != "" && lineNum > 0 {
			sb.WriteString(content)
		}
	}
	return sb.String()
}
