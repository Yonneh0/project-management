package tools

import (
	"context"
	"fmt"
	"path/filepath"

	"project-management/core"
	"project-management/pkg"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Re-export functions for use in handlers.
var (
	openProject      = core.OpenProject
	closeProject     = pkg.CloseProject
	getGlobalProject = pkg.GetGlobalProject
	isWithinProject  = pkg.IsWithinProject
)

// ==================== Tool Registration ====================

// RegisterTools registers all MCP tools with the given server.
func RegisterTools(mcpServer *server.MCPServer, store *pkg.FileStore, rootDir string) {

	// ==================== OpenProject ====================
	openProjectTool := mcp.NewTool("OpenProject",
		mcp.WithDescription(
			"Open an existing project or create a new one. Sets the active project context for all subsequent operations.\n\n"+
				"How to use:\n"+
				"- Call this FIRST before any file operations (CreateItem, GetItem, EditItem, etc.)\n"+
				"- Use listProjects=true to discover available projects without opening one\n"+
				"- If path is empty: auto-generates a YYYYMMDD-named project folder and initializes git\n"+
				"- If path exists as directory: opens it as the current project (initializes git if needed)\n"+
				"- If path doesn't exist: creates it and initializes git",
		),
		mcp.WithString("path", mcp.Description("Project path (absolute or relative to rootDir). Empty = auto-generate YYYYMMDD name.")),
		mcp.WithBoolean("listProjects", mcp.DefaultBool(false), mcp.Description("If true, list available projects instead of opening one. Returns array of project metadata. Does NOT require an open project context.")),
	)

	mcpServer.AddTool(openProjectTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := extractArg[string](req, "path")
		if err != nil {
			path = "" // empty means auto-generate
		}

		listProjects, _ := extractOptionalBool(req, "listProjects")

		if listProjects {
			result, err2 := core.HandleListProjects(rootDir)
			if err2 != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to list projects: %v", err2)), nil
			}
			return result, nil
		}

		projectDir, err := openProject(rootDir, path)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to open project: %v", err)), nil
		}

		pctx := getGlobalProject()
		hint := ""
		if pctx.NameHint != "" {
			hint = fmt.Sprintf("\nProject name hint: %s", pctx.NameHint)
		}
		isNewMsg := ""
		if pctx.IsNew {
			isNewMsg = "\nNote: This project was newly created."
		}

		return mcp.NewToolResultText(fmt.Sprintf("Project opened successfully!\nPath: %s%s%s", projectDir, hint, isNewMsg)), nil
	})

	// ==================== CloseProject ====================
	closeProjectTool := mcp.NewTool("CloseProject",
		mcp.WithDescription(
			"Close the current project and reset the global context.\n\n"+
				"How to use:\n"+
				"- Call this when done with a project to free resources\n"+
				"- All tools will require an OpenProject call before use after closing",
		),
	)

	mcpServer.AddTool(closeProjectTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		closeProject()
		return mcp.NewToolResultText("Project closed. All tools will now require an OpenProject call before use."), nil
	})

	// ==================== CreateItem (CreateFile + CreateFolder) ====================
	createItemTool := mcp.NewTool("CreateItem",
		mcp.WithDescription(
			"Create a new file or directory at the specified path. Automatically creates parent directories.\n\n"+
				"How to use:\n"+
				"- Call OpenProject first to set the active project context\n"+
				"- For files: provide path and content parameters\n"+
				"- For folders: set isFolder=true\n"+
				"- All changes are auto-committed to git\n\n"+
				"Batch mode:\n"+
				"- Provide 'items' array for batch creation (multiple files/folders at once)\n"+
				"- Each item has: path, content (files only), isFolder, overwrite",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path where the item should be created")),
		mcp.WithString("content", mcp.Description("Content to write to the file (required for files, optional for folders)")),
		mcp.WithBoolean("isFolder", mcp.DefaultBool(false), mcp.Description("If true, create a folder instead of a file")),
		mcp.WithBoolean("overwrite", mcp.DefaultBool(false), mcp.Description("For files: if true, overwrite existing file. For folders: if true, return success when folder exists")),
		mcp.WithArray("items", mcp.Description("Batch mode: array of items to create. Each item has {path, content?, isFolder?, overwrite?}"),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":      map[string]any{"type": "string", "description": "Path for this item"},
					"content":   map[string]any{"type": "string", "description": "Content for file items"},
					"isFolder":  map[string]any{"type": "boolean", "description": "If true, create as folder"},
					"overwrite": map[string]any{"type": "boolean", "description": "If true, overwrite existing"},
				},
				"required": []string{"path"},
			}),
		),
	)

	mcpServer.AddTool(createItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Check for batch mode first
		if result, ok := handleBatchCreate(req, rootDir); ok {
			return result, nil
		}

		return handleCreateItem(ctx, req, store, rootDir)
	})

	// ==================== GetItem (GetDirectory + ReadFile + GetFileInfo + CompileStatus + CompareFile) ====================
	getItemTool := mcp.NewTool("GetItem",
		mcp.WithDescription(
			"Read file content, list directory contents, get metadata, check compile status, or compare files.\n\n"+
				"How to use:\n"+
				"- Call OpenProject first to set the active project context\n"+
				"- Use action=auto to let the tool detect whether path is a file or directory\n"+
				"- For file content: use action=read (default for files)\n"+
				"- For directory listing: use action=list (default for directories)\n"+
				"- For metadata only: use action=info with length=0\n"+
				"- For build status: use action=compile\n"+
				"- For comparing two files: use action=diff with file2 parameter\n"+
				"- Supports archive paths like 'archive.zip/subdir/file.txt'\n\n"+
				"Batch mode:\n"+
				"- Provide 'paths' array to get multiple items at once",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path of the item")),
		mcp.WithString("action", mcp.DefaultString("auto"), mcp.Description("Action: auto (detect), read (file content), list (directory contents), info (metadata only), compile (build status), diff (compare with file2), archive-list (list archive contents)")),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Description("Byte offset for file reading (0 = start)")),
		mcp.WithNumber("length", mcp.DefaultNumber(-1), mcp.Description("Read control: -1=read entire file, 0=metadata only, >0=N bytes to read")),
		mcp.WithNumber("line", mcp.Description("1-based single line number to read (overrides offset/length)")),
		mcp.WithNumber("startLine", mcp.Description("1-based start of line range (inclusive)")),
		mcp.WithNumber("endLine", mcp.Description("1-based end of line range (inclusive)")),
		mcp.WithString("format", mcp.DefaultString("auto"), mcp.Description("Output format for read action: auto (text/binary detection), text (force text), hex (hex dump)")),
		mcp.WithBoolean("recursive", mcp.DefaultBool(false), mcp.Description("For directories: list recursively")),
		mcp.WithNumber("maxItems", mcp.DefaultNumber(100), mcp.Description("Maximum directory entries to return (0 = unlimited)")),
		mcp.WithBoolean("includeHidden", mcp.DefaultBool(false), mcp.Description("Include hidden/dot files in results")),
		mcp.WithString("sortBy", mcp.DefaultString("name"), mcp.Description("Sort order: name, size, date, type")),
		mcp.WithString("severity", mcp.DefaultString("all"), mcp.Description("For compile action: errors | warnings | all")),
		mcp.WithString("checksum", mcp.Description("Compute hash: md5 or sha256 (file mode only)")),
		mcp.WithString("file2", mcp.Description("Second file path for diff action")),
		mcp.WithBoolean("ignoreWhitespace", mcp.DefaultBool(false), mcp.Description("For diff: ignore leading/trailing whitespace")),
		mcp.WithBoolean("ignoreCase", mcp.DefaultBool(false), mcp.Description("For diff: case-insensitive comparison")),
		mcp.WithBoolean("noCache", mcp.DefaultBool(false), mcp.Description("For compile: bypass cache")),
		mcp.WithArray("paths", mcp.Description("Batch mode: array of paths to get. Returns per-path results with success/failure status."),
			mcp.WithStringItems(),
		),
	)

	mcpServer.AddTool(getItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Check for batch mode first
		if result, ok := handleBatchGet(req, rootDir); ok {
			return result, nil
		}

		return handleGetItem(ctx, req, store, rootDir)
	})

	// ==================== EditItem (Edit + Delete + Compress + Extract) ====================
	editItemTool := mcp.NewTool("EditItem",
		mcp.WithDescription(
			"Edit, delete, compress, or extract files. Supports multiple operations via the 'action' parameter.\n\n"+
				"How to use:\n"+
				"- Call OpenProject first to set the active project context\n"+
				"- All changes are auto-committed to git\n\n"+
				"Actions:\n"+
				"  edit: Find and replace text in a file (default)\n"+
				"    - Provide oldText and newText parameters\n"+
				"    - Use count=0 to replace all occurrences (default is 1)\n"+
				"    - Use format=hex for binary pattern replacement\n"+
				"  delete: Delete a file or directory\n"+
				"    - Set recursive=true for directories\n"+
				"    - ignoreMissing=true returns success if item doesn't exist (default)\n"+
				"  compress: Compress file(s)/folder into an archive\n"+
				"    - Provide compressToArchive with destination path (.zip, .tar.gz, etc.)\n"+
				"    - Set deleteOriginalAfterCompress=true to remove source after archiving\n"+
				"  extract: Extract from archive and edit in place\n"+
				"    - Use extractFromArchive parameter (e.g., 'archive.zip/entry/path')\n\n"+
				"Batch mode:\n"+
				"- Provide 'edits' array for batch operations (multiple files at once)\n"+
				"- Each edit has: path, action, oldText, newText, count, compressToArchive, destination",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path of the target. For archives use 'archive.zip/path/entry' format.")),
		mcp.WithString("action", mcp.DefaultString("edit"), mcp.Description("Action: edit, delete, compress, extract, copy, move")),
		mcp.WithString("oldText", mcp.Description("For action=edit: text to find and replace (hex-encoded when format=hex)")),
		mcp.WithString("newText", mcp.Description("For action=edit: replacement text (hex-encoded when format=hex)")),
		mcp.WithNumber("count", mcp.DefaultNumber(1), mcp.Description("For action=edit: number of occurrences to replace (0 = all, default 1)")),
		mcp.WithString("compressToArchive", mcp.Description("For action=compress: destination archive path (.zip, .tar.gz, etc.)")),
		mcp.WithBoolean("deleteOriginalAfterCompress", mcp.DefaultBool(false), mcp.Description("For action=compress: delete source after compressing")),
		mcp.WithString("extractFromArchive", mcp.Description("For action=extract: archive path to extract from (e.g., 'archive.zip/entry/path')")),
		mcp.WithString("destination", mcp.Description("For action=copy/move: destination file or directory path (absolute or relative)")),
		mcp.WithBoolean("recursive", mcp.DefaultBool(false), mcp.Description("For action=delete on directories: if true, delete all contents recursively")),
		mcp.WithBoolean("ignoreMissing", mcp.DefaultBool(true), mcp.Description("For action=delete: return success instead of error when item doesn't exist")),
		mcp.WithString("format", mcp.DefaultString("text"), mcp.Description("Input format for edit action: text (string matching, default) or hex (binary byte pattern replacement)")),
		mcp.WithArray("edits", mcp.Description("Batch mode: array of edit operations. Each has {path, action?, oldText?, newText?, count?, compressToArchive?, extractFromArchive?}"),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":               map[string]any{"type": "string", "description": "Target path"},
					"action":             map[string]any{"type": "string", "description": "Action: edit, delete, compress, extract, copy, move"},
					"oldText":            map[string]any{"type": "string", "description": "Find text for edit action"},
					"newText":            map[string]any{"type": "string", "description": "Replacement text for edit action"},
					"count":              map[string]any{"type": "integer", "description": "Occurrence count for edit action"},
					"compressToArchive":  map[string]any{"type": "string", "description": "Archive destination for compress action"},
					"extractFromArchive": map[string]any{"type": "string", "description": "Source archive for extract action"},
					"destination":        map[string]any{"type": "string", "description": "Destination path for copy/move actions"},
				},
				"required": []string{"path"},
			}),
		),
	)

	mcpServer.AddTool(editItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Check for batch mode first
		if result, ok := handleBatchEdit(req, rootDir); ok {
			return result, nil
		}

		return handleEditItem(ctx, req, store, rootDir)
	})

	// ==================== Search (with regex/grep support) ====================
	searchTool := mcp.NewTool("Search",
		mcp.WithDescription(
			"Search for files and directories by name pattern, regex, or file content (grep).\n\n"+
				"How to use:\n"+
				"- Call OpenProject first to set the active project context\n"+
				"- mode=name: substring match on filenames (default)\n"+
				"- mode=regex: Go regex pattern applied to filenames\n"+
				"- mode=grep: search file contents for pattern\n\n"+
				"Tips:\n"+
				"- Use extensions='.go,.py' to filter by file type\n"+
				"- Set fileOnly=true or dirOnly=true to limit result types\n"+
				"- For grep mode, use contextLines=N to show surrounding lines",
		),
		mcp.WithString("path", mcp.Required(), mcp.Description("Root directory path to search within")),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("Pattern to search for (substring match, regex pattern, or grep pattern)")),
		mcp.WithString("mode", mcp.DefaultString("name"), mcp.Description("Search mode: name (substring), regex (Go regex on filenames), grep (search file contents)")),
		mcp.WithNumber("limit", mcp.DefaultNumber(50), mcp.Description("Maximum number of results to return")),
		mcp.WithNumber("maxMatchesPerFile", mcp.DefaultNumber(10), mcp.Description("For grep mode: maximum matches per file")),
		mcp.WithBoolean("includeHidden", mcp.DefaultBool(false), mcp.Description("Include hidden/dot files in search")),
		mcp.WithBoolean("fileOnly", mcp.DefaultBool(false), mcp.Description("Search only files (exclude directories)")),
		mcp.WithBoolean("dirOnly", mcp.DefaultBool(false), mcp.Description("Search only directories (exclude files)")),
		mcp.WithString("extensions", mcp.Description("Comma-separated list of file extensions to filter by (e.g., '.go,.py')")),
		mcp.WithNumber("contextLines", mcp.DefaultNumber(0), mcp.Description("For grep mode: number of context lines around matches")),
		mcp.WithArray("paths", mcp.Description("Multi-root search: array of root directories to search within. Each result includes a 'root' field indicating which root it was found in."),
			mcp.WithStringItems(),
		),
	)

	mcpServer.AddTool(searchTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Check for multi-root search first
		if result, ok := handleMultiSearch(req, rootDir); ok {
			return result, nil
		}

		return handleSearch(ctx, req, store, rootDir)
	})

	// ==================== Git Tool ====================
	gitTool := mcp.NewTool("Git",
		mcp.WithDescription(
			"Execute git commands within a project directory. Supports status, log, diff, add, commit, push, pull, branch, stash, reset, and more.\n\n"+
				"How to use:\n"+
				"- Call OpenProject first (uses open project path by default)\n"+
				"- Or provide a specific 'path' parameter for git repos outside the open project\n\n"+
				"Actions:\n"+
				"  status: Working tree status (default)\n"+
				"    - Returns branch info, staged/unstaged changes, untracked files\n"+
				"  log: Commit history\n"+
				"    - Use maxCount=N to limit results (default 20)\n"+
				"    - Use format=json|short|fuller|oneline for output format\n"+
				"  diff: Unstaged or staged changes\n"+
				"    - Use staged=true to show staged changes instead of unstaged\n"+
				"    - Use path='file.go' to limit diff to specific file\n"+
				"  add: Stage files for commit\n"+
				"    - Provide 'files' array with paths to stage\n"+
				"  commit: Create a commit\n"+
				"    - Required: message parameter\n"+
				"    - Optional: amend=true to modify last commit\n"+
				"  push: Push to remote\n"+
				"    - Remote defaults to 'origin'\n"+
				"    - Use force=true for force push\n"+
				"  pull: Pull from remote (defaults to origin)\n"+
				"  branch: List/create/switch branches\n"+
				"    - action=list (default), create, delete, switch\n"+
				"    - name parameter required for create/delete/switch\n"+
				"  stash: Stash/unstash changes\n"+
				"    - action=save (default), pop, list, apply\n"+
				"    - message parameter for save action\n"+
				"  reset: Reset working tree\n"+
				"    - mode=soft|mixed (default)|hard\n"+
				"    - commit parameter to specify target commit\n"+
				"  clean: Remove untracked files\n"+
				"    - dryRun=true shows what would be deleted\n"+
				"    - directories=true also removes untracked directories\n"+
				"  tag: List/create tags\n"+
				"    - action=list (default), create, delete\n"+
				"  remote: Manage remotes\n"+
				"    - action=list (default), add, remove, set-url\n"+
				"  checkout: Switch branches or restore files\n"+
				"    - Required: target parameter (branch name or file path)\n"+
				"    - create=true to create new branch\n"+
				"  revert: Revert a commit\n"+
				"    - Required: commit parameter (commit hash)",
		),
		mcp.WithString("action", mcp.Required(), mcp.Description("Git action: status, log, diff, add, commit, push, pull, branch, stash, reset, clean, tag, remote, checkout, revert")),
		mcp.WithString("path", mcp.Description("Project directory (defaults to open project)")),
		mcp.WithArray("args", mcp.Description("Additional raw git arguments passed directly"),
			mcp.WithStringItems(),
		),
	)

	mcpServer.AddTool(gitTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGitTool(ctx, req, store, rootDir)
	})
}

// ==================== Helper: ValidateProjectActive ====================

// validateProjectActive checks if a project is currently open.
func validateProjectActive() error {
	pctx := getGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return fmt.Errorf("no project open. Call OpenProject first to set the active project context")
	}
	return nil
}

// ==================== Helper: Resolve and Validate Path ====================

// resolveAndValidatePath resolves a path relative to the current project.
func resolveAndValidatePath(rootDir, path string) (string, error) {
	pctx := getGlobalProject()
	if pctx == nil || pctx.Path == "" {
		// No project open, use rootDir directly
		if !filepath.IsAbs(path) {
			path = filepath.Join(rootDir, path)
		}
		return filepath.Clean(path), nil
	}

	// Resolve relative to current project
	var resolved string
	if filepath.IsAbs(path) {
		resolved = filepath.Clean(path)
	} else {
		resolved = filepath.Join(pctx.Path, path)
	}
	resolved = filepath.Clean(resolved)

	// Check boundary if within project scope
	if !isWithinProject(resolved) {
		return "", fmt.Errorf("path '%s' is outside the open project '%s'", path, pctx.Path)
	}

	return resolved, nil
}

// ==================== Helper: Auto-Commit ====================

func autoCommit(projectPath, operation, targetPath string) error {
	message := generateCommitMessage(operation, targetPath)
	return commitChanges(projectPath, message)
}

// ==================== Helper: Check Project Context for Path Resolution ====================

// resolvePath resolves a path relative to the current project or root directory.
func resolvePath(rootDir, path string) (string, error) {
	pctx := getGlobalProject()
	if pctx != nil && pctx.Path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(pctx.Path, path)
		}
		return filepath.Clean(path), nil
	}

	// Fallback to rootDir if no project open
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	return filepath.Clean(path), nil
}

// resolvePathWithBoundaryCheck resolves a path and validates it's within the project.
func resolvePathWithBoundaryCheck(rootDir, path string) (string, error) {
	resolved, err := resolvePath(rootDir, path)
	if err != nil {
		return "", err
	}

	pctx := getGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return resolved, nil
	}

	if !isWithinProject(resolved) {
		return "", fmt.Errorf("path '%s' is outside the open project '%s'", path, pctx.Path)
	}

	return resolved, nil
}
