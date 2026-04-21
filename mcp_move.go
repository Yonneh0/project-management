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

// ==================== MoveItem Tool Handler ====================

func handleMoveItem(ctx context.Context, req mcp.CallToolRequest, store *fileStore, rootDir string) (*mcp.CallToolResult, error) {
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

	srcSize := srcInfo.Size()
	srcModTime := srcInfo.ModTime().UTC().Format(time.RFC3339)
	srcMD5, _ := computeMD5(resolvedSource)

	if _, err = os.Stat(resolvedDest); err == nil {
		if !overwrite {
			return mcp.NewToolResultText(fmt.Sprintf("Destination exists: %s\nSet overwrite=true to replace.\nSource: %s (%s, modified %s)", resolvedDest, resolvedSource, humanReadableSize(srcSize), srcModTime)), nil
		}
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

			store.deleteFile(resolvedSource)
			destInfo3, _ := os.Stat(resolvedDest)
			if destInfo3 != nil && destInfo3.IsDir() {
				newModTime := destInfo3.ModTime().UTC().Format(time.RFC3339)
				store.upsertFile(resolvedDest, true, 0, newModTime, "")
			}

			autoCommit(pctx.Path, "move", resolvedSource)

			return mcp.NewToolResultText(fmt.Sprintf("%s directory: %s -> %s\nType: %s | Size: %s (%d bytes)\nOriginal modified: %s | MD5: %s\nIndex updated: old entry removed, new entry added", moveType, resolvedSource, resolvedDest, moveType, humanReadableSize(srcSize), srcSize, srcModTime, srcMD5)), nil
		}

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

	store.deleteFile(resolvedSource)
	destInfo3, _ := os.Stat(resolvedDest)
	if destInfo3 != nil && !destInfo3.IsDir() {
		newMD5, _ := computeMD5(resolvedDest)
		newModTime := destInfo3.ModTime().UTC().Format(time.RFC3339)
		store.upsertFile(resolvedDest, false, destInfo3.Size(), newModTime, newMD5)
	}

	autoCommit(pctx.Path, "move", resolvedSource)

	return mcp.NewToolResultText(fmt.Sprintf("%s: %s -> %s\nType: %s | Size: %s (%d bytes)\nOriginal modified: %s | MD5: %s\nIndex updated: old entry removed, new entry added", moveType, resolvedSource, resolvedDest, moveType, humanReadableSize(srcSize), srcSize, srcModTime, srcMD5)), nil
}
