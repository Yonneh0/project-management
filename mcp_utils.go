package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ==================== Utility Functions ====================

func detectMIMEType(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unknown"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return "unknown"
	}
	buf = buf[:n]

	signatures := []struct {
		magic    string
		mimeType string
	}{
		{"\x89PNG\r\n\x1a\n", "image/png"},
		{"\xff\xd8\xff", "image/jpeg"},
		{"GIF87a", "image/gif"},
		{"GIF89a", "image/gif"},
		{"PK\x03\x04", "application/zip"},
		{"%PDF-", "application/pdf"},
		{"\x1f\x8b", "application/gzip"},
		{"Rar!\x1a\x07\x00", "application/x-rar-compressed"},
		{"Rar!\x1a\x07\x01\x00", "application/x-rar-compressed"},
		{"<html", "text/html"},
	}

	for _, sig := range signatures {
		if strings.HasPrefix(string(buf), sig.magic) {
			return sig.mimeType
		}
	}

	contentType := http.DetectContentType(buf)
	return contentType
}

func humanReadableSize(size int64) string {
	if size < 0 {
		return "unknown"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	sizeFloat := float64(size)
	if sizeFloat <= 1 {
		return fmt.Sprintf("%d B", size)
	}
	log := math.Log2(sizeFloat) / math.Log2(1024)
	index := int(log)
	if index >= len(units) {
		index = len(units) - 1
	}
	value := sizeFloat / math.Pow(1024, float64(index))
	return fmt.Sprintf("%.2f %s", value, units[index])
}

func formatPermissions(info os.FileInfo) string {
	mode := info.Mode()
	var parts []string
	if mode.IsDir() {
		parts = append(parts, "d")
	} else {
		parts = append(parts, "-")
	}

	parts = append(parts, formatTriple(uint32(mode.Perm())>>6, "rwx"))
	parts = append(parts, formatTriple(uint32(mode.Perm())>>3, "rwx"))
	parts = append(parts, formatTriple(uint32(mode.Perm()), "rwx"))

	return strings.Join(parts, "")
}

func formatTriple(perm uint32, chars string) string {
	var s string
	for i := 0; i < 3; i++ {
		if (perm & 1) == 1 {
			s += string(chars[2-i])
		} else {
			s += "-"
		}
		perm >>= 1
	}
	return s
}

func getUnixPermissions(info os.FileInfo) string {
	return fmt.Sprintf("%04o", info.Mode().Perm())
}

// generateCommitMessage creates an auto-commit message for MCP operations.
func generateCommitMessage(operation, path string) string {
	baseName := filepath.Base(path)
	return fmt.Sprintf("MCP: %s %s", strings.ToUpper(operation), baseName)
}

// commitChanges commits all changes in the project directory with a generated message.
func commitChanges(projectPath, message string) error {
	gitDir := filepath.Join(projectPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return nil // no git repo, skip
	}

	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = projectPath
	if err := addCmd.Run(); err != nil {
		// Try from parent if needed
		addCmd = exec.Command("git", "add", "-A")
		addCmd.Dir = filepath.Dir(projectPath)
		if err := addCmd.Run(); err != nil {
			return fmt.Errorf("git add failed: %w", err)
		}
	}

	commitCmd := exec.Command("git", "commit", "-m", message)
	commitCmd.Dir = projectPath
	if err := commitCmd.Run(); err != nil {
		// Check if there's nothing to commit (common on first commit with no changes)
		return nil
	}

	return nil
}

func expectedMIMEFromExt(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".js", ".mjs":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".md":
		return "text/markdown"
	case ".txt":
		return "text/plain"
	case ".yaml", ".yml":
		return "application/x-yaml"
	case ".csv":
		return "text/csv"
	case ".sh":
		return "text/x-shellscript"
	case ".bat", ".cmd":
		return "text/x-windows-bat"
	default:
		return ""
	}
}

func runDiff(file1, file2 string, ignoreWhitespace, ignoreCase bool) string {
	var args []string
	args = append(args, "-u")
	if ignoreWhitespace {
		args = append(args, "-w")
	}
	if ignoreCase {
		args = append(args, "--ignore-case")
	}
	args = append(args, "--label", file1, "--label", file2, file1, file2)

	cmd := exec.Command("diff", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// diff returns exit code 1 when files differ, which is expected
		exitCode := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		if exitCode == 1 {
			return string(output)
		}
		var fallbackArgs []string
		if ignoreWhitespace {
			fallbackArgs = append(fallbackArgs, "-w")
		}
		if ignoreCase {
			fallbackArgs = append(fallbackArgs, "--ignore-case")
		}
		fallbackArgs = append(fallbackArgs, "--label", file1, "--label", file2, file1, file2)
		cmd = exec.Command("diff", fallbackArgs...)
		output, _ = cmd.CombinedOutput()
		if len(output) > 0 {
			return string(output)
		}
		return "(diff output unavailable - files differ)"
	}
	return string(output)
}

func formatDiff(diff int64) string {
	if diff == 0 {
		return "identical"
	}
	sign := "+"
	if diff < 0 {
		sign = "-"
		diff = -diff
	}
	return fmt.Sprintf("%s%s (%d bytes)", sign, humanReadableSize(diff), diff)
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "identical"
	}
	sign := "+"
	if d < 0 {
		sign = "-"
		d = -d
	}
	return fmt.Sprintf("%s%s", sign, d.Round(time.Second))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ==================== Hex Dump Utilities ====================

// toHexDump converts raw bytes to a formatted hex dump string with address,
// hex byte values, and ASCII representation.
func toHexDump(data []byte) string {
	if len(data) == 0 {
		return "(empty file)\n"
	}

	var sb strings.Builder
	sb.WriteString("Offset      00 01 02 03 04 05 06 07 08 09 0A 0B 0C 0D 0E 0F  ASCII\n")

	for i := 0; i < len(data); i += 16 {
		// Offset
		sb.WriteString(fmt.Sprintf("%08X    ", i))

		end := i + 16
		if end > len(data) {
			end = len(data)
		}

		// Hex bytes
		for j := 0; j < 16; j++ {
			if i+j < len(data) {
				sb.WriteString(fmt.Sprintf("%02X ", data[i+j]))
			} else {
				sb.WriteString("   ")
			}
			if j == 7 {
				sb.WriteString(" ")
			}
		}

		// ASCII representation
		sb.WriteString("  |")
		for j := i; j < end; j++ {
			b := data[j]
			if b >= 0x20 && b < 0x7E {
				sb.WriteByte(b)
			} else {
				sb.WriteString(".")
			}
		}
		sb.WriteString("|\n")
	}

	return sb.String()
}

// fromHexDump parses a hex dump string (with or without formatting) back into raw bytes.
// It accepts:
//   - Raw hex strings like "4D5A9000..."
//   - Formatted hex dumps with addresses, spaces, and ASCII columns
func fromHexDump(hexString string) ([]byte, error) {
	// First, try to parse as a raw hex string (no whitespace/formatting)
	cleaned := strings.TrimSpace(hexString)

	// Check if it looks like a formatted hex dump (contains "  |" or address patterns)
	if strings.Contains(cleaned, "|") || strings.Contains(cleaned, "  ") {
		return parseFormattedHexDump(cleaned)
	}

	// Try raw hex string parsing
	cleaned = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == ':' {
			return -1
		}
		return r
	}, cleaned)

	if len(cleaned) == 0 {
		return nil, fmt.Errorf("empty hex string")
	}

	if len(cleaned)%2 != 0 {
		return nil, fmt.Errorf("hex string has odd length: %d", len(cleaned))
	}

	var result []byte
	for i := 0; i < len(cleaned); i += 2 {
		var b byte
		_, err := fmt.Sscanf(cleaned[i:i+2], "%02x", &b)
		if err != nil {
			return nil, fmt.Errorf("invalid hex byte at position %d: %w", i, err)
		}
		result = append(result, b)
	}

	return result, nil
}

// parseFormattedHexDump extracts byte values from a formatted hex dump output.
func parseFormattedHexDump(hexString string) ([]byte, error) {
	var result []byte
	lines := strings.Split(hexString, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Offset") {
			continue
		}

		// Find the hex bytes section (after the offset, before the ASCII column)
		asciiIdx := strings.Index(line, "|")
		hexSection := line
		if asciiIdx > 0 {
			hexSection = line[:asciiIdx]
		} else if idx := strings.Index(line, "  "); idx > 0 {
			parts := strings.SplitN(line, "  ", 2)
			if len(parts) == 2 {
				hexSection = parts[1]
			}
		}

		// Extract hex bytes from the line
		hexSection = strings.TrimSpace(hexSection)
		parts := strings.Fields(hexSection)

		for _, part := range parts {
			if len(part) != 2 {
				continue
			}
			var b byte
			_, err := fmt.Sscanf(part, "%02x", &b)
			if err == nil && b != 0 || (err == nil && part != "   ") {
				result = append(result, b)
			}
		}
	}

	return result, nil
}

// ==================== Directory Copy Helper ====================

func copyDirectoryRecursive(srcPath, destPath string) (int64, error) {
	var totalBytes int64

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return 0, err
	}

	if err := os.MkdirAll(destPath, srcInfo.Mode()); err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(srcPath)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		srcEntryPath := filepath.Join(srcPath, entry.Name())
		destEntryPath := filepath.Join(destPath, entry.Name())

		if entry.IsDir() {
			bytes, err := copyDirectoryRecursive(srcEntryPath, destEntryPath)
			if err != nil {
				return totalBytes, err
			}
			totalBytes += bytes
		} else {
			fileInfo, _ := entry.Info()
			if fileInfo == nil {
				continue
			}

			srcFile, err := os.Open(srcEntryPath)
			if err != nil {
				return totalBytes, err
			}

			destFile, err := os.Create(destEntryPath)
			if err != nil {
				srcFile.Close()
				return totalBytes, err
			}

			bytesCopied, err := io.Copy(destFile, srcFile)
			destFile.Close()
			srcFile.Close()
			if err != nil {
				return totalBytes, err
			}
			totalBytes += bytesCopied
		}
	}

	return totalBytes, nil
}

// ==================== Archive Sandbox Helpers ====================

// sanitizeArchiveEntryPath cleans an archive entry path to prevent directory traversal.
// It replaces backslashes with forward slashes, resolves . and .. segments,
// and ensures the path doesn't escape the archive's parent directory.
func sanitizeArchiveEntryPath(entryName string) (string, error) {
	// Replace backslashes with forward slashes for cross-platform consistency
	entryName = strings.ReplaceAll(entryName, "\\", "/")

	// Clean the path (resolves . and .. segments)
	cleaned := filepath.Clean(entryName)

	// On Windows, filepath.Clean converts / to \, so normalize back to /
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")

	// Ensure it doesn't start with ../ or ..\
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("entry path escapes archive: %s", entryName)
	}

	return cleaned, nil
}

// validateInSandbox checks that a path is within the project sandbox.
func validateInSandbox(projectPath, checkPath string) error {
	absProject := filepath.Clean(projectPath)
	absCheck := filepath.Clean(checkPath)

	if absCheck == absProject {
		return nil // Exact match is valid
	}

	// Normalize both paths to forward slash for prefix checking
	absProject = strings.ReplaceAll(absProject, "\\", "/")
	absCheck = strings.ReplaceAll(absCheck, "\\", "/")

	if strings.HasPrefix(absCheck, absProject+"/") {
		return nil // Within sandbox (nested under project)
	}

	return fmt.Errorf("path '%s' escapes project sandbox '%s'", checkPath, projectPath)
}

// ==================== Archive Helpers ====================

// resolveArchivePath parses a path like "archive.zip/subdir/file.txt" and returns
// the archive file path and the internal entry path.
func resolveArchivePath(path string) (string, string, bool) {
	// Find the correct split point for archive paths.
	// On Windows, simple SplitN(path, "/", 2) fails for paths like "C:/Projects/test.zip/test.txt"
	// because it splits on the first "/" after the drive letter, giving ["C:", "Projects/..."].
	// We need to find a separator (either / or \) that comes after an archive file extension.

	archiveFile := ""
	entryPath := ""

	// Search for the rightmost separator that comes after an archive extension.
	// We look for patterns like ".zip/", ".tar.gz/", ".gz/", ".rar/", ".7z/" etc.
	// Also handle Windows backslash separators: ".zip\", ".tar.gz\", etc.
	archiveExts := []string{".zip", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".gz", ".bz2", ".rar", ".7z", ".xz"}

	bestSplitIdx := -1
	for _, ext := range archiveExts {
		// Check forward slash separator
		searchStrFwd := ext + "/"
		idxFwd := strings.Index(path, searchStrFwd)
		if idxFwd > 0 {
			splitPoint := idxFwd + len(searchStrFwd) - 1 // position of the "/"
			if splitPoint > bestSplitIdx {
				bestSplitIdx = splitPoint
			}
		}

		// Check backslash separator (Windows paths)
		searchStrBak := ext + "\\"
		idxBak := strings.Index(path, searchStrBak)
		if idxBak > 0 {
			splitPoint := idxBak + len(searchStrBak) - 1 // position of the "\"
			if splitPoint > bestSplitIdx {
				bestSplitIdx = splitPoint
			}
		}
	}

	if bestSplitIdx >= 0 {
		archiveFile = path[:bestSplitIdx]
		entryPath = path[bestSplitIdx+1:]
	} else {
		// Fallback: try simple split on first "/" or "\" (works for Unix-style paths without drive letters)
		parts := strings.SplitN(path, "/", 2)
		if len(parts) >= 2 {
			archiveFile = parts[0]
			entryPath = parts[1]
		} else {
			parts = strings.SplitN(path, "\\", 2)
			if len(parts) < 2 {
				return "", "", false
			}
			archiveFile = parts[0]
			entryPath = parts[1]
		}
	}

	// Make archive file absolute if needed
	if !filepath.IsAbs(archiveFile) {
		if globalProject != nil && globalProject.Path != "" {
			archiveFile = filepath.Join(globalProject.Path, archiveFile)
		}
	}

	archiveFile = filepath.Clean(archiveFile)

	if IsArchiveFile(archiveFile) {
		return archiveFile, entryPath, true
	}
	return "", "", false
}

// openArchive loads an archive into memory.
func openArchive(archivePath string) (*ArchiveInfo, error) {
	// Check cache first
	archiveMu.RLock()
	if cached, ok := archiveCache[archivePath]; ok && cached.IsOpen {
		archiveMu.RUnlock()
		return cached, nil
	}
	archiveMu.RUnlock()

	format := GetArchiveFormat(archivePath)
	if format == "" {
		return nil, fmt.Errorf("unsupported archive format")
	}

	info := &ArchiveInfo{
		Path:    archivePath,
		Entries: make(map[string]ArchiveEntry),
		Format:  format,
		IsOpen:  true,
	}

	switch format {
	case "zip":
		err := loadZipArchive(info)
		if err != nil {
			return nil, fmt.Errorf("failed to open zip archive: %w", err)
		}
	case "tar":
		err := loadTarArchive(info, archivePath, false)
		if err != nil {
			return nil, fmt.Errorf("failed to open tar archive: %w", err)
		}
	case "tar.gz":
		err := loadTarArchive(info, archivePath, true)
		if err != nil {
			return nil, fmt.Errorf("failed to open tar.gz archive: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", format)
	}

	archiveMu.Lock()
	archiveCache[archivePath] = info
	archiveMu.Unlock()

	return info, nil
}

// loadZipArchive loads entries from a zip file.
func loadZipArchive(info *ArchiveInfo) error {
	reader, err := zip.OpenReader(info.Path)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		entry := ArchiveEntry{
			Name:    f.Name,
			IsDir:   f.FileInfo().IsDir(),
			ModTime: f.ModTime(),
		}

		if !f.FileInfo().IsDir() {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			entry.Content, _ = io.ReadAll(rc)
			rc.Close()
		}

		info.Entries[f.Name] = entry
	}

	return nil
}

// loadTarArchive loads entries from a tar or tar.gz file.
func loadTarArchive(info *ArchiveInfo, archivePath string, compressed bool) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	if compressed {
		reader, err = gzip.NewReader(reader)
		if err != nil {
			return err
		}
	}

	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		entry := ArchiveEntry{
			Name:    header.Name,
			IsDir:   header.Typeflag == tar.TypeDir,
			ModTime: header.ModTime,
		}

		if header.Typeflag == tar.TypeReg {
			entry.Content, _ = io.ReadAll(tarReader)
		}

		info.Entries[header.Name] = entry
	}

	return nil
}

// saveArchive writes the archive info back to disk.
func saveArchive(info *ArchiveInfo) error {
	switch info.Format {
	case "zip":
		return saveZipArchive(info)
	case "tar":
		return saveTarArchive(info, false)
	case "tar.gz":
		return saveTarArchive(info, true)
	default:
		return fmt.Errorf("unsupported archive format for saving: %s", info.Format)
	}
}

// saveZipArchive writes entries to a zip file.
func saveZipArchive(info *ArchiveInfo) error {
	// Create temp file
	tmpFile := info.Path + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return err
	}

	writer := zip.NewWriter(f)

	for name, entry := range info.Entries {
		// Sanitize entry names when writing to prevent traversal in saved archives
		sanitizedName, sanitizeErr := sanitizeArchiveEntryPath(name)
		if sanitizeErr != nil {
			continue // skip entries with invalid paths
		}

		if entry.IsDir {
			di, _ := writer.Create(sanitizedName + "/")
			_ = di
		} else {
			w, err := writer.Create(sanitizedName)
			if err != nil {
				continue
			}
			w.Write(entry.Content)
		}
	}

	if err := writer.Close(); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return err
	}

	f.Close()
	return os.Rename(tmpFile, info.Path)
}

// saveTarArchive writes entries to a tar or tar.gz file.
func saveTarArchive(info *ArchiveInfo, compressed bool) error {
	tmpFile := info.Path + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return err
	}

	var gzWriter *gzip.Writer
	var tarWriter *tar.Writer

	if compressed {
		gzWriter = gzip.NewWriter(f)
		tarWriter = tar.NewWriter(gzWriter)
	} else {
		tarWriter = tar.NewWriter(f)
	}

	for name, entry := range info.Entries {
		// Sanitize entry names when writing to prevent traversal in saved archives
		sanitizedName, sanitizeErr := sanitizeArchiveEntryPath(name)
		if sanitizeErr != nil {
			continue // skip entries with invalid paths
		}

		header := &tar.Header{
			Name:     sanitizedName,
			Mode:     0644,
			ModTime:  entry.ModTime,
			Size:     int64(len(entry.Content)),
			Typeflag: tar.TypeReg,
		}

		if entry.IsDir {
			header.Typeflag = tar.TypeDir
			header.Mode = 0755
			header.Name = sanitizedName + "/"
		} else if len(entry.Content) == 0 && !entry.IsDir {
			continue // skip empty non-dir entries
		}

		if err := tarWriter.WriteHeader(header); err != nil {
			continue
		}

		if !entry.IsDir && len(entry.Content) > 0 {
			tarWriter.Write(entry.Content)
		}
	}

	// Close tar writer first to flush all data
	if err := tarWriter.Close(); err != nil {
		f.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("failed to finalize tar: %w", err)
	}

	// Close gzip writer second to flush compression data
	if compressed && gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			f.Close()
			os.Remove(tmpFile)
			return fmt.Errorf("failed to finalize gzip: %w", err)
		}
	}

	// Close the underlying file after tar/gzip writers are closed
	if err := f.Close(); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to close archive file: %w", err)
	}

	// Now safe to rename on Windows (no file handles open)
	return os.Rename(tmpFile, info.Path)
}

// listArchiveEntries returns a formatted listing of archive contents.
func listArchiveEntries(info *ArchiveInfo) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== Archive: %s ===\n", filepath.Base(info.Path)))
	sb.WriteString(fmt.Sprintf("Format: %s | Entries: %d\n\n", info.Format, len(info.Entries)))

	fileCount := 0
	dirCount := 0
	for name, entry := range info.Entries {
		if entry.IsDir {
			dirCount++
			sb.WriteString(fmt.Sprintf("[D] %s/\n", name))
		} else {
			fileCount++
			sb.WriteString(fmt.Sprintf("[F] %s (%d bytes)\n", name, len(entry.Content)))
		}
	}

	sb.WriteString(fmt.Sprintf("\nTotal: %d files, %d directories\n", fileCount, dirCount))
	return sb.String()
}

// readArchiveFile reads content from an archive entry.
func readArchiveFile(info *ArchiveInfo, entryPath string) ([]byte, bool) {
	entry, ok := info.Entries[entryPath]
	if !ok || entry.IsDir {
		return nil, false
	}
	return entry.Content, true
}

// writeArchiveFile writes/updates an entry in the archive.
func writeArchiveFile(info *ArchiveInfo, entryPath string, content []byte) error {
	info.Entries[entryPath] = ArchiveEntry{
		Name:    entryPath,
		Content: content,
		IsDir:   false,
		ModTime: time.Now(),
	}
	return saveArchive(info)
}

// deleteArchiveEntry removes an entry from the archive.
func deleteArchiveEntry(info *ArchiveInfo, entryPath string) bool {
	if _, ok := info.Entries[entryPath]; !ok {
		return false
	}
	delete(info.Entries, entryPath)
	return saveArchive(info) == nil
}

// compressToArchive compresses a file or directory into an archive.
func compressToArchive(sourcePath string, archiveDest string, deleteOriginal bool) (string, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("source not found: %s", sourcePath)
	}

	// Open or create the destination archive
	var archInfo *ArchiveInfo
	if existing, err := openArchive(archiveDest); err == nil {
		archInfo = existing
	} else {
		archInfo = &ArchiveInfo{
			Path:    archiveDest,
			Entries: make(map[string]ArchiveEntry),
			Format:  GetArchiveFormat(archiveDest),
			IsOpen:  true,
		}
	}

	if archInfo.Format == "" {
		return "", fmt.Errorf("unsupported archive format for: %s", archiveDest)
	}

	if info.IsDir() {
		// Add directory contents to archive
		err = filepath.WalkDir(sourcePath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			relPath, relErr := filepath.Rel(filepath.Dir(sourcePath), path)
			if relErr != nil {
				relPath = filepath.Base(path)
			}

			// Sanitize entry name to prevent sandbox escapes in archive
			sanitizedName, sanitizeErr := sanitizeArchiveEntryPath(relPath)
			if sanitizeErr != nil {
				return nil // skip entries that escape (e.g., symlinks)
			}

			if d.IsDir() {
				info2, infoErr := d.Info()
				if infoErr != nil {
					return nil
				}
				archInfo.Entries[sanitizedName+"/"] = ArchiveEntry{
					Name:    sanitizedName + "/",
					IsDir:   true,
					ModTime: info2.ModTime(),
				}
			} else {
				content, readErr := os.ReadFile(path)
				if readErr != nil {
					return nil // skip unreadable files
				}
				info2, infoErr := d.Info()
				if infoErr != nil {
					return nil
				}
				archInfo.Entries[sanitizedName] = ArchiveEntry{
					Name:    sanitizedName,
					Content: content,
					IsDir:   false,
					ModTime: info2.ModTime(),
				}
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("failed to add directory to archive: %w", err)
		}
	} else {
		// Add single file to archive
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return "", fmt.Errorf("failed to read source file: %w", err)
		}
		baseName := filepath.Base(sourcePath)
		archInfo.Entries[baseName] = ArchiveEntry{
			Name:    baseName,
			Content: content,
			IsDir:   false,
			ModTime: info.ModTime(),
		}
	}

	if err := saveArchive(archInfo); err != nil {
		return "", fmt.Errorf("failed to save archive: %w", err)
	}

	resultMsg := fmt.Sprintf("Compressed '%s' → %s", filepath.Base(sourcePath), filepath.Base(archiveDest))
	if deleteOriginal {
		os.RemoveAll(sourcePath)
		resultMsg += ", original deleted"
	}

	return resultMsg, nil
}

// extractFromArchive extracts an archive entry to the filesystem.
func extractFromArchive(archivePath string, entryPath string, destPath string) (string, error) {
	archInfo, err := openArchive(archivePath)
	if err != nil {
		return "", fmt.Errorf("failed to open archive: %w", err)
	}

	// Sanitize the entry path to prevent directory traversal
	sanitizedEntry, err := sanitizeArchiveEntryPath(entryPath)
	if err != nil {
		return "", fmt.Errorf("entry path validation failed: %w", err)
	}

	entry, ok := archInfo.Entries[sanitizedEntry]
	if !ok || entry.IsDir {
		return "", fmt.Errorf("entry not found in archive: %s", entryPath)
	}

	// Validate destination is within sandbox (destPath should be validated by caller via resolvePathWithBoundaryCheck)
	// Ensure destination directory exists
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	if err := os.WriteFile(destPath, entry.Content, 0644); err != nil {
		return "", fmt.Errorf("failed to write extracted file: %w", err)
	}

	return fmt.Sprintf("Extracted '%s' from %s → %s", entryPath, filepath.Base(archivePath), destPath), nil
}

// ==================== Binary Search/Replace Helpers ====================

// binaryFindBytes finds all occurrences of needle in haystack and returns their byte offsets.
func binaryFindBytes(haystack, needle []byte) []int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return nil
	}

	var positions []int
	searchStart := 0
	for {
		idx := bytes.Index(haystack[searchStart:], needle)
		if idx == -1 {
			break
		}
		positions = append(positions, searchStart+idx)
		searchStart += idx + 1
	}

	return positions
}

// binaryReplaceN replaces up to n occurrences of oldData with newData in content.
func binaryReplaceN(content, oldData, newData []byte, n int) ([]byte, int) {
	if len(oldData) == 0 {
		return content, 0
	}

	result := content
	replacedCount := 0
	searchStart := 0

	for replacedCount < n {
		idx := bytes.Index(result[searchStart:], oldData)
		if idx == -1 {
			break
		}

		absIdx := searchStart + idx
		newResult := make([]byte, 0, len(result)-len(oldData)+len(newData))
		newResult = append(newResult, result[:absIdx]...)
		newResult = append(newResult, newData...)
		newResult = append(newResult, result[absIdx+len(oldData):]...)
		result = newResult
		replacedCount++
		searchStart = absIdx + len(newData)
	}

	return result, replacedCount
}
