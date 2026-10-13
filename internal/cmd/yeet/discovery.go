package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// releaseCandidate is a changed module and its latest stable release version.
// A nil oldVersion means the module has no stable release tags.
type releaseCandidate struct {
	target     releaseTarget
	oldVersion *semver
}

// changedModules finds changed SDK and contrib modules using locally available tags.
// It inspects the working tree, including staged additions and tracked local edits.
// Missing changelogs do not exclude modules; readiness is checked separately.
func (w *worktree) changedModules() ([]releaseCandidate, error) {
	dirs, err := w.listModuleDirs()
	if err != nil {
		return nil, err
	}
	tags, err := w.listTags("*")
	if err != nil {
		return nil, err
	}

	var candidates []releaseCandidate
	for _, dir := range dirs {
		var target releaseTarget
		if dir == "" {
			target = sdkTarget()
		} else {
			if !strings.HasPrefix(dir, "contrib/") {
				continue
			}
			target, err = contribTarget(dir)
			if err != nil {
				return nil, err
			}
		}
		oldVersion := latestReleasedVersion(tags, target.tagPrefix())
		changed, err := w.moduleChanged(target, oldVersion, dirs)
		if err != nil {
			return nil, err
		}
		if changed {
			candidates = append(candidates, releaseCandidate{target: target, oldVersion: oldVersion})
		}
	}
	return candidates, nil
}

// listModuleDirs lists tracked module roots, including non-release modules so their
// files can be excluded from parent modules. The SDK root is the empty string.
func (w *worktree) listModuleDirs() ([]string, error) {
	output, err := w.eff.runCommand(w.worktreeRoot, "git", "ls-files", "-z", "--", "go.mod", "**/go.mod")
	if err != nil {
		return nil, fmt.Errorf("list module directories: %w", err)
	}
	var dirs []string
	for _, file := range strings.Split(output, "\x00") {
		if file == "" {
			continue
		}
		dir := path.Dir(file)
		if dir == "." {
			dir = ""
		}
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	return slices.Compact(dirs), nil
}

func (w *worktree) moduleChanged(target releaseTarget, oldVersion *semver, moduleDirs []string) (bool, error) {
	if oldVersion == nil {
		return true, nil
	}
	dir := target.dir
	if dir == "" {
		dir = "."
	}
	// Treat renames as a deletion and an addition so moves across module boundaries
	// count as changes to both modules.
	args := []string{"diff", "--name-only", "--no-renames", "-z", target.tag(*oldVersion), "--", dir}
	for _, nested := range moduleDirs {
		if nested != target.dir && (target.dir == "" || strings.HasPrefix(nested, target.dir+"/")) {
			args = append(args, ":(exclude,literal)"+nested)
		}
	}
	output, err := w.eff.runCommand(w.worktreeRoot, "git", args...)
	if err != nil {
		return false, fmt.Errorf("check %s for changes since %s: %w", target.modulePath(), target.tag(*oldVersion), err)
	}
	return output != "", nil
}
