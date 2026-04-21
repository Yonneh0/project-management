# MCP Project File Management

A Go-based [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that indexes, searches, and manages files using a local SQLite database. It provides tools for AI assistants to interact with your file system via stdio transport.

## Features

- **Project Context System** — `OpenProject`/`CloseProject` establish project scope; all paths resolve relative to the active project; automatic YYYYMMDD naming and git initialization
- **Unified CreateItem** — Create files or directories in a single tool with automatic parent directory creation and archive support
- **Unified GetItem** — Read file content, list directories, get metadata, check compile status, compare files, or browse archives via an `action` parameter
- **Unified EditItem** — Edit (find/replace), delete, compress to archive, or extract from archive in one tool
- **CopyItem** — Copy files or recursively copy directories with full metadata reporting and MD5 verification
- **MoveItem** — Move/rename files or directories with fallback for cross-filesystem operations
- **Search** — Three search modes: substring name matching, Go regex on filenames, and grep (file content search)
- **Compile Status** — Check build status for Node.js, Python, .NET, and Go projects with caching (60s TTL)
- **Archive Support** — Read/write ZIP, TAR, TAR.GZ archives; compress files/folders; extract entries to filesystem
- **Auto-Monitoring** — File watcher automatically indexes new/modified files in real-time (ignores its own database file)
- **Local SQLite Storage** — All index data stored in a lightweight, portable database with MD5 hashing

## Prerequisites

- [Go 1.21+](https://go.dev/dl/) installed on your system
- A C compiler (required by the `modernc.org/sqlite` pure-Go SQLite library)
  - **Windows**: Download [MinGW-w64](https://www.mingw-w64.org/) or use [TDM-GCC](https://tdm-gcc.tdmlab.com/)
  - **macOS**: Xcode Command Line Tools (`xcode-select --install`)
  - **Linux**: `build-essential` package (`sudo apt install build-essential`)

## Installation

### Build from Source

```bash
# Navigate to the project directory
cd mcp-project-file-management

# Download dependencies
go mod download

# Build the binary
go build -o .
```

The compiled binary `mcp-project-file-management` (or `mcp-project-file-management.exe` on Windows) will be created in the current directory.

### Run Directly (Development)

```bash
go run .
```

Or specify a custom target directory:

```bash
go run . "E:\sandbox\AI_SLOP\"
```

## Usage

The server runs in **stdio mode** by default, communicating via standard input/output — the standard transport for MCP servers.

```bash
./mcp-project-file-management [target_directory]
```

| Argument | Description | Default |
|----------|-------------|---------|
| `target_directory` (optional) | Root directory to monitor and index files | `C:\Projects\AI` |

The server will:
1. Initialize a SQLite database at `<target_directory>/.mcp_file_index.db`
2. Start monitoring the target directory for file changes
3. Begin accepting MCP tool calls via stdin/stdout

## Adding to LM Studio

LM Studio supports MCP servers, allowing AI models to call your tools directly. Here's how to configure it:

### Step 1: Build or Place the Binary

Ensure the compiled binary is accessible at a known path, e.g.:
- `C:\Projects\AI\mcp-project-file-management\mcp-project-file-management.exe`
- Or wherever you built/placed it

### Step 2: Configure MCP Servers in LM Studio

1. Open **LM Studio**
2. Navigate to **Settings** → **MCP Servers** (or open the MCP configuration)
3. Add a new MCP server with the following configuration:

| Field | Value |
|-------|-------|
| **Server Name** | `mcp-project-file-management` |
| **Command** | Full path to the binary (e.g., `C:\Projects\AI\mcp-project-file-management\mcp-project-file-management.exe`) |
| **Arguments** | *(optional)* Target directory, e.g., `C:\Projects\AI` |

Or via the LM Studio MCP settings JSON file (`settings.json` or `mcp-config.json`):

```json
{
  "mcpServers": {
    "mcp-project-file-management": {
      "command": "C:\\Projects\\AI\\mcp-project-file-management\\mcp-project-file-management.exe",
      "args": ["C:\\Projects\\AI"]
    }
  }
}
```

> **Note:** Replace the paths above with your actual binary location and desired target directory.

### Step 3: Verify Connection

1. Save the configuration
2. Restart LM Studio or reload MCP servers
3. The server's tools should now appear in the model's available toolset

## Available Tools

### `OpenProject`

Open an existing project or create a new one. Sets the active project context for all subsequent operations. All file paths are resolved relative to this project until closed. Automatically initializes git if not already present.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | No | "" | Project path (absolute or relative to rootDir). Empty = auto-generate YYYYMMDD name. |

**Behavior:**
- If `path` is empty: Auto-generates a `YYYYMMDD`-named project folder and initializes git
- If `path` exists as directory: Opens it as the current project (initializes git if needed)
- If `path` doesn't exist: Creates it and initializes git

**Response:** Project path, name hint, and creation status.

---

### `CloseProject`

Close the current project and reset the global context. All tools will require an `OpenProject` call before use afterward.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| *(none)* | — | — | — | — |

**Response:** Confirmation that project is closed.

---

### `CreateItem`

Create a new file or directory at the specified path. Automatically creates parent directories if they don't exist. Supports creating entries inside archives (ZIP, TAR, TAR.GZ). All changes are auto-committed to git when a project is open.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | Yes | — | Absolute or relative path where the item should be created. For archives use `archive.zip/path/to/entry`. |
| `content` | string | No | "" | Content to write to the file (required for files, optional for folders) |
| `isFolder` | boolean | No | false | If true, create a folder instead of a file |
| `overwrite` | boolean | No | false | For files: if true, overwrite existing file. For folders: if true, return success when folder exists |

**Response:** Action type (Created/Overwritten), absolute path, size in human-readable format and bytes, MIME type detection, modification time, parent directories created count, operation elapsed time, index update confirmation with MD5 hash.

---

### `GetItem`

Read file content, list directory contents, get metadata, check compile status, compare files, or browse archives — all through a single unified tool with an `action` parameter. Supports archive paths like `archive.zip/subdir/file.txt`.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | Yes | — | Absolute or relative path of the item. For archives use `archive.zip/path/to/entry`. |
| `action` | string | No | "auto" | Action: `auto` (detect), `read` (file content), `list` (directory contents), `info` (metadata only), `compile` (build status), `diff` (compare with file2), `archive-list` (list archive contents) |
| `offset` | number | No | 0 | Byte offset for file reading (0 = start) |
| `length` | number | No | -1 | Read control: -1=read entire file, 0=metadata only, >0=N bytes to read |
| `line` | number | No | — | 1-based single line number to read (overrides offset/length) |
| `startLine` | number | No | — | 1-based start of line range (inclusive) |
| `endLine` | number | No | — | 1-based end of line range (inclusive) |
| `encoding` | string | No | "utf-8" | Character encoding hint |
| `recursive` | boolean | No | false | For directories: list recursively |
| `maxItems` | number | No | 100 | Maximum directory entries to return (0 = unlimited) |
| `includeHidden` | boolean | No | false | Include hidden/dot files in results |
| `sortBy` | string | No | "name" | Sort order: name, size, date, type |
| `severity` | string | No | "all" | For compile action: errors \| warnings \| all |
| `checksum` | string | No | — | Compute hash: md5 or sha256 (file mode only) |
| `file2` | string | No | — | Second file path for diff action |
| `ignoreWhitespace` | boolean | No | false | For diff: ignore leading/trailing whitespace |
| `ignoreCase` | boolean | No | false | For diff: case-insensitive comparison |
| `noCache` | boolean | No | false | For compile: bypass cache |

**Action modes:**

- **`auto`** (default): Automatically detects whether path is a file (`read`) or directory (`list`). For archives, uses `archive-list`.
- **`read`**: Read file content with offset/length support, line-range selection, binary detection. Supports reading from archive entries.
- **`list`**: List directory contents with sorting, filtering, recursive walking. Also lists archive contents.
- **`info`**: Get detailed metadata (permissions, MIME type, MD5 hash, timestamps). Archive info shows format and entry count.
- **`compile`**: Check build status for Node.js, Python, .NET, and Go projects (with 60s cache)
- **`diff`**: Compare two files with unified diff output and metadata comparison
- **`archive-list`**: List all entries in a ZIP/TAR/TAR.GZ archive

**Response varies by action:**
- `read`: File content with size, MIME type, offset/length info, binary detection warning
- `list`: Directory/archive entries with type indicators ([D]/[F]), sizes, sort order used, summary counts
- `info`: Full metadata including permissions (rwx + octal), MIME type, MD5 hash, timestamps
- `compile`: Detection results per runtime, build status, error/warning output
- `diff`: Metadata comparison table, MD5 match check, unified diff with lines added/removed

---

### `EditItem`

Edit, delete, compress, or extract files. Supports multiple operations via the `action` parameter. All changes are auto-committed to git when a project is open.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | Yes | — | Absolute or relative path of the target. For archives use `archive.zip/path/entry` format. |
| `action` | string | No | "edit" | Action: `edit`, `delete`, `compress`, `extract` |
| `oldText` | string | No | — | For action=edit: text to find and replace |
| `newText` | string | No | — | For action=edit: replacement text |
| `count` | number | No | 1 | For action=edit: number of occurrences to replace (0 = all, default 1) |
| `compressToArchive` | string | No | — | For action=compress: destination archive path (.zip, .tar.gz, etc.) |
| `deleteOriginalAfterCompress` | boolean | No | false | For action=compress: delete source after compressing |
| `extractFromArchive` | string | No | — | For action=extract: archive path to extract from (e.g., 'archive.zip/entry/path') |
| `recursive` | boolean | No | false | For action=delete on directories: if true, delete all contents recursively |
| `ignoreMissing` | boolean | No | true | For action=delete: return success instead of error when item doesn't exist |

**Action modes:**

- **`edit`** (default): Find and replace text in a file. Reports occurrences found vs replacements made with size delta.
- **`delete`**: Delete a file or directory. Directories require `recursive=true` if not empty. Returns error for missing items unless `ignoreMissing=true`.
- **`compress`**: Compress file(s)/folder into an archive (.zip, .tar.gz). Supports adding to existing archives. Optional `deleteOriginalAfterCompress`.
- **`extract`**: Extract from archive and write to filesystem. Use `path` as destination or `archive.zip/entry/path` format.

**Supported archive formats:** ZIP, TAR, TAR.GZ (GZIP compressed)

---

### `CopyItem`

Copy a file or directory from source to destination. Directories are copied recursively with full content preservation. All changes are auto-committed to git when a project is open.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `source` | string | Yes | — | Source file or directory path (absolute or relative) |
| `destination` | string | Yes | — | Destination file or directory path (absolute or relative) |
| `overwrite` | boolean | No | false | If true, overwrite existing destination |

**Response includes:** Action type (Copied/Overwritten), source and destination paths (both absolute), full metadata for both files (size in human-readable and bytes, MIME type, modification time, permissions, MD5 hash), total bytes copied, operation elapsed time. For directories: recursive copy with byte count.

---

### `MoveItem`

Move or rename a file or directory. Directories are moved with all contents. Uses OS rename when possible, falls back to copy+delete for cross-filesystem moves. All changes are auto-committed to git when a project is open.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `source` | string | Yes | — | Source file or directory path (absolute or relative) |
| `destination` | string | Yes | — | Destination file or directory path (absolute or relative) |
| `overwrite` | boolean | No | false | If true, overwrite existing destination |

**Response includes:** Move type classification (Moved/Renamed based on whether same directory), source and destination paths, size in human-readable and bytes, modification time, MD5 hash of original, index update confirmation. For directories: recursive move with byte count.

---

### `Search`

Search for files and directories by name pattern, regex, or file content (grep). Three search modes available. All changes are auto-committed to git when a project is open.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | Yes | — | Root directory path to search within |
| `pattern` | string | Yes | — | Pattern to search for (substring match, regex pattern, or grep pattern) |
| `mode` | string | No | "name" | Search mode: `name` (substring), `regex` (Go regex on filenames), `grep` (search file contents) |
| `limit` | number | No | 50 | Maximum number of results to return |
| `maxMatchesPerFile` | number | No | 10 | For grep mode: maximum matches per file |
| `includeHidden` | boolean | No | false | Include hidden/dot files in search |
| `fileOnly` | boolean | No | false | Search only files (exclude directories) |
| `dirOnly` | boolean | No | false | Search only directories (exclude files) |
| `extensions` | string | No | — | Comma-separated list of file extensions to filter by (e.g., '.go,.py') |
| `contextLines` | number | No | 0 | For grep mode: number of context lines around matches |

**Search modes:**

- **`name`** (default): Case-insensitive substring matching on filenames. Returns files and directories whose names contain the pattern.
- **`regex`**: Go regex syntax matched against both filename and full path. Compile error returned for invalid patterns.
- **`grep`**: Searches actual file contents (skips binary files). Supports context lines around matches.

**Response includes:** Search root path, pattern used, mode description, total matches found count, breakdown by file/directory counts. Each result shows type indicator ([D]/[F]), full path, size annotation for files, and MIME type hint where applicable. Results truncated at limit with count of hidden results. Grep mode shows `filepath:lineNumber: content` format.

---

### Compile Status (via GetItem action="compile")

Check compile/build status for projects using various runtimes (Node.js, Python, .NET, Go). Scans the specified directory for project files and reports the status of each runtime. Results are cached for 60 seconds with automatic invalidation on file changes.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `path` | string | Yes | — | Path to the project directory to check |
| `severity` | string | No | "all" | Filter: errors, warnings, or all |
| `noCache` | boolean | No | false | Bypass cache to force fresh check |

**Response includes:** Detection results per runtime (Node.js, Python, .NET, Go), installed runtimes status with version, package manager availability, build/compile test results. Cached responses marked with `(cached)`.

## Project Structure

```
mcp-project-file-management/
├── main.go                    # Server entry point, stdio transport setup, database initialization
├── shared_types.go            # Shared types (dirEntryInfo, sortEntries, CompileResult, compileCache, ArchiveInfo, ProjectContext)
├── db.go                      # SQLite database operations (index, search, duplicates, MD5 hashing, directory stats)
├── watcher.go                 # File system watcher for auto-indexing (ignores .mcp_file_index.db)
├── newproject.go              # OpenProject/CloseProject project lifecycle with git initialization
├── mcp_tools.go               # MCP tool registration (OpenProject, CloseProject, CreateItem, GetItem, EditItem, CopyItem, MoveItem, Search)
├── mcp_args.go                # Argument extraction helpers (extractArg, extractOptional*)
├── mcp_create.go              # CreateItem tool handler (files, directories, and archive entries)
├── mcp_get.go                 # GetItem tool handler (read, list, info, compile, diff, archive-list actions)
├── mcp_edit.go                # EditItem tool handler (edit, delete, compress, extract actions)
├── mcp_copy.go                # CopyItem tool handler (files and recursive directory copy)
├── mcp_move.go                # MoveItem tool handler (rename/move with cross-filesystem fallback)
├── mcp_search.go              # Search tool handler (name, regex, grep modes)
├── mcp_compile.go             # Compile status helpers (Node.js, Python, .NET, Go detection and build checks)
├── mcp_utils.go               # Utility functions (MIME detection, human-readable sizes, diff, directory copy, archive I/O, git commit)
├── go.mod                     # Go module definition
├── go.sum                     # Dependency checksums
└── README.md                  # This file
```

## Architecture

- **MCP Server**: Uses [`github.com/mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) library for MCP protocol compliance
- **Database**: [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) — pure Go SQLite implementation (no CGO required)
- **File Watching**: `github.com/fsnotify/fsnotify` for cross-platform file system event monitoring
- **Compile Cache**: In-memory cache with configurable TTL (60 seconds default), auto-invalidated on file changes via watcher hooks
- **Archive Cache**: In-memory cache for open archives (ZIP, TAR, TAR.GZ) with lazy loading

## Project Context System

This server uses a project context model where operations are scoped to an active project:

1. **Call `OpenProject` first** to set the working directory and establish path boundaries
2. All subsequent tools resolve relative paths against the open project
3. Paths outside the project boundary are rejected for safety
4. **Call `CloseProject`** when done to reset the context

This ensures that operations like `CreateItem`, `CopyItem`, etc. only affect files within the active project scope.

## Data Model

The SQLite database stores indexed files with the following schema:

```sql
CREATE TABLE IF NOT EXISTS files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    size INTEGER NOT NULL,
    mod_time TEXT NOT NULL,
    md5_hash TEXT NOT NULL,
    is_dir BOOLEAN NOT NULL DEFAULT 0
);
```

- Each file is indexed with its MD5 hash for duplicate detection
- Directories are tracked with size 0 and empty MD5 (sizes calculated on-demand)
- Index updates automatically via file watcher or tool operations
- Database file (`*.mcp_file_index.db*`) is excluded from its own scan and watching

## License

MIT