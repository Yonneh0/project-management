package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ==================== Git Tool ====================

type gitResult struct {
	ExitCode int         `json:"exit_code"`
	Stdout   string      `json:"stdout,omitempty"`
	Stderr   string      `json:"stderr,omitempty"`
	Parsed   interface{} `json:"parsed,omitempty"`
}

func handleGitTool(_ context.Context, req mcp.CallToolRequest, _ *fileStore, rootDir string) (*mcp.CallToolResult, error) {
	action, err := extractArg[string](req, "action")
	if err != nil {
		return mcp.NewToolResultError("missing required argument 'action'"), nil
	}

	pathStr, _ := extractOptionalString(req, "path")
	rawArgs, _ := extractOptionalStringArray(req, "args")

	// Resolve path: use provided path or open project
	targetPath := ""
	if pathStr != "" {
		if !filepath.IsAbs(pathStr) {
			pctx := GetGlobalProject()
			if pctx != nil && pctx.Path != "" {
				pathStr = filepath.Join(pctx.Path, pathStr)
			} else {
				pathStr = filepath.Join(rootDir, pathStr)
			}
		}
		targetPath = filepath.Clean(pathStr)
	} else {
		pctx := GetGlobalProject()
		if pctx != nil && pctx.Path != "" {
			targetPath = pctx.Path
		} else {
			return mcp.NewToolResultError("no project open and no path provided. Call OpenProject first or provide a path."), nil
		}
	}

	// Verify it's a git repo
	gitDir := filepath.Join(targetPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return mcp.NewToolResultError(fmt.Sprintf("not a git repository: %s", targetPath)), nil
	}

	var result *gitResult
	switch action {
	case "status":
		result = gitStatus(targetPath, rawArgs)
	case "log":
		maxCount := 20
		if mc, ok := extractOptionalInt(req, "maxCount"); ok && mc > 0 {
			maxCount = mc
		}
		format := "short"
		if f, ok := extractOptionalString(req, "format"); ok {
			format = f
		}
		result = gitLog(targetPath, maxCount, format)
	case "diff":
		staged, _ := extractOptionalBool(req, "staged")
		diffPath, _ := extractOptionalString(req, "path")
		result = gitDiff(targetPath, staged, diffPath)
	case "add":
		files, ok := extractOptionalStringArray(req, "files")
		if !ok || len(files) == 0 {
			return mcp.NewToolResultError("missing required argument 'files' for action=add"), nil
		}
		result = gitAdd(targetPath, files)
	case "commit":
		message, err := extractArg[string](req, "message")
		if err != nil {
			return mcp.NewToolResultError("missing required argument 'message' for action=commit"), nil
		}
		amend, _ := extractOptionalBool(req, "amend")
		result = gitCommit(targetPath, message, amend)
	case "push":
		remote := "origin"
		if r, ok := extractOptionalString(req, "remote"); ok {
			remote = r
		}
		force, _ := extractOptionalBool(req, "force")
		result = gitPush(targetPath, remote, force)
	case "pull":
		remote := "origin"
		if r, ok := extractOptionalString(req, "remote"); ok {
			remote = r
		}
		result = gitPull(targetPath, remote)
	case "branch":
		bAction := "list"
		if a, ok := extractOptionalString(req, "action"); ok {
			bAction = a
		}
		name, _ := extractOptionalString(req, "name")
		result = gitBranch(targetPath, bAction, name)
	case "stash":
		sAction := "save"
		if a, ok := extractOptionalString(req, "action"); ok {
			sAction = a
		}
		msg, _ := extractOptionalString(req, "message")
		result = gitStash(targetPath, sAction, msg)
	case "reset":
		mode := "mixed"
		if m, ok := extractOptionalString(req, "mode"); ok {
			mode = m
		}
		commit, _ := extractOptionalString(req, "commit")
		result = gitReset(targetPath, mode, commit)
	case "clean":
		dryRun, _ := extractOptionalBool(req, "dryRun")
		dirs, _ := extractOptionalBool(req, "directories")
		result = gitClean(targetPath, dryRun, dirs)
	case "tag":
		tAction := "list"
		if a, ok := extractOptionalString(req, "action"); ok {
			tAction = a
		}
		name, _ := extractOptionalString(req, "name")
		msg, _ := extractOptionalString(req, "message")
		result = gitTag(targetPath, tAction, name, msg)
	case "remote":
		rAction := "list"
		if a, ok := extractOptionalString(req, "action"); ok {
			rAction = a
		}
		name, _ := extractOptionalString(req, "name")
		url, _ := extractOptionalString(req, "url")
		result = gitRemote(targetPath, rAction, name, url)
	case "checkout":
		target, err := extractArg[string](req, "target")
		if err != nil {
			return mcp.NewToolResultError("missing required argument 'target' for action=checkout"), nil
		}
		create, _ := extractOptionalBool(req, "create")
		result = gitCheckout(targetPath, target, create)
	case "revert":
		commit, err := extractArg[string](req, "commit")
		if err != nil {
			return mcp.NewToolResultError("missing required argument 'commit' for action=revert"), nil
		}
		noCommit, _ := extractOptionalBool(req, "noCommit")
		result = gitRevert(targetPath, commit, noCommit)
	default:
		return mcp.NewToolResultError(fmt.Sprintf("unknown git action: %s. Supported actions: status, log, diff, add, commit, push, pull, branch, stash, reset, clean, tag, remote, checkout, revert", action)), nil
	}

	if result.ExitCode != 0 && result.Stderr == "" {
		return mcp.NewToolResultError(fmt.Sprintf("git %s failed (exit code %d)", action, result.ExitCode)), nil
	}

	// Format response based on parsed data
	var text string
	switch r := result.Parsed.(type) {
	case []map[string]interface{}:
		text = formatGitListResult(action, r)
	default:
		if result.Stdout != "" {
			text = fmt.Sprintf("git %s completed:\n\n%s", action, result.Stdout)
		} else if result.Stderr != "" {
			text = fmt.Sprintf("git %s output:\n\n%s", action, result.Stderr)
		} else {
			text = fmt.Sprintf("git %s completed successfully (exit code: %d)", action, result.ExitCode)
		}
	}

	return mcp.NewToolResultText(text), nil
}

// ==================== Git Action Implementations ====================

func runGitCommand(dir string, args ...string) *gitResult {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := &gitResult{
		ExitCode: exitCode,
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
	}

	return result
}

func gitStatus(dir string, extraArgs []string) *gitResult {
	args := []string{"status", "--porcelain=v1", "--branch"}
	args = append(args, extraArgs...)
	result := runGitCommand(dir, args...)

	if result.ExitCode == 0 && result.Stdout != "" {
		parsed := parseGitStatus(result.Stdout)
		result.Parsed = parsed
	}

	return result
}

func parseGitStatus(output string) []map[string]interface{} {
	var results []map[string]interface{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		entry := map[string]interface{}{
			"line": line,
		}
		// Parse porcelain format: "XY [HEAD -> branch] filename"
		if len(line) >= 3 {
			entry["indexStatus"] = string([]rune(line)[0])
			entry["worktreeStatus"] = string([]rune(line)[1])
			rest := strings.TrimSpace(line[2:])
			if idx := strings.Index(rest, "]"); idx > 0 {
				entry["ref"] = rest[:idx+1]
				rest = strings.TrimSpace(rest[idx+1:])
			}
			entry["path"] = rest
		}
		results = append(results, entry)
	}
	return results
}

func gitLog(dir string, maxCount int, format string) *gitResult {
	var args []string
	switch format {
	case "json":
		args = []string{"log", "--max-count", fmt.Sprintf("%d", maxCount), "--pretty=format:{\"sha\":\"%H\",\"abbreviatedSha\":\"%h\",\"authorName\":\"%an\",\"authorEmail\":\"%ae\",\"date\":\"%ai\",\"subject\":\"%s\"}", "\n"}
	case "oneline":
		args = []string{"log", "--max-count", fmt.Sprintf("%d", maxCount), "--pretty=format:%H %s"}
	case "fuller":
		args = []string{"log", "--max-count", fmt.Sprintf("%d", maxCount), "--pretty=fuller"}
	default: // short
		args = []string{"log", "--max-count", fmt.Sprintf("%d", maxCount), "--pretty=short"}
	}

	result := runGitCommand(dir, args...)

	if result.ExitCode == 0 && format == "json" && result.Stdout != "" {
		var logs []map[string]interface{}
		for _, line := range strings.Split(result.Stdout, "\n") {
			if line == "" {
				continue
			}
			var entry map[string]interface{}
			if err := json.Unmarshal([]byte(line), &entry); err == nil {
				logs = append(logs, entry)
			}
		}
		result.Parsed = logs
	}

	return result
}

func gitDiff(dir string, staged bool, path string) *gitResult {
	var args []string
	if staged {
		args = []string{"diff", "--staged", "-U3"}
	} else {
		args = []string{"diff", "-U3"}
	}
	if path != "" {
		args = append(args, "--", path)
	}

	result := runGitCommand(dir, args...)
	return result
}

func gitAdd(dir string, files []string) *gitResult {
	args := append([]string{"add"}, files...)
	return runGitCommand(dir, args...)
}

func gitCommit(dir string, message string, amend bool) *gitResult {
	var args []string
	if amend {
		args = []string{"commit", "--amend", "-m", message}
	} else {
		args = []string{"commit", "-m", message}
	}
	return runGitCommand(dir, args...)
}

func gitPush(dir string, remote string, force bool) *gitResult {
	var args []string
	if force {
		args = []string{"push", "--force", remote}
	} else {
		args = []string{"push", remote}
	}
	return runGitCommand(dir, args...)
}

func gitPull(dir string, remote string) *gitResult {
	args := []string{"pull", remote}
	return runGitCommand(dir, args...)
}

func gitBranch(dir string, action string, name string) *gitResult {
	switch action {
	case "list":
		result := runGitCommand(dir, "branch", "-v")
		if result.ExitCode == 0 && result.Stdout != "" {
			parsed := parseGitBranchList(result.Stdout)
			result.Parsed = parsed
		}
		return result
	case "create":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "branch name required for action=create"}
		}
		return runGitCommand(dir, "branch", name)
	case "delete":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "branch name required for action=delete"}
		}
		return runGitCommand(dir, "branch", "-d", name)
	case "switch":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "branch name required for action=switch"}
		}
		return runGitCommand(dir, "checkout", name)
	default:
		return &gitResult{ExitCode: -1, Stderr: fmt.Sprintf("unknown branch action: %s", action)}
	}
}

func parseGitBranchList(output string) []map[string]interface{} {
	var branches []map[string]interface{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		entry := map[string]interface{}{
			"line": line,
		}
		// Current branch is marked with *
		isCurrent := strings.HasPrefix(line, "* ")
		entry["current"] = isCurrent
		if !isCurrent {
			line = strings.TrimPrefix(line, "  ")
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) >= 1 {
			entry["name"] = parts[0]
		}
		if len(parts) >= 2 {
			entry["commitMsg"] = parts[1]
		}
		branches = append(branches, entry)
	}
	return branches
}

func gitStash(dir string, action string, message string) *gitResult {
	switch action {
	case "save":
		var args []string
		if message != "" {
			args = []string{"stash", "push", "-m", message}
		} else {
			args = []string{"stash", "push"}
		}
		return runGitCommand(dir, args...)
	case "pop":
		return runGitCommand(dir, "stash", "pop")
	case "list":
		result := runGitCommand(dir, "stash", "list")
		if result.ExitCode == 0 && result.Stdout != "" {
			parsed := parseGitStashList(result.Stdout)
			result.Parsed = parsed
		}
		return result
	case "apply":
		return runGitCommand(dir, "stash", "apply")
	default:
		return &gitResult{ExitCode: -1, Stderr: fmt.Sprintf("unknown stash action: %s", action)}
	}
}

func parseGitStashList(output string) []map[string]interface{} {
	var stashes []map[string]interface{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		stashes = append(stashes, map[string]interface{}{
			"line": line,
		})
	}
	return stashes
}

func gitReset(dir string, mode string, commit string) *gitResult {
	var args []string
	args = append(args, "reset", fmt.Sprintf("-%s", mode))
	if commit != "" {
		args = append(args, commit)
	}
	return runGitCommand(dir, args...)
}

func gitClean(dir string, dryRun bool, directories bool) *gitResult {
	var args []string
	args = append(args, "clean")
	if dryRun {
		args = append(args, "-n")
	}
	if directories {
		args = append(args, "-d")
	} else {
		args = append(args, "-f")
	}
	return runGitCommand(dir, args...)
}

func gitTag(dir string, action string, name string, message string) *gitResult {
	switch action {
	case "list":
		result := runGitCommand(dir, "tag", "-l", "-n")
		if result.ExitCode == 0 && result.Stdout != "" {
			parsed := parseGitTagList(result.Stdout)
			result.Parsed = parsed
		}
		return result
	case "create":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "tag name required for action=create"}
		}
		var args []string
		args = append(args, "tag")
		if message != "" {
			args = append(args, "-a", name, "-m", message)
		} else {
			args = append(args, name)
		}
		return runGitCommand(dir, args...)
	case "delete":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "tag name required for action=delete"}
		}
		return runGitCommand(dir, "tag", "-d", name)
	default:
		return &gitResult{ExitCode: -1, Stderr: fmt.Sprintf("unknown tag action: %s", action)}
	}
}

func parseGitTagList(output string) []map[string]interface{} {
	var tags []map[string]interface{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		tags = append(tags, map[string]interface{}{
			"line": line,
		})
	}
	return tags
}

func gitRemote(dir string, action string, name string, url string) *gitResult {
	switch action {
	case "list":
		result := runGitCommand(dir, "remote", "-v")
		if result.ExitCode == 0 && result.Stdout != "" {
			parsed := parseGitRemoteList(result.Stdout)
			result.Parsed = parsed
		}
		return result
	case "add":
		if name == "" || url == "" {
			return &gitResult{ExitCode: -1, Stderr: "remote name and URL required for action=add"}
		}
		return runGitCommand(dir, "remote", "add", name, url)
	case "remove":
		if name == "" {
			return &gitResult{ExitCode: -1, Stderr: "remote name required for action=remove"}
		}
		return runGitCommand(dir, "remote", "remove", name)
	case "set-url":
		if name == "" || url == "" {
			return &gitResult{ExitCode: -1, Stderr: "remote name and URL required for action=set-url"}
		}
		return runGitCommand(dir, "remote", "set-url", name, url)
	default:
		return &gitResult{ExitCode: -1, Stderr: fmt.Sprintf("unknown remote action: %s", action)}
	}
}

func parseGitRemoteList(output string) []map[string]interface{} {
	var remotes []map[string]interface{}
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			entry := map[string]interface{}{
				"name": parts[0],
				"url":  parts[1],
				"line": line,
			}
			if len(parts) >= 3 {
				entry["fetchPush"] = parts[2]
			}
			remotes = append(remotes, entry)
		}
	}
	return remotes
}

func gitCheckout(dir string, target string, create bool) *gitResult {
	var args []string
	if create {
		args = []string{"checkout", "-b", target}
	} else {
		args = []string{"checkout", target}
	}
	return runGitCommand(dir, args...)
}

func gitRevert(dir string, commit string, noCommit bool) *gitResult {
	var args []string
	args = append(args, "revert", commit)
	if noCommit {
		args = append(args, "--no-commit")
	}
	return runGitCommand(dir, args...)
}

// ==================== Response Formatting ====================

func formatGitListResult(action string, items []map[string]interface{}) string {
	var sb strings.Builder

	switch action {
	case "status":
		sb.WriteString("Working tree status:\n\n")
		modified := 0
		untracked := 0
		for _, item := range items {
			line, _ := item["line"].(string)
			if line == "" {
				continue
			}
			idx, _ := item["indexStatus"].(string)
			wt, _ := item["worktreeStatus"].(string)
			if idx == "?" || wt == "?" {
				untracked++
			} else {
				modified++
			}
			sb.WriteString(fmt.Sprintf("  %s\n", line))
		}
		sb.WriteString(fmt.Sprintf("\n%d modified, %d untracked files\n", modified, untracked))

	case "branch":
		sb.WriteString("Branches:\n\n")
		for _, item := range items {
			name, _ := item["name"].(string)
			current, _ := item["current"].(bool)
			msg, _ := item["commitMsg"].(string)
			prefix := "  "
			if current {
				prefix = "* "
			}
			sb.WriteString(fmt.Sprintf("%s%s %s\n", prefix, name, msg))
		}

	case "stash":
		sb.WriteString("Stashes:\n\n")
		for _, item := range items {
			line, _ := item["line"].(string)
			if line != "" {
				sb.WriteString(fmt.Sprintf("  %s\n", line))
			}
		}

	case "tag":
		sb.WriteString("Tags:\n\n")
		for _, item := range items {
			line, _ := item["line"].(string)
			if line != "" {
				sb.WriteString(fmt.Sprintf("  %s\n", line))
			}
		}

	case "remote":
		sb.WriteString("Remotes:\n\n")
		for _, item := range items {
			name, _ := item["name"].(string)
			url, _ := item["url"].(string)
			sb.WriteString(fmt.Sprintf("  %s -> %s\n", name, url))
		}

	default:
		for _, item := range items {
			line, _ := item["line"].(string)
			if line != "" {
				sb.WriteString(line + "\n")
			}
		}
	}

	return sb.String()
}
