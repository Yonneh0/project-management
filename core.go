package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const projectManagementFile = "project-management.json"

// ProjectPersistence holds the persisted open project state.
type ProjectPersistence struct {
	CurrentProject string `json:"current_project"`
	OpenedAt       string `json:"opened_at"`
}

// isValidProjectName checks if a project name is valid (1-64 chars, alphanumeric, hyphens, underscores, dots, slashes; no leading dot or ..).
func isValidProjectName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(filepath.Base(name), ".") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		for _, ch := range component {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
				(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.') {
				return false
			}
		}
	}
	return true
}

// loadProjectPersistence loads the persisted open project state from rootDir.
func loadProjectPersistence(rootDir string) *ProjectPersistence {
	persistencePath := filepath.Join(rootDir, projectManagementFile)
	data, err := os.ReadFile(persistencePath)
	if err != nil {
		return nil
	}

	var p ProjectPersistence
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}

	return &p
}

// saveProjectPersistence saves the current open project state to rootDir.
func saveProjectPersistence(rootDir string, projectPath string) error {
	persistencePath := filepath.Join(rootDir, projectManagementFile)
	p := ProjectPersistence{
		CurrentProject: projectPath,
		OpenedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal persistence data: %w", err)
	}

	if err := os.WriteFile(persistencePath, data, 0644); err != nil {
		return fmt.Errorf("failed to save project persistence: %w", err)
	}

	return nil
}

// OpenProject opens a project by name and returns its path.
func OpenProject(rootDir, path string) (string, error) {
	if strings.TrimSpace(rootDir) == "" {
		return "", fmt.Errorf("root directory cannot be empty")
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("project name is required. Provide a valid project name (e.g., 'my-project')")
	}

	if !isValidProjectName(path) {
		return "", fmt.Errorf("invalid project name '%s'. Project names must be 1-64 characters, containing only alphanumeric letters, hyphens (-), underscores (_), and dots (.). Cannot start with '.' or contain '..'", path)
	}

	absRootDir := rootDir
	if !filepath.IsAbs(absRootDir) {
		var err error
		absRootDir, err = filepath.Abs(rootDir)
		if err != nil {
			return "", fmt.Errorf("failed to resolve root directory: %w", err)
		}
	}

	projectDir, err := ResolveRootPath(absRootDir, path)
	if err != nil {
		return "", fmt.Errorf("failed to resolve project path: %w", err)
	}
	projectDir = filepath.Clean(projectDir)

	info, statErr := os.Stat(projectDir)
	isNew := statErr != nil || !info.IsDir()

	cleanRoot := filepath.Clean(absRootDir)
	if strings.EqualFold(cleanRoot, projectDir) {
		return "", fmt.Errorf("cannot open root directory directly. Please specify a project name/subdirectory (e.g., 'my-project' or 'subfolder/my-project')")
	}

	if isNew {
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create project directory: %w", err)
		}
	} else if !info.IsDir() {
		return "", fmt.Errorf("path exists but is not a directory: %s", projectDir)
	}

	SetGlobalProject(&ProjectContext{
		RootDir: absRootDir,
		Path:    projectDir,
	})

	if err := saveProjectPersistence(rootDir, projectDir); err != nil {
		log.Printf("[warn] failed to persist project state for '%s': %v", projectDir, err)
	}

	return projectDir, nil
}

// toolHandlerWrapper wraps a tool handler with panic recovery.
// After recover(), returns the error result directly — does not call handler again.
func toolHandlerWrapper(handler func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)) func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		defer func() {
			if r := recover(); r != nil {
				stackBuf := make([]byte, 4096)
				n := runtime.Stack(stackBuf, false)
				log.Printf("[PANIC recovered in MCP tool handler]: %v\nstack: %s\n", r, stackBuf[:n])
			}
		}()
		result, err := handler(ctx, req)
		return result, err
	}
}

// RegisterTools registers all MCP tools with the given server.
func RegisterTools(mcpServer *server.MCPServer, rootDir string) {
	mcpServer.AddTool(mcp.NewTool("ListProjects",
		mcp.WithDescription(
			"List all available projects in the root directory. Does NOT require an open project context.\n\n"+
				"Returns a JSON array of project info (name, path, size, modification time), sorted by most recently modified.",
		),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleListProjects(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("OpenProject",
		mcp.WithDescription(
			"Open an existing project or create a new one. Sets the active project context for all subsequent file operations.\n\n"+
				"Call this FIRST before any file operations (GetItem, EditItem, ExecShell, etc.).\n\n"+
				"- Provide path parameter: opens it as the current project\n"+
				"- If path exists as directory (even if already open): re-opens it\n"+
				"- If path doesn't exist: creates it as a new project\n"+
				"- All paths in subsequent tool calls are resolved relative to this project root (your project PWD)\n"+
				"- Paths outside the project boundary are rejected for safety\n"+
				"- Automatically closes any previously open project before opening a new one\n\n"+
				"Project name: 1-64 chars, alphanumeric letters, hyphens (-), underscores (_), and dots (.). Cannot start with '.' or contain '..'.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Project name/path (relative to rootDir/target_directory). Must be a valid project name (e.g., 'my-project' or 'subfolder/my-project').")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleOpenProject(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("GetItem",
		mcp.WithDescription(
			"Read file content or list directory contents, and get metadata. Requires an open project context.\n\n"+
				"- action=auto (default): detects whether path is a file or directory\n"+
				"- For file content: use offset/length for chunked reading\n"+
				"- Use length=0 for metadata only (returns permissions, timestamps, size — no content)\n"+
				"- Windows permission bits may not reflect actual writability\n"+
				"- Files >10MB require offset/length to read in chunks\n"+
				"- For detailed binary viewing use ViewBinary instead",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path of the file or directory (resolved against open project root)")),
		mcp.WithString("action", mcp.DefaultString("auto"), mcp.Description("Action: auto (auto-detect file/dir), read (file content or directory listing)")),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Description("Byte offset for file reading (0 = start of file). Use with length for chunked reading.")),
		mcp.WithNumber("length", mcp.DefaultNumber(-1), mcp.Description("Read control: -1=read entire file, 0=metadata only (no content returned), >0=read exactly N bytes from offset. When reading partial data, output includes '... (X remaining, use offset/length to read more)' to indicate more data is available.")),
		mcp.WithNumber("line", mcp.Description("1-based single line number to read (overrides offset/length parameters). Returns just that line.")),
		mcp.WithNumber("startLine", mcp.Description("For text read: 1-based start of line range (inclusive). Use with endLine for range.")),
		mcp.WithNumber("endLine", mcp.Description("For text read: 1-based end of line range (inclusive). Required with startLine.")),
		mcp.WithString("format", mcp.DefaultString("auto"), mcp.Description("Output format: auto (auto-detect; binary files show hex dump), text (force text mode), hex (always hex dump). Note: GetItem reads file content — for detailed binary viewing use ViewBinary.")),
		mcp.WithBoolean("recursive", mcp.DefaultBool(false), mcp.Description("For directory listing: walk directory recursively. Note: hidden files are skipped when includeHidden=false. Hidden directories are skipped entirely (not their contents walked).")),
		mcp.WithNumber("maxItems", mcp.DefaultNumber(100), mcp.Description("Maximum directory entries to return. 0 = unlimited (no limit). Omitted or negative = use default (100). Applies to both top-level and recursive listings.")),
		mcp.WithBoolean("includeHidden", mcp.DefaultBool(false), mcp.Description("For directory listing: include hidden/dot files and directories (prefixed with '.')")),
		mcp.WithString("sortBy", mcp.DefaultString("name"), mcp.Description("For directory listing: sort order — name (alphabetical), size, date (modification time), type (files first, then dirs)")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetItem(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("EditItem",
		mcp.WithDescription(
			"Create, edit files and directories. Requires an open project context.\n\n"+
				"- action=edit (default): Replace lines by 1-based line number range on existing files\n"+
				"- For files: provide path and content (empty = empty file)\n"+
				"- For folders: set isFolder=true; automatically creates parent directories\n"+
				"- Use overwrite=true to replace existing items\n"+
				"- action=create: Create a new file or directory\n"+
				"  - Provide startLine and endLine (1-based, inclusive); if only startLine given, replaces a single line\n"+
				"  - File's trailing newline is preserved: \\n appended automatically if original file ends with \\n",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path of the target file or directory (resolved against open project root)")),
		mcp.WithString("action", mcp.DefaultString("edit"), mcp.Description("Action: edit (default, line-based replacement on existing files), create (create new file/directory)")),
		mcp.WithNumber("startLine", mcp.Description("1-based start line number (inclusive). If omitted and only one number given, replaces a single line.")),
		mcp.WithNumber("endLine", mcp.Description("1-based end line number (inclusive). If omitted, equals startLine (single line).")),
		mcp.WithString("replacement", mcp.Description("New text to replace the specified range with.")),
		mcp.WithBoolean("recursive", mcp.DefaultBool(false), mcp.Description("For directories: if true, delete all contents recursively. Without it, non-empty directories return an error.")),
		mcp.WithBoolean("ignoreMissing", mcp.DefaultBool(true), mcp.Description("Return success when item already exists (create action). Default is true — set to false during development to catch errors on missing items.")),
		mcp.WithBoolean("overwrite", mcp.DefaultBool(false), mcp.Description("Overwrite existing FILE or directory. For action=create with isFolder=true: return success when folder exists.")),
		mcp.WithString("content", mcp.Description("Content to write to the file on create. Empty string creates an empty file. Optional — if omitted, creates empty file.")),
		mcp.WithBoolean("isFolder", mcp.DefaultBool(false), mcp.Description("For action=create: if true, create a directory instead of a file.")),
		mcp.WithString("encoding", mcp.DefaultString("utf-8"), mcp.Description("Text encoding for file operations. Options:\n  - utf-8 (default): Modern standard, no BOM\n  - utf-8-bom: UTF-8 with Byte Order Mark (BOM EF BB BF) — use for Excel/legacy Windows compatibility\n  - utf-16le: UTF-16 Little Endian with BOM — use for legacy Windows/Java interop\n  - utf-16be: UTF-16 Big Endian with BOM — use for network/Java interop\n  - cp1252: Windows-1252 (ANSI) — use for legacy Windows text files\n  - ascii: Strict 7-bit ASCII, non-ASCII chars replaced with spaces")),
		mcp.WithBoolean("showProgress", mcp.DefaultBool(false), mcp.Description("Show progress indicators for large file operations. Triggered when file size >= 1MB (ProgressThreshold). Displays percentage progress updates every 5% during the operation.")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleEditItem(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("CloseProject",
		mcp.WithDescription(
			"Close the currently active project. Resets the global project context so all subsequent file operations require calling OpenProject first.\n\n"+
				"Closes the current project, removes persistence state from project-management.json, and returns confirmation.",
		),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleCloseProject(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("ExecShell",
		mcp.WithDescription(
			"Execute a shell command in the current project directory. Requires an open project context.\n\n"+
				"- Select the shell interpreter: cmd, powershell/pwsh (PowerShell), node (Node.js -e [JavaScript code]), python/python3 (Python -c [code]), sh/bash/zsh\n"+
				"- stream=true for long-running commands (builds, compilations) for responsive output\n"+
				"- Output is truncated at 10,000 bytes; killed if they exceed the timeout\n\n"+
				"Environment: PWD and OP_PROJECT_PATH set automatically. Use env parameter to set custom variables (format: \"KEY=value;KEY2=value2\").\n\n"+
				"Cross-platform: default shell is cmd on Windows, sh on Unix-like systems.\n"+
				"Quote escaping: cmd.exe nested quotes require backslash escaping (\\\"); complex commands may need powershell.\n\n"+
				"Note: node and python shells execute the command as code directly (like `-e` / `-c` flags), NOT through a shell interpreter. For example, `shell=\"node\"; command=\"console.log('hello')\"` runs valid JavaScript, while `shell=\"cmd\"; command=\"echo hello\"` runs a native shell command.\n\n"+
				"Exit codes: 0=success, 124=timed out, 125=canceled, 127=not found.",
		),
		mcp.WithString("command", mcp.Required(), mcp.Description("The command to execute. Syntax depends on the shell interpreter selected.")),
		mcp.WithString("shell", mcp.DefaultString("cmd"), mcp.Description("Shell interpreter: cmd (default on Windows, uses cmd /C), powershell/pwsh (PowerShell), node (Node.js -e [JavaScript code directly]), python/python3 (Python -c [Python code directly]), sh/bash/zsh (POSIX sh, default on Unix). Cross-platform: default shell varies by OS — \"cmd\" on Windows, \"sh\" on Unix-like systems.")),
		mcp.WithNumber("timeout", mcp.DefaultNumber(DefaultShellTimeout), mcp.Description("Timeout in seconds (default: 30). Must be > 0. Negative/zero values are ignored and default to 30 seconds.")),
		mcp.WithString("env", mcp.Description("Custom environment variables (format: \"KEY=value;KEY2=value2\" — semicolon or newline separated). Example: \"MY_VAR=hello;OTHER_VAR=world\". Note: Variables set here are inherited by the child process. In PowerShell, access them via $env:VAR syntax (e.g., $env:MY_VAR), not direct $VAR access. OS environment variables from the parent process are also inherited.")),
		mcp.WithBoolean("stream", mcp.DefaultBool(false), mcp.Description("For long-running commands (builds, compilations): if true, uses streaming output via separate stdout/stderr buffers that are combined in the output. Note: output order may not be guaranteed for long-running commands. Recommended for commands expected to run >10 seconds.")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleExecShell(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("GetURL",
		mcp.WithDescription(
			"Fetch a URL with configurable HTTP options. Does NOT require an open project context.\n\n"+
				"- Optionally specify method, headers, cookies, referrer, user agent, and body\n"+
				"- Responses include status code, headers, and body (truncated at 10,000 bytes)\n"+
				"- Follows redirects automatically (up to 10 hops); self-signed certificates will fail\n"+
				"- Uses Go's default HTTP client: system CA certificate bundle for TLS\n"+
				"- For POST/PUT/PATCH: Content-Type is NOT set automatically — provide via headers if needed\n\n"+
				"Saving to project (saveToProject=true): saves the response body to the open project directory.\n"+
				"  - When saveToProject=true: the response body is NOT returned in the result. Requires an open project context.\n"+
				"- Optionally specify filename; if omitted, auto-generates from URL (host + path).",
		),
		mcp.WithString("url", mcp.Required(), mcp.Description("The full URL to fetch (including protocol, e.g., https://example.com)")),
		mcp.WithString("method", mcp.DefaultString("GET"), mcp.Description("HTTP method: GET (default), POST, PUT, DELETE, PATCH, HEAD")),
		mcp.WithString("cookies", mcp.Description("Cookies to send in Cookie header (format: 'name=value; name2=value2')")),
		mcp.WithString("referrer", mcp.Description("Referrer/Referer header value")),
		mcp.WithString("userAgent", mcp.Description("User-Agent header value")),
		mcp.WithString("headers", mcp.Description("Additional headers (format: 'Key: Value\\nKey2: Value2'. Whitespace around colons is trimmed)")),
		mcp.WithString("body", mcp.Description("Request body (only used for POST/PUT/PATCH methods; ignored for GET/HEAD/DELETE)")),
		mcp.WithNumber("timeout", mcp.DefaultNumber(DefaultGetURLTimeout), mcp.Description("Request timeout in seconds (default: 30)")),
		mcp.WithBoolean("saveToProject", mcp.DefaultBool(false), mcp.Description("If true, saves the response body to the open project directory instead of returning it in the result. NOTE: When this is true, an open project context IS required (call OpenProject first). The 'does not require project context' note above applies only to the HTTP fetch itself.")),
		mcp.WithString("filename", mcp.Description("Optional filename to save the response body as. Only used when saveToProject=true. If omitted, auto-generates from URL (e.g., 'example.com-api-response.json'). Filename is sanitized: filepath.Base() removes directories and special chars, characters : < > | replaced with -, spaces replaced with _.")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetURL(ctx, req)
	}))

	mcpServer.AddTool(mcp.NewTool("ViewBinary",
		mcp.WithDescription(
			"Display a hex dump of a binary file with configurable format and width options.\n\n"+
				"- Three output formats: hex (full dump with ASCII column), raw (plain hex string), compact (one line per offset block)\n"+
				"- Configure row width (8, 16, or 32) and address display\n"+
				"- Use offset/length to read portions of large files in chunks\n\n"+
				"hex: Standard hex dump with ASCII preview (16 bytes per row by default). raw: Continuous hex string. compact: one line per offset block.",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Path to the binary file (resolved against open project root)")),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Description("Starting byte position in the file. Must be >= 0.")),
		mcp.WithNumber("length", mcp.DefaultNumber(-1), mcp.Description("Bytes to read: -1 = entire remaining file from offset, >0 = exact number of bytes.")),
		mcp.WithString("format", mcp.DefaultString("hex"), mcp.Description("Output format: hex (standard hex dump with ASCII preview), raw (plain continuous hex string), compact (one line per offset block).")),
		mcp.WithNumber("bytesPerRow", mcp.DefaultNumber(16), mcp.Description("Bytes per row in hex output. Options: 8, 16 (default), 32.")),
		mcp.WithBoolean("showAddresses", mcp.DefaultBool(true), mcp.Description("Include byte addresses on each line of the hex dump.")),
	), toolHandlerWrapper(func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleViewBinary(ctx, req)
	}))
}

// handleCloseProject closes the current project and updates persistence.
func handleCloseProject(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	snap := GetGlobalProjectSnapshot()

	if snap.Path == "" {
		return mcp.NewToolResultText("No project is currently open."), nil
	}

	projectPath := snap.Path
	CloseProject()

	// Clear persistence file to indicate no active project.
	persistencePath := filepath.Join(GetGlobalRootDir(), projectManagementFile)
	if err := os.Remove(persistencePath); err != nil && !os.IsNotExist(err) {
		fmt.Printf("[warn] failed to remove project persistence: %v\n", err)
	}

	return mcp.NewToolResultText(fmt.Sprintf("Project closed successfully!\nPrevious project path: %s\nAll subsequent file operations will require calling OpenProject first.", projectPath)), nil
}

func handleOpenProject(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rootDir := GetGlobalRootDir()

	path, err := extractArg[string](req, "path")
	if err != nil {
		return mcp.NewToolResultError("project name is required. Provide a valid project name (e.g., 'my-project')"), nil
	}

	pctx := GetGlobalProjectSnapshot()
	if pctx.Path != "" {
		CloseProject()
	}

	projectDir, err := OpenProject(rootDir, path)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to open project: %v", err)), nil
	}

	contextInfo := GetSystemContextForOpenProject(projectDir)

	return mcp.NewToolResultText(fmt.Sprintf("Project opened successfully!\nPath: %s%s\n\nNote: This path is your project PWD (working directory). All subsequent file paths are resolved relative to this directory.", projectDir, contextInfo)), nil
}

// handleListProjects lists all available projects in the root directory.
func handleListProjects(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rootDir := GetGlobalRootDir()
	if rootDir == "" {
		return mcp.NewToolResultError("no project open. Call OpenProject first."), nil
	}
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read root directory: %v", err)), nil
	}

	var projectInfos []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		dirPath := filepath.Join(GetGlobalRootDir(), entry.Name())
		projectInfo := map[string]interface{}{
			"name":     entry.Name(),
			"path":     dirPath,
			"size":     info.Size(),
			"modified": info.ModTime().Format(time.RFC3339),
		}
		projectInfos = append(projectInfos, projectInfo)
	}

	sort.Slice(projectInfos, func(i, j int) bool {
		ti := projectInfos[i]
		tj := projectInfos[j]
		mi, _ := ti["modified"].(string)
		mj, _ := tj["modified"].(string)
		return mi > mj
	})

	if len(projectInfos) == 0 {
		return mcp.NewToolResultText("No projects found."), nil
	}

	data, err := json.MarshalIndent(projectInfos, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal project list: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Found %d projects:\n\n%s", len(projectInfos), string(data))), nil
}

func getWindowsMemoryInfo() (totalPhys uint64, availPhys uint64) {
	kernel32, err := syscall.LoadLibrary("kernel32.dll")
	if err != nil {
		return 0, 0
	}
	defer syscall.FreeLibrary(kernel32)

	proc, err := syscall.GetProcAddress(kernel32, "GlobalMemoryStatusEx")
	if err != nil {
		return 0, 0
	}

	info := MEMORYSTATUSEX{DwLength: uint32(unsafe.Sizeof(MEMORYSTATUSEX{}))}
	ret, _, _ := syscall.SyscallN(uintptr(proc), uintptr(unsafe.Pointer(&info)))
	if ret != 0 {
		return info.UllTotalPhys, info.UllAvailPhys
	}
	return 0, 0
}

func getEnvVars() map[string]string {
	relevantVars := []string{
		"PATH", "HOME", "USERPROFILE", "TEMP", "TMP",
		"GOBIN", "GOPATH", "GOROOT", "NPM_CONFIG_PREFIX",
		"PYTHONPATH", "PYTHONHOME", "DOTNET_CLI_HOME",
		"WINDIR", "PROGRAMFILES", "PROGRAMFILES(X86)",
		"PROGRAMDATA", "SYSTEMDRIVE", "SYSTEMROOT",
	}

	result := make(map[string]string)
	for _, v := range relevantVars {
		if val := os.Getenv(v); val != "" {
			result[v] = val
		}
	}
	return result
}

func tryGetVersion(name string) (string, string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", "", fmt.Errorf("not found: %w", err)
	}

	// Special handling for 'go'
	if name == "go" {
		cmd := exec.Command(path, "version")
		output, _ := execTimeout(cmd, 10*time.Second)
		if len(output) > 0 {
			versionOutput := strings.TrimSpace(string(output))
			parts := strings.Fields(versionOutput)
			if len(parts) >= 3 {
				return name, parts[2], nil
			}
			return name, versionOutput, nil
		}
		return name, "", fmt.Errorf("go version failed")
	}

	versionFlags := []string{"--version", "-version", "/version"}
	for _, flag := range versionFlags {
		cmd := exec.Command(path, flag)
		output, _ := execTimeout(cmd, 5*time.Second)
		if len(output) > 0 {
			versionOutput := strings.TrimSpace(strings.Split(strings.TrimSpace(string(output)), "\n")[0])
			if len(versionOutput) > 100 {
				versionOutput = versionOutput[:100] + "..."
			}
			return name, versionOutput, nil
		}
	}

	return name, "", nil
}

func getInstalledTools() []string {
	tools := []string{
		"go", "python", "python3", "pip", "pip3",
		"node", "npm", "npx",
		"dotnet",
		"git",
		"cmake", "make", "gcc", "g++", "clang",
		"rustc", "cargo",
		"java", "javac",
		"ruby", "gem",
		"wget", "curl",
	}

	var result []string

	for _, name := range tools {
		version, path, err := tryGetVersion(name)
		if err != nil {
			continue
		}
		if version == "" {
			continue
		}

		if name == "dotnet" && version != "installed" {
			result = append(result, fmt.Sprintf("  %s: %s (%s)", name, version, path))
			if sdkVersion := getDotNetSdkInfo(); sdkVersion != "" {
				result = append(result, sdkVersion)
			}
			continue
		}

		result = append(result, fmt.Sprintf("  %s: %s (%s)", name, version, path))
	}

	return result
}

func getDotNetSdkInfo() string {
	cmd := exec.Command("dotnet", "--info")
	output, _ := execTimeout(cmd, 10*time.Second) // timeout after 10 seconds to prevent blocking
	if len(output) == 0 {
		return ""
	}

	var infoLines []string
	lines := strings.Split(string(output), "\n")
	inRuntimeSections := false

	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))

		if strings.Contains(lower, ".net sdk:") || strings.Contains(lower, "microsoft.net.sdk") {
			inRuntimeSections = true
		}
		if inRuntimeSections && strings.HasPrefix(strings.TrimSpace(line), "  ") && len(strings.TrimSpace(line)) > 10 {
			infoLines = append(infoLines, fmt.Sprintf("    %s", strings.TrimSpace(line)))
		}

		if strings.Contains(lower, ".net runtimes:") || strings.Contains(lower, "microsoft.netcore.app.runtime") {
			inRuntimeSections = true
		}
		if inRuntimeSections && strings.HasPrefix(strings.TrimSpace(line), "  ") && len(strings.TrimSpace(line)) > 10 {
			infoLines = append(infoLines, fmt.Sprintf("    %s", strings.TrimSpace(line)))
		}

		if strings.Contains(lower, "base url:") || strings.Contains(lower, "microsoft visual") {
			break
		}
	}

	if len(infoLines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("  .NET Details:\n")
	for _, l := range infoLines {
		sb.WriteString(l + "\n")
	}
	return sb.String()
}

func getSystemResources() []string {
	var result []string

	result = append(result, fmt.Sprintf("  OS: %s/%s", runtime.GOOS, runtime.GOARCH))
	result = append(result, fmt.Sprintf("  CPUs: %d", runtime.NumCPU()))
	result = append(result, fmt.Sprintf("  Goroutines: %d", runtime.NumGoroutine()))

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	result = append(result, fmt.Sprintf("  Memory Used: %s", humanReadableSize(int64(mem.Alloc))))

	if runtime.GOOS == "windows" {
		totalPhys, availPhys := getWindowsMemoryInfo()
		if totalPhys > 0 && availPhys > 0 {
			result = append(result, fmt.Sprintf("  Memory Total: %s", humanReadableSize(int64(totalPhys))))
			result = append(result, fmt.Sprintf("  Memory Available: %s", humanReadableSize(int64(availPhys))))
		} else {
			result = append(result, fmt.Sprintf("  Memory Total: %s (fallback)", humanReadableSize(int64(mem.HeapSys))))
			result = append(result, "  Memory Available: unknown")
		}
	} else {
		result = append(result, "  Memory Total: unknown")
		result = append(result, "  Memory Available: unknown")
	}

	if runtime.GOOS == "windows" {
		kernel32 := syscall.NewLazyDLL("kernel32.dll")
		getDiskFreeSpaceEx := kernel32.NewProc("GetDiskFreeSpaceExW")
		dir, _ := os.Getwd()
		pathPtr, err := syscall.UTF16PtrFromString(dir)
		if err == nil {
			var freeBytesAvailable, totalBytes, totalFreeBytes uint64
			ret, _, _ := getDiskFreeSpaceEx.Call(
				uintptr(unsafe.Pointer(pathPtr)),
				uintptr(unsafe.Pointer(&freeBytesAvailable)),
				uintptr(unsafe.Pointer(&totalBytes)),
				uintptr(unsafe.Pointer(&totalFreeBytes)),
			)
			if ret != 0 {
				result = append(result, fmt.Sprintf("  Disk Available: %s", humanReadableSize(int64(freeBytesAvailable))))
				result = append(result, fmt.Sprintf("  Disk Total: %s", humanReadableSize(int64(totalBytes))))
			} else {
				result = append(result, "  Disk Available: unknown")
				result = append(result, "  Disk Total: unknown")
			}
		} else {
			result = append(result, "  Disk Available: unknown")
			result = append(result, "  Disk Total: unknown")
		}
	} else {
		result = append(result, "  Disk Available: unknown (Unix systems: use df command)")
		result = append(result, "  Disk Total: unknown")
	}

	return result
}

type treeEntry struct {
	name      string
	isDir     bool
	size      int64
	modTime   string
	isSymlink bool
}

func getProjectTree(rootDir string, maxLines int) string {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return "(error reading directory: " + err.Error() + ")"
	}

	baseName := filepath.Base(rootDir)
	if baseName == "" {
		baseName = rootDir
	}

	var lines []string
	lineCount := 0

	lines = append(lines, baseName+"/")
	lineCount++

	var dirs []os.DirEntry
	var files []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry)
		} else {
			files = append(files, entry)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() < dirs[j].Name() })
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })
	allEntries := append(dirs, files...)

	var treeItems []treeEntry
	ignoredDirs := map[string]bool{
		".git": true, ".svn": true, ".hg": true,
		"node_modules": true, "vendor": true,
	}
	for _, entry := range allEntries {
		if ignoredDirs[entry.Name()] {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		item := treeEntry{
			name:      entry.Name(),
			isDir:     entry.IsDir(),
			size:      info.Size(),
			isSymlink: entry.Type()&os.ModeSymlink != 0,
		}
		if !info.ModTime().IsZero() {
			item.modTime = info.ModTime().Format("2006-01-02 15:04")
		}
		treeItems = append(treeItems, item)
	}

	maxDepth := 3
	visited := make(map[string]bool)
	renderTree(rootDir, treeItems, "", &lines, &lineCount, maxLines, maxDepth, 1, visited)

	return strings.Join(lines, "\n")
}

func renderTree(dirPath string, items []treeEntry, prefix string, lines *[]string, lineCount *int, maxLines, maxDepth, depth int, visitedPaths map[string]bool) {
	if *lineCount >= maxLines || depth >= maxDepth {
		if *lineCount < maxLines {
			remaining := len(items)
			*lines = append(*lines, prefix+"└── ... (+"+fmt.Sprintf("%d", remaining)+" more)")
			*lineCount++
		}
		return
	}

	absPath, err := filepath.Abs(dirPath)
	if err == nil {
		if visitedPaths[absPath] {
			if *lineCount < maxLines {
				*lines = append(*lines, prefix+"└── (symlink cycle detected)")
				*lineCount++
			}
			return
		}
		visitedPaths[absPath] = true
	}

	for i, item := range items {
		if *lineCount >= maxLines {
			return
		}

		isLast := i == len(items)-1
		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		sizeStr := formatSize(item.size)
		if item.isDir {
			sizeStr = dirSizePreview(filepath.Join(dirPath, item.name))
		}

		nameCol := item.name
		if item.isSymlink {
			target, _ := os.Readlink(filepath.Join(dirPath, item.name))
			nameCol = fmt.Sprintf("%s -> %s", item.name, target)
		}
		if item.isDir {
			nameCol = item.name + "/"
		}

		line := fmt.Sprintf("%s%-24s  %10s", prefix+connector, nameCol, sizeStr)

		if item.modTime != "" {
			line += "  " + item.modTime
		}

		*lines = append(*lines, line)
		*lineCount++

		if item.isDir && !item.isSymlink {
			subEntries, err := os.ReadDir(filepath.Join(dirPath, item.name))
			if err == nil {
				var subDirs []treeEntry
				var subFiles []treeEntry
				for _, subEntry := range subEntries {
					if strings.HasPrefix(subEntry.Name(), ".") {
						continue
					}
					info, err := subEntry.Info()
					if err != nil {
						continue
					}
					entry := treeEntry{
						name:      subEntry.Name(),
						isDir:     subEntry.IsDir(),
						size:      info.Size(),
						isSymlink: subEntry.Type()&os.ModeSymlink != 0,
					}
					if !info.ModTime().IsZero() {
						entry.modTime = info.ModTime().Format("2006-01-02 15:04")
					}
					if subEntry.IsDir() {
						subDirs = append(subDirs, entry)
					} else {
						subFiles = append(subFiles, entry)
					}
				}
				sort.Slice(subDirs, func(i, j int) bool { return subDirs[i].name < subDirs[j].name })
				sort.Slice(subFiles, func(i, j int) bool { return subFiles[i].name < subFiles[j].name })
				subItems := append(subDirs, subFiles...)
				renderTree(filepath.Join(dirPath, item.name), subItems, childPrefix, lines, lineCount, maxLines, maxDepth, depth+1, visitedPaths)
			}
		}
	}
}

func formatSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(size)/1024)
	}
	if size < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(size)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(size)/(1024*1024*1024))
}

func dirSizePreview(dirPath string) string {
	var total int64
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "0 B"
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return formatSize(total)
}

func GetSystemContextForOpenProject(rootDir string) string {
	var sb strings.Builder

	sb.WriteString("\n=== System Environment Context ===\n")

	sb.WriteString("\nEnvironment Variables:\n")
	envVars := getEnvVars()
	for k, v := range envVars {
		sb.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
	}

	sb.WriteString("\nInstalled Tools:\n")
	tools := getInstalledTools()
	if len(tools) == 0 {
		sb.WriteString("  (none detected)\n")
	} else {
		for _, t := range tools {
			sb.WriteString(t + "\n")
		}
	}

	sb.WriteString("\nSystem Resources:\n")
	resources := getSystemResources()
	for _, r := range resources {
		sb.WriteString(r + "\n")
	}

	sb.WriteString("\nProject Directory Tree:\n")
	tree := getProjectTree(rootDir, 20)
	sb.WriteString(tree + "\n")

	sb.WriteString("=== End Context ===\n")

	return sb.String()
}
