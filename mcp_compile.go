package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ==================== Compile Status Helpers ====================

func detectNodeProject(dir string) string {
	var pkgPath string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "package.json" && !d.IsDir() {
			pkgPath = path
			return filepath.SkipAll
		}
		return nil
	})
	return pkgPath
}

func detectPythonProject(dir string) []string {
	var pyFiles []string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if name == "requirements.txt" || name == "pyproject.toml" || name == "setup.py" || name == "Pipfile" || strings.HasSuffix(name, ".py") {
			pyFiles = append(pyFiles, path)
		}
		return nil
	})
	if len(pyFiles) > 0 {
		return pyFiles
	}
	return nil
}

func detectDotnetProject(dir string) string {
	var dotnetFile string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if (strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".vbproj") || strings.HasSuffix(name, ".sln")) && !d.IsDir() {
			dotnetFile = path
			return filepath.SkipAll
		}
		return nil
	})
	return dotnetFile
}

func detectGoProject(dir string) string {
	var goModPath string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == "go.mod" && !d.IsDir() {
			goModPath = path
			return filepath.SkipAll
		}
		return nil
	})
	return goModPath
}

func checkNodeStatus(sb *strings.Builder, projectPath string, pkgPath string) {
	sb.WriteString(fmt.Sprintf("Package file: %s\n", pkgPath))

	nodeCmd := exec.Command("node", "--version")
	if err := nodeCmd.Run(); err != nil {
		sb.WriteString("Runtime (node): NOT INSTALLED\n")
		sb.WriteString("Build status: Cannot build - node not available\n")
		return
	}
	output, _ := nodeCmd.Output()
	sb.WriteString(fmt.Sprintf("Runtime (node): INSTALLED (%s)\n", strings.TrimSpace(string(output))))

	npmCmd := exec.Command("npm", "--version")
	if err := npmCmd.Run(); err != nil {
		sb.WriteString("Package manager (npm): NOT INSTALLED\n")
	} else {
		npmOutput, _ := npmCmd.Output()
		sb.WriteString(fmt.Sprintf("Package manager (npm): INSTALLED (%s)\n", strings.TrimSpace(string(npmOutput))))
	}

	nodeModulesPath := filepath.Join(filepath.Dir(pkgPath), "node_modules")
	if _, err := os.Stat(nodeModulesPath); err == nil {
		sb.WriteString("Dependencies installed: Yes (node_modules/ found)\n")
	} else {
		sb.WriteString("Dependencies installed: No (run 'npm install' first)\n")
	}

	pkgContent, err := os.ReadFile(pkgPath)
	if err == nil {
		var pkgData map[string]interface{}
		if jsonErr := json.Unmarshal(pkgContent, &pkgData); jsonErr == nil {
			if scripts, ok := pkgData["scripts"].(map[string]interface{}); ok && len(scripts) > 0 {
				sb.WriteString("Available scripts:\n")
				for name := range scripts {
					sb.WriteString(fmt.Sprintf("  - %s\n", name))
				}
				if buildScript, hasBuild := scripts["build"].(string); hasBuild && buildScript != "" {
					sb.WriteString("\nAttempting 'npm run build'...\n")
					buildCmd := exec.Command("npm", "run", "build")
					buildCmd.Dir = filepath.Dir(pkgPath)
					buildOutput, buildErr := buildCmd.CombinedOutput()
					if buildErr == nil {
						sb.WriteString("Build status: SUCCESS\n")
					} else {
						sb.WriteString(fmt.Sprintf("Build status: FAILED\nBuild output:\n%s\n", string(buildOutput)))
					}
				}
			} else {
				sb.WriteString("Available scripts: None defined in package.json\n")
			}
		}
	}

	sb.WriteString("\n")
}

func checkPythonStatus(sb *strings.Builder, projectPath string, pyFiles []string) {
	sb.WriteString(fmt.Sprintf("Project files found: %d\n", len(pyFiles)))
	for _, f := range pyFiles[:minInt(len(pyFiles), 5)] {
		sb.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	if len(pyFiles) > 5 {
		sb.WriteString(fmt.Sprintf("  ... and %d more files\n", len(pyFiles)-5))
	}

	pythonCmd := exec.Command("python3", "--version")
	if err := pythonCmd.Run(); err != nil {
		pythonCmd = exec.Command("python", "--version")
		if err := pythonCmd.Run(); err != nil {
			sb.WriteString("Runtime (python): NOT INSTALLED\n")
			sb.WriteString("Compile status: Cannot compile - Python not available\n")
			return
		}
		output, _ := pythonCmd.Output()
		sb.WriteString(fmt.Sprintf("Runtime (python): INSTALLED (%s)\n", strings.TrimSpace(string(output))))
	} else {
		output, _ := pythonCmd.Output()
		sb.WriteString(fmt.Sprintf("Runtime (python3): INSTALLED (%s)\n", strings.TrimSpace(string(output))))
	}

	hasRequirements := false
	for _, f := range pyFiles {
		if filepath.Base(f) == "requirements.txt" {
			hasRequirements = true
			sb.WriteString(fmt.Sprintf("Requirements file: %s\n", f))

			pipCmd := exec.Command("pip3", "list")
			if pipErr := pipCmd.Run(); pipErr != nil {
				pipCmd = exec.Command("pip", "list")
				if pipErr = pipCmd.Run(); pipErr != nil {
					sb.WriteString("Package manager (pip): NOT INSTALLED\n")
				} else {
					output, _ := pipCmd.Output()
					sb.WriteString(fmt.Sprintf("Package manager (pip): INSTALLED (%s)\n", strings.TrimSpace(string(output))))
				}
			} else {
				pipOutput, _ := pipCmd.Output()
				sb.WriteString(fmt.Sprintf("Package manager (pip): INSTALLED (%s)\n", strings.TrimSpace(string(pipOutput))))
			}

			checkCmd := exec.Command("pip3", "install", "-r", f, "--dry-run")
			if checkErr := checkCmd.Run(); checkErr != nil {
				checkCmd = exec.Command("pip", "install", "-r", f, "--dry-run")
				if checkErr := checkCmd.Run(); checkErr != nil {
					sb.WriteString("Requirements status: Cannot verify (pip check failed)\n")
				} else {
					output, _ := checkCmd.CombinedOutput()
					sb.WriteString(fmt.Sprintf("Requirements status: OK (dry-run passed)\n%s\n", string(output)))
				}
			} else {
				output, _ := checkCmd.CombinedOutput()
				sb.WriteString(fmt.Sprintf("Requirements status: OK (dry-run passed)\n%s\n", string(output)))
			}
			break
		}
	}

	if !hasRequirements {
		sb.WriteString("Requirements file: None found\n")
	}

	syntaxErrors := 0
	syntaxChecked := 0
	for _, f := range pyFiles {
		if strings.HasSuffix(f, ".py") && !strings.HasSuffix(f, ".pyc") {
			syntaxChecked++
			pyCmd := exec.Command("python3", "-m", "py_compile", f)
			if pyErr := pyCmd.Run(); pyErr != nil {
				pyCmd = exec.Command("python", "-m", "py_compile", f)
				if pyErr := pyCmd.Run(); pyErr != nil {
					syntaxErrors++
				}
			}
		}
	}

	if syntaxChecked > 0 {
		sb.WriteString(fmt.Sprintf("Syntax check: %d/%d files passed\n", syntaxChecked-syntaxErrors, syntaxChecked))
	}

	sb.WriteString("\n")
}

func checkDotnetStatus(sb *strings.Builder, projectPath string, dotnetFile string) {
	sb.WriteString(fmt.Sprintf("Project file: %s\n", dotnetFile))

	dotnetCmd := exec.Command("dotnet", "--version")
	if err := dotnetCmd.Run(); err != nil {
		sb.WriteString("Runtime/SDK (.NET): NOT INSTALLED\n")
		sb.WriteString("Build status: Cannot build - .NET SDK not available\n")
		return
	}
	output, _ := dotnetCmd.Output()
	sb.WriteString(fmt.Sprintf("Runtime/SDK (.NET): INSTALLED (version %s)\n", strings.TrimSpace(string(output))))

	restoreCmd := exec.Command("dotnet", "restore", dotnetFile)
	restoreOutput, restoreErr := restoreCmd.CombinedOutput()
	if restoreErr != nil {
		sb.WriteString(fmt.Sprintf("Restore status: FAILED\nRestore output:\n%s\n", string(restoreOutput)))
	} else {
		sb.WriteString("Restore status: SUCCESS\n")
	}

	buildCmd := exec.Command("dotnet", "build", dotnetFile)
	buildOutput, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		sb.WriteString(fmt.Sprintf("Build status: FAILED\nBuild output:\n%s\n", string(buildOutput)))
	} else {
		sb.WriteString("Build status: SUCCESS\n")
	}

	sb.WriteString("\n")
}

func checkGoStatus(sb *strings.Builder, goModPath string) {
	sb.WriteString(fmt.Sprintf("Module file: %s\n", goModPath))

	goCmd := exec.Command("go", "--version")
	if err := goCmd.Run(); err != nil {
		sb.WriteString("Runtime (Go): NOT INSTALLED\n")
		sb.WriteString("Build status: Cannot build - Go not available\n")
		return
	}
	output, _ := goCmd.Output()
	sb.WriteString(fmt.Sprintf("Runtime (Go): INSTALLED (%s)\n", strings.TrimSpace(string(output))))

	buildDir := filepath.Dir(goModPath)
	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = buildDir
	buildOutput, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		sb.WriteString(fmt.Sprintf("Build status: FAILED\nBuild output:\n%s\n", string(buildOutput)))
	} else {
		sb.WriteString("Build status: SUCCESS\n")
	}

	sb.WriteString("\n")
}
