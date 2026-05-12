package mission

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxProjectScanDepth = 5

type bridgeProject struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	RelativePath string `json:"relative_path"`
}

func defaultProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join("Documents", "repos")
	}
	return filepath.Join(home, "Documents", "repos")
}

func cleanProjectRoot(root string) (string, error) {
	root = expandUserPath(strings.TrimSpace(root))
	if root == "" {
		root = defaultProjectsRoot()
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("projects root not found: %s", abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("projects root is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func listBridgeProjects(root string) ([]bridgeProject, error) {
	root, err := cleanProjectRoot(root)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bridgeProject)
	err = filepath.WalkDir(root, func(projectPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if projectPath == root {
			return nil
		}
		if shouldSkipProjectDir(entry.Name()) {
			return filepath.SkipDir
		}

		rel, err := filepath.Rel(root, projectPath)
		if err != nil {
			return nil
		}
		depth := pathDepth(rel)
		if depth > maxProjectScanDepth {
			return filepath.SkipDir
		}
		if depth == 1 || hasProjectMarker(projectPath) {
			path := filepath.Clean(projectPath)
			seen[path] = bridgeProject{
				Name:         filepath.Base(projectPath),
				Path:         path,
				RelativePath: filepath.ToSlash(rel),
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	projects := make([]bridgeProject, 0, len(seen))
	for _, project := range seen {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].RelativePath) < strings.ToLower(projects[j].RelativePath)
	})
	return projects, nil
}

func validateProjectPath(root, rawPath string) (string, error) {
	root, err := cleanProjectRoot(root)
	if err != nil {
		return "", err
	}
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("project path is required")
	}
	projectPath, err := filepath.Abs(expandUserPath(rawPath))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(projectPath)
	if err != nil {
		return "", fmt.Errorf("project path not found: %s", projectPath)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %s", projectPath)
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	projectPath, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", err
	}
	if !pathInsideRoot(root, projectPath) {
		return "", fmt.Errorf("project must be under %s", root)
	}
	return filepath.Clean(projectPath), nil
}

func pathInsideRoot(root, projectPath string) bool {
	root = filepath.Clean(root)
	projectPath = filepath.Clean(projectPath)
	rel, err := filepath.Rel(root, projectPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func pathDepth(rel string) int {
	if rel == "." || rel == "" {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

func shouldSkipProjectDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build", ".build", ".next", ".turbo":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func hasProjectMarker(projectPath string) bool {
	for _, marker := range []string{
		".git",
		"go.mod",
		"package.json",
		"Cargo.toml",
		"pyproject.toml",
		"Package.swift",
		"project.yml",
		"pom.xml",
		"Gemfile",
	} {
		if _, err := os.Stat(filepath.Join(projectPath, marker)); err == nil {
			return true
		}
	}
	entries, err := os.ReadDir(projectPath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".xcodeproj") || strings.HasSuffix(name, ".xcworkspace") {
			return true
		}
	}
	return false
}
