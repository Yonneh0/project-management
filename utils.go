package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	MaxOutputLength      = 10000            // Maximum shell output length before truncation
	MaxResponseBodyLen   = 10000            // Maximum HTTP response body length before truncation
	DefaultMaxItems      = 100              // Default maximum directory entries to return
	DefaultShellTimeout  = 30               // Default shell command timeout in seconds
	DefaultGetURLTimeout = 30               // Default HTTP request timeout in seconds
	MaxFileSize          = 10 * 1024 * 1024 // Maximum file size before read rejection (10 MB)

	// ProgressThreshold is the file size (in bytes) below which progress indicators are hidden.
	// Files >= this size will show progress when showProgress=true.
	ProgressThreshold = 1 * 1024 * 1024 // 1 MB

	// LargeFileThreshold is the file size (in bytes) that triggers a "large file" hint.
	// Files >= this size will include a hint suggesting chunked operations.
	LargeFileThreshold = 5 * 1024 * 1024 // 5 MB

	// ProgressUpdateInterval is the percentage interval at which progress updates are displayed.
	ProgressUpdateInterval = 5 // Update every 5 percent
)

// humanReadableSize converts a byte count to a human-readable string (e.g., "1.50 MB").
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

// formatPermissions returns a 10-character permission string (e.g., "drwxr-xr-x").
// Note: On Windows, permission bits are simulated — may not reflect actual permissions.
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

// formatPermissionsFull returns formatted permissions with a Windows compatibility note.
// If no execute bits are set (common on Windows), adds "(incomplete on Windows)" note.
func formatPermissionsFull(info os.FileInfo) string {
	perms := info.Mode().Perm()
	hasExecuteBits := uint32(perms)&WindowsExecuteBits != 0
	if !hasExecuteBits {
		return fmt.Sprintf("%s (incomplete on Windows)", formatPermissions(info))
	}
	return formatPermissions(info)
}

// toHexDump converts bytes to a hex dump format with ASCII preview.
// Default: 16 bytes per row, addresses shown. Use toHexDumpWithConfig for configurable output.
func toHexDump(data []byte) string {
	return toHexDumpWithConfig(data, 16, true)
}

// toHexDumpWithConfig generates a hex dump with configurable row width and address display.
func toHexDumpWithConfig(data []byte, bytesPerRow int, showAddresses bool) string {
	if len(data) == 0 {
		return "(empty file)\n"
	}
	var sb strings.Builder

	// Header row: "XX XX XX ... |ASCII|"
	for i := 0; i < bytesPerRow; i++ {
		sb.WriteString(fmt.Sprintf("%02X ", i))
		if i == (bytesPerRow/2)-1 && bytesPerRow > 8 {
			sb.WriteString(" ")
		}
	}
	sb.WriteString("|ASCII|\n")

	for i := 0; i < len(data); i += bytesPerRow {
		if showAddresses {
			sb.WriteString(fmt.Sprintf("%08X    ", i))
		} else {
			sb.WriteString("            ")
		}

		end := i + bytesPerRow
		if end > len(data) {
			end = len(data)
		}

		for j := 0; j < bytesPerRow; j++ {
			if i+j < len(data) {
				sb.WriteString(fmt.Sprintf("%02X ", data[i+j]))
			} else {
				sb.WriteString("   ")
			}
			if j == (bytesPerRow/2)-1 && bytesPerRow > 8 {
				sb.WriteString(" ")
			}
		}

		sb.WriteString("|")
		for j := i; j < end; j++ {
			b := data[j]
			if b >= 0x20 && b <= 0x7E {
				sb.WriteByte(b)
			} else {
				sb.WriteString(".")
			}
		}
		sb.WriteString("|\n")
	}

	return sb.String()
}

// toCompactHex generates a compact hex dump format with configurable row width and address display.
// Each line is: [OFFSET:] <hex bytes> (no ASCII column).
func toCompactHex(data []byte, bytesPerRow int, showAddresses bool, base uint64) string {
	if len(data) == 0 {
		return "(empty file)\n"
	}
	var sb strings.Builder

	for i := 0; i < len(data); i += bytesPerRow {
		addr := base + uint64(i)
		if showAddresses {
			sb.WriteString(fmt.Sprintf("%08X: ", addr))
		}

		end := i + bytesPerRow
		if end > len(data) {
			end = len(data)
		}

		for j := i; j < end; j++ {
			sb.WriteString(fmt.Sprintf("%02X", data[j]))
		}
		sb.WriteByte('\n')
	}

	return sb.String()
}

// fromHexDump converts hex string (formatted or raw) back to bytes.
// Supports both raw hex (e.g., "48656C6C6F") and formatted hex dump (with offsets and ASCII columns).
func fromHexDump(hexString string) ([]byte, error) {
	cleaned := strings.TrimSpace(hexString)

	if strings.Contains(cleaned, "|") || strings.Contains(cleaned, "  ") {
		return parseFormattedHexDump(cleaned)
	}

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
		// Pad with leading zero to handle odd-length input
		cleaned = "0" + cleaned
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

func parseFormattedHexDump(hexString string) ([]byte, error) {
	var result []byte
	lines := strings.Split(hexString, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Offset") {
			continue
		}
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
		hexSection = strings.TrimSpace(hexSection)
		parts := strings.Fields(hexSection)
		for _, part := range parts {
			if len(part) != 2 {
				continue
			}
			var b byte
			_, err := fmt.Sscanf(part, "%02x", &b)
			if err == nil {
				result = append(result, b)
			}
		}
	}
	return result, nil
}

// copyDirectoryRecursive copies a directory tree from srcPath to destPath.
// Returns total bytes copied. Creates destPath if it doesn't exist.
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

// Supported encodings for file operations.
type TextEncoding int

// WindowsExecuteBits represents the Unix permission bits that indicate execute permissions.
// These are r-x (read for owner, group, and others) in octal: 0x168 = 360 in decimal.
const WindowsExecuteBits uint32 = 0x168

const (
	EncodingUTF8 TextEncoding = iota
	EncodingUTF8BOM
	EncodingUTF16LE
	EncodingUTF16BE
	EncodingCP1252
	EncodingASCII
)

// encodingFromString converts a string to TextEncoding constant.
func encodingFromString(s string) TextEncoding {
	switch strings.ToLower(s) {
	case "utf-8", "utf8", "":
		return EncodingUTF8
	case "utf-8-bom", "utf8-bom", "utf-8bom", "utf8bom":
		return EncodingUTF8BOM
	case "utf-16le", "utf16le", "utf-16-le", "utf16-le":
		return EncodingUTF16LE
	case "utf-16be", "utf16be", "utf-16-be", "utf16-be":
		return EncodingUTF16BE
	case "cp1252", "windows-1252", "win1252", "ansi":
		return EncodingCP1252
	case "ascii", "us-ascii", "usascii":
		return EncodingASCII
	default:
		return EncodingUTF8 // default to UTF-8
	}
}

// encodeToBytes converts UTF-8 string to the specified encoding.
func encodeToBytes(content string, enc TextEncoding) ([]byte, error) {
	switch enc {
	case EncodingUTF8:
		return []byte(content), nil
	case EncodingUTF8BOM:
		bom := []byte{0xEF, 0xBB, 0xBF}
		data := []byte(content)
		return append(bom, data...), nil
	case EncodingUTF16LE:
		return utf8ToUTF16LE(content), nil
	case EncodingUTF16BE:
		return utf8ToUTF16BE(content), nil
	case EncodingCP1252:
		return utf8ToCP1252(content), nil
	case EncodingASCII:
		return utf8ToASCII(content), nil
	default:
		return []byte(content), nil
	}
}

// decodeFromBytes converts bytes from the specified encoding to UTF-8 string.
func decodeFromBytes(data []byte, enc TextEncoding) (string, error) {
	switch enc {
	case EncodingUTF8:
		return string(data), nil
	case EncodingUTF8BOM:
		if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
			data = data[3:]
		}
		return string(data), nil
	case EncodingUTF16LE:
		return utf16LEToUTF8(data), nil
	case EncodingCP1252:
		return cp1252ToUTF8(data), nil
	case EncodingASCII:
		return asciiToUTF8(data), nil
	default:
		return string(data), nil
	}
}

// utf8ToUTF16LE converts UTF-8 string to UTF-16 LE with BOM.
func utf8ToUTF16LE(s string) []byte {
	bom := []byte{0xFF, 0xFE}
	var result []byte
	for _, r := range s {
		if r <= 0xFFFF {
			result = append(result, byte(r), byte(r>>8))
		} else {
			// surrogate pair for characters outside BMP
			r -= 0x10000
			highSurrogate := 0xD800 + int(r>>10)
			lowSurrogate := 0xDC00 + int(r&0x3FF)
			result = append(result, byte(highSurrogate), byte(highSurrogate>>8))
			result = append(result, byte(lowSurrogate), byte(lowSurrogate>>8))
		}
	}
	return append(bom, result...)
}

// utf8ToUTF16BE converts UTF-8 string to UTF-16 BE with BOM.
func utf8ToUTF16BE(s string) []byte {
	bom := []byte{0xFE, 0xFF}
	var result []byte
	for _, r := range s {
		if r <= 0xFFFF {
			result = append(result, byte(r>>8), byte(r))
		} else {
			// surrogate pair for characters outside BMP
			r -= 0x10000
			highSurrogate := 0xD800 + int(r>>10)
			lowSurrogate := 0xDC00 + int(r&0x3FF)
			result = append(result, byte(highSurrogate>>8), byte(highSurrogate))
			result = append(result, byte(lowSurrogate>>8), byte(lowSurrogate))
		}
	}
	return append(bom, result...)
}

// utf16LEToUTF8 converts UTF-16 LE bytes to UTF-8 string.
func utf16LEToUTF8(data []byte) string {
	// Skip BOM if present
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		data = data[2:]
	}
	var result []rune
	for i := 0; i+1 < len(data); i += 2 {
		r := uint16(data[i]) | uint16(data[i+1])<<8
		if r >= 0xD800 && r <= 0xDBFF && i+3 < len(data) {
			// Surrogate pair
			surrogate := uint16(data[i+2]) | uint16(data[i+3])<<8
			r = uint16(0x10000 + int32(r-0xD800)<<10 + int32(surrogate-0xDC00))
			i += 2
		}
		result = append(result, rune(r))
	}
	return string(result)
}

// utf8ToCP1252 converts UTF-8 to Windows-1252 (simplified mapping).
func utf8ToCP1252(s string) []byte {
	// CP1252 mapping for 0x80-0x9F range
	cp1252Map := map[rune]byte{
		0x20AC: 0x80, // €
		0x201A: 0x82, // ‚
		0x0192: 0x83, // ƒ
		0x201E: 0x84, // „
		0x2026: 0x85, // …
		0x2021: 0x86, // †
		0x2020: 0x87, // ‡
		0x02C6: 0x88, // ˆ
		0x2030: 0x89, // ‰
		0x0160: 0x8A, // Š
		0x2039: 0x8B, // ‹
		0x0152: 0x8C, // Œ
		0x017D: 0x8E, // Ž
		0x2044: 0x8F, // ⁄
		0x2018: 0x91, // '
		0x2019: 0x92, // '
		0x201C: 0x93, // "
		0x201D: 0x94, // "
		0x2022: 0x95, // •
		0x2013: 0x96, // –
		0x2014: 0x97, // —
		0x02DC: 0x98, // ˜
		0x2122: 0x99, // ™
		0x0161: 0x9A, // š
		0x203A: 0x9B, // ›
		0x0153: 0x9C, // œ
		0x017E: 0x9E, // ž
		0x0178: 0x9F, // Ÿ
	}

	var result []byte
	for _, r := range s {
		if r < 0x80 {
			result = append(result, byte(r))
		} else if mapping, ok := cp1252Map[r]; ok {
			result = append(result, mapping)
		} else {
			// Fall back to replacement character
			result = append(result, 0x3F) // ?
		}
	}
	return result
}

// cp1252ToUTF8 converts Windows-1252 to UTF-8.
func cp1252ToUTF8(data []byte) string {
	cp1252Reverse := map[byte]rune{
		0x80: 0x20AC, 0x82: 0x201A, 0x83: 0x0192, 0x84: 0x201E,
		0x85: 0x2026, 0x86: 0x2021, 0x87: 0x2020, 0x88: 0x02C6,
		0x89: 0x2030, 0x8A: 0x0160, 0x8B: 0x2039, 0x8C: 0x0152,
		0x8E: 0x017D, 0x8F: 0x2044, 0x91: 0x2018, 0x92: 0x2019,
		0x93: 0x201C, 0x94: 0x201D, 0x95: 0x2022, 0x96: 0x2013,
		0x97: 0x2014, 0x98: 0x02DC, 0x99: 0x2122, 0x9A: 0x0161,
		0x9B: 0x203A, 0x9C: 0x0153, 0x9E: 0x017E, 0x9F: 0x0178,
	}

	var result strings.Builder
	for _, b := range data {
		if b < 0x80 {
			result.WriteByte(b)
		} else if r, ok := cp1252Reverse[b]; ok {
			result.WriteRune(r)
		} else {
			result.WriteByte(0x3F) // ?
		}
	}
	return result.String()
}

// utf8ToASCII converts UTF-8 to ASCII (drops non-ASCII chars).
func utf8ToASCII(s string) []byte {
	var result []byte
	for _, r := range s {
		if r < 128 {
			result = append(result, byte(r))
		} else {
			result = append(result, 0x20) // space for non-ASCII
		}
	}
	return result
}

// asciiToUTF8 converts ASCII bytes to UTF-8 string.
func asciiToUTF8(data []byte) string {
	var result strings.Builder
	for _, b := range data {
		if b < 128 {
			result.WriteByte(b)
		} else {
			result.WriteByte(0x20) // space
		}
	}
	return result.String()
}

// IsSymlink checks if a path points to a symlink.
// Returns true if the path is a symlink, false otherwise.
// On Windows, requires developer mode or admin privileges for symlinks to work properly.
func IsSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

// GetSymlinkTarget returns the target of a symbolic link, or empty string if not a symlink.
func GetSymlinkTarget(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return target
}

// suggestFileExists provides filename suggestions when a path is not found.
// It searches the parent directory for similar filenames using prefix matching.
func suggestFileExists(requestedPath string) string {
	parentDir := filepath.Dir(requestedPath)
	baseName := filepath.Base(requestedPath)

	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return ""
	}

	var suggestions []string
	requestedLower := strings.ToLower(baseName)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		entryName := entry.Name()
		entryLower := strings.ToLower(entryName)

		// Check for similarity: exact match (case-insensitive), prefix match, or common suffix
		if requestedLower == entryLower {
			suggestions = append(suggestions, filepath.Join(parentDir, entryName))
		} else if strings.HasPrefix(requestedLower, strings.TrimSuffix(entryLower, filepath.Ext(entryLower))) ||
			strings.HasPrefix(strings.TrimSuffix(requestedLower, filepath.Ext(requestedLower)), entryLower) {
			suggestions = append(suggestions, filepath.Join(parentDir, entryName))
		} else {
			// Simple character overlap check
			overlap := 0
			for i := 0; i < len(requestedLower) && i < len(entryLower); i++ {
				if requestedLower[i] == entryLower[i] {
					overlap++
				}
			}
			if overlap >= len(requestedLower)*2/3 && len(suggestions) < 3 {
				suggestions = append(suggestions, filepath.Join(parentDir, entryName))
			}
		}

		if len(suggestions) >= 3 {
			break
		}
	}

	if len(suggestions) == 0 {
		return ""
	}

	var sb strings.Builder
	for i, s := range suggestions {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(s)
	}
	return sb.String()
}

// extractArg extracts and type-converts an argument from MCP tool request.
// Handles JSON float64 to int/uint conversion with range validation.
// Handles JSON bool to bool conversion.
// Handles pointer types (e.g., *int) by first extracting the concrete value then boxing into a new pointer.
// Returns zero value and error if argument is missing or type mismatch.
func extractArg[T any](req mcp.CallToolRequest, key string) (T, error) {
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		var zero T
		return zero, fmt.Errorf("invalid arguments format")
	}

	val, exists := args[key]
	if !exists {
		var zero T
		return zero, fmt.Errorf("missing argument: %s", key)
	}

	// Handle bool conversion from JSON (JSON booleans are parsed as bool, not float64)
	var zeroVal T
	if reflect.TypeOf(zeroVal).Kind() == reflect.Bool {
		if boolVal, ok := val.(bool); ok {
			return any(boolVal).(T), nil
		}
		var zero T
		return zero, fmt.Errorf("type mismatch for argument: %s (got %T, want bool)", key, val)
	}

	// Handle pointer types first: extract the underlying value then wrap in a new pointer.
	if reflect.TypeOf(zeroVal).Kind() == reflect.Ptr {
		ptrElem := reflect.TypeOf(zeroVal).Elem()

		// If the raw value is already the correct type, return it directly.
		if reflect.TypeOf(val) == ptrElem {
			return any(val).(T), nil
		}

		// Handle float64 -> *int/*uint conversion (JSON numbers are always float64).
		if floatVal, ok := val.(float64); ok {
			switch ptrElem.Kind() {
			case reflect.Int:
				v := int(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Int8:
				if floatVal < -128 || floatVal > 127 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int8", key, floatVal)
				}
				v := int8(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Int16:
				if floatVal < -32768 || floatVal > 32767 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int16", key, floatVal)
				}
				v := int16(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Int32:
				if floatVal < -2147483648 || floatVal > 2147483647 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int32", key, floatVal)
				}
				v := int32(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Int64:
				v := int64(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Uint:
				if floatVal < 0 {
					var zero T
					return zero, fmt.Errorf("argument '%s': negative value %.0f cannot be converted to unsigned integer", key, floatVal)
				}
				v := uint(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Uint8:
				if floatVal > 255 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint8", key, floatVal)
				}
				v := uint8(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Uint16:
				if floatVal > 65535 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint16", key, floatVal)
				}
				v := uint16(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Uint32:
				if floatVal > 4294967295 {
					var zero T
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint32", key, floatVal)
				}
				v := uint32(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Uint64:
				if floatVal < 0 {
					var zero T
					return zero, fmt.Errorf("argument '%s': negative value %.0f cannot be converted to unsigned integer", key, floatVal)
				}
				v := uint64(math.Round(floatVal))
				return any(&v).(T), nil
			case reflect.Float32:
				v := float32(floatVal)
				return any(&v).(T), nil
			case reflect.Float64:
				v := float64(floatVal)
				return any(&v).(T), nil
			}
		}

		// Handle direct pointer types (e.g., *string when val is string).
		if ptrElem.Kind() == reflect.String && reflect.TypeOf(val) == reflect.TypeOf("") {
			s := val.(string)
			return any(&s).(T), nil
		}

		// Direct type assertion for pointer values.
		res, ok := val.(T)
		if ok {
			return res, nil
		}
	}

	// Handle float64 to numeric types conversion (JSON numbers are always float64).
	if floatVal, ok := val.(float64); ok {
		var zero T
		targetType := reflect.TypeOf(zero).Kind()

		if targetType == reflect.Int || targetType == reflect.Int8 || targetType == reflect.Int16 || targetType == reflect.Int32 || targetType == reflect.Int64 {
			if floatVal != math.Round(floatVal) {
				return zero, fmt.Errorf("argument '%s': float value %.6f cannot be converted to integer", key, floatVal)
			}
			switch reflect.TypeOf(zero).Kind() {
			case reflect.Int:
				return any(int(floatVal)).(T), nil
			case reflect.Int8:
				if floatVal < -128 || floatVal > 127 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int8", key, floatVal)
				}
				return any(int8(floatVal)).(T), nil
			case reflect.Int16:
				if floatVal < -32768 || floatVal > 32767 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int16", key, floatVal)
				}
				return any(int16(floatVal)).(T), nil
			case reflect.Int32:
				if floatVal < -2147483648 || floatVal > 2147483647 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for int32", key, floatVal)
				}
				return any(int32(floatVal)).(T), nil
			case reflect.Int64:
				return any(int64(floatVal)).(T), nil
			default:
				return zero, fmt.Errorf("argument '%s': unsupported integer type", key)
			}
		}

		if targetType == reflect.Uint || targetType == reflect.Uint8 || targetType == reflect.Uint16 || targetType == reflect.Uint32 || targetType == reflect.Uint64 {
			if floatVal < 0 {
				return zero, fmt.Errorf("argument '%s': negative value %.0f cannot be converted to unsigned integer", key, floatVal)
			}
			if floatVal != math.Round(floatVal) {
				return zero, fmt.Errorf("argument '%s': float value %.6f cannot be converted to unsigned integer", key, floatVal)
			}
			switch reflect.TypeOf(zero).Kind() {
			case reflect.Uint:
				return any(uint(floatVal)).(T), nil
			case reflect.Uint8:
				if floatVal > 255 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint8", key, floatVal)
				}
				return any(uint8(floatVal)).(T), nil
			case reflect.Uint16:
				if floatVal > 65535 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint16", key, floatVal)
				}
				return any(uint16(floatVal)).(T), nil
			case reflect.Uint32:
				if floatVal > 4294967295 {
					return zero, fmt.Errorf("argument '%s': value %.0f out of range for uint32", key, floatVal)
				}
				return any(uint32(floatVal)).(T), nil
			case reflect.Uint64:
				return any(uint64(floatVal)).(T), nil
			default:
				return zero, fmt.Errorf("argument '%s': unsupported unsigned type", key)
			}
		}

		// float64 target (e.g., T is float64)
		if targetType == reflect.Float64 {
			return any(floatVal).(T), nil
		}
	}

	// Handle float64 to string fallback (some JSON parsers may encode numbers as strings)
	if strVal, ok := val.(string); ok {
		if reflect.TypeOf(zeroVal).Kind() == reflect.String {
			return any(strVal).(T), nil
		}
	}

	res, ok := val.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("type mismatch for argument: %s (got %T)", key, val)
	}

	return res, nil
}
