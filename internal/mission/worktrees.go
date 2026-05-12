package mission

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type bridgeProjectGitResponse struct {
	CWD                   string              `json:"cwd"`
	RepoPath              string              `json:"repo_path,omitempty"`
	IsGit                 bool                `json:"is_git"`
	CurrentBranch         string              `json:"current_branch,omitempty"`
	Branches              []bridgeGitBranch   `json:"branches,omitempty"`
	Worktrees             []bridgeGitWorktree `json:"worktrees,omitempty"`
	SuggestedWorktreeName string              `json:"suggested_worktree_name,omitempty"`
}

type bridgeGitBranch struct {
	Name         string `json:"name"`
	Current      bool   `json:"current,omitempty"`
	Remote       bool   `json:"remote,omitempty"`
	CheckedOut   bool   `json:"checked_out,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
}

type bridgeGitWorktree struct {
	Path         string `json:"path"`
	RelativePath string `json:"relative_path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	Head         string `json:"head,omitempty"`
	Current      bool   `json:"current,omitempty"`
}

type bridgeCreateWorktreeRequest struct {
	CWD    string `json:"cwd"`
	Branch string `json:"branch"`
	Name   string `json:"name,omitempty"`
}

type bridgeCreateWorktreeResponse struct {
	Worktree bridgeGitWorktree        `json:"worktree"`
	Context  bridgeProjectGitResponse `json:"context"`
}

type parsedGitWorktree struct {
	Path   string
	Branch string
	Head   string
}

func loadProjectGitContext(projectRoot, cwd string) (bridgeProjectGitResponse, error) {
	projectPath, err := validateProjectPath(projectRoot, cwd)
	if err != nil {
		return bridgeProjectGitResponse{}, err
	}
	response := bridgeProjectGitResponse{
		CWD:   projectPath,
		IsGit: false,
	}

	repoPath, err := gitOutput(projectPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return response, nil
	}
	repoPath = filepath.Clean(repoPath)
	if err := validateExistingPathInsideRoot(projectRoot, repoPath, "git repo"); err != nil {
		return bridgeProjectGitResponse{}, err
	}
	response.RepoPath = repoPath
	response.IsGit = true

	if current, err := gitOutput(repoPath, "branch", "--show-current"); err == nil {
		response.CurrentBranch = strings.TrimSpace(current)
	}

	worktrees, checkedOut, err := bridgeGitWorktrees(projectRoot, repoPath, projectPath)
	if err != nil {
		return bridgeProjectGitResponse{}, err
	}
	response.Worktrees = worktrees

	branches, err := bridgeGitBranches(repoPath, response.CurrentBranch, checkedOut)
	if err != nil {
		return bridgeProjectGitResponse{}, err
	}
	response.Branches = branches
	response.SuggestedWorktreeName = defaultWorktreeName(repoPath, response.CurrentBranch)
	return response, nil
}

func createProjectWorktree(projectRoot string, request bridgeCreateWorktreeRequest) (bridgeCreateWorktreeResponse, error) {
	projectPath, err := validateProjectPath(projectRoot, request.CWD)
	if err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}
	branch := normalizeBranchInput(request.Branch)
	if err := validateWorktreeBranch(branch); err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}

	context, err := loadProjectGitContext(projectRoot, projectPath)
	if err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}
	if !context.IsGit || context.RepoPath == "" {
		return bridgeCreateWorktreeResponse{}, fmt.Errorf("selected project is not a git repo")
	}

	name := sanitizeWorktreeName(request.Name)
	if name == "" {
		name = defaultWorktreeName(context.RepoPath, branch)
	}
	if name == "" {
		return bridgeCreateWorktreeResponse{}, fmt.Errorf("worktree name is required")
	}
	worktreePath := filepath.Clean(filepath.Join(filepath.Dir(context.RepoPath), name))
	if err := validateNewPathInsideRoot(projectRoot, worktreePath, "worktree"); err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}

	checkedOut := false
	for _, candidate := range context.Branches {
		if candidate.Name == branch {
			checkedOut = candidate.CheckedOut
			break
		}
	}

	args := []string{"worktree", "add"}
	if strings.HasPrefix(branch, "origin/") || checkedOut {
		localBranch := sanitizeWorktreeBranchName(name)
		if localBranch == "" {
			return bridgeCreateWorktreeResponse{}, fmt.Errorf("worktree branch name is required")
		}
		args = append(args, "-b", localBranch, worktreePath, branch)
	} else {
		args = append(args, worktreePath, branch)
	}
	if _, err := gitOutput(context.RepoPath, args...); err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}

	nextContext, err := loadProjectGitContext(projectRoot, worktreePath)
	if err != nil {
		return bridgeCreateWorktreeResponse{}, err
	}
	created := bridgeGitWorktree{Path: worktreePath}
	if rel, err := filepath.Rel(cleanPathForRel(projectRoot), worktreePath); err == nil && !strings.HasPrefix(rel, "..") {
		created.RelativePath = filepath.ToSlash(rel)
	}
	for _, worktree := range nextContext.Worktrees {
		if sameExistingPath(worktree.Path, worktreePath) {
			created = worktree
			break
		}
	}
	return bridgeCreateWorktreeResponse{
		Worktree: created,
		Context:  nextContext,
	}, nil
}

func bridgeGitBranches(repoPath, currentBranch string, checkedOut map[string]string) ([]bridgeGitBranch, error) {
	out, err := gitOutput(repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var branches []bridgeGitBranch
	for _, raw := range strings.Split(out, "\n") {
		name := strings.TrimSpace(raw)
		if name == "" || name == "HEAD" || strings.HasSuffix(name, "/HEAD") || seen[name] {
			continue
		}
		seen[name] = true
		worktreePath := checkedOut[name]
		branch := bridgeGitBranch{
			Name:         name,
			Current:      name == currentBranch,
			Remote:       strings.HasPrefix(name, "origin/"),
			CheckedOut:   worktreePath != "",
			WorktreePath: worktreePath,
		}
		branches = append(branches, branch)
	}
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].Current != branches[j].Current {
			return branches[i].Current
		}
		if branches[i].Remote != branches[j].Remote {
			return !branches[i].Remote
		}
		return strings.ToLower(branches[i].Name) < strings.ToLower(branches[j].Name)
	})
	return branches, nil
}

func bridgeGitWorktrees(projectRoot, repoPath, currentPath string) ([]bridgeGitWorktree, map[string]string, error) {
	out, err := gitOutput(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, nil, err
	}
	root, err := cleanProjectRoot(projectRoot)
	if err != nil {
		return nil, nil, err
	}
	parsed := parseGitWorktreeList(out)
	checkedOut := make(map[string]string)
	worktrees := make([]bridgeGitWorktree, 0, len(parsed))
	for _, worktree := range parsed {
		if worktree.Path == "" {
			continue
		}
		branch := normalizeWorktreeBranch(worktree.Branch)
		if branch != "" {
			checkedOut[branch] = worktree.Path
		}
		if !existingPathInsideRoot(root, worktree.Path) {
			continue
		}
		rel, _ := filepath.Rel(root, filepath.Clean(worktree.Path))
		worktrees = append(worktrees, bridgeGitWorktree{
			Path:         filepath.Clean(worktree.Path),
			RelativePath: filepath.ToSlash(rel),
			Branch:       branch,
			Head:         worktree.Head,
			Current:      sameExistingPath(worktree.Path, currentPath),
		})
	}
	sort.Slice(worktrees, func(i, j int) bool {
		if worktrees[i].Current != worktrees[j].Current {
			return worktrees[i].Current
		}
		return strings.ToLower(worktrees[i].RelativePath) < strings.ToLower(worktrees[j].RelativePath)
	})
	return worktrees, checkedOut, nil
}

func parseGitWorktreeList(output string) []parsedGitWorktree {
	var worktrees []parsedGitWorktree
	var current parsedGitWorktree
	flush := func() {
		if current.Path != "" {
			worktrees = append(worktrees, current)
		}
		current = parsedGitWorktree{}
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "worktree":
			if current.Path != "" {
				flush()
			}
			current.Path = strings.TrimSpace(value)
		case "HEAD":
			current.Head = strings.TrimSpace(value)
		case "branch":
			current.Branch = strings.TrimSpace(value)
		}
	}
	flush()
	return worktrees
}

func normalizeWorktreeBranch(branch string) string {
	branch = normalizeBranchInput(branch)
	branch = strings.TrimPrefix(branch, "refs/heads/")
	return branch
}

func validateWorktreeBranch(branch string) error {
	if branch == "" {
		return fmt.Errorf("branch is required")
	}
	if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, "\x00\r\n") {
		return fmt.Errorf("invalid branch name")
	}
	return nil
}

func defaultWorktreeName(repoPath, branch string) string {
	repoName := filepath.Base(filepath.Clean(repoPath))
	slug := branchSlug(branch)
	if slug == "" {
		slug = "worktree"
	}
	return sanitizeWorktreeName(repoName + "-" + slug)
}

func sanitizeWorktreeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	cleaned := strings.Trim(b.String(), "-.")
	if len(cleaned) > 80 {
		cleaned = strings.Trim(cleaned[:80], "-.")
	}
	return cleaned
}

func sanitizeWorktreeBranchName(name string) string {
	return strings.Trim(sanitizeWorktreeName(name), ".")
}

func validateExistingPathInsideRoot(root, path, label string) error {
	root, err := cleanProjectRoot(root)
	if err != nil {
		return err
	}
	if !existingPathInsideRoot(root, path) {
		return fmt.Errorf("%s must be under %s", label, root)
	}
	return nil
}

func validateNewPathInsideRoot(root, path, label string) error {
	root, err := cleanProjectRoot(root)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if !pathInsideRoot(root, path) {
		return fmt.Errorf("%s must be under %s", label, root)
	}
	if dirExists(path) {
		return fmt.Errorf("%s already exists: %s", label, path)
	}
	return nil
}

func existingPathInsideRoot(root, path string) bool {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return pathInsideRoot(root, path)
}

func sameExistingPath(a, b string) bool {
	aEval, aErr := filepath.EvalSymlinks(a)
	bEval, bErr := filepath.EvalSymlinks(b)
	if aErr == nil && bErr == nil {
		return filepath.Clean(aEval) == filepath.Clean(bEval)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func cleanPathForRel(path string) string {
	cleaned, err := cleanProjectRoot(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return cleaned
}
