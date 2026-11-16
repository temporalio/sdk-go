package main

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

// repo represents the original checkout that owns temporary worktrees.
type repo struct {
	eff            effects
	repoRoot       string
	contribModules []pure.Module
}

func newRepo(eff effects) (repo, error) {
	root, err := repoRoot()
	if err != nil {
		return repo{}, err
	}
	output, err := eff.runCommand(root, "git", "ls-tree", "-r", "--name-only", "-z", "HEAD", "--", "contrib/")
	if err != nil {
		return repo{}, fmt.Errorf("list contrib module files at HEAD: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	return repo{
		eff:            eff,
		repoRoot:       root,
		contribModules: pure.ContribModules(paths),
	}, nil
}

func (r *repo) modulePathspecs(module pure.Module) []string {
	dir := module.Dir
	if module.Name() == "sdk" {
		dir = "."
	}

	// Pathspecs matching all files in the module except go.mod,
	// go.sum, and any nested contrib modules.
	pathspecs := []string{dir + "/"}
	for _, file := range []string{"go.mod", "go.sum"} {
		pathspecs = append(pathspecs, ":(exclude,glob)"+path.Join(dir, "**", file))
	}
	for _, nested := range r.contribModules {
		if module.Name() == "sdk" || strings.HasPrefix(nested.Dir, dir+"/") {
			pathspecs = append(pathspecs, ":(exclude,literal)"+nested.Dir+"/")
		}
	}

	return pathspecs
}

// repoRoot locates the git repo that contains this script.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate publisher source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}

// Git/GitHub operations

func (r *repo) moduleChanged(module pure.Module, baseCommit string) (bool, error) {
	args := []string{"diff", "--name-only", "--no-renames", baseCommit, "HEAD", "--"}
	args = append(args, r.modulePathspecs(module)...)
	output, err := r.eff.runCommand(r.repoRoot, "git", args...)
	if err != nil {
		return false, fmt.Errorf("check changes for %s since %s: %w", module.Name(), baseCommit, err)
	}
	return strings.TrimSpace(output) != "", nil
}

func (r *repo) fetchOrigin() error {
	_, err := r.eff.runCommand(r.repoRoot, "git", "fetch", "--tags", "origin")
	if err != nil {
		return fmt.Errorf("fetch origin: %w", err)
	}
	return nil
}

func (r *repo) resolveCommit(ref string) (string, error) {
	commit, err := r.eff.runCommand(r.repoRoot, "git", "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve commit %q: %w", ref, err)
	}
	return strings.TrimSpace(commit), nil
}

func (r *repo) createWorktree(commit string) (*worktree, error) {
	root, err := r.eff.mkdirTemp("", "prepare-go-release-")
	if err != nil {
		return nil, fmt.Errorf("create temporary worktree: %w", err)
	}
	_, err = r.eff.runCommand(r.repoRoot, "git", "worktree", "add", "--detach", root, commit)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	return &worktree{
		eff:          r.eff,
		repo:         r,
		worktreeRoot: root,
	}, nil
}

func (r *repo) createWorktreeAndBranch(commit, branch string) (*worktree, error) {
	root, err := r.eff.mkdirTemp("", "prepare-go-release-")
	if err != nil {
		return nil, fmt.Errorf("create temporary worktree: %w", err)
	}
	_, err = r.eff.runCommand(r.repoRoot, "git", "worktree", "add", "-b", branch, root, commit)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	return &worktree{
		eff:          r.eff,
		repo:         r,
		worktreeRoot: root,
		branch:       branch,
	}, nil
}

func (r *repo) removeWorktree(root string) error {
	_, err := r.eff.runCommand(r.repoRoot, "git", "worktree", "remove", "--force", root)
	if err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}
