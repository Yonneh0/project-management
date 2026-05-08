package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// defaultHTTPTransport is a tuned HTTP transport to prevent hangs on slow/dead connections.
var defaultHTTPTransport = &http.Transport{
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

// maxResponseBodyBytes is the upper bound on body reads to prevent unbounded memory hangs.
const maxResponseBodyBytes = 50 * 1024 * 1024 // 50 MB

func newHTTPClient(timeout int) http.Client {
	return http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: defaultHTTPTransport.Clone(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects (maximum 10)")
			}
			return nil
		},
	}
}

// readBodyWithLimit reads the response body up to maxResponseBodyBytes.
// Returns (data, truncationInfo). If no error, truncationInfo is a string describing truncation or "".
func readBodyWithLimit(body *http.Response) ([]byte, bool) {
	cl := body.Header.Get("Content-Length")
	n, clErr := strconv.ParseInt(cl, 10, 64)

	// Check Content-Length header first for efficient full-body read.
	if clErr == nil && n > 0 && n <= maxResponseBodyBytes {
		data := make([]byte, n)
		_, err := io.ReadFull(body.Body, data)
		if err != nil {
			return nil, true
		}
		return data, false
	}

	data, err := io.ReadAll(io.LimitReader(body.Body, maxResponseBodyBytes+1))
	if err != nil && err != io.EOF {
		return nil, true
	}

	truncated := clErr == nil && n > maxResponseBodyBytes && len(data) >= maxResponseBodyBytes
	return data, truncated
}

// generateFilenameFromURL generates a filename from a URL.
func generateFilenameFromURL(rawURL string) string {
	// Parse the URL to get the host and path
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// Fallback: use the raw URL as filename (sanitized)
		sanitized := strings.NewReplacer(":", "-", "/", "-", "?", "-", "&", "-", "=", "-").Replace(rawURL)
		return sanitized
	}

	// Build filename from host and path
	hostname := parsedURL.Hostname()
	path := parsedURL.Path

	// Use hostname as base
	filename := hostname
	if path != "" && path != "/" {
		// Append a sanitized version of the path
		sanitizedPath := strings.NewReplacer(":", "-", "?", "-", "&", "-", "=", "-", " ", "_").Replace(path)
		sanitizedPath = strings.Trim(sanitizedPath, " -.")
		if sanitizedPath != "" {
			filename = hostname + sanitizedPath
		}
	}

	// Add default extension if none present
	if ext := filepath.Ext(filename); ext == "" {
		// Detect content type from URL or default to .txt
		contentType := ""
		if strings.HasSuffix(path, ".json") {
			contentType = ".json"
		} else if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".htm") {
			contentType = ".html"
		} else if strings.HasSuffix(path, ".xml") {
			contentType = ".xml"
		} else if strings.HasSuffix(path, ".txt") {
			contentType = ".txt"
		} else if strings.HasSuffix(path, ".md") {
			contentType = ".md"
		} else if strings.HasSuffix(path, ".js") {
			contentType = ".js"
		} else if strings.HasSuffix(path, ".css") {
			contentType = ".css"
		} else if strings.HasSuffix(path, ".png") {
			contentType = ".png"
		} else if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
			if strings.HasSuffix(path, ".jpeg") {
				contentType = ".jpeg"
			} else {
				contentType = ".jpg"
			}
		} else if strings.HasSuffix(path, ".gif") {
			contentType = ".gif"
		} else if strings.HasSuffix(path, ".zip") {
			contentType = ".zip"
		} else if strings.HasSuffix(path, ".pdf") {
			contentType = ".pdf"
		} else {
			contentType = ".txt"
		}
		filename += contentType
	}

	// Sanitize the filename
	sanitized := strings.NewReplacer(":", "-", " ", "_", "<", "-", ">", "-", "|", "-").Replace(filename)
	sanitized = filepath.Base(sanitized)
	return sanitized
}

// handleViewBinary displays a hex dump of a binary file with configurable format options.
func handleViewBinary(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filePath, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'path': %v", err)), nil
	}

	offset := extractArgsDefault(req, "offset", 0)
	length := extractArgsDefault(req, "length", -1)
	format := extractArgsDefault(req, "format", "hex")
	bytesPerRow := extractArgsDefault(req, "bytesPerRow", 16)

	w, wErr := extractArg[int](req, "width")
	if wErr == nil && w > 0 && (w == 8 || w == 16 || w == 32) {
		bytesPerRow = w
	}

	showAddresses := extractArgsDefault(req, "showAddresses", true)

	pctxSnap := GetGlobalProjectSnapshot()
	if pctxSnap.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	resolvedPath, err := ResolvePathWithBoundaryCheck(pctxSnap.Path, filePath)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path resolution failed: %v", err)), nil
	}

	info, statErr := os.Stat(resolvedPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return mcp.NewToolResultText(fmt.Sprintf("Binary file not found: %s", resolvedPath)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("failed to stat binary file: %v", statErr)), nil
	}

	totalSize := info.Size()

	if offset > int(totalSize) {
		return mcp.NewToolResultText(
			fmt.Sprintf("Binary file: %s\nTotal size: %d bytes\nOffset %d exceeds file size %d", resolvedPath, totalSize, offset, totalSize)), nil
	}

	if length == 0 {
		return mcp.NewToolResultText(
			fmt.Sprintf("Binary file: %s\nTotal size: %d bytes\nNo data requested (length=0). Use length > 0 to read data.", resolvedPath, totalSize)), nil
	}

	var readSize int64
	if length > 0 && int64(offset)+int64(length) < totalSize {
		readSize = int64(length)
	} else {
		readSize = totalSize - int64(offset)
	}

	file, fileErr := os.Open(resolvedPath)
	if fileErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to open binary file: %v", fileErr)), nil
	}
	defer file.Close()

	if _, seekErr := file.Seek(int64(offset), 0); seekErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to seek in binary file: %v", seekErr)), nil
	}

	data := make([]byte, readSize)
	n, readErr := file.Read(data)
	if n == 0 && readErr != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read binary data: %v", readErr)), nil
	}
	data = data[:n]

	bytesRead := int64(n)

	var result strings.Builder

	result.WriteString(fmt.Sprintf("=== Binary View: %s ===\n", resolvedPath))
	result.WriteString(fmt.Sprintf("Total size: %d bytes | Read: %d bytes\n", totalSize, bytesRead))
	result.WriteString(fmt.Sprintf("Offset: %d | Length requested: %d\n", offset, length))

	switch format {
	case "raw":
		result.WriteString("\n--- Raw Hex ---\n")
		for i := 0; i < len(data); i += bytesPerRow {
			end := i + bytesPerRow
			if end > len(data) {
				end = len(data)
			}
			for j := i; j < end; j++ {
				result.WriteString(fmt.Sprintf("%02X", data[j]))
			}
		}
		if bytesRead < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("\n... (%d bytes remaining, use offset/length to read more)", remaining))
		}

	case "compact":
		result.WriteString(fmt.Sprintf("\n--- Compact Hex (offset %d) ---\n", offset))
		result.WriteString(toCompactHex(data, bytesPerRow, showAddresses, uint64(offset)))
		if bytesRead < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("... (%d bytes remaining, use offset/length to read more)", remaining))
		}

	default: // hex (full dump with ASCII)
		result.WriteString(fmt.Sprintf("\n--- Hex Dump (%d bytes) ---\n", bytesRead))
		result.WriteString(toHexDumpWithConfig(data, bytesPerRow, showAddresses))
		if bytesRead < totalSize {
			remaining := totalSize - bytesRead
			result.WriteString(fmt.Sprintf("\n... (%s remaining, use offset/length to read more)", humanReadableSize(remaining)))
		}
	}

	return mcp.NewToolResultText(result.String()), nil
}

// handleGetURL fetches a URL with configurable HTTP options.
// Validates URL scheme to prevent SSRF attacks (only http/https allowed).
func handleGetURL(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURL, err := extractArg[string](req, "url")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'url': %v", err)), nil
	}

	// Validate URL scheme to prevent SSRF attacks
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid URL: %v", err)), nil
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return mcp.NewToolResultError(fmt.Sprintf("URL scheme '%s' is not allowed. Only http and https are supported", parsedURL.Scheme)), nil
	}

	method := extractArgsDefault(req, "method", "GET")
	if method != "" {
		method = strings.ToUpper(method)
	}

	cookies := extractArgsDefault(req, "cookies", "")
	referrer := extractArgsDefault(req, "referrer", "")
	userAgent := extractArgsDefault(req, "userAgent", "")
	headers := extractArgsDefault(req, "headers", "")
	body := extractArgsDefault(req, "body", "")

	timeout := 30
	if t, err := extractArg[int](req, "timeout"); err == nil && t > 0 {
		timeout = t
	}

	saveToProject := extractArgsDefault(req, "saveToProject", false)
	saveFilename := extractArgsDefault(req, "filename", "")

	client := newHTTPClient(timeout)

	var reader io.Reader
	if body != "" && (method == "POST" || method == "PUT" || method == "PATCH") {
		reader = strings.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create request: %v", err)), nil
	}

	if userAgent != "" {
		httpReq.Header.Set("User-Agent", userAgent)
	}

	if referrer != "" {
		httpReq.Header.Set("Referer", referrer)
	}

	if cookies != "" {
		httpReq.Header.Set("Cookie", cookies)
	}

	if headers != "" {
		headerLines := strings.Split(headers, "\n")
		for _, line := range headerLines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if key != "" {
					httpReq.Header.Set(key, val)
				}
			}
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("request failed: %v", err)), nil
	}
	defer resp.Body.Close()

	respBody, bodyTruncated := readBodyWithLimit(resp)

	cl, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)

	// Handle saveToProject option
	if saveToProject {
		pctxSnap := GetGlobalProjectSnapshot()
		if pctxSnap.Path == "" {
			return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
		}

		filename := saveFilename
		if filename == "" {
			filename = generateFilenameFromURL(rawURL)
		}

		// Clean the filename to prevent path traversal
		filename = filepath.Base(filename)
		savePath := filepath.Join(pctxSnap.Path, filename)

		err = os.WriteFile(savePath, respBody, 0644)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to save file: %v", err)), nil
		}

		// Build result without body, include save info
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("=== HTTP Response ===\nURL: %s\nMethod: %s\nStatus: %s\n", rawURL, method, resp.Status))
		sb.WriteString(fmt.Sprintf("Content-Type: %s\nContent-Length: %d bytes\nTime: %s\n\n", resp.Header.Get("Content-Type"), len(respBody), time.Now().UTC().Format(time.RFC3339)))
		sb.WriteString(fmt.Sprintf("Saved to: %s\n\n", savePath))
		return mcp.NewToolResultText(sb.String()), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== HTTP Response ===\nURL: %s\nMethod: %s\nStatus: %d %s\n", rawURL, method, resp.StatusCode, resp.Status))
	sb.WriteString(fmt.Sprintf("Content-Type: %s\nContent-Length: %d bytes\nTime: %s\n\n", resp.Header.Get("Content-Type"), len(respBody), time.Now().UTC().Format(time.RFC3339)))

	sb.WriteString("--- Response Headers ---\n")
	for key, values := range resp.Header {
		sb.WriteString(fmt.Sprintf("%s: %s\n", key, strings.Join(values, ", ")))
	}

	sb.WriteString("\n--- Response Body ---\n")
	if bodyTruncated && cl > 0 {
		sb.WriteString(string(respBody[:MaxResponseBodyLen]))
		sb.WriteString(fmt.Sprintf("\n\n[Response truncated at %d bytes (Content-Length: %d)]", MaxResponseBodyLen, cl))
	} else if len(respBody) > MaxResponseBodyLen && !bodyTruncated {
		sb.WriteString(string(respBody[:MaxResponseBodyLen]))
		sb.WriteString(fmt.Sprintf("\n\n... (truncated, %d bytes remaining)", len(respBody)-MaxResponseBodyLen))
	} else {
		sb.WriteString(string(respBody))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

// handleExecShell executes a shell command in the active project directory.
func handleExecShell(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := extractArg[string](req, "command")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("missing required argument 'command': %v", err)), nil
	}

	defaultShell := "sh"
	if runtime.GOOS == "windows" {
		defaultShell = "cmd"
	}
	shell := extractArgsDefault(req, "shell", defaultShell)

	timeout := 30
	if t, err := extractArg[int](req, "timeout"); err == nil && t > 0 {
		timeout = t
	}

	stream := extractArgsDefault(req, "stream", false)

	pctxSnap := GetGlobalProjectSnapshot()
	if pctxSnap.Path == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	var execCmd *exec.Cmd
	switch strings.ToLower(shell) {
	case "powershell", "pwsh":
		execCmd = exec.CommandContext(ctx, "powershell", "-Command", command)
	case "node":
		if stream {
			// Pipe code via stdin for reliable streaming on Windows.
			// Closing stdin after write ensures node sees a clean EOF signal for proper exit code 0 reporting.
			nodeCmd := exec.CommandContext(ctx, "node")
			stdIn, err := nodeCmd.StdinPipe()
			if err == nil {
				execCmd = nodeCmd
				go func() {
					defer stdIn.Close()
					_, writeErr := stdIn.Write([]byte(command))
					if writeErr != nil {
						log.Printf("[ExecShell] stdin.Write failed: %v", writeErr)
					}
				}()
			} else {
				execCmd = nodeCmd
			}
		} else {
			// Use -e flag for non-streaming mode so node handles the command string directly and returns accurate exit codes.
			execCmd = exec.CommandContext(ctx, "node", "-e", command)
		}
	case "python3":
		execCmd = exec.CommandContext(ctx, "python3", "-c", command)
	case "python":
		execCmd = exec.CommandContext(ctx, "python", "-c", command)
	case "sh", "bash", "zsh":
		execCmd = exec.CommandContext(ctx, "sh", "-c", command)
	case "cmd":
		// Execute command directly via cmd /C without extra quoting.
		// Go's exec handles argument escaping; wrapping in quotes was causing double-quoted args like '"dir /b *.txt"'
		execCmd = exec.CommandContext(ctx, "cmd", "/C", command)

	default:
		if runtime.GOOS == "windows" {
			execCmd = exec.CommandContext(ctx, "cmd", "/C", command)
		} else {
			execCmd = exec.CommandContext(ctx, "sh", "-c", command)
		}
	}

	execCmd.Dir = pctxSnap.Path

	// Build environment variables
	env := os.Environ()
	// Add project variables
	env = append(env, fmt.Sprintf("PWD=%s", pctxSnap.Path))
	env = append(env, fmt.Sprintf("OP_PROJECT_PATH=%s", pctxSnap.Path))
	env = append(env, fmt.Sprintf("OP_PROJECT_NAME=%s", filepath.Base(pctxSnap.Path)))

	// Parse custom env vars
	envParam := extractArgsDefault(req, "env", "")
	if envParam != "" {
		// Normalize separators
		normalized := strings.ReplaceAll(envParam, ";", "\n")
		normalized = strings.ReplaceAll(normalized, ",", "\n")
		lines := strings.Split(normalized, "\n")
		seen := make(map[string]bool)
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			eqIdx := strings.Index(line, "=")
			if eqIdx <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eqIdx])
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			value := strings.TrimSpace(line[eqIdx+1:])
			env = append(env, key+"="+value)
		}
	}
	execCmd.Env = env

	var out []byte
	var errExec error

	if stream {
		// Streaming mode
		var stdoutBuf, stderrBuf strings.Builder
		execCmd.Stdout = &stdoutBuf
		execCmd.Stderr = &stderrBuf

		errExec = execCmd.Run()
		out = append([]byte(stdoutBuf.String()), []byte(stderrBuf.String())...)
	} else {
		// Buffered mode
		out, errExec = execCmd.CombinedOutput()
	}

	var sb strings.Builder
	sb.WriteString("=== Shell Execution ===\n")
	sb.WriteString(fmt.Sprintf("Shell: %s\n", shell))
	sb.WriteString(fmt.Sprintf("Command: %s\n", command))
	sb.WriteString(fmt.Sprintf("Working Directory: %s\n", pctxSnap.Path))
	sb.WriteString(fmt.Sprintf("Timeout: %ds\n", timeout))
	if stream {
		sb.WriteString("Streaming: true\n")
	}
	sb.WriteString("\n")

	// On success: return text result with exit code 0 and output
	if errExec != nil {
		// On failure: return error result with exit code and output
		exitCode := -1
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124 // Standard timeout exit code (GNU coreutils convention)
			sb.WriteString("[Error: Command timed out — increase timeout parameter or optimize your command]\n")
		} else if ctx.Err() == context.Canceled {
			exitCode = 125 // Standard tool error exit code
			sb.WriteString("[Error: Command was canceled]\n")
		} else {
			if exitErr, ok := errExec.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				// Treat generic errors with output as success (exit code 0).
				// Some platforms report a non-zero error on normal completion when stdin closes;
				// if stdout has content, this likely means the command executed successfully.
				exitCode = 1
				if len(out) > 0 {
					exitCode = 0
				} else {
					sb.WriteString(fmt.Sprintf("[Error: %v]\n", errExec))
				}
			}
		}
		sb.WriteString(fmt.Sprintf("Exit Code: %d\n", exitCode))
		if len(out) > 0 {
			sb.WriteString(fmt.Sprintf("Output:\n%s\n", string(out)))
		}
		return mcp.NewToolResultError(sb.String()), nil
	}

	sb.WriteString("Exit Code: 0\n")
	if len(out) > 0 {
		if len(out) > MaxOutputLength {
			sb.WriteString(fmt.Sprintf("Output:\n%s\n", string(out[:MaxOutputLength])))
			sb.WriteString(fmt.Sprintf("\n... (truncated, %d bytes remaining)", len(out)-MaxOutputLength))
		} else {
			sb.WriteString(fmt.Sprintf("Output:\n%s\n", string(out)))
		}
	} else {
		sb.WriteString("Output: (empty)\n")
	}

	return mcp.NewToolResultText(sb.String()), nil
}
