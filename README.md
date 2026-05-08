# MCP Project File Management

A Go-based [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that provides AI assistants with unified file system tools, shell execution, and HTTP client capabilities via **stdio transport**.

- Brought to you by Carls' Jr.

## Features

- **stdio Transport** — Standard MCP transport via stdin/stdout for MCP hosts like LM Studio, Claude Desktop, etc.
- **Project Context System** — Open, close, and list projects (`OpenProject`, `CloseProject`, `ListProjects`). Project state persisted in `project-management.json`; all file paths resolve relative to the open project.
- **Unified GetItem** — Read file content (text or binary), list directories, get metadata via a single tool with offset/length/line support; auto-detects text/binary format and provides hex dump output for binary files. Supports chunked reading for large files (>5MB threshold). For detailed binary viewing use ViewBinary instead.
- **Unified EditItem** — Create files/directories and perform line-based text replacement in one tool; for binary editing use ViewBinary.
- **ViewBinary** — Hex dump viewer for binary files: three formats (hex/compact/raw), configurable row width (8/16/32) and address display, chunked reading via offset/length parameters
- **Shell Execution** — Execute commands via cmd, PowerShell, Node.js, Python, sh, bash, or zsh; supports streaming output mode for long-running builds and custom environment variables
- **HTTP Client (GetURL)** — Fetch URLs with configurable HTTP options: method, headers, cookies, referrer, user agent, and body; save response bodies to the open project directory; validates URL scheme to prevent SSRF attacks

## Prerequisites

- [Go 1.26.2](https://go.dev/dl/) installed on your system
- No C compiler required (pure Go, no CGO needed)

## Installation

### Build from Source

```bash
# Navigate to the project directory
cd project-management

# Download dependencies
go mod download

# Build binary
go build -trimpath -ldflags="-s -w" -o .
```

The compiled binary will be created in the current directory (`project-management.exe` on Windows, `project-management` on Linux/macOS).

---

## Command-Line Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-target-dir` | string | Current working directory | Root directory for project operations and `OpenProject` path resolution. Defaults to current directory if omitted |

---

## Integrating with MCP Clients

### Generic MCP Hosts (stdio transport)

Any MCP host that supports stdio transport can use the server. Example JSON-RPC config:

```json
{
  "mcpServers": {
    "project-management": {
      "command": "/home/username/projects/project-management",
      "args": ["-target-dir", "/home/username/projects/ai" ]
    }
  }
}
```

```json
{
  "mcpServers": {
    "project-management": {
      "command": "E:\\Projects\\AI\\project-management.exe",
      "args": [ "-target-dir", "E:\\Projects\\AI" ]
    }
  }
}
```
---

## Constants Reference

| Constant | Value | Description |
|----------|-------|-------------|
| `MaxFileSize` | 10MB (10,485,760 bytes) | Maximum single-file read size for line-based reads; offset/length reading supports larger files up to 50MB |
| `LargeFileThreshold` | 5MB (5,242,880 bytes) | Threshold for "large file" hint and chunked read suggestions |
| `MaxOutputLength` | 10,000 bytes | Default shell output truncation limit |
| `DefaultShellTimeout` | 30 seconds | Default shell command timeout |
| `DefaultGetURLTimeout` | 30 seconds | Default HTTP request timeout |
| `ProgressThreshold` | 1MB (1,048,576 bytes) | Minimum file size for progress indicators (`showProgress=true`) |
| `DefaultMaxItems` | 100 | Default max directory listing entries |

## Project Structure

```
project-management/
├── core.go                    # MCP tool registration, project context, path resolution, thread-safe state
├── edit.go                    # EditItem handler: create/edit operations, line-based text replacement
├── get.go                     # GetItem handler: read/list/info actions, directory listing, metadata
├── go.mod                     # Go module definition (Go 1.26.2)
├── go.sum                     # Dependency checksums
├── main.go                    # Server entry point, flag parsing, stdio mode
├── README.md                  # This file
├── shell.go                   # ExecShell + GetURL handlers: multi-shell command execution, HTTP client, binary viewer (ViewBinary)
└── utils.go                   # Utilities: hex dump/parse, generic extractArg, text encoding, permissions, system information
```
# MCP Tools Reference
1. [ListProjects](#1-listprojects)
2. [OpenProject](#2-openproject)
3. [GetItem](#3-getitem)
4. [EditItem](#4-edititem)
5. [CloseProject](#5-closeproject)
6. [ExecShell](#6-execshell)
7. [GetURL](#7-geturl)
8. [ViewBinary](#8-viewbinary)

---

## 1. ListProjects

**Purpose:** List all available projects in the root directory without opening any specific one.

**Project Context Required:** No (this tool works independently)

### Parameters

*This tool accepts no parameters.*

### Behavior

- Returns a JSON array of project info with name, path, size, and modification time
- Sorted by most recently modified (descending)
- Useful as a first step before calling OpenProject or ViewBinary

### Example

```
ListProjects()
```

---

## 2. OpenProject

**Purpose:** Open an existing project or create a new one. Sets the active project context for all subsequent file operations.

**Project Context Required:** No (this is the bootstrap tool)

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | **Yes** | — | Project name/path relative to rootDir (required). |

### Behavior

- A valid project name/path must be provided; if blank/empty, an error is returned
- If `path` exists as a directory: opens it as the current project
- If `path` does not exist: creates it as a new project
- Automatically closes any previously open project before opening a new one
- All subsequent tool paths are resolved relative to the opened project root

### Project Name Requirements

- Must be 1–64 characters
- Alphanumeric letters, hyphens (`-`), underscores (`_`), and dots (`.`) only
- Cannot start with `.` or contain `..`
- Cannot be the root directory itself

### Examples

```
# Open an existing directory by name
OpenProject(path="my-project")

# Create a new project
OpenProject(path="new-project")

# Re-open an already-open project (refreshes context)
OpenProject(path="existing-directory")
```

---

## 3. GetItem

**Purpose:** Read file content (text or binary), list directory contents, or get file/directory metadata via a single tool with offset/length/line support. Auto-detects text/binary format and provides hex dump output for binary files; set `format="hex"` to force hex output regardless of auto-detected type. For detailed binary viewing use ViewBinary instead.

**Project Context Required:** Yes

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | **Yes** | — | Path of the file or directory (resolved against project root) |
| `action` | string | No | `"auto"` | `auto` (detect file/dir), `read` (content or listing) |
| `offset` | number | No | `0` | Byte offset for file reading (`0` = start of file). Use with length for chunked reading. |
| `length` | number | No | `-1` | Read control: `-1` = entire file, `0` = metadata only (no content returned), `>0` = exactly N bytes from offset |
| `line` | number | No | — | 1-based single line number to read (overrides offset/length). **Note:** Line-based reading requires the entire file to fit within MaxFileSize (10MB). For larger files, use offset/length. |
| `startLine` | number | No | — | For text read: 1-based start of range (inclusive). |
| `endLine` | number | No | — | For text read: 1-based end of range (inclusive). Required with startLine. |
| `format` | string | No | `"auto"` | `auto`, `text`, `hex` (hex dump with ASCII preview) |
| `recursive` | boolean | No | `false` | Walk directory recursively |
| `maxItems` | number | No | `100` | Maximum directory entries to return. `0` = unlimited. |
| `includeHidden` | boolean | No | `false` | Include hidden/dot files and directories |
| `sortBy` | string | No | `"name"` | `name`, `size`, `date`, `type` |

### Examples

```
# Read first 1000 bytes of a text file (auto format detects text → returns raw text)
GetItem(path="src/main.go", offset=0, length=1000)

# Read with hex dump format output
GetItem(path="data.bin", format="hex")

# Read line 42
GetItem(path="src/main.go", line=42)

# Read lines 10-20 (inclusive)
GetItem(path="src/main.go", startLine=10, endLine=20)

# List directory recursively
GetItem(path="src/", recursive=true)

# Metadata only (no content returned)
GetItem(path="src/main.go", length=0)

# Chunked read: bytes 512–768 from a large file
GetItem(path="data.bin", offset=512, length=257)
```

### Common Errors

| Error | Cause |
|-------|-------|
| `Item not found` | Path does not exist |
| `path resolution failed` | Path is outside project boundary (outside active project) |
| `file size exceeds maximum` | File >10MB requires offset/length for line-based reads |
| `no project open` | Call OpenProject first |

### Notes

- The 10MB `MaxFileSize` limit applies specifically to **line-based reading** (`line`, `startLine/endLine`). Regular offset/length reading supports larger files up to the file's actual size.
- Windows permission bits may not reflect actual writability (simulated on Windows)
- Hidden files are skipped when includeHidden=false in directory listings
- Hidden directories are skipped entirely (not their contents walked) during recursive listing

---

## 4. EditItem

**Purpose:** Create and edit **TEXT** files and directories via line-based replacement (for binary/hex editing use ViewBinary). Returns structured JSON metadata alongside the text result for easy AI parsing.

**Project Context Required:** Yes

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | **Yes** | — | Target file or directory path (resolved against project root) |
| `action` | string | No | `"edit"` | `edit`, `create` |
| `content` | string | No | — | Content for `create` action |
| `replacement` | string | No | — | New text to replace the specified line range with |
| `startLine` | number | No | — | 1-based start line (inclusive) |
| `endLine` | number | No | — | 1-based end line (inclusive; if omitted, equals startLine) |
| `isFolder` | boolean | No | `false` | Create a directory instead of a file |
| `recursive` | boolean | No | `false` | Delete directory contents recursively |
| `ignoreMissing` | boolean | No | `true` | Return success if item already exists (create action) |
| `overwrite` | boolean | No | `false` | Overwrite existing files or directories |
| `encoding` | string | No | `"utf-8"` | Text encoding: `utf-8`, `utf-8-bom`, `utf-16le`, `utf-16be`, `cp1252`, `ascii` |
| `showProgress` | boolean | No | `false` | Show progress for files ≥ 1 MB |

### Actions

#### edit (default)

Replace lines in a file by 1-based line number range (line-based TEXT replacement; for binary/hex editing use ViewBinary). File's trailing newline is preserved. Returns structured JSON metadata at the end of the result.

- If only `startLine` is given: replaces a single line (`endLine` defaults to `startLine`)
- Trailing newline behavior: if original file ends with `\n`, replacement text gets `\n` appended automatically; otherwise no `\n` is added.

```
# Replace lines 5-10 (multi-line)
EditItem(path="src/main.go", startLine=5, endLine=10, replacement="// new code here\n// with continuation")

# Replace single line (endLine defaults to startLine — no action needed by default)
EditItem(path="src/main.go", startLine=42, replacement="// updated function")
```

#### create

Create a new file or directory. Automatically creates parent directories. Returns structured JSON metadata at the end of the result.

- Specify `action="create"` to explicitly request creation (not needed for files — edit is default).
- For folders: set `isFolder=true`. (Works with both `action=edit` and `action=create`; parent directories are automatically created.)

```
# Create with content
EditItem(path="src/main.go", action="create", content="package main\n\nfunc main() {}\n")

# Create a directory (also works with action="edit" + isFolder=true)
EditItem(path="assets/images", action="create", isFolder=true)
```

### Parameter Notes

| Note | Details |
|------|---------|
| `ignoreMissing` | Default: `true`. When creating, returns success if the item already exists (no error). Set to `false` during development to catch creation errors. |

### Structured Metadata (re #4)

All EditItem results include a JSON metadata block:

```json
{
  "operation": "create_file" or "edit",
  "affected_path": "/path/to/file",
  "lines_changed": { "start": 5, "end": 10, "count": 6 },
  "encoding_used": "utf-8" or other encoding
}
```

---

## 5. CloseProject

**Purpose:** Close the currently active project. Resets the global project context so all subsequent file operations will require calling OpenProject first.

**Project Context Required:** No (detects whether a project is open)

### Parameters

*This tool accepts no parameters.*

### Behavior

- Closes the current project and resets GlobalProject to nil
- Automatically removes persistence state from project-management.json
- Returns confirmation message with previously open project path

### Example

```
CloseProject()
```

---

## 6. ExecShell

**Purpose:** Execute a shell command in the current project directory with timeout control.

**Note:** All paths are relative to the open project directory. Quote escaping differs between shells — see below for examples.

**Project Context Required:** Yes

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `command` | string | **Yes** | — | The command to execute |
| `shell` | string | No | `cmd` (Windows) / `sh` (Unix) | Shell interpreter: `cmd`, `powershell`, `node`, `python`, `sh`, `bash`, `zsh` |
| `timeout` | number | No | `30` | Timeout in seconds (must be > 0) |
| `env` | string | No | — | Custom env vars: `"KEY=value;KEY2=value2"` |
| `stream` | boolean | No | `false` | Streaming output for long-running commands |

### Auto-Set Environment Variables

| Variable | Value |
|----------|-------|
| `PWD` | Current project path |
| `OP_PROJECT_PATH` | Current project path |
| `OP_PROJECT_NAME` | Current project directory name |

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `124` | Timed out (standard GNU timeout code) |
| `125` | Canceled |
| `127` | Command not found |
| Other | Command-specific |

### Examples

```
# Basic command (Windows)
ExecShell(command="dir /b", shell="cmd")

# Streaming build
ExecShell(command="npm run build", stream=true, timeout=300)

# Custom environment variables (semicolon or newline separated)
ExecShell(command="go run main.go", env="GOOS=linux;GOPROXY=https://goproxy.io")

# PowerShell (uses -Command flag internally)
ExecShell(command="Get-Process", shell="powershell")

# Node.js (executes JavaScript via node -e, no wrapper needed)
ExecShell(command="console.log(require('fs').readdirSync('.'))", shell="node")

# Python (Python code directly via python -c)
ExecShell(command="print([x for x in range(10)])", shell="python")
```

### Cross-Platform Notes

| Note | Details |
|------|---------|
| Default shell | `cmd` on Windows, `sh` on Unix-like systems |
| Path separators | Forward slashes (`/`) work on all platforms |
| Line endings | Output preserves original line endings (CRLF on Windows, LF on Unix) |

### Quote Escaping Examples

```
# In cmd.exe: escaped nested quotes (cmd requires backslash for nested quotes)
ExecShell(command="echo \"hello \\\"world\\\"", shell="cmd")

# In PowerShell: single quotes for literal strings, double quotes for interpolation
ExecShell(command='Write-Output "Hello World"', shell="powershell")

# Complex command with mixed quotes (use powershell for complex quoting in cmd)
ExecShell(command='echo "hello world" | Get-Member', shell="powershell")
```
---

## 7. GetURL

**Purpose:** Fetch a URL with configurable HTTP options. The fetch operation does NOT require an open project context; however, saving the response to the project requires one (`saveToProject=true`).

**Project Context Required:** No (required only when `saveToProject=true`)

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `url` | string | **Yes** | — | Full URL including protocol (e.g., `https://example.com`) |
| `method` | string | No | `"GET"` | HTTP method: `GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `HEAD` |
| `headers` | string | No | — | Additional headers: `"Key: Value\nKey2: Value2"` (whitespace around colons trimmed) |
| `cookies` | string | No | — | Cookies: `"name=value; name2=value2"` |
| `referrer` | string | No | — | Referrer/Referer header value |
| `userAgent` | string | No | — | User-Agent header value |
| `body` | string | No | — | Request body (POST/PUT/PATCH only; ignored for GET/HEAD/DELETE) |
| `timeout` | number | No | `30` | Request timeout in seconds |
| `saveToProject` | boolean | No | `false` | Save response body to project directory |
| `filename` | string | No | — | Custom filename for saved response (auto-generated from URL if omitted) |

### Supported URL Schemes

- `http://` — Plain HTTP
- `https://` — HTTPS (TLS with system CA certificates)

### Examples

```
# Simple GET (does not require OpenProject)
GetURL(url="https://api.example.com/data")

# POST with JSON body and multiple headers
GetURL(url="https://api.example.com/submit", method="POST", body='{"key":"value"}', headers="Content-Type: application/json\nAccept: text/html")

# Save response to project (requires OpenProject first — response body is NOT returned in result)
GetURL(url="https://example.com/file.json", saveToProject=true, filename="downloaded.json")

# Request with cookies and user agent
GetURL(url="https://api.example.com/data", cookies="session=abc123; auth=xyz", userAgent="MCP-Agent/1.0")
```

### Notes

| Note | Details |
|------|---------|
| Redirects | Followed automatically (up to 10 hops) |
| TLS | Uses Go's default HTTP client: system CA certificate bundle — self-signed certificates will fail |
| Proxy | No proxy support currently configured |
| Content-Type | NOT set automatically for POST/PUT/PATCH — provide via headers if needed |

### Filename Generation (saveToProject)

When `filename` is omitted, filename is auto-generated from URL: `host + path`. Characters sanitized (`:` → `-`, `< > | → -`, spaces → `_`). Example: `https://api.example.com/data.json` → `api.example.com-data.json`.

---

## 8. ViewBinary

**Purpose:** Display a hex dump of a binary file with configurable format and width options.

**Project Context Required:** Yes (all paths resolved against open project root)

### Parameters

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | **Yes** | — | Path to the binary file (resolved against open project root, same behavior as GetItem) |
| `offset` | number | No | `0` | Starting byte position in the file. Must be >= 0. |
| `length` | number | No | `-1` | Bytes to read: `-1` = entire remaining file from offset, `>0` = exact number of bytes. |
| `format` | string | No | `"hex"` | Output format: `hex`, `raw`, `compact`. |
| `bytesPerRow` | number | No | `16` | Bytes per row in hex output (options: 8, 16, 32). Alias: `width`. |
| `showAddresses` | boolean | No | `true` | Include byte addresses on each line of the hex dump. |

### Output Formats

| Format | Description |
|--------|-------------|
| `hex` (default) | Standard hex dump with ASCII preview, 16 bytes per row. Includes byte addresses. |
| `raw` | Continuous hex string without spaces or columns — useful for copying to other tools. |
| `compact` | One line of hex per offset block (`offset: hex...`) — easy to read and compact. |

### Examples

```
# Full hex dump (hex format, 16 bytes per row)
ViewBinary(path="data.bin")

# Read first 64 bytes in raw format, 8 per row
ViewBinary(path="data.bin", length=64, format="raw", width=8)

# Compact hex view
ViewBinary(path="data.bin", format="compact")

# View bytes 1024-2048 (offset=1024, length=1024)
ViewBinary(path="data.bin", offset=1024, length=1024)

# Hex dump without address column
ViewBinary(path="data.bin", showAddresses=false)

# 32 bytes per row in hex format
ViewBinary(path="data.bin", bytesPerRow=32)
```

### Notes

| Note | Details |
|------|---------|
| Chunked reading | Use `offset` and `length` for large files (>5MB triggers chunked read hint) |
| Path resolution | Resolved against open project root (same behavior as GetItem) |
| Format alias | `width` is an alias for `bytesPerRow` |
| Text vs binary | ViewBinary reads raw bytes and displays them as a configurable hex dump — ideal for binary files (images, executables, etc.). Use offset/length to read portions of large files. |

---

## Security & Safety

### Path Boundary Enforcement

All file operations are confined to the active project directory. Paths outside the project boundary are rejected to prevent unauthorized access.

### Project Name Validation

Project names are validated to prevent path traversal attacks:
- No `..` sequences
- No leading dots
- Alphanumeric characters, hyphens, underscores, and dots only

### Timeout Protection

- Shell commands: Default 30-second timeout (configurable)
- HTTP requests: Default 30-second timeout (configurable)

---

## Constants Reference

| Constant | Value | Description |
|----------|-------|-------------|
| `MaxFileSize` | 10MB | Maximum single-file read size (10,485,760 bytes) |
| `LargeFileThreshold` | 5MB | Threshold for chunked read hints (5,242,880 bytes) |
| `MaxOutputLength` | 10000 bytes | Default output truncation limit |
| `DefaultShellTimeout` | 30 seconds | Default shell command timeout |
| `DefaultGetURLTimeout` | 30 seconds | Default HTTP request timeout |
| `ProgressThreshold` | 1MB | Minimum file size for progress indicators (files ≥ this size show progress) |
| `DefaultMaxItems` | 100 | Default max directory listing entries |

---
