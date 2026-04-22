package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== CopyItem Tool Handler ====================

func handleCopyItem(_ context.Context, req mcp.CallToolRequest, store *fileStore, _ string) (*mcp.CallToolResult, error) {
	sourcePath, err := extractArg[string](req, "source")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	destPath, err := extractArg[string](req, "destination")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	overwrite := false
	if v, ok := extractOptionalBool(req, "overwrite"); ok {
		overwrite = v
	}

	pctx := GetGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	// Resolve paths with boundary check
	resolvedSource, err := resolvePathWithBoundaryCheck(pctx.Path, sourcePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("source path resolution failed: %v", err)), nil
	}

	resolvedDest, err := resolvePathWithBoundaryCheck(pctx.Path, destPath)
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

	var destExists bool
	var destInfo os.FileInfo
	if destInfo, err = os.Stat(resolvedDest); err == nil {
		destExists = true
		if !overwrite {
			return mcp.NewToolResultText(fmt.Sprintf("Destination already exists: %s\nSize: %s | Modified: %s | Type: %s\nSet overwrite=true to replace.\nSource: %s (%s, modified %s)", resolvedDest, humanReadableSize(destInfo.Size()), destInfo.ModTime().UTC().Format(time.RFC3339), detectMIMEType(resolvedDest), resolvedSource, humanReadableSize(srcInfo.Size()), srcInfo.ModTime().UTC().Format(time.RFC3339))), nil
		}
	}

	if srcInfo.IsDir() {
		startTime := time.Now()
		bytesCopied, err := copyDirectoryRecursive(resolvedSource, resolvedDest)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("copy failed: %v", err)), nil
		}
		elapsed := time.Since(startTime)

		destInfoNew, _ := os.Stat(resolvedDest)
		if destInfoNew != nil && destInfoNew.IsDir() {
			modTime := destInfoNew.ModTime().UTC().Format(time.RFC3339)
			store.upsertFile(resolvedDest, true, 0, modTime, "")
		}

		autoCommit(pctx.Path, "copy", resolvedSource)

		return mcp.NewToolResultText(fmt.Sprintf("Copied directory: %s -> %s\nSource: %s | Size: %s (%d bytes)\nDestination: %s | Bytes copied: %d | Operation time: %v", resolvedSource, resolvedDest, resolvedSource, humanReadableSize(srcInfo.Size()), srcInfo.Size(), resolvedDest, bytesCopied, elapsed)), nil
	}

	srcMIMEType := detectMIMEType(resolvedSource)
	srcSize := srcInfo.Size()
	srcModTime := srcInfo.ModTime().UTC().Format(time.RFC3339)
	srcPerms := formatPermissions(srcInfo)

	srcFile, err := os.Open(resolvedSource)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to open source: %v", err)), nil
	}
	defer srcFile.Close()

	destDir := filepath.Dir(resolvedDest)
	if _, err := os.Stat(destDir); os.IsNotExist(err) {
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to create destination directories: %v", err)), nil
		}
	}

	destFile, err := os.Create(resolvedDest)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create destination: %v", err)), nil
	}

	startTime := time.Now()
	bytesCopied, err := io.Copy(destFile, srcFile)
	if err != nil {
		destFile.Close()
		os.Remove(resolvedDest)
		return mcp.NewToolResultError(fmt.Sprintf("copy failed: %v", err)), nil
	}
	destFile.Close()
	elapsed := time.Since(startTime)

	srcMD5, _ := computeMD5(resolvedSource)
	destMD5, _ := computeMD5(resolvedDest)

	if destInfoNew, _ := os.Stat(resolvedDest); destInfoNew != nil && !destInfoNew.IsDir() {
		destModTime := destInfoNew.ModTime().UTC().Format(time.RFC3339)
		store.upsertFile(resolvedDest, false, bytesCopied, destModTime, destMD5)

		actionStr := "Copied"
		if destExists {
			actionStr = "Overwritten"
		}

		autoCommit(pctx.Path, "copy", resolvedSource)

		return mcp.NewToolResultText(fmt.Sprintf("%s file: %s -> %s\nSource: %s | Size: %s (%d bytes) | MIME: %s | Modified: %s | Permissions: %s | MD5: %s\nDestination: %s | Size: %s (%d bytes) | MIME: %s | Modified: %s | MD5: %s\nBytes copied: %d | Operation time: %v | Action: %s",
			actionStr, resolvedSource, resolvedDest,
			resolvedSource, humanReadableSize(srcSize), srcSize, srcMIMEType, srcModTime, srcPerms, srcMD5,
			resolvedDest, humanReadableSize(bytesCopied), bytesCopied, detectMIMEType(resolvedDest), destModTime, destMD5,
			bytesCopied, elapsed, actionStr)), nil
	}

	autoCommit(pctx.Path, "copy", resolvedSource)

	return mcp.NewToolResultText(fmt.Sprintf("Copied file: %s -> %s\nSource: %s | Size: %s (%d bytes) | MIME: %s | Modified: %s | Permissions: %s\nDestination: %s | Size: %s (%d bytes)\nBytes copied: %d | Operation time: %v",
		resolvedSource, resolvedDest,
		resolvedSource, humanReadableSize(srcSize), srcSize, srcMIMEType, srcModTime, srcPerms,
		resolvedDest, humanReadableSize(bytesCopied), bytesCopied,
		bytesCopied, elapsed)), nil
}
