package main

import (
	"archive/tar"
	"archive/zip"
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

// ==================== Archive Helpers ====================

// resolveArchivePath parses a path like "archive.zip/subdir/file.txt" and returns
// the archive file path and the internal entry path.
func resolveArchivePath(path string) (string, string, bool) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return "", "", false
	}

	archiveFile := parts[0]
	entryPath := parts[1]

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
		if entry.IsDir {
			di, _ := writer.Create(name + "/")
			_ = di
		} else {
			w, err := writer.Create(name)
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
	defer f.Close()

	var writer io.Writer = f
	var gzWriter *gzip.Writer
	if compressed {
		gzWriter = gzip.NewWriter(f)
		writer = gzWriter
	}

	tarWriter := tar.NewWriter(writer)
	if err := tarWriter.Close(); err != nil {
		return fmt.Errorf("failed to finalize tar: %w", err)
	}

	if compressed && gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			return fmt.Errorf("failed to finalize gzip: %w", err)
		}
	}

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

			if d.IsDir() {
				info2, infoErr := d.Info()
				if infoErr != nil {
					return nil
				}
				archInfo.Entries[relPath+"/"] = ArchiveEntry{
					Name:    relPath + "/",
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
				archInfo.Entries[relPath] = ArchiveEntry{
					Name:    relPath,
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

	entry, ok := archInfo.Entries[entryPath]
	if !ok || entry.IsDir {
		return "", fmt.Errorf("entry not found in archive: %s", entryPath)
	}

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
