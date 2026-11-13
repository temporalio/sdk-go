package main

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

const contribSummaryHeading = "### Contrib packages with changes since their last release:"

const contribSummaryNote = "**NOTE:** Dependency-only changes (go.mod and go.sum) are excluded. These updates may still require a contrib release."

func (w *worktree) draftPRBody() (string, error) {
	body := w.target.releaseCommitMessage(w.version)
	if w.target.dir != "" {
		return body, nil
	}
	summary, err := w.contribSummary("HEAD")
	if err != nil {
		return "", err
	}
	return body + "\n\n" + contribSummaryHeading + "\n\n" +
		contribSummaryNote + "\n\n" +
		"Consider releasing these packages after this Go SDK release.\n\n" + summary, nil
}

func (a *app) checkContrib() error {
	if err := a.fetchMain(); err != nil {
		return err
	}
	summary, err := a.worktreeAt(a.repoRoot, "").contribSummary("origin/main")
	if err != nil {
		return err
	}
	printLine(a.out, "%s\n\n%s\n\n%s", contribSummaryHeading, contribSummaryNote, summary)
	return nil
}

func (w *worktree) contribSummary(head string) (string, error) {
	tags, err := w.listTags("contrib/*")
	if err != nil {
		return "", err
	}
	output, err := w.eff.runCommand(w.worktreeRoot, "git", "ls-tree", "-r", "--name-only", "-z", head, "--", "contrib/")
	if err != nil {
		return "", fmt.Errorf("list contrib module files at %s: %w", head, err)
	}
	files := make(map[string]bool)
	for _, file := range strings.Split(output, "\x00") {
		files[file] = true
	}
	var modules []string
	for file := range files {
		if path.Base(file) != "go.mod" {
			continue
		}
		dir := path.Dir(file)
		if _, err := contribTarget(dir); err == nil && files[dir+"/CHANGELOG.md"] {
			modules = append(modules, dir)
		}
	}
	slices.Sort(modules)

	var summary strings.Builder
	for _, dir := range modules {
		ref, baseline := head, "Never released"
		if version := latestReleasedVersion(tags, dir+"/"); version != nil {
			tag := dir + "/v" + version.String()
			ref, baseline = tag+".."+head, "since `"+tag+"`"
		}
		args := []string{"log", "--full-history", "--topo-order", "--no-renames", "--format=%H%x09%s", ref, "--", dir + "/"}
		for _, file := range []string{"go.mod", "go.sum"} {
			args = append(args, ":(exclude,glob)"+dir+"/**/"+file)
		}
		for _, nested := range modules {
			if strings.HasPrefix(nested, dir+"/") {
				args = append(args, ":(exclude)"+nested+"/")
			}
		}
		output, err := w.eff.runCommand(w.worktreeRoot, "git", args...)
		if err != nil {
			return "", fmt.Errorf("list commits for %s (%s): %w", dir, ref, err)
		}
		if strings.TrimSpace(output) == "" {
			continue
		}
		fmt.Fprintf(&summary, "- `%s` (%s)\n", dir, baseline)
		for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
			sha, subject, ok := strings.Cut(line, "\t")
			if !ok || len(sha) < 7 {
				return "", fmt.Errorf("invalid commit for %s: %q", dir, line)
			}
			fmt.Fprintf(&summary, "  - [%s](https://github.com/temporalio/sdk-go/commit/%s) %s\n", sha[:7], sha, subject)
		}
	}
	if summary.Len() == 0 {
		summary.WriteString("None.\n")
	}
	return summary.String(), nil
}
