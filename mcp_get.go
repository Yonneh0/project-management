package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== GetItem Tool Handler ====================

func handleGetItem(ctx context.Context, req mcp.CallToolRequest, store *fileStore, rootDir string) (*mcp.CallToolResult, error) {
	filePath, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	action := "auto"
	if v, ok := extractOptionalString(req, "action"); ok {
		action = v
	}

	offset := 0
	if o, ok := extractOptionalInt(req, "offset"); ok {
		offset = o
	}

	length := -1
	if l, ok := extractOptionalInt(req, "length"); ok {
		length = l
	}

	lineNum, hasLine := extractOptionalInt(req, "line")
	startLine, hasStartLine := extractOptionalInt(req, "startLine")
	endLine, hasEndLine := extractOptionalInt(req, "endLine")

	encoding, _ := extractOptionalString(req, "encoding")
	_ = encoding

	recursive := false
	if v, ok := extractOptionalBool(req, "recursive"); ok {
		recursive = v
	}

	maxItems := 100
	if m, ok := extractOptionalInt(req, "maxItems"); ok && m > 0 {
		maxItems = m
	}

	includeHidden := false
	if v, ok := extractOptionalBool(req, "includeHidden"); ok {
		includeHidden = v
	}

	sortBy := "name"
	if v, ok := extractOptionalString(req, "sortBy"); ok {
		sortBy = v
	}

	severity := "all"
	if v, ok := extractOptionalString(req, "severity"); ok {
		severity = v
	}

	_, _ = extractOptionalString(req, "checksum")

	file2Path, hasFile2 := extractOptionalString(req, "file2")

	ignoreWhitespace := false
	if v, ok := extractOptionalBool(req, "ignoreWhitespace"); ok {
		ignoreWhitespace = v
	}

	ignoreCase := false
	if v, ok := extractOptionalBool(req, "ignoreCase"); ok {
		ignoreCase = v
	}

	noCache := false
	if v, ok := extractOptionalBool(req, "noCache"); ok {
		noCache = v
	}

	pctx := GetGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	// Resolve path with boundary check (also handles archive paths)
	var isArchive bool
	var archInfo *ArchiveInfo
	var actualPath string

	resolvedPath, err := resolvePathWithBoundaryCheck(pctx.Path, filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	// Check for archive path format (archive.zip/entry/path)
	archFile, entryPath, ok := resolveArchivePath(resolvedPath)
	if ok {
		ai, ae := openArchive(archFile)
		if ae == nil {
			isArchive = true
			archInfo = ai
			actualPath = resolvedPath
		} else {
			actualPath = resolvedPath
		}
	} else {
		actualPath = resolvedPath
	}

	info, err := os.Stat(actualPath)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(fmt.Sprintf("Item not found: %s", actualPath)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
	}

	isDir := info.IsDir()

	if action == "auto" {
		if isArchive && archInfo != nil {
			action = "archive-list"
		} else if isDir {
			action = "list"
		} else {
			action = "read"
		}
	}

	switch action {
	case "archive-list":
		if !isArchive || archInfo == nil {
			return mcp.NewToolResultError("path must be an archive file (e.g., 'archive.zip') for archive-list action"), nil
		}
		return mcp.NewToolResultText(listArchiveEntries(archInfo)), nil
	case "info":
		if isArchive && archInfo != nil {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== Archive Information ===\n"))
			sb.WriteString(fmt.Sprintf("Path: %s\n", actualPath))
			sb.WriteString(fmt.Sprintf("Format: %s | Entries: %d\n", archInfo.Format, len(archInfo.Entries)))
			return mcp.NewToolResultText(sb.String()), nil
		}
		info, err := os.Stat(actualPath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultText(fmt.Sprintf("Item not found: %s", actualPath)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
		}
		return getItemInfoAction(actualPath, info, info.IsDir())
	case "read":
		if isArchive && archInfo != nil {
			content, found := readArchiveFile(archInfo, entryPath)
			if !found {
				return mcp.NewToolResultText(fmt.Sprintf("Entry not found in archive: %s", entryPath)), nil
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("=== %s ===\n", actualPath))
			sb.WriteString(fmt.Sprintf("Size: %d bytes (from archive)\n\n", len(content)))
			sb.WriteString(string(content))
			return mcp.NewToolResultText(sb.String()), nil
		}
		info, err := os.Stat(actualPath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultText(fmt.Sprintf("Item not found: %s", actualPath)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
		}
		if info.IsDir() {
			return mcp.NewToolResultError("Path is a directory. Use action='list' to view contents."), nil
		}
		return getItemReadAction(actualPath, info, offset, length, lineNum, hasLine, startLine, endLine, hasStartLine, hasEndLine)
	case "list":
		if isArchive && archInfo != nil {
			return mcp.NewToolResultText(listArchiveEntries(archInfo)), nil
		}
		info, err := os.Stat(actualPath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultText(fmt.Sprintf("Item not found: %s", actualPath)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
		}
		if !info.IsDir() {
			return mcp.NewToolResultError("Path is a file. Use action='read' or 'info' for file access."), nil
		}
		return getItemListAction(actualPath, info, recursive, maxItems, includeHidden, sortBy)
	case "compile":
		if !isDir {
			return mcp.NewToolResultError("Path is a file. Use action='read' or 'info' for file access."), nil
		}
		return getItemCompileAction(actualPath, severity, noCache)
	case "diff":
		if info.IsDir() {
			return mcp.NewToolResultError("Path is a directory. Directories cannot be compared with diff."), nil
		}
		return getItemDiffAction(actualPath, file2Path, hasFile2, pctx.Path, ignoreWhitespace, ignoreCase)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("Unknown action: %s", action)), nil
	}
}

// ==================== GetItem Helper Functions ====================

func getItemInfoAction(filePath string, info os.FileInfo, isDir bool) (*mcp.CallToolResult, error) {
	mimeType := "unknown"
	fileSize := int64(0)
	modTime := info.ModTime().UTC().Format(time.RFC3339)
	createdTime := modTime
	perms := formatPermissions(info)
	unixPerms := getUnixPermissions(info)

	isHidden := strings.HasPrefix(info.Name(), ".")
	readable := true
	writable := info.Mode().Perm()&0200 != 0

	if !isDir {
		mimeType = detectMIMEType(filePath)
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
	sb.WriteString(fmt.Sprintf("Path: %s\n", filePath))
	sb.WriteString(fmt.Sprintf("Type: %s | Name: %s\n", map[bool]string{true: "directory", false: "file"}[isDir], info.Name()))
	sb.WriteString(fmt.Sprintf("Size: %s (%d bytes)\n", humanReadableSize(fileSize), fileSize))
	sb.WriteString(fmt.Sprintf("MIME type: %s\n", mimeType))
	sb.WriteString(fmt.Sprintf("Permissions: %s (unix: %s)\n", perms, unixPerms))
	sb.WriteString(fmt.Sprintf("Readable: %v | Writable: %v\n", readable, writable))
	sb.WriteString(fmt.Sprintf("Hidden: %v\n", isHidden))
	sb.WriteString(fmt.Sprintf("Created: %s\n", createdTime))
	sb.WriteString(fmt.Sprintf("Modified: %s\n", modTime))

	if !isDir {
		md5Hash, _ := computeMD5(filePath)
		sb.WriteString(fmt.Sprintf("MD5 hash: %s\n", md5Hash))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func getItemReadAction(filePath string, info os.FileInfo, offset int, length int, lineNum int, hasLine bool, startLine int, endLine int, hasStartLine, hasEndLine bool) (*mcp.CallToolResult, error) {
	totalSize := info.Size()
	mimeType := detectMIMEType(filePath)
	modTime := info.ModTime().UTC().Format(time.RFC3339)
	sizeStr := humanReadableSize(totalSize)
	perms := formatPermissions(info)

	if hasLine || hasStartLine {
		content, err := os.ReadFile(filePath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
		}

		lines := strings.Split(string(content), "\n")
		var result strings.Builder
		result.WriteString(fmt.Sprintf("=== %s ===\n", filePath))
		result.WriteString(fmt.Sprintf("Size: %s (%d bytes) | MIME type: %s | Modified: %s | Permissions: %s\n\n", sizeStr, totalSize, mimeType, modTime, perms))

		if hasLine {
			lineIdx := lineNum - 1
			if lineIdx < 0 || lineIdx >= len(lines) {
				return mcp.NewToolResultText(fmt.Sprintf("File: %s\nTotal lines: %d\nLine %d out of range", filePath, len(lines), lineNum)), nil
			}
			result.WriteString(fmt.Sprintf("Line %d:\n%s\n", lineNum, lines[lineIdx]))
		} else if hasStartLine {
			startIdx := startLine - 1
			endIdx := endLine - 1
			if startIdx < 0 {
				startIdx = 0
			}
			if endIdx >= len(lines) {
				endIdx = len(lines) - 1
			}
			for i := startIdx; i <= endIdx && i < len(lines); i++ {
				result.WriteString(fmt.Sprintf("%d | %s\n", i+1, lines[i]))
			}
		}

		return mcp.NewToolResultText(result.String()), nil
	}

	if length == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("File: %s\nSize: %s (%d bytes)\nMIME type: %s | Modified: %s | Permissions: %s", filePath, sizeStr, totalSize, mimeType, modTime, perms)), nil
	}

	if offset > int(totalSize) {
		return mcp.NewToolResultText(fmt.Sprintf("File: %s\nSize: %s (%d bytes)\nMIME type: %s | Modified: %s | Permissions: %s\nOffset %d exceeds file size %d", filePath, sizeStr, totalSize, mimeType, modTime, perms, offset, totalSize)), nil
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
	if err != nil && n == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}
	data = data[:n]

	bytesRead := int64(n)
	isBinary := strings.HasPrefix(mimeType, "image/") || (strings.HasPrefix(mimeType, "application/") && !strings.Contains(mimeType, "json") && !strings.Contains(mimeType, "xml") && mimeType != "unknown")

	var result strings.Builder
	result.WriteString(fmt.Sprintf("=== %s ===\n", filePath))
	result.WriteString(fmt.Sprintf("Size: %s (%d bytes) | Read: %d bytes\n", sizeStr, totalSize, bytesRead))
	result.WriteString(fmt.Sprintf("MIME type: %s | Modified: %s | Permissions: %s\n", mimeType, modTime, perms))
	result.WriteString(fmt.Sprintf("Offset: %d | Length requested: %d\n", offset, length))

	if isBinary {
		result.WriteString("\n[Binary file - content not displayed]\n")
	} else {
		content := string(data)
		lineCount := strings.Count(content, "\n") + 1
		result.WriteString(fmt.Sprintf("Lines: %d\n\n", lineCount))
		result.WriteString(content)
		if int64(n) < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("\n\n... (%s remaining, use offset/length to read more)", humanReadableSize(remaining)))
		}
	}

	return mcp.NewToolResultText(result.String()), nil
}

func getItemListAction(filePath string, info os.FileInfo, recursive bool, maxItems int, includeHidden bool, sortBy string) (*mcp.CallToolResult, error) {
	var sb strings.Builder
	perms := formatPermissions(info)
	modTime := info.ModTime().UTC().Format(time.RFC3339)

	sb.WriteString(fmt.Sprintf("=== %s ===\n", filePath))
	sb.WriteString(fmt.Sprintf("Type: directory | Permissions: %s\n", perms))
	sb.WriteString(fmt.Sprintf("Created: %s | Modified: %s\n", modTime, modTime))
	sb.WriteString(fmt.Sprintf("Sort by: %s | Max items: %d | Include hidden: %v\n\n", sortBy, maxItems, includeHidden))

	if recursive {
		var allEntries []dirEntryInfo
		err := filepath.WalkDir(filePath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(filePath, path)
			if relPath == "." {
				return nil
			}

			info2, _ := d.Info()
			if info2 != nil && !includeHidden && strings.HasPrefix(d.Name(), ".") {
				if !d.IsDir() {
					return nil
				}
			}

			allEntries = append(allEntries, dirEntryInfo{
				name:    d.Name(),
				path:    path,
				isDir:   d.IsDir(),
				info:    info2,
				relPath: relPath,
			})
			return nil
		})

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("walk failed: %v", err)), nil
		}

		sortEntries(allEntries, sortBy)

		fileCount := 0
		dirCount := 0
		var totalSize int64

		for _, e := range allEntries {
			if maxItems > 0 && fileCount+dirCount >= maxItems {
				break
			}
			if e.isDir {
				dirCount++
			} else if e.info != nil {
				fileCount++
				totalSize += e.info.Size()
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
			if e.isDir {
				typeStr = "D"
			}
			sizeStr := ""
			if e.info != nil && !e.isDir {
				sizeStr = fmt.Sprintf(" (%s)", humanReadableSize(e.info.Size()))
			}
			sb.WriteString(fmt.Sprintf("[%s] %s%s\n", typeStr, e.relPath, sizeStr))
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

		var entryInfos []dirEntryInfo
		for _, entry := range entries {
			info2, _ := entry.Info()
			name := entry.Name()
			if !includeHidden && strings.HasPrefix(name, ".") && !entry.IsDir() {
				continue
			}
			entryInfos = append(entryInfos, dirEntryInfo{
				name:  name,
				isDir: entry.IsDir(),
				info:  info2,
			})
		}

		sortEntries(entryInfos, sortBy)

		fileCount := 0
		dirCount := 0
		var totalSize int64
		for _, e := range entryInfos {
			if e.isDir {
				dirCount++
			} else if e.info != nil {
				fileCount++
				totalSize += e.info.Size()
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
			if e.isDir {
				typeStr = "D"
			}
			sizeStr := ""
			if e.info != nil && !e.isDir {
				sizeStr = fmt.Sprintf(" (%s)", humanReadableSize(e.info.Size()))
			}
			sb.WriteString(fmt.Sprintf("[%s] %s%s\n", typeStr, e.name, sizeStr))
			count++
		}

		if len(entryInfos) > maxItems && maxItems > 0 {
			sb.WriteString(fmt.Sprintf("\n... (%d additional items hidden, limit: %d)", len(entryInfos)-maxItems, maxItems))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func getItemCompileAction(projectPath string, severity string, noCache bool) (*mcp.CallToolResult, error) {
	if globalStore == nil || globalStore.compileCache == nil {
		return mcp.NewToolResultText("=== Compile/Build Status Report ===\nProject path: " + projectPath + "\n\nCompile cache not initialized.\n"), nil
	}

	cacheKey := globalStore.compileCache.generateKey(projectPath, severity, nil)

	if !noCache {
		if cached, ok := globalStore.compileCache.get(cacheKey); ok {
			cachedResp := *cached
			cachedResp.Cached = true
			return mcp.NewToolResultText(formatCompileResult(&cachedResp, severity)), nil
		}
	}

	var sb strings.Builder
	sb.WriteString("=== Compile/Build Status Report ===\n")
	sb.WriteString(fmt.Sprintf("Project path: %s\n\n", projectPath))

	nodePkg := detectNodeProject(projectPath)
	pythonFiles := detectPythonProject(projectPath)
	dotnetFile := detectDotnetProject(projectPath)
	goMod := detectGoProject(projectPath)

	var langStatuses []LanguageStatus

	ns := LanguageStatus{Language: "node"}
	if nodePkg != "" {
		ns.Detected = true
		nodeCmd := exec.Command("node", "--version")
		if err := nodeCmd.Run(); err == nil {
			output, _ := nodeCmd.Output()
			ns.Runtime = strings.TrimSpace(string(output))
		}

		buildCmd := exec.Command("npm", "run", "build")
		buildCmd.Dir = filepath.Dir(nodePkg)
		if err := buildCmd.Run(); err != nil {
			output, _ := buildCmd.CombinedOutput()
			ns.BuildStatus = "failed"
			ns.Errors = append(ns.Errors, string(output))
		} else {
			ns.BuildStatus = "success"
		}
	} else {
		ns.Detected = false
		ns.BuildStatus = "skipped"
	}
	langStatuses = append(langStatuses, ns)

	ps := LanguageStatus{Language: "python"}
	if len(pythonFiles) > 0 {
		ps.Detected = true
		pythonCmd := exec.Command("python3", "--version")
		if err := pythonCmd.Run(); err != nil {
			pythonCmd = exec.Command("python", "--version")
			if err := pythonCmd.Run(); err == nil {
				output, _ := pythonCmd.Output()
				ps.Runtime = strings.TrimSpace(string(output))
			}
		} else {
			output, _ := pythonCmd.Output()
			ps.Runtime = strings.TrimSpace(string(output))
		}

		syntaxErrors := 0
		syntaxChecked := 0
		for _, f := range pythonFiles {
			if strings.HasSuffix(f, ".py") && !strings.HasSuffix(f, ".pyc") {
				syntaxChecked++
				pyCmd := exec.Command("python3", "-m", "py_compile", f)
				if pyErr := pyCmd.Run(); pyErr != nil {
					pyCmd = exec.Command("python", "-m", "py_compile", f)
					if pyErr := pyCmd.Run(); pyErr != nil {
						syntaxErrors++
					}
				}
			}
		}

		if syntaxChecked > 0 {
			if syntaxErrors == 0 {
				ps.BuildStatus = "success"
			} else {
				ps.BuildStatus = "failed"
				ps.Errors = append(ps.Errors, fmt.Sprintf("%d/%d Python files have syntax errors", syntaxErrors, syntaxChecked))
			}
		} else {
			ps.BuildStatus = "success"
		}
	} else {
		ps.Detected = false
		ps.BuildStatus = "skipped"
	}
	langStatuses = append(langStatuses, ps)

	ds := LanguageStatus{Language: "dotnet"}
	if dotnetFile != "" {
		ds.Detected = true
		dotnetCmd := exec.Command("dotnet", "--version")
		if err := dotnetCmd.Run(); err == nil {
			output, _ := dotnetCmd.Output()
			ds.Runtime = strings.TrimSpace(string(output))
		}

		buildCmd := exec.Command("dotnet", "build", dotnetFile)
		buildOutput, buildErr := buildCmd.CombinedOutput()
		if buildErr != nil {
			ds.BuildStatus = "failed"
			ds.Errors = append(ds.Errors, string(buildOutput))
		} else {
			ds.BuildStatus = "success"
		}
	} else {
		ds.Detected = false
		ds.BuildStatus = "skipped"
	}
	langStatuses = append(langStatuses, ds)

	gs := LanguageStatus{Language: "golang"}
	if goMod != "" {
		gs.Detected = true
		goCmd := exec.Command("go", "--version")
		if err := goCmd.Run(); err == nil {
			output, _ := goCmd.Output()
			gs.Runtime = strings.TrimSpace(string(output))
		}

		buildDir := filepath.Dir(goMod)
		buildCmd := exec.Command("go", "build", "./...")
		buildCmd.Dir = buildDir
		buildOutput, buildErr := buildCmd.CombinedOutput()
		if buildErr != nil {
			gs.BuildStatus = "failed"
			gs.Errors = append(gs.Errors, string(buildOutput))
		} else {
			gs.BuildStatus = "success"
		}
	} else {
		gs.Detected = false
		gs.BuildStatus = "skipped"
	}
	langStatuses = append(langStatuses, gs)

	result := &CompileResult{
		Path:             projectPath,
		Timestamp:        time.Now(),
		Cached:           false,
		LanguageStatuses: langStatuses,
	}

	globalStore.compileCache.set(cacheKey, result)

	return mcp.NewToolResultText(formatCompileResult(result, severity)), nil
}

func formatCompileResult(result *CompileResult, severity string) string {
	var sb strings.Builder
	sb.WriteString("=== Compile/Build Status Report ===\n")
	sb.WriteString(fmt.Sprintf("Project path: %s\n", result.Path))
	if result.Cached {
		sb.WriteString("(cached)\n")
	}

	for _, ls := range result.LanguageStatuses {
		sb.WriteString(fmt.Sprintf("\n--- %s ---\n", ls.Language))
		sb.WriteString(fmt.Sprintf("Detected: %v\n", ls.Detected))
		if ls.Runtime != "" {
			sb.WriteString(fmt.Sprintf("Runtime: %s\n", ls.Runtime))
		}
		sb.WriteString(fmt.Sprintf("Build status: %s\n", ls.BuildStatus))

		switch severity {
		case "errors":
			for _, e := range ls.Errors {
				sb.WriteString(fmt.Sprintf("Error: %s\n", e))
			}
		case "warnings":
			for _, w := range ls.Warnings {
				sb.WriteString(fmt.Sprintf("Warning: %s\n", w))
			}
			for _, e := range ls.Errors {
				sb.WriteString(fmt.Sprintf("Error: %s\n", e))
			}
		default:
			for _, w := range ls.Warnings {
				sb.WriteString(fmt.Sprintf("Warning: %s\n", w))
			}
			for _, e := range ls.Errors {
				sb.WriteString(fmt.Sprintf("Error: %s\n", e))
			}
		}
	}

	return sb.String()
}

func getItemDiffAction(filePath1, filePath2 string, hasFile2 bool, rootDir string, ignoreWhitespace, ignoreCase bool) (*mcp.CallToolResult, error) {
	if !hasFile2 || filePath2 == "" {
		return mcp.NewToolResultError("file2 is required for diff action"), nil
	}

	if !filepath.IsAbs(filePath2) {
		filePath2 = filepath.Join(rootDir, filePath2)
	}
	filePath2 = filepath.Clean(filePath2)

	info1, err := os.Stat(filePath1)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("File not found: %s", filePath1)), nil
	}
	info2, err := os.Stat(filePath2)
	if err != nil {
		return mcp.NewToolResultText(fmt.Sprintf("File not found: %s", filePath2)), nil
	}

	if info1.IsDir() || info2.IsDir() {
		return mcp.NewToolResultError("Both paths must be files for diff comparison"), nil
	}

	var sb strings.Builder
	sb.WriteString("=== File Comparison ===\n")
	sb.WriteString(fmt.Sprintf("file1: %s\n", filePath1))
	sb.WriteString(fmt.Sprintf("file2: %s\n", filePath2))
	sb.WriteString(fmt.Sprintf("Options: ignoreWhitespace=%v, ignoreCase=%v\n\n", ignoreWhitespace, ignoreCase))

	sizeDiff := info2.Size() - info1.Size()
	timeDiff := info2.ModTime().Sub(info1.ModTime())

	mime1 := detectMIMEType(filePath1)
	mime2 := detectMIMEType(filePath2)
	mimeMatch := mime1 == mime2

	sb.WriteString("--- Metadata Comparison ---\n")
	sb.WriteString(fmt.Sprintf("file1 size: %s (%d bytes)\n", humanReadableSize(info1.Size()), info1.Size()))
	sb.WriteString(fmt.Sprintf("file2 size: %s (%d bytes)\n", humanReadableSize(info2.Size()), info2.Size()))
	sb.WriteString("Size difference: " + formatDiff(sizeDiff) + "\n")
	sb.WriteString(fmt.Sprintf("file1 modified: %s\n", info1.ModTime().UTC().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("file2 modified: %s\n", info2.ModTime().UTC().Format(time.RFC3339)))
	sb.WriteString("Time difference: " + formatDuration(timeDiff) + "\n")
	sb.WriteString(fmt.Sprintf("file1 MIME type: %s\n", mime1))
	sb.WriteString(fmt.Sprintf("file2 MIME type: %s | Match: %v\n", mime2, mimeMatch))

	md5_1, _ := computeMD5(filePath1)
	md5_2, _ := computeMD5(filePath2)
	sameHash := md5_1 == md5_2
	sb.WriteString(fmt.Sprintf("file1 MD5: %s\n", md5_1))
	sb.WriteString(fmt.Sprintf("file2 MD5: %s | Match: %v\n\n", md5_2, sameHash))

	if sameHash {
		sb.WriteString("--- Files are identical (MD5 match) ---\n")
		return mcp.NewToolResultText(sb.String()), nil
	}

	diffOutput := runDiff(filePath1, filePath2, ignoreWhitespace, ignoreCase)

	if diffOutput == "" {
		sb.WriteString("--- No textual differences found (files may differ in binary content or metadata only) ---\n")
	} else {
		linesAdded := strings.Count(diffOutput, "+ ")
		linesRemoved := strings.Count(diffOutput, "- ")

		sb.WriteString(fmt.Sprintf("\n--- Unified Diff (%d lines added, %d lines removed) ---\n", linesAdded, linesRemoved))
		sb.WriteString(diffOutput)
	}

	return mcp.NewToolResultText(sb.String()), nil
}
