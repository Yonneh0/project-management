package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== Search Tool Handler ====================

func handleSearch(_ context.Context, req mcp.CallToolRequest, _ *fileStore, _ string) (*mcp.CallToolResult, error) {
	searchPath, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pattern, err := extractArg[string](req, "pattern")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	mode := "name"
	if v, ok := extractOptionalString(req, "mode"); ok {
		mode = v
	}

	limit := 50
	if l, ok := extractOptionalInt(req, "limit"); ok && l > 0 {
		limit = l
	}

	maxMatchesPerFile := 10
	if m, ok := extractOptionalInt(req, "maxMatchesPerFile"); ok && m > 0 {
		maxMatchesPerFile = m
	}

	includeHidden := false
	if v, ok := extractOptionalBool(req, "includeHidden"); ok {
		includeHidden = v
	}

	fileOnly := false
	if v, ok := extractOptionalBool(req, "fileOnly"); ok {
		fileOnly = v
	}

	dirOnly := false
	if v, ok := extractOptionalBool(req, "dirOnly"); ok {
		dirOnly = v
	}

	var extensions []string
	if extStr, ok := extractOptionalString(req, "extensions"); ok && extStr != "" {
		for _, ext := range strings.Split(extStr, ",") {
			ext = strings.TrimSpace(ext)
			if ext != "" {
				extensions = append(extensions, ext)
			}
		}
	}

	contextLines := 0
	if c, ok := extractOptionalInt(req, "contextLines"); ok && c >= 0 {
		contextLines = c
	}

	pctx := GetGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	resolvedSearchPath, err := resolvePathWithBoundaryCheck(pctx.Path, searchPath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("search path resolution failed: %v", err)), nil
	}
	searchPath = resolvedSearchPath

	info, err := os.Stat(searchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return mcp.NewToolResultText(fmt.Sprintf("Search path not found: %s", searchPath)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("stat failed: %v", err)), nil
	}

	if !info.IsDir() {
		return mcp.NewToolResultError(fmt.Sprintf("Search path is a file: %s", searchPath)), nil
	}

	switch mode {
	case "grep":
		return searchGrepMode(searchPath, pattern, limit, includeHidden, fileOnly, dirOnly, extensions, contextLines, pctx.Path)
	case "regex":
		return searchRegexMode(searchPath, pattern, limit, includeHidden, fileOnly, dirOnly, extensions, pctx.Path)
	default:
		// "name" mode (default): search by filename matching
		type nameSearchResult struct {
			path     string
			isDir    bool
			info     os.FileInfo
			mimeType string
		}

		var results []nameSearchResult
		projectBase := pctx.Path
		err = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
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

			var fileInfo os.FileInfo
			mimeType := "unknown"
			if !d.IsDir() {
				fileInfo, _ = d.Info()
				if fileInfo != nil {
					mimeType = detectMIMEType(path)
				}
			}

			// Boundary check for nested paths
			if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(projectBase)+string(os.PathSeparator)) && filepath.Clean(path) != filepath.Clean(projectBase) {
				return nil
			}

			results = append(results, nameSearchResult{
				path:     path,
				isDir:    d.IsDir(),
				info:     fileInfo,
				mimeType: mimeType,
			})
			return nil
		})

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("search failed: %v", err)), nil
		}

		fileCount := 0
		dirCount := 0
		for _, r := range results {
			if r.isDir {
				dirCount++
			} else {
				fileCount++
			}
		}

		var sb strings.Builder
		sb.WriteString("=== Search Results ===\n")
		sb.WriteString(fmt.Sprintf("Root path: %s\n", searchPath))
		sb.WriteString(fmt.Sprintf("Pattern: '%s'\n", pattern))
		sb.WriteString(fmt.Sprintf("Mode: %s | Limit: %d | Max matches/file: %d | Context lines: %d\n", mode, limit, maxMatchesPerFile, contextLines))
		sb.WriteString(fmt.Sprintf("Total matches found: %d\n", len(results)))
		sb.WriteString(fmt.Sprintf("Breakdown: %d files, %d directories\n", fileCount, dirCount))

		if len(results) == 0 {
			sb.WriteString("\nNo matches found.\n")
		} else {
			displayResults := results
			shown := len(results)
			if len(results) > limit {
				displayResults = results[:limit]
				shown = limit
				sb.WriteString(fmt.Sprintf("Showing first %d of %d results (limit: %d)\n\n", shown, len(results), limit))
			} else {
				sb.WriteString(fmt.Sprintf("\nAll %d results shown:\n\n", shown))
			}

			for _, r := range displayResults {
				typeStr := "F"
				sizeStr := ""
				if r.isDir {
					typeStr = "D"
				} else if r.info != nil {
					sizeStr = fmt.Sprintf(" | Size: %s", humanReadableSize(r.info.Size()))
				}
				mimeStr := ""
				if !r.isDir && r.mimeType != "unknown" {
					mimeStr = fmt.Sprintf(" | MIME: %s", r.mimeType)
				}
				sb.WriteString(fmt.Sprintf("[%s] %s%s%s\n", typeStr, r.path, sizeStr, mimeStr))
			}
		}

		return mcp.NewToolResultText(sb.String()), nil
	}
}

// ==================== Search Mode Helpers ====================

func searchGrepMode(searchPath, pattern string, limit int, includeHidden bool, fileOnly bool, dirOnly bool, extensions []string, contextLines int, projectBase string) (*mcp.CallToolResult, error) {
	var sb strings.Builder
	sb.WriteString("=== Grep Search Results ===\n")
	sb.WriteString(fmt.Sprintf("Root path: %s\n", searchPath))
	sb.WriteString(fmt.Sprintf("Pattern: '%s'\n", pattern))

	type grepMatch struct {
		filePath string
		lineNum  int
		content  string
	}

	var matches []grepMatch
	matchCount := 0

	err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if fileOnly {
				return filepath.SkipDir
			}
			name := d.Name()
			if !includeHidden && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		if dirOnly {
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

		// Boundary check for nested paths
		if projectBase != "" && !strings.HasPrefix(filepath.Clean(path), filepath.Clean(projectBase)+string(os.PathSeparator)) && filepath.Clean(path) != filepath.Clean(projectBase) {
			return nil
		}

		fileContent, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(fileContent), "\n")
		for i, line := range lines {
			if strings.Contains(line, pattern) {
				matches = append(matches, grepMatch{filePath: path, lineNum: i + 1, content: line})
				matchCount++
				if matchCount >= limit {
					return filepath.SkipAll
				}
			}
		}

		return nil
	})

	if err != nil && !strings.Contains(err.Error(), "SkipAll") {
		return mcp.NewToolResultError(fmt.Sprintf("grep search failed: %v", err)), nil
	}

	fileCount := 0
	seenFiles := make(map[string]bool)
	for _, m := range matches {
		if !seenFiles[m.filePath] {
			fileCount++
			seenFiles[m.filePath] = true
		}
	}

	sb.WriteString(fmt.Sprintf("Total matches: %d (in %d files)\n", matchCount, fileCount))

	if len(matches) == 0 {
		sb.WriteString("\nNo matches found.\n")
	} else {
		shown := limit
		if len(matches) < limit {
			shown = len(matches)
		}

		sb.WriteString(fmt.Sprintf("Showing first %d of %d results (limit: %d)\n\n", shown, matchCount, limit))

		for i := 0; i < shown && i < len(matches); i++ {
			m := matches[i]
			contextStart := m.lineNum - contextLines
			if contextStart < 1 {
				contextStart = 1
			}
			sb.WriteString(fmt.Sprintf("%s:%d: %s\n", m.filePath, m.lineNum, m.content))
		}
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func searchRegexMode(searchPath, pattern string, limit int, includeHidden bool, fileOnly bool, dirOnly bool, extensions []string, projectBase string) (*mcp.CallToolResult, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid regex: %v", err)), nil
	}

	type searchResult struct {
		path     string
		isDir    bool
		info     os.FileInfo
		mimeType string
	}

	var results []searchResult

	err = filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
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
		if !matched {
			return nil
		}

		// Boundary check for nested paths
		if projectBase != "" && !strings.HasPrefix(filepath.Clean(path), filepath.Clean(projectBase)+string(os.PathSeparator)) && filepath.Clean(path) != filepath.Clean(projectBase) {
			return nil
		}

		var fileInfo os.FileInfo
		mimeType := "unknown"
		if !d.IsDir() {
			fileInfo, _ = d.Info()
			if fileInfo != nil {
				mimeType = detectMIMEType(path)
			}
		}

		results = append(results, searchResult{
			path:     path,
			isDir:    d.IsDir(),
			info:     fileInfo,
			mimeType: mimeType,
		})

		return nil
	})

	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("regex search failed: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString("=== Regex Search Results ===\n")
	sb.WriteString(fmt.Sprintf("Root path: %s\n", searchPath))
	sb.WriteString(fmt.Sprintf("Pattern: '%s' (Go regex)\n", pattern))
	sb.WriteString(fmt.Sprintf("Total matches found: %d\n\n", len(results)))

	fileCount := 0
	dirCount := 0
	for _, r := range results {
		if r.isDir {
			dirCount++
		} else {
			fileCount++
		}
	}
	sb.WriteString(fmt.Sprintf("Breakdown: %d files, %d directories\n", fileCount, dirCount))

	shown := limit
	if len(results) < limit {
		shown = len(results)
	}

	for i := 0; i < shown && i < len(results); i++ {
		r := results[i]
		typeStr := "F"
		sizeStr := ""
		if r.isDir {
			typeStr = "D"
		} else if r.info != nil {
			sizeStr = fmt.Sprintf(" | Size: %s", humanReadableSize(r.info.Size()))
		}
		mimeStr := ""
		if !r.isDir && r.mimeType != "unknown" {
			mimeStr = fmt.Sprintf(" | MIME: %s", r.mimeType)
		}
		sb.WriteString(fmt.Sprintf("[%s] %s%s%s\n", typeStr, r.path, sizeStr, mimeStr))
	}

	if len(results) > limit {
		sb.WriteString(fmt.Sprintf("\n... (%d additional results hidden, limit: %d)", len(results)-limit, limit))
	}

	return mcp.NewToolResultText(sb.String()), nil
}
