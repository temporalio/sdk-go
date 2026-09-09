package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// worktree represents a temporary git worktree that the script creates to validate
// and update release files.
type worktree struct {
	out io.Writer
	eff effects
	// target represents the module we're releasing.
	target releaseTarget
	// version is the version of the module that the user wants to release.
	version semver
	// releaseDate is the date the new release took place on.
	releaseDate time.Time
	// repoRoot is the root of the user's own checkout, which owns this worktree.
	repoRoot string
	// worktreeRoot is the worktreeRoot directory of this worktree.
	worktreeRoot string
	// branch is the git branch that this worktree has checked out.
	branch string
}

// validateEverything validates everything about the release worktree before
// anything gets written:
// - check that go.mod exists and has the correct module path;
// - check that all Temporal dependencies in go.mod point to official releases;
// - check that the version constant, if the module has one, agrees with the tags;
// - check that the new version is "one greater" than the current release;
// - check that the changelog exists and is ready to cut.
func (w *worktree) validateEverything() error {
	modulePath := w.target.modulePath()

	// Validate go.mod
	goMod, err := w.eff.readFile(w.pathToGoMod())
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	err = validateGoMod(goMod, modulePath)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}

	// Validate version number
	tags, err := w.listTags(w.target.tagPattern())
	if err != nil {
		return err
	}
	oldVersion := latestReleasedVersion(tags, w.target.tagPrefix())
	err = w.validateVersionConstant(oldVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	if oldVersion != nil {
		err = validateVersionIncrease(*oldVersion, w.version)
		if err != nil {
			return fmt.Errorf("%s: %w", modulePath, err)
		}
	}

	// Validate changelog
	changelog, err := w.eff.readFile(w.pathToChangelog())
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	err = validateChangelog(changelog, oldVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}

	return nil
}

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

// validateVersionConstant checks that the version in version.go
// (if it exists) matches the expected version.
func (w *worktree) validateVersionConstant(expectedVersion *semver) error {
	if w.target.versionFile == "" {
		return nil
	}
	text, err := w.eff.readFile(w.pathToVersionFile())
	if err != nil {
		return err
	}
	declared, err := extractSDKVersion(text)
	if err != nil {
		return err
	}
	if expectedVersion == nil {
		return fmt.Errorf("%s declares version %s, but no release tags match %q",
			w.target.versionFile, declared, w.target.tagPattern())
	}
	if declared != *expectedVersion {
		return fmt.Errorf("%s declares version %s, but the latest release tag is %s",
			w.target.versionFile, declared, w.target.tag(*expectedVersion))
	}
	return nil
}

// updateReleaseFiles updates the changelog, computes release notes, and
// updates version.go (if the module has one). Returns the release notes.
func (w *worktree) updateReleaseFiles() (string, error) {
	updatedChangelog, err := w.updateFile(w.pathToChangelog(), func(text string) (string, error) {
		return computeNewChangelog(text, w.version, w.releaseDate)
	})
	if err != nil {
		return "", err
	}

	releaseNotes, err := computeReleaseNotes(updatedChangelog, w.version)
	if err != nil {
		return "", err
	}

	versionFile := w.pathToVersionFile()
	if versionFile != "" {
		_, err = w.updateFile(versionFile, func(text string) (string, error) {
			return computeNewVersionFile(text, w.version)
		})
		if err != nil {
			return "", err
		}
	}

	return releaseNotes, nil
}

func (w *worktree) commitRelease() error {
	target := w.target
	gitArgs := append([]string{"commit", "-m", target.releaseCommitMessage(w.version), "--"}, target.releaseFiles()...)
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

func (w *worktree) openDraftPR() (string, error) {
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", "pr", "create", "--draft",
		"--base", "main",
		"--head", w.branch,
		"--label", "skip-changelog",
		"--title", w.target.releaseCommitMessage(w.version),
		"--body", w.target.releaseCommitMessage(w.version))
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.out, "Draft PR: %s", url)
	return url, nil
}

func (w *worktree) createDraftRelease(releaseNotes string) (string, error) {
	target := w.target
	tag := target.tag(w.version)
	releaseNotesPath := filepath.Join(w.worktreeRoot, "prepare-release-notes.md")
	err := w.eff.writeFile(releaseNotesPath, releaseNotes)
	if err != nil {
		return "", fmt.Errorf("write release notes to %s: %w", releaseNotesPath, err)
	}
	printDetail(w.out, "Release notes: %s", releaseNotesPath)

	ghArgs := []string{"release", "create", tag, "--draft", "--title", tag, "--notes-file", releaseNotesPath}
	if target.generateNotes {
		ghArgs = append(ghArgs, "--generate-notes")
	}
	if !target.markLatest {
		ghArgs = append(ghArgs, "--latest=false")
	}
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", ghArgs...)
	if err != nil {
		return "", fmt.Errorf("create draft release: %w", err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.out, "Draft release: %s", url)
	return url, nil
}

func (w *worktree) cleanup() error {
	_, err := w.eff.runCommand(w.repoRoot, "git", "worktree", "remove", "--force", w.worktreeRoot)
	if err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}

// pathToChangelog points to the release target's CHANGELOG.md
func (w *worktree) pathToChangelog() string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(w.target.changelog()))
}

// pathToGoMod points to the release target's go.mod
func (w *worktree) pathToGoMod() string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(w.target.goMod()))
}

// pathToVersionFile points to the release target's version.go if it exists
func (w *worktree) pathToVersionFile() string {
	if w.target.versionFile == "" {
		return ""
	}
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(w.target.versionFile))
}

func (w *worktree) updateFile(path string, update func(string) (string, error)) (string, error) {
	data, err := w.eff.readFile(path)
	if err != nil {
		return "", err
	}
	updated, err := update(data)
	if err != nil {
		return "", err
	}
	err = w.eff.writeFile(path, updated)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return updated, nil
}
