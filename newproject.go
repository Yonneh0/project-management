package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
		projectDir, err = ResolveRootPath(rootDir, path)
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
	globalProject = &ProjectContext{
		Path:     projectDir,
		NameHint: nameHint,
		GitInit:  true,
		IsNew:    isNew,
	}

	return projectDir, nil
}

// CloseProject resets the global project context.
func CloseProject() {
	globalProject = nil
}

// GetGlobalProject returns the current project context.
func GetGlobalProject() *ProjectContext {
	return globalProject
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
