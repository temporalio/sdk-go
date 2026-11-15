package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

// worktree represents a temporary git worktree that the script creates to validate
// and update release files.
type worktree struct {
	eff effects
	// repo is the original checkout that owns this worktree.
	repo *repo
	// worktreeRoot is the worktreeRoot directory of this worktree.
	worktreeRoot string
	// branch is the git branch that this worktree has checked out.
	branch string
}

// releaseFile holds a prepared file so the entire batch can be checked before writing.
type releaseFile struct {
	path     string
	contents string
}

// newReleaseFiles prepares the changelog and version.go (if the module has one)
// without writing them.
func (w *worktree) newReleaseFiles(module pure.VersionedModule) ([]releaseFile, error) {
	changelogPath := w.pathToChangelog(module.Module)
	changelog, err := w.eff.readFile(changelogPath)
	if err != nil {
		return nil, err
	}
	changelog, err = pure.ComputeNewChangelog(changelog, module.Version, w.eff.today())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", changelogPath, err)
	}
	files := []releaseFile{{path: changelogPath, contents: changelog}}

	versionFile := w.pathToVersionFile(module.Module)
	if versionFile != "" {
		text, err := w.eff.readFile(versionFile)
		if err != nil {
			return nil, err
		}
		text, err = pure.ComputeNewVersionFile(text, module.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", versionFile, err)
		}
		files = append(files, releaseFile{path: versionFile, contents: text})
	}

	return files, nil
}

func (w *worktree) readReleaseNotes(module pure.VersionedModule) (string, error) {
	m := module.Module
	changelog, err := w.eff.readFile(w.pathToChangelog(m))
	if err != nil {
		return "", err
	}
	releaseNotes, err := pure.ComputeReleaseNotes(changelog, module.Version)
	if err != nil {
		return "", fmt.Errorf("%s: %w", m.Changelog(), err)
	}
	return releaseNotes, nil
}

// pathToChangelog points to the module's CHANGELOG.md
func (w *worktree) pathToChangelog(m pure.Module) string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(m.Changelog()))
}

// pathToGoMod points to the module's go.mod
func (w *worktree) pathToGoMod(m pure.Module) string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(m.GoMod()))
}

// pathToVersionFile points to the module's version.go if it exists
func (w *worktree) pathToVersionFile(m pure.Module) string {
	if m.VersionFile == "" {
		return ""
	}
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(m.VersionFile))
}

func (w *worktree) writeReleaseFiles(files []releaseFile) error {
	for _, file := range files {
		err := w.eff.writeFile(file.path, file.contents)
		if err != nil {
			return fmt.Errorf("write %s: %w", file.path, err)
		}
	}
	return nil
}

// Git/GitHub operations

func (w *worktree) commitRelease(modules []pure.VersionedModule) error {
	gitArgs := []string{"commit", "-m", pure.ReleaseCommitMessage(modules, w.eff.today()), "--"}
	for _, module := range modules {
		gitArgs = append(gitArgs, module.Module.ReleaseFiles()...)
	}
	_, err := w.eff.runCommand(w.worktreeRoot, "git", gitArgs...)
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
	if m.GenerateNotes {
		ghArgs = append(ghArgs, "--generate-notes")
	}
	if !m.MarkLatest {
		ghArgs = append(ghArgs, "--latest=false")
	}
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", ghArgs...)
	if err != nil {
		return "", fmt.Errorf("create draft release %s: %w", tag, err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.eff, "Draft release: %s", url)
	return url, nil
}

func (w *worktree) cleanup() error {
	return w.repo.removeWorktree(w.worktreeRoot)
}
