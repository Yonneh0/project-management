package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ==================== Tool Registration ====================

func registerTools(mcpServer *server.MCPServer, store *fileStore, rootDir string) {

	// ==================== OpenProject ====================
	openProjectTool := mcp.NewTool("OpenProject",
		mcp.WithDescription("Open an existing project or create a new one. Sets the active project context for all subsequent operations.\n\n"+
			"- If path is empty: auto-generates a YYYYMMDD-named project folder and initializes git.\n"+
			"- If path exists as directory: opens it as the current project (initializes git if needed).\n"+
			"- If path doesn't exist: creates it and initializes git."),
		mcp.WithString("path", mcp.Description("Project path (absolute or relative to rootDir). Empty = auto-generate YYYYMMDD name.")),
	)

	mcpServer.AddTool(openProjectTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, err := extractArg[string](req, "path")
		if err != nil {
			path = "" // empty means auto-generate
		}

		projectDir, err := OpenProject(rootDir, path)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to open project: %v", err)), nil
		}

		pctx := GetGlobalProject()
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
		mcp.WithDescription("Close the current project and reset the global context."),
	)

	mcpServer.AddTool(closeProjectTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		CloseProject()
		return mcp.NewToolResultText("Project closed. All tools will now require an OpenProject call before use."), nil
	})

	// ==================== CreateItem (CreateFile + CreateFolder) ====================
	createItemTool := mcp.NewTool("CreateItem",
		mcp.WithDescription("Create a new file or directory at the specified path. Automatically creates parent directories.\n\n"+
			"Requires an active project context (call OpenProject first).\n"+
			"All changes are auto-committed to git."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path where the item should be created")),
		mcp.WithString("content", mcp.Description("Content to write to the file (required for files, optional for folders)")),
		mcp.WithBoolean("isFolder", mcp.DefaultBool(false), mcp.Description("If true, create a folder instead of a file")),
		mcp.WithBoolean("overwrite", mcp.DefaultBool(false), mcp.Description("For files: if true, overwrite existing file. For folders: if true, return success when folder exists")),
	)

	mcpServer.AddTool(createItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleCreateItem(ctx, req, store, rootDir)
	})

	// ==================== GetItem (GetDirectory + ReadFile + GetFileInfo + CompileStatus + CompareFile) ====================
	getItemTool := mcp.NewTool("GetItem",
		mcp.WithDescription("Read file content, list directory contents, get metadata, check compile status, or compare files.\n\n"+
			"Requires an active project context (call OpenProject first).\n"+
			"Supports archive paths like 'archive.zip/subdir/file.txt'."),
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
	)

	mcpServer.AddTool(getItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleGetItem(ctx, req, store, rootDir)
	})

	// ==================== EditItem (Edit + Delete + Compress + Extract) ====================
	editItemTool := mcp.NewTool("EditItem",
		mcp.WithDescription("Edit, delete, compress, or extract files. Supports multiple operations via the 'action' parameter.\n\n"+
			"Requires an active project context (call OpenProject first).\n"+
			"All changes are auto-committed to git.\n\n"+
			"Actions:\n"+
			"  edit: Find and replace text in a file (default)\n"+
			"  delete: Delete a file or directory\n"+
			"  compress: Compress file(s)/folder into an archive\n"+
			"  extract: Extract from archive and edit in place"),
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute or relative path of the target. For archives use 'archive.zip/path/entry' format.")),
		mcp.WithString("action", mcp.DefaultString("edit"), mcp.Description("Action: edit, delete, compress, extract")),
		mcp.WithString("oldText", mcp.Description("For action=edit: text to find and replace (hex-encoded when format=hex)")),
		mcp.WithString("newText", mcp.Description("For action=edit: replacement text (hex-encoded when format=hex)")),
		mcp.WithNumber("count", mcp.DefaultNumber(1), mcp.Description("For action=edit: number of occurrences to replace (0 = all, default 1)")),
		mcp.WithString("compressToArchive", mcp.Description("For action=compress: destination archive path (.zip, .tar.gz, etc.)")),
		mcp.WithBoolean("deleteOriginalAfterCompress", mcp.DefaultBool(false), mcp.Description("For action=compress: delete source after compressing")),
		mcp.WithString("extractFromArchive", mcp.Description("For action=extract: archive path to extract from (e.g., 'archive.zip/entry/path')")),
		mcp.WithBoolean("recursive", mcp.DefaultBool(false), mcp.Description("For action=delete on directories: if true, delete all contents recursively")),
		mcp.WithBoolean("ignoreMissing", mcp.DefaultBool(true), mcp.Description("For action=delete: return success instead of error when item doesn't exist")),
		mcp.WithString("format", mcp.DefaultString("text"), mcp.Description("Input format for edit action: text (string matching, default) or hex (binary byte pattern replacement)")),
	)

	mcpServer.AddTool(editItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleEditItem(ctx, req, store, rootDir)
	})

	// ==================== CopyItem (CopyFile + directory support) ====================
	copyItemTool := mcp.NewTool("CopyItem",
		mcp.WithDescription("Copy a file or directory from source to destination. Directories are copied recursively.\n\n"+
			"Requires an active project context (call OpenProject first).\n"+
			"All changes are auto-committed to git."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source file or directory path (absolute or relative)")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination file or directory path (absolute or relative)")),
		mcp.WithBoolean("overwrite", mcp.DefaultBool(false), mcp.Description("If true, overwrite existing destination")),
	)

	mcpServer.AddTool(copyItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleCopyItem(ctx, req, store, rootDir)
	})

	// ==================== MoveItem (MoveFile + directory support) ====================
	moveItemTool := mcp.NewTool("MoveItem",
		mcp.WithDescription("Move or rename a file or directory. Directories are moved with all contents.\n\n"+
			"Requires an active project context (call OpenProject first).\n"+
			"All changes are auto-committed to git."),
		mcp.WithString("source", mcp.Required(), mcp.Description("Source file or directory path (absolute or relative)")),
		mcp.WithString("destination", mcp.Required(), mcp.Description("Destination file or directory path (absolute or relative)")),
		mcp.WithBoolean("overwrite", mcp.DefaultBool(false), mcp.Description("If true, overwrite existing destination")),
	)

	mcpServer.AddTool(moveItemTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleMoveItem(ctx, req, store, rootDir)
	})

	// ==================== Search (with regex/grep support) ====================
	searchTool := mcp.NewTool("Search",
		mcp.WithDescription("Search for files and directories by name pattern, regex, or file content (grep).\n\n"+
			"Requires an active project context (call OpenProject first)."),
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
	)

	mcpServer.AddTool(searchTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleSearch(ctx, req, store, rootDir)
	})
}

// ==================== Helper: ValidateProjectActive ====================

func validateProjectActive() error {
	if GetGlobalProject() == nil || GetGlobalProject().Path == "" {
		return fmt.Errorf("no project open. Call OpenProject first to set the active project context")
	}
	return nil
}

// ==================== Helper: Resolve and Validate Path ====================

func resolveAndValidatePath(rootDir, path string) (string, error) {
	pctx := GetGlobalProject()
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
	if !IsWithinProject(resolved) {
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

func resolvePath(rootDir, path string) (string, error) {
	pctx := GetGlobalProject()
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

func resolvePathWithBoundaryCheck(rootDir, path string) (string, error) {
	resolved, err := resolvePath(rootDir, path)
	if err != nil {
		return "", err
	}

	pctx := GetGlobalProject()
	if pctx == nil || pctx.Path == "" {
		return resolved, nil
	}

	if !IsWithinProject(resolved) {
		return "", fmt.Errorf("path '%s' is outside the open project '%s'", path, pctx.Path)
	}

	return resolved, nil
}
