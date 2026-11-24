package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

// worktree represents a temporary git worktree that the script creates to inspect,
// validate, and update release files.
type worktree struct {
	eff effects
	// repo is the original checkout that owns this worktree.
	repo *repo
	// worktreeRoot is the worktreeRoot directory of this worktree.
	worktreeRoot string
	// branch is the git branch that this worktree has checked out.
	branch string
}

// Git/GitHub operations

// listTags returns the repository's git tags matching the given glob pattern.
func (w *worktree) listTags(pattern string) ([]string, error) {
	output, err := w.eff.runCommand(w.worktreeRoot, "git", "tag", "--list", pattern)
	if err != nil {
		return nil, fmt.Errorf("list tags matching %s: %w", pattern, err)
	}
	var tags []string
	for _, line := range strings.Split(output, "\n") {
		if tag := strings.TrimSpace(line); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func (w *worktree) hasCommits(ref string, pathspecs ...string) (bool, error) {
	args := []string{"log", "--full-history", "--no-renames", "--max-count=1", "--format=%H", ref, "--"}
	args = append(args, pathspecs...)
	output, err := w.eff.runCommand(w.worktreeRoot, "git", args...)
	if err != nil {
		return false, fmt.Errorf("check commits at %s: %w", ref, err)
	}
	return strings.TrimSpace(output) != "", nil
}

func (w *worktree) viewCommits(ref string, pathspecs ...string) error {
	args := []string{"log", "--no-color", "--full-history", "--topo-order", "--no-renames", "--abbrev=7",
		"--date=format:%d %b %Y",
		"--format=%ad (%h)  %s%n ↪ https://github.com/temporalio/sdk-go/commit/%H%n", ref, "--"}
	args = append(args, pathspecs...)
	output, err := w.eff.runCommand(w.worktreeRoot, "git", args...)
	if err != nil {
		return err
	}
	return w.eff.page(output)
}

func (w *worktree) viewDiff(base string, pathspecs ...string) error {
	args := []string{"--paginate", "diff", "--no-renames", base, "HEAD", "--"}
	args = append(args, pathspecs...)
	return w.eff.runInteractiveCommand(w.worktreeRoot, "git", args...)
}

func (w *worktree) commitRelease(modules []pure.VersionedModule) error {
	paths := []string{}
	for _, module := range modules {
		paths = append(paths, module.Module.ReleaseFiles("")...)
		deleted, err := w.eff.runCommand(w.worktreeRoot, "git", "diff",
			"--name-only", "--diff-filter=D", "-z", "--", module.Module.Fragments("")+"/")
		if err != nil {
			return fmt.Errorf("list consumed fragments: %w", err)
		}
		if deleted != "" {
			paths = append(paths, strings.Split(strings.TrimSuffix(deleted, "\x00"), "\x00")...)
		}
	}
	_, err := w.eff.runCommand(w.worktreeRoot, "git", append([]string{"add", "-A", "--"}, paths...)...)
	if err != nil {
		return fmt.Errorf("stage release files: %w", err)
	}
	gitArgs := []string{"commit", "-m", pure.ReleaseCommitMessage(modules, w.eff.today())}
	_, err = w.eff.runCommand(w.worktreeRoot, "git", gitArgs...)
	if err != nil {
		return fmt.Errorf("commit release files: %w", err)
	}
	return nil
}

func (w *worktree) pushBranch() error {
	_, err := w.eff.runCommand(w.worktreeRoot, "git", "push", "--set-upstream", "origin", w.branch)
	if err != nil {
		return fmt.Errorf("push branch: %w", err)
	}
	return nil
}

func (w *worktree) openDraftPR(modules []pure.VersionedModule) (string, error) {
	title, body, _ := strings.Cut(pure.ReleaseCommitMessage(modules, w.eff.today()), "\n\n")
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", "pr", "create", "--draft",
		"--base", "main",
		"--head", w.branch,
		"--label", "skip-changelog",
		"--title", title,
		"--body", body)
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.eff, "Draft PR: %s", url)
	return url, nil
}

func (w *worktree) createDraftRelease(module pure.VersionedModule, commit, releaseNotes string) (string, error) {
	m := module.Module
	tag := m.Tag(module.Version)
	releaseNotesPath := filepath.Join(w.worktreeRoot, "prepare-release-notes.md")
	err := w.eff.writeFile(releaseNotesPath, releaseNotes)
	if err != nil {
		return "", fmt.Errorf("write release notes to %s: %w", releaseNotesPath, err)
	}
	ghArgs := []string{"release", "create", tag, "--draft", "--target", commit, "--title", tag, "--notes-file", releaseNotesPath}
	if m.GenerateNotes() {
		ghArgs = append(ghArgs, "--generate-notes")
	}
	if !m.MarkLatest() {
		ghArgs = append(ghArgs, "--latest=false")
	}
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", ghArgs...)
	if err != nil {
		return "", fmt.Errorf("create draft release %s: %w", tag, err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.eff, "Draft release: %s", url)
	if !m.MarkLatest() {
		printDetail(w.eff, "WARNING: Editing on GitHub enables \"Set as the latest release\". Make sure to toggle it back off when you release %s.", tag)
	}
	return url, nil
}

func (w *worktree) cleanup() error {
	return w.repo.removeWorktree(w.worktreeRoot)
}
