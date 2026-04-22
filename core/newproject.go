package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"project-management/pkg"

	"github.com/mark3labs/mcp-go/mcp"
)

// NewProjectOptions configures how a new project is created.
type NewProjectOptions struct {
	// Name is the name of the project (also used for the folder name).
	Name string

	// RootDir is the parent directory where the project folder will be created.
	RootDir string
}

// OpenProject opens or creates a project and sets it as the global context.
// If path is empty, it auto-generates a YYYYMMDD-named project.
// It returns the absolute path to the opened/created project directory and any error encountered.
func OpenProject(rootDir, path string) (string, error) {
	var projectDir string
	var isNew bool

	// Auto-generate name if empty
	if strings.TrimSpace(path) == "" {
		autoName := time.Now().Format("20060102")
		projectDir = filepath.Join(rootDir, autoName)
		isNew = true
	} else {
		var err error
		projectDir, err = pkg.ResolveRootPath(rootDir, path)
		if err != nil {
			return "", fmt.Errorf("failed to resolve project path: %w", err)
		}
		// Check if it already exists
		info, statErr := os.Stat(projectDir)
		isNew = statErr != nil || !info.IsDir()
	}

	projectDir = filepath.Clean(projectDir)

	// Create directory if it doesn't exist
	if isNew {
		if err := os.MkdirAll(projectDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create project directory: %w", err)
		}
	} else {
		// Verify it's a directory
		info, _ := os.Stat(projectDir)
		if !info.IsDir() {
			return "", fmt.Errorf("path exists but is not a directory: %s", projectDir)
		}
	}

	// Always initialize git repository
	if err := initGitRepo(projectDir); err != nil {
		// Non-fatal: continue even if git init fails
		fmt.Printf("Warning: git init failed for %s: %v\n", projectDir, err)
	}

	// Set global project context
	nameHint := filepath.Base(projectDir)
	pkg.GlobalProject = &pkg.ProjectContext{
		Path:     projectDir,
		NameHint: nameHint,
		GitInit:  true,
		IsNew:    isNew,
	}

	return projectDir, nil
}

// HandleListProjects lists available projects without opening one.
func HandleListProjects(rootDir string) (*mcp.CallToolResult, error) {
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		return mcp.NewToolResultText("No projects found."), nil
	}

	var projectInfos []map[string]interface{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		dirPath := filepath.Join(rootDir, entry.Name())
		projectInfo := map[string]interface{}{
			"name":     entry.Name(),
			"path":     dirPath,
			"size":     info.Size(),
			"modified": info.ModTime().Format(time.RFC3339),
		}

		// Check if it's a git repo
		gitDir := filepath.Join(dirPath, ".git")
		projectInfo["hasGit"] = pkg.DirExists(gitDir)

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

// initGitRepo initializes a local git repository in the given directory.
func initGitRepo(dir string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init output: %s", string(output))
	}

	// Configure git with safe directory for Windows (avoids permission issues)
	if err := exec.Command("git", "-C", dir, "config", "--local", "core.autocrlf", "false").Run(); err != nil {
		// Non-fatal: continue even if config fails
	}

	return nil
}

// GetGlobalProject returns the current global project context.
func GetGlobalProject() *pkg.ProjectContext {
	return pkg.GlobalProject
}

// NewProject creates a new project folder with git initialized.
// It always initializes a git repository in the new project directory.
// It returns the absolute path to the created project directory and any error encountered.
func NewProject(opts NewProjectOptions) (string, error) {
	// Validate options
	if opts.Name == "" {
		return "", fmt.Errorf("Name is required")
	}

	// Build the target directory path
	projectDir := filepath.Join(opts.RootDir, opts.Name)
	projectDir = filepath.Clean(projectDir)

	// Create the project directory (and parents if needed)
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create project directory: %w", err)
	}

	// Always initialize git repository
	if err := initGitRepo(projectDir); err != nil {
		return "", fmt.Errorf("failed to initialize git repo: %w", err)
	}

	return projectDir, nil
}
