# MCP Project File Management

A Go-based [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that indexes, searches, and manages files using an in-memory store with JSON persistence. It provides tools for AI assistants to interact with your file system via stdio transport.

## Features

- **Project Context System** — `OpenProject`/`CloseProject` establish project scope; all paths resolve relative to the active project; automatic YYYYMMDD naming and git initialization
- **Unified CreateItem** — Create files or directories in a single tool with automatic parent directory creation and archive support
- **Unified GetItem** — Read file content, list directories, get metadata, check compile status, compare files, or browse archives via an `action` parameter
- **Unified EditItem** — Edit (find/replace), delete, compress to archive, or extract from archive in one tool
- **Copy/Move actions in EditItem** — Copy files or recursively copy directories; move/rename files or directories with fallback for cross-filesystem operations
- **Search** — Three search modes: substring name matching, Go regex on filenames, and grep (file content search)
- **Compile Status** — Check build status for Node.js, Python, .NET, and Go projects with caching (60s TTL)
- **Archive Support** — Read/write ZIP, TAR, TAR.GZ archives; compress files/folders; extract entries to filesystem
- **Auto-Monitoring** — File watcher automatically indexes new/modified files in real-time (ignores its own database file)
- **In-Memory Storage** — All index data stored in thread-safe memory with debounced JSON persistence and MD5 hashing

## Prerequisites

- [Go 1.21+](https://go.dev/dl/) installed on your system
- No C compiler required (pure Go, no CGO needed)

## Installation

### Build from Source

```bash
# Navigate to the project directory
cd project-management

# Download dependencies
go mod download

# Build with optimizations (PowerShell)
$env:CGO_ENABLED="0"; go build -ldflags="-s -w" -o project-management.exe

# Build with optimizations (cmd)
set CGO_ENABLED=0 && go build -ldflags="-s -w" -o project-management.exe
```

The `-ldflags="-s -w"` flags strip symbol tables and debug information, producing a smaller binary. `CGO_ENABLED=0` enables static linking — no external dependencies required.

The compiled binary `project-management.exe` will be created in the current directory.

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
.\project-management.exe [target_directory]
```

| Argument | Description | Default |
|----------|-------------|---------|
| `target_directory` (optional) | Root directory to monitor and index files | `C:\Projects\AI` |

The server will:
1. Initialize an in-memory store with JSON persistence at `<target_directory>/.mcp_file_index.db`
2. Start monitoring the target directory for file changes
3. Begin accepting MCP tool calls via stdin/stdout

## Adding to LM Studio

LM Studio supports MCP servers, allowing AI models to call your tools directly. Here's how to configure it:

### Step 1: Build or Place the Binary

Ensure the compiled binary is accessible at a known path, e.g.:
- `C:\Projects\AI\project-management\project-management.exe`
- Or wherever you built/placed it

### Step 2: Configure MCP Servers in LM Studio

1. Open **LM Studio**
2. Navigate to **Settings** → **MCP Servers** (or open the MCP configuration)
3. Add a new MCP server with the following configuration:

| Field | Value |
|-------|-------|
| **Server Name** | `project-management` |
| **Command** | Full path to the binary (e.g., `C:\Projects\AI\project-management\project-management.exe`) |
| **Arguments** | *(optional)* Target directory, e.g., `C:\Projects\AI` |

Or via the LM Studio MCP settings JSON file (`settings.json` or `mcp-config.json`):

```json
{
  "mcpServers": {
    "project-management": {
      "command": "C:\\Projects\\AI\\project-management\\project-management.exe",
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
| `path` | string | No | "" | Project path (absolute or relative to target_directory). Empty = auto-generate YYYYMMDD name. |
| `listProjects` | boolean | No | false | If true, list available projects instead of opening one. Returns array of project metadata. Does NOT require an open project context. |

**Behavior:**
- If `path` is empty: Auto-generates a `YYYYMMDD`-named project folder and initializes git
- If `path` exists as directory: Opens it as the current project (initializes git if needed)
- If `path` doesn't exist: Creates it and initializes git

**Response:** Project path, name hint, and creation status. When using `listProjects=true`, returns an array of available projects with their paths, names, and metadata.

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
| `format` | string | No | "auto" | Output format for read action: auto (text/binary detection), text (force text), hex (hex dump) |
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

- **`auto`** (default): Automatically detects whether path is a file (`read`) or directory (`list`). For directories, returns `list`. For archives, uses `archive-list`.
- **`read`**: Read file content with offset/length support, line-range selection, binary detection. Supports reading from archive entries.
- **`list`**: List directory contents with sorting, filtering, recursive walking. Also lists archive contents.
- **`info`**: Get detailed metadata (permissions, MIME type, MD5 hash, timestamps). For archives, shows format and entry count instead of file metadata.
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
| `action` | string | No | "edit" | Action: `edit`, `delete`, `compress`, `extract`, `copy`, `move` |
| `oldText` | string | No | — | For action=edit: text to find and replace |
| `newText` | string | No | — | For action=edit: replacement text |
| `count` | number | No | 1 | For action=edit: number of occurrences to replace (0 = all, default 1) |
| `compressToArchive` | string | No | — | For action=compress: destination archive path (.zip, .tar.gz, etc.) |
| `deleteOriginalAfterCompress` | boolean | No | false | For action=compress: delete source after compressing |
| `extractFromArchive` | string | No | — | For action=extract: archive path to extract from (e.g., 'archive.zip/entry/path') |
| `recursive` | boolean | No | false | For action=delete on directories: if true, delete all contents recursively |
| `ignoreMissing` | boolean | No | true | For action=delete: return success instead of error when item doesn't exist |

**Action modes:**

- **`edit`** (default): Find and replace text in a file. Reports occurrences found vs replacements made with size delta. Only works on regular files, not directories or archives.
- **`delete`**: Delete a file or directory. Directories require `recursive=true` if not empty. Returns error for missing items unless `ignoreMissing=true`.
- **`compress`**: Compress file(s)/folder into an archive (.zip, .tar.gz). Supports adding to existing archives. Optional `deleteOriginalAfterCompress`.
- **`extract`**: Extract from archive and write to filesystem. Use `path` as destination or `archive.zip/entry/path` format.
- **`copy`**: Copy a file or directory from source to destination. Directories are copied recursively with full content preservation. Requires `destination` parameter. Supports `overwrite`.
- **`move`**: Move/rename a file or directory from source to destination. Uses OS rename when possible, falls back to copy+delete for cross-filesystem moves. Requires `destination` parameter. Supports `overwrite`.

**Supported archive formats:** ZIP, TAR, TAR.GZ (GZIP compressed)

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

### Batch Operations

All tools support batch mode via array parameters for bulk operations with per-item success/failure reporting:

- **CreateItem**: Provide `items` array — each item has `{path, content?, isFolder?, overwrite?}`
- **EditItem**: Provide `edits` array — each edit has `{path, action?, oldText?, newText?, count?, compressToArchive?, extractFromArchive?, destination?}` (destination required for copy/move actions)
- **GetItem**: Provide `paths` array to read/list/info multiple items at once

Batch responses include a summary with total/successful/failed counts and per-item status. Failed items show error messages while successful operations confirm completion.

---

### Multi-Root Search

The `Search` tool supports searching across multiple root directories simultaneously via the `paths` parameter (array of paths). Each result includes a `root` field indicating which search root it was found in. This is useful for searching across unrelated directories or workspaces.

---

### Git Tool

Execute git commands within a project directory. Supports all major git operations with structured output parsing.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `action` | string | Yes | — | Git action: status, log, diff, add, commit, push, pull, branch, stash, reset, clean, tag, remote, checkout, revert |
| `path` | string | No | (open project) | Project directory (defaults to the currently open project path) |
| `args` | array | No | — | Additional raw git arguments passed directly |

**Common parameters (vary by action):**

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `maxCount` | number | No | 20 | For log: maximum commits to return |
| `format` | string | No | "short" | For log: output format — json, short, fuller, oneline |
| `staged` | boolean | No | false | For diff: show staged changes instead of unstaged |
| `path` (diff) | string | No | — | For diff: limit diff to specific file path |
| `files` | array | Yes (add) | — | For add: array of file paths to stage |
| `message` | string | Yes (commit/stash/save/tag) | — | Commit/stash/tag message |
| `amend` | boolean | No | false | For commit: modify last commit instead of creating new one |
| `remote` | string | No | "origin" | For push/pull: remote name |
| `force` | boolean | No | false | For push: force push to remote |
| `branch action` | string | No | "list" | For branch: list (default), create, delete, switch |
| `name` | string | Varies | — | Branch/tag name for create/delete/switch operations |
| `stash action` | string | No | "save" | For stash: save (default), pop, list, apply |
| `reset mode` | string | No | "mixed" | For reset: soft, mixed (default), hard |
| `commit` | string | Varies | — | Target commit hash for reset/revert operations |
| `dryRun` | boolean | No | false | For clean: show what would be deleted without deleting |
| `directories` | boolean | No | false | For clean: also remove untracked directories |
| `target` | string | Yes (checkout) | — | Branch name for checkout/switch, or file path for restore |
| `create` | boolean | No | false | For checkout: create new branch instead of switching existing |
| `noCommit` | boolean | No | false | For revert: perform the revert but don't commit |

**Actions:**

- **status**: Working tree status — returns structured data with index/worktree status codes, branch info, untracked file count. Output format: `[INDEX][WORKTREE] path`
- **log**: Commit history — `format=json` returns parseable JSON array with sha, authorName, authorEmail, date, subject fields. `format=oneline` returns compact one-line-per-commit format.
- **diff**: Unstaged or staged changes — unified diff with 3 lines of context (`-U3`). Use `staged=true` to show index vs HEAD. Use `path="file.go"` to limit to specific file.
- **add**: Stage files for commit — provides `files` array parameter with paths to stage.
- **commit**: Create a commit — required: `message`. Optional: `amend=true` to modify last commit. Auto-configures user.name/user.email if not set (defaults to "MCP Tool"). Automatically runs `git add -A` before non-amend commits.
- **push/pull**: Push/pull from remote — `remote` parameter defaults to 'origin'. `force=true` for force push.
- **branch**: List/create/switch branches — `action=list` (default, shows all branches with current marker), `action=create`, `action=delete`, `action=switch`. Required: `name` for create/delete/switch.
- **stash**: Stash/unstash changes — `action=save` (default, uses `git stash push`), `action=pop`, `action=list`, `action=apply`. Optional: `message` parameter for save action.
- **reset**: Reset working tree — `mode=soft\|mixed` (default)\|hard. Optional: `commit` parameter to specify target commit hash.
- **clean**: Remove untracked files — `dryRun=true` shows what would be deleted without deleting. `directories=true` also removes untracked directories (`-d`). Default is `-f` (force remove files only).
- **tag**: List/create tags — `action=list` (default, lists all tags with `-l -n`). `action=create`: requires `name`, optional `message` for annotated tag. `action=delete`: requires `name`.
- **remote**: Manage remotes — `action=list` (default, shows remote name and URL). `action=add`: requires `name` and `url`. `action=remove`: requires `name`. `action=set-url`: requires `name` and `url`.
- **checkout**: Switch branches or restore files — required: `target` parameter (branch name). Optional: `create=true` to create new branch (`git checkout -b`).
- **revert**: Revert a commit — required: `commit` parameter (commit hash). Optional: `noCommit=true` to perform revert changes without auto-committing.

**Response format:** Actions that return lists (status, log with json format, branch, stash, tag, remote) return structured parsed data. Other actions return raw git output or success/failure messages. Exit code 0 indicates success; non-zero exit codes with stderr indicate errors.

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
project-management/
├── main.go                    # Server entry point, stdio transport setup
├── go.mod                     # Go module definition
├── go.sum                     # Dependency checksums
├── README.md                  # This file
├── core/
│   └── newproject.go          # OpenProject/CloseProject project lifecycle with git initialization
├── pkg/
│   ├── shared_types.go        # Shared types (dirEntryInfo, CompileResult, compileCache, ArchiveInfo)
│   ├── storage.go             # In-memory file store with JSON persistence (thread-safe, debounced saves)
│   ├── db.go                  # FileStore interface (index, search, duplicates, MD5 hashing)
│   └── watcher.go             # File system watcher for auto-indexing (ignores .mcp_file_index.db)
└── tools/
    ├── register.go            # MCP tool registration and path resolution helpers
    ├── args.go                # Argument extraction helpers (extractArg, extractOptional*, array helpers)
    ├── create.go              # CreateItem tool handler (files, directories, archive entries)
    ├── get.go                 # GetItem tool handler (read, list, info, compile, diff actions)
    ├── edit.go                # EditItem tool handler (edit, delete, compress, extract, copy, move actions; copy.go/move.go helpers)
    ├── search.go              # Search tool handler (name, regex, grep modes)
    ├── compile.go             # Compile status helpers (Node.js, Python, .NET, Go detection)
    ├── utils.go               # Utility functions (MIME detection, archive I/O, sandbox validation, git commit)
    ├── batch.go               # Batch operation handlers (batch create/edit/copy/move/get/search)
    └── git.go                 # Git tool handler (status, log, diff, add, commit, push, pull, branch, stash, reset, clean, tag, remote, checkout, revert)
```

## Architecture

- **MCP Server**: Uses [`github.com/mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) library for MCP protocol compliance
- **Storage**: In-memory map-based store with thread-safe access (`sync.RWMutex`) and debounced JSON persistence (5s interval)
- **File Watching**: `github.com/fsnotify/fsnotify` for cross-platform file system event monitoring
- **Compile Cache**: In-memory cache with configurable TTL (60 seconds default), auto-invalidated on file changes via watcher hooks
- **Archive Cache**: In-memory cache for open archives (ZIP, TAR, TAR.GZ) with lazy loading

## Project Context System

This server uses a project context model where operations are scoped to an active project:

1. **Call `OpenProject` first** to set the working directory and establish path boundaries
2. All subsequent tools resolve relative paths against the open project
3. Paths outside the project boundary are rejected for safety
4. **Call `CloseProject`** when done to reset the context

This ensures that operations like `CreateItem`, `EditItem` (copy/move), etc. only affect files within the active project scope.

## Sandbox Security

All tools enforce strict sandbox boundaries to prevent directory traversal attacks:

### Path Resolution
- All file paths are resolved relative to the open project using `resolvePathWithBoundaryCheck()`
- Paths escaping the project root (e.g., `../secret.txt`) are rejected with "path is outside the open project" error
- Windows drive letters are handled case-insensitively (`C:\` vs `c:\`)

### Archive Entry Sanitization
When reading/writing archives, entry names are sanitized to prevent sandbox escapes:
- Backslashes are converted to forward slashes for cross-platform consistency
- Path segments (`.` and `..`) are resolved before validation
- Entries starting with `../` or containing `..` as a standalone segment are rejected
- The sanitization function is applied in all archive operations: create, read, compress, extract

### Sandbox Validation Functions
- **`IsWithinProject(path)`**: Checks if an absolute path falls within the current project root (handles Windows drive letters)
- **`validateInSandbox(projectPath, checkPath)`**: Validates a path is within the sandbox using normalized forward-slash comparison
- **`sanitizeArchiveEntryPath(entryName)`**: Cleans and validates archive entry names before use

### Security Guarantees
| Operation | Sandbox Check | Archive Sanitization |
|-----------|---------------|---------------------|
| CreateItem | ✅ resolvePathWithBoundaryCheck | ✅ sanitizeArchiveEntryPath |
| GetItem (read/list/info) | ✅ resolvePathWithBoundaryCheck | ✅ validateInSandbox for diff file2 |
| EditItem (edit/delete/compress/extract/copy/move) | ✅ resolvePathWithBoundaryCheck | ✅ sanitizeArchiveEntryPath |
| Search | ✅ resolvePathWithBoundaryCheck + WalkDir boundary check | N/A |

## Data Model

Indexed files are stored in memory as records with the following fields:

| Field | Type | Description |
|-------|------|-------------|
| `path` | string | Absolute file path (unique key) |
| `name` | string | File/directory basename |
| `size` | int64 | File size in bytes (0 for directories) |
| `mod_time` | string | Modification time in RFC3339 format |
| `md5_hash` | string | MD5 hash of file contents (empty for directories) |
| `is_dir` | bool | Whether the entry is a directory |

- Each file is indexed with its MD5 hash for duplicate detection
- Directories are tracked with size 0 and empty MD5 (sizes calculated on-demand)
- Index updates automatically via file watcher or tool operations
- Persistence file (`*.mcp_file_index.db`) stores JSON data, excluded from its own scan and watching

**JSON persistence format:**
```json
{
  "version": 1,
  "updated_at": "2026-04-22T06:30:00Z",
  "files": [
    {
      "path": "/some/path",
      "name": "file.go",
      "size": 1234,
      "mod_time": "2026-04-22T06:00:00Z",
      "md5_hash": "abc123...",
      "is_dir": false
    }
  ]
}
```

## License

MIT