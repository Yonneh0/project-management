package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// getItemInfoAction returns file/directory metadata.
func getItemInfoAction(filePath string, info os.FileInfo, isDir bool) (*mcp.CallToolResult, error) {
	fileSize := int64(0)
	modTime := info.ModTime().UTC().Format(time.RFC3339)
	createdTime := modTime

	isHidden := strings.HasPrefix(info.Name(), ".")
	isSymlink := info.Mode()&os.ModeSymlink != 0
	symlinkTarget := ""
	if isSymlink {
		symlinkTarget, _ = os.Readlink(filePath)
	}
	readable := true
	writable := info.Mode().Perm()&0200 != 0

	if !isDir {
		fileSize = info.Size()
		testFile, testErr := os.Open(filePath)
		if testErr != nil {
			readable = false
		} else {
			testFile.Close()
		}
	}

	var sb strings.Builder
	sb.WriteString("=== File Information ===\n")
	sb.WriteString(fmt.Sprintf("Path: %s\nType: %s | Name: %s\n", filePath, map[bool]string{true: "directory", false: "file"}[isDir], info.Name()))
	sb.WriteString(fmt.Sprintf("Size: %s (%d bytes)\n", humanReadableSize(fileSize), fileSize))
	sb.WriteString(fmt.Sprintf("Permissions: %s\nReadable: %v | Writable: %v\n", formatPermissionsFull(info), readable, writable))
	sb.WriteString(fmt.Sprintf("Hidden: %v\n", isHidden))
	sb.WriteString(fmt.Sprintf("Symlink: %v", isSymlink))
	if isSymlink {
		sb.WriteString(fmt.Sprintf(" -> %s", symlinkTarget))
	}
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Created: %s\nModified: %s\n", createdTime, modTime))
	return mcp.NewToolResultText(sb.String()), nil
}

// getItemReadAction handles file reading and directory listing.
func getItemReadAction(filePath string, info os.FileInfo, offset int, length int, lineNum int, hasLine bool, startLine int, endLine int, hasStartLine bool, format string, isDir bool, recursive bool, maxItems int, includeHidden bool, sortBy string) (*mcp.CallToolResult, error) {
	if isDir {
		dirModTime := info.ModTime().UTC().Format(time.RFC3339)
		dirPerms := formatPermissions(info)

		if length == 0 {
			msg := fmt.Sprintf("Directory: %s\nSize: %d bytes\nModified: %s | Permissions: %s", filePath, info.Size(), dirModTime, dirPerms)
			return mcp.NewToolResultText(msg), nil
		}
		return getItemListAction(filePath, info, recursive, maxItems, includeHidden, sortBy)
	}

	totalSize := info.Size()
	modTime := info.ModTime().UTC().Format(time.RFC3339)
	sizeStr := humanReadableSize(totalSize)
	perms := formatPermissions(info)

	var largeFileHint string
	if totalSize >= LargeFileThreshold {
		hint := BuildFileHint("read", filePath, totalSize)
		largeFileHint = fmt.Sprintf("\n[Hint: %s]", hint.ThresholdHint)
	}

	if hasLine || hasStartLine {
		if totalSize > MaxFileSize {
			return mcp.NewToolResultError(fmt.Sprintf("file size (%s) exceeds maximum allowed read size (%s). Use offset/length to read in chunks.", humanReadableSize(totalSize), humanReadableSize(MaxFileSize))), nil
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
		}

		var result strings.Builder
		result.WriteString(fmt.Sprintf("=== %s ===\n", filePath))
		result.WriteString(fmt.Sprintf("Size: %s (%d bytes) | Modified: %s | Permissions: %s\n\n", sizeStr, totalSize, modTime, perms))
		if largeFileHint != "" {
			result.WriteString(largeFileHint)
		}

		rawLines := strings.Split(string(content), "\n")
		hasTrailingNewline := len(content) > 0 && content[len(content)-1] == '\n'
		totalLines := len(rawLines)
		if hasTrailingNewline && totalLines > 0 && rawLines[totalLines-1] == "" {
			totalLines--
		}

		if hasLine {
			lineIdx := lineNum - 1
			if lineIdx < 0 || lineIdx >= len(rawLines) {
				return mcp.NewToolResultText(fmt.Sprintf("File: %s\nTotal lines: %d\nLine %d out of range", filePath, totalLines, lineNum)), nil
			}
			result.WriteString(fmt.Sprintf("%s\n", rawLines[lineIdx]))

			if len(content) > 0 {
				var lineCount int
				if hasTrailingNewline && totalLines > 0 && rawLines[totalLines-1] == "" {
					lineCount = totalLines - 1
				} else {
					lineCount = totalLines
				}
				result.WriteString(fmt.Sprintf("\nLines: %d\n", lineCount))
			}

			return mcp.NewToolResultText(result.String()), nil
		}

		if hasStartLine {
			startIdx := startLine - 1
			endIdx := endLine - 1
			if startIdx < 0 {
				startIdx = 0
			}
			if endIdx >= totalLines {
				endIdx = totalLines - 1
			}
			if startIdx > endIdx {
				startIdx, endIdx = endIdx, startIdx
			}
			if startLine > endLine {
				result.WriteString(fmt.Sprintf("Note: startLine (%d) > endLine (%d); swapping to read lines %d-%d\n", startLine, endLine, startLine, endLine))
			}
			for i := startIdx; i <= endIdx && i < len(rawLines); i++ {
				result.WriteString(fmt.Sprintf("%d | %s\n", i+1, rawLines[i]))
			}

			result.WriteString(fmt.Sprintf("\nLines: %d\n", totalLines))
			return mcp.NewToolResultText(result.String()), nil
		}
	}

	if length == 0 {
		msg := fmt.Sprintf("File: %s\nSize: %s (%d bytes)\nModified: %s | Permissions: %s", filePath, sizeStr, totalSize, modTime, perms)
		if largeFileHint != "" {
			msg += largeFileHint
		}
		return mcp.NewToolResultText(msg), nil
	}

	if offset > int(totalSize) {
		msg := fmt.Sprintf("File: %s\nSize: %s (%d bytes)\nModified: %s | Permissions: %s\nOffset %d exceeds file size %d", filePath, sizeStr, totalSize, modTime, perms, offset, totalSize)
		if largeFileHint != "" {
			msg += largeFileHint
		}
		return mcp.NewToolResultText(msg), nil
	}

	if offset == int(totalSize) && totalSize > 0 {
		var hexMsg, textMsg string
		hexMsg = fmt.Sprintf("=== %s === Size: %s (%d bytes) | Read: 0 bytes\nModified: %s | Permissions: %s | Offset: %d | Length requested: %d\n\n(offset is at EOF — no data to read)\n--- Hex Dump (0 bytes) ---\n(offset is at EOF, no data to dump)", filePath, sizeStr, totalSize, modTime, perms, offset, length)
		textMsg = fmt.Sprintf("=== %s === Size: %s (%d bytes) | Read: 0 bytes\nModified: %s | Permissions: %s | Offset: %d | Length requested: %d\n\n(offset is at EOF — no data to read)", filePath, sizeStr, totalSize, modTime, perms, offset, length)
		if largeFileHint != "" {
			hexMsg += largeFileHint
			textMsg += largeFileHint
		}
		if format == "hex" {
			return mcp.NewToolResultText(hexMsg), nil
		}
		return mcp.NewToolResultText(textMsg), nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to open file: %v", err)), nil
	}
	defer file.Close()

	if _, err := file.Seek(int64(offset), 0); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to seek: %v", err)), nil
	}

	var readSize int64
	if length > 0 && int64(offset)+int64(length) < totalSize {
		readSize = int64(length)
	} else {
		readSize = totalSize - int64(offset)
	}

	data := make([]byte, readSize)
	n, err := file.Read(data)
	if n == 0 && err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}
	data = data[:n]

	bytesRead := int64(n)

	var result strings.Builder
	result.WriteString(fmt.Sprintf("=== %s === Size: %s (%d bytes) | Read: %d bytes\n", filePath, sizeStr, totalSize, bytesRead))
	result.WriteString(fmt.Sprintf("Modified: %s | Permissions: %s | Offset: %d | Length requested: %d\n", modTime, perms, offset, length))
	if largeFileHint != "" {
		result.WriteString(largeFileHint)
	}

	if format == "hex" {
		result.WriteString(fmt.Sprintf("\n--- Hex Dump (%d bytes) ---\n", bytesRead))
		result.WriteString(toHexDump(data))
		if int64(n) < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("\n... (%s remaining, use offset/length to read more)", humanReadableSize(remaining)))
		}
		return mcp.NewToolResultText(result.String()), nil
	}

	if format == "auto" {
		contentStr := escapeNonPrintable(string(data))
		var lineCount int
		if len(data) == 0 {
			lineCount = 0
		} else {
			rawLines := strings.Split(contentStr, "\\n")
			hasTrailingNewline := contentStr[len(contentStr)-1] == '\\' && len(contentStr) > 2
			totalLines := len(rawLines)
			if hasTrailingNewline && totalLines > 0 && rawLines[totalLines-1] == "" {
				totalLines--
			}
			lineCount = totalLines
		}
		result.WriteString(fmt.Sprintf("Lines: %d\n\n", lineCount))
		result.WriteString(contentStr)
		if int64(n) < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("\n\n... (%s remaining, use offset/length to read more)", humanReadableSize(remaining)))
		}
	}

	return mcp.NewToolResultText(result.String()), nil
}

// getItemListAction lists directory contents.
func getItemListAction(filePath string, info os.FileInfo, recursive bool, maxItems int, includeHidden bool, sortBy string) (*mcp.CallToolResult, error) {
	var sb strings.Builder
	perms := formatPermissions(info)
	modTime := info.ModTime().UTC().Format(time.RFC3339)

	sb.WriteString(fmt.Sprintf("=== %s ===\n", filePath))
	sb.WriteString(fmt.Sprintf("Type: directory | Permissions: %s\n", perms))
	sb.WriteString(fmt.Sprintf("Created: %s | Modified: %s\n", modTime, modTime))
	sb.WriteString(fmt.Sprintf("Sort by: %s | Max items: %d | Include hidden: %v | Empty dirs included: yes\n\n", sortBy, maxItems, includeHidden))

	if recursive {
		var allEntries []DirEntryInfo
		err := filepath.WalkDir(filePath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(filePath, path)
			if relPath == "." {
				return nil
			}

			info2, _ := d.Info()
			if !includeHidden && strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			isLink := d.Type()&os.ModeSymlink != 0
			linkTarget := ""
			if isLink {
				linkTarget, _ = os.Readlink(path)
			}

			allEntries = append(allEntries, DirEntryInfo{
				Name:          d.Name(),
				Path:          path,
				IsDir:         d.IsDir(),
				Info:          info2,
				RelPath:       relPath,
				IsSymlink:     isLink,
				SymlinkTarget: linkTarget,
			})
			return nil
		})

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("walk failed: %v", err)), nil
		}

		sort.Slice(allEntries, func(i, j int) bool {
			switch sortBy {
			case "size":
				if allEntries[i].IsDir != allEntries[j].IsDir {
					return !allEntries[i].IsDir
				}
				si := int64(0)
				if allEntries[i].Info != nil {
					si = allEntries[i].Info.Size()
				}
				sj := int64(0)
				if allEntries[j].Info != nil {
					sj = allEntries[j].Info.Size()
				}
				return si < sj
			case "date":
				var timeI, timeJ time.Time
				if allEntries[i].Info != nil {
					timeI = allEntries[i].Info.ModTime()
				}
				if allEntries[j].Info != nil {
					timeJ = allEntries[j].Info.ModTime()
				}
				return timeI.Before(timeJ)
			case "type":
				if allEntries[i].IsDir != allEntries[j].IsDir {
					return !allEntries[i].IsDir
				}
				return allEntries[i].Name < allEntries[j].Name
			default:
				return allEntries[i].Name < allEntries[j].Name
			}
		})

		fileCount := 0
		dirCount := 0
		var totalSize int64

		for _, e := range allEntries {
			if maxItems > 0 && fileCount+dirCount >= maxItems {
				break
			}
			if e.IsDir {
				dirCount++
			} else if e.Info != nil {
				fileCount++
				totalSize += e.Info.Size()
			}
		}

		sb.WriteString(fmt.Sprintf("Summary: %d files, %d directories | Total size: %s (%d bytes)\n\n", fileCount, dirCount, humanReadableSize(totalSize), totalSize))
		sb.WriteString("--- Contents ---\n")

		count := 0
		for _, e := range allEntries {
			if maxItems > 0 && count >= maxItems {
				break
			}
			typeStr := "F"
			if e.IsDir {
				typeStr = "D"
			}
			sizeStr := ""
			if e.Info != nil && !e.IsDir {
				sizeStr = fmt.Sprintf(" (%s)", humanReadableSize(e.Info.Size()))
			}
			symlinkStr := ""
			if e.IsSymlink {
				symlinkStr = fmt.Sprintf(" -> %s", e.SymlinkTarget)
			}
			sb.WriteString(fmt.Sprintf("[%s] %s%s%s\n", typeStr, e.RelPath, sizeStr, symlinkStr))
			count++
		}

		if len(allEntries) > maxItems && maxItems > 0 {
			sb.WriteString(fmt.Sprintf("\n... (%d additional items hidden, limit: %d)", len(allEntries)-maxItems, maxItems))
		}
	} else {
		entries, err := os.ReadDir(filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list directory: %v", err)), nil
		}

		var entryInfos []DirEntryInfo
		for _, entry := range entries {
			info2, _ := entry.Info()
			name := entry.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				continue
			}
			entryInfos = append(entryInfos, DirEntryInfo{
				Name:  name,
				IsDir: entry.IsDir(),
				Info:  info2,
			})
		}

		sort.Slice(entryInfos, func(i, j int) bool {
			switch sortBy {
			case "size":
				if entryInfos[i].IsDir != entryInfos[j].IsDir {
					return !entryInfos[i].IsDir
				}
				si := int64(0)
				if entryInfos[i].Info != nil {
					si = entryInfos[i].Info.Size()
				}
				sj := int64(0)
				if entryInfos[j].Info != nil {
					sj = entryInfos[j].Info.Size()
				}
				return si < sj
			case "date":
				var timeI, timeJ time.Time
				if entryInfos[i].Info != nil {
					timeI = entryInfos[i].Info.ModTime()
				}
				if entryInfos[j].Info != nil {
					timeJ = entryInfos[j].Info.ModTime()
				}
				return timeI.Before(timeJ)
			case "type":
				if entryInfos[i].IsDir != entryInfos[j].IsDir {
					return !entryInfos[i].IsDir
				}
				return entryInfos[i].Name < entryInfos[j].Name
			default:
				return entryInfos[i].Name < entryInfos[j].Name
			}
		})

		fileCount := 0
		dirCount := 0
		var totalSize int64
		for _, e := range entryInfos {
			if e.IsDir {
				dirCount++
			} else if e.Info != nil {
				fileCount++
				totalSize += e.Info.Size()
			}
		}

		sb.WriteString(fmt.Sprintf("Summary: %d files, %d directories | Total size: %s (%d bytes)\n\n", fileCount, dirCount, humanReadableSize(totalSize), totalSize))
		sb.WriteString("--- Contents ---\n")

		count := 0
		for _, e := range entryInfos {
			if maxItems > 0 && count >= maxItems {
				break
			}
			typeStr := "F"
			if e.IsDir {
				typeStr = "D"
			}
			sizeStr := ""
			if e.Info != nil && !e.IsDir {
				sizeStr = fmt.Sprintf(" (%s)", humanReadableSize(e.Info.Size()))
			}
			sb.WriteString(fmt.Sprintf("[%s] %s%s\n", typeStr, e.Name, sizeStr))
			count++
		}

		if len(entryInfos) > maxItems && maxItems > 0 {
			sb.WriteString(fmt.Sprintf("\n... (%d additional items hidden, limit: %d)", len(entryInfos)-maxItems, maxItems))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleGetItem processes the GetItem tool request.
func handleGetItem(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	action := "auto"
	if v, err := extractArg[string](req, "action"); err == nil {
		action = v
	}

	offset := extractArgsDefault(req, "offset", 0)
	length := extractArgsDefault(req, "length", -1)
	lineNum := extractArgsDefault[*int](req, "line", nil)
	startLine := extractArgsDefault[*int](req, "startLine", nil)
	endLine := extractArgsDefault[*int](req, "endLine", nil)
	format := extractArgsDefault(req, "format", "auto")
	recursive := extractArgsDefault(req, "recursive", false)
	maxItems := extractArgsDefault(req, "maxItems", DefaultMaxItems)
	includeHidden := extractArgsDefault(req, "includeHidden", false)
	sortBy := extractArgsDefault(req, "sortBy", "name")

	pctxSnap := GetGlobalProjectSnapshot()
	if pctxSnap.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	resolvedPath, err := ResolvePathWithBoundaryCheck(pctxSnap.Path, filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}
	actualPath := resolvedPath

	info, err := os.Stat(actualPath)
	if err != nil {
		if os.IsNotExist(err) {
			parentDir := filepath.Dir(actualPath)
			baseName := filepath.Base(actualPath)
			suggestion := suggestFileExists(actualPath)

			var msg string
			hasDot := false
			for _, c := range baseName {
				if c == '.' {
					hasDot = true
					break
				}
			}
			if strings.HasSuffix(actualPath, "/") || !hasDot && len(baseName) > 0 {
				msg = fmt.Sprintf("Item not found: %s (looking for directory)\nParent: %s", actualPath, parentDir)
			} else {
				msg = fmt.Sprintf("Item not found: %s (looking for file)\nParent: %s", actualPath, parentDir)
			}

			if suggestion != "" {
				msg += fmt.Sprintf("\nDid you mean: %s?", suggestion)
			}

			return mcp.NewToolResultText(msg), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
	}

	isDir := info.IsDir()

	if action == "auto" {
		action = "read"
	}

	switch action {
	case "info":
		return getItemInfoAction(actualPath, info, isDir)
	case "read":
		hasLine := lineNum != nil
		lineVal := 0
		if lineNum != nil {
			lineVal = *lineNum
		}
		if isDir {
			return getItemReadAction(actualPath, info, offset, length, lineVal, hasLine, 0, 0, false, format, true, recursive, int(maxItems), includeHidden, sortBy)
		}
		hasStartLine := startLine != nil
		startVal := 0
		endVal := 0
		if startLine != nil {
			startVal = *startLine
		}
		if endLine != nil {
			endVal = *endLine
		}
		return getItemReadAction(actualPath, info, offset, length, lineVal, hasLine, startVal, endVal, hasStartLine, format, false, false, int(maxItems), includeHidden, sortBy)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Unknown action: %s", action)), nil
	}
}