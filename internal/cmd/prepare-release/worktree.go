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
	// releaseDate is the date the new release took place on.
	releaseDate time.Time
	// repoRoot is the root of the user's own checkout, which owns this worktree.
	repoRoot string
	// worktreeRoot is the worktreeRoot directory of this worktree.
	worktreeRoot string
	// branch is the git branch that this worktree has checked out.
	branch string
}

// validateEverything validates everything about the module release before
// anything gets written. The command is "pr" or "release". For "release", the version
// constant and changelog must match the requested release instead of the latest tag:
//   - check that go.mod exists and has the correct module path;
//   - check that all Temporal dependencies in go.mod point to published releases;
//   - check that the version constant, if the module has one, agrees with the tags
//     (or the requested version for a prepared release);
//   - check that the new version is "one greater" than the current release;
//   - check that the changelog exists and has the required sections.
func (w *worktree) validateEverything(release releaseSpec, command string) error {
	target := release.target
	modulePath := target.modulePath()

	// Validate go.mod
	goMod, err := w.eff.readFile(w.pathToGoMod(target))
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	requirements, err := validateGoMod(goMod, modulePath)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	err = w.validateRequirementsPublished(requirements)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}

	// Validate version number
	tags, err := w.listTags(target.tagPattern())
	if err != nil {
		return err
	}
	oldVersion := latestReleasedVersion(tags, target.tagPrefix())
	expectedVersion := oldVersion
	if command == "release" && oldVersion != nil {
		expectedVersion = &release.version
	}
	err = w.validateVersionConstant(target, expectedVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	if oldVersion != nil {
		err = validateVersionIncrease(*oldVersion, release.version)
		if err != nil {
			return fmt.Errorf("%s: %w", modulePath, err)
		}
	}

	// Validate changelog
	changelog, err := w.eff.readFile(w.pathToChangelog(target))
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	if command == "release" {
		expectedVersion = &release.version
	}
	err = validateChangelog(changelog, expectedVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}

	return nil
}

func (w *worktree) validateRequirementsPublished(requirements []moduleRequirement) error {
	for _, requirement := range requirements {
		published, err := w.eff.checkModulePublished(requirement.modulePath, requirement.version)
		if err != nil {
			return err
		}
		if !published {
			return fmt.Errorf("%s %s is not published on the Go module proxy",
				requirement.modulePath, requirement.version)
		}
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
func (w *worktree) validateVersionConstant(target releaseTarget, expectedVersion *semver) error {
	if target.versionFile == "" {
		return nil
	}
	text, err := w.eff.readFile(w.pathToVersionFile(target))
	if err != nil {
		return err
	}
	declared, err := extractSDKVersion(text)
	if err != nil {
		return err
	}
	if expectedVersion == nil {
		return fmt.Errorf("%s declares version %s, but no release tags match %q",
			target.versionFile, declared, target.tagPattern())
	}
	if declared != *expectedVersion {
		return fmt.Errorf("%s declares version %s, expected %s",
			target.versionFile, declared, *expectedVersion)
	}
	return nil
}

// updateReleaseFiles updates the changelog and version.go (if the module has one).
func (w *worktree) updateReleaseFiles(release releaseSpec) error {
	_, err := w.updateFile(w.pathToChangelog(release.target), func(text string) (string, error) {
		return computeNewChangelog(text, release.version, w.releaseDate)
	})
	if err != nil {
		return err
	}

	versionFile := w.pathToVersionFile(release.target)
	if versionFile != "" {
		_, err = w.updateFile(versionFile, func(text string) (string, error) {
			return computeNewVersionFile(text, release.version)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *worktree) commitRelease(release releaseSpec) error {
	target := release.target
	gitArgs := append([]string{"commit", "-m", target.releaseCommitMessage(release.version), "--"}, target.releaseFiles()...)
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

func (w *worktree) openDraftPR(release releaseSpec) (string, error) {
	url, err := w.eff.runCommand(w.worktreeRoot, "gh", "pr", "create", "--draft",
		"--base", "main",
		"--head", w.branch,
		"--label", "skip-changelog",
		"--title", release.target.releaseCommitMessage(release.version),
		"--body", release.target.releaseCommitMessage(release.version))
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	url = strings.TrimSpace(url)
	printDetail(w.out, "Draft PR: %s", url)
	return url, nil
}

func (w *worktree) createDraftRelease(release releaseSpec, commit string) (string, error) {
	target := release.target
	tag := target.tag(release.version)
	changelog, err := w.eff.readFile(w.pathToChangelog(target))
	if err != nil {
		return "", err
	}
	releaseNotes, err := computeReleaseNotes(changelog, release.version)
	if err != nil {
		return "", fmt.Errorf("%s: %w", target.changelog(), err)
	}
	releaseNotesPath := filepath.Join(w.worktreeRoot, "prepare-release-notes.md")
	err = w.eff.writeFile(releaseNotesPath, releaseNotes)
	if err != nil {
		return "", fmt.Errorf("write release notes to %s: %w", releaseNotesPath, err)
	}
	printDetail(w.out, "Release notes: %s", releaseNotesPath)

	ghArgs := []string{"release", "create", tag, "--draft", "--target", commit, "--title", tag, "--notes-file", releaseNotesPath}
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
func (w *worktree) pathToChangelog(target releaseTarget) string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(target.changelog()))
}

// pathToGoMod points to the release target's go.mod
func (w *worktree) pathToGoMod(target releaseTarget) string {
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(target.goMod()))
}

// pathToVersionFile points to the release target's version.go if it exists
func (w *worktree) pathToVersionFile(target releaseTarget) string {
	if target.versionFile == "" {
		return ""
	}
	return filepath.Join(w.worktreeRoot, filepath.FromSlash(target.versionFile))
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
