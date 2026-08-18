// Command prepare-release prepares checked-in files for a Go SDK release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// Matches release versions such as "1.48.0".
	versionRE = regexp.MustCompile(`^1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// Matches tagged Go module release versions such as "v1.48.0".
	// Shouldn't match pseudo-versions such as "v1.48.0-0.20260804123456-abcdef123456".
	taggedGoVersionRE = regexp.MustCompile(`^v1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	// Matches changelog headings such as "## [1.48.0] - 2026-08-04".
	changelogHeadingRE = regexp.MustCompile(`^## \[([^]]+)](?:\s+-\s+.*)?\s*$`)
	// Matches changelog section headers such as "### :boom: Breaking Changes".
	changelogHeaderRE = regexp.MustCompile(`^### (.+?)\s*$`)
	// Matches the SDK version declaration: SDKVersion = "1.48.0".
	sdkVersionRE = regexp.MustCompile(`(?m)^(\s*SDKVersion\s*=\s*")[^"]+("\s*)$`)
	// Matches the go.temporal.io/api dependency line in go.mod.
	apiVersionRE = regexp.MustCompile(`(?m)^\s*(?:require\s+)?go\.temporal\.io/api\s+(v\S+)\s*$`)
)

var changelogHeaders = []string{
	"Added",
	"Changed",
	"Deprecated",
	":boom: Breaking Changes",
	"Fixed",
	"Security",
}

var releaseFiles = []string{"CHANGELOG.md", "internal/version.go"}

// COMMAND-LINE WRAPPER

func main() {
	err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr)
		log.Print(err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("prepare-release", flag.ContinueOnError)
	date := flags.String("date", time.Now().Format(time.DateOnly), "release date in YYYY-MM-DD format")
	stopBeforePush := flags.Bool("stop-before-push", false, "stop after committing the release files but before pushing")
	err := flags.Parse(args)
	if err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: prepare-release [--date YYYY-MM-DD] [--stop-before-push] VERSION")
	}
	version, err := validateVersion(flags.Arg(0))
	if err != nil {
		return err
	}
	releaseDate, err := time.Parse(time.DateOnly, *date)
	if err != nil {
		return fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", *date, err)
	}

	err = prepareEverything(RealWorld{}, version, releaseDate, *stopBeforePush)
	return err
}

// CORE LOGIC

func prepareEverything(eff Effects, version string, releaseDate time.Time, stopBeforePush bool) (retErr error) {
	branch := "chore/release-" + version

	worktreeRoot, cleanupWorktree, err := prepareWorktree(eff, branch)
	if err != nil {
		return err
	}
	defer func() {
		if retErr != nil {
			retErr = fmt.Errorf(
				"%w\n\nWorktree preserved at %s.\n"+
					"After inspecting any changes, remove it and its local branch from the repository root with:\n"+
					"  %s\n  %s",
				retErr,
				worktreeRoot,
				formatCommand("git", "worktree", "remove", "--force", worktreeRoot),
				formatCommand("git", "branch", "--delete", "--force", branch),
			)
		}
	}()

	releaseNotes, prURL, err := prepareDraftPR(eff, worktreeRoot, branch, version, releaseDate, stopBeforePush)
	if err != nil {
		return err
	}
	eff.printf("PR: %s\n", prURL)

	draftReleaseURL, err := createDraftRelease(eff, worktreeRoot, version, releaseNotes)
	if err != nil {
		return err
	}
	eff.printf("Draft release: %s\n", draftReleaseURL)

	err = cleanupWorktree()
	if err != nil {
		return err
	}

	return nil
}

func prepareWorktree(eff Effects, branch string) (worktreeRoot string, cleanup func() error, retErr error) {
	root, err := eff.repoRoot()
	if err != nil {
		return "", nil, err
	}

	err = fetchMain(eff, root)
	if err != nil {
		return "", nil, err
	}

	worktreeRoot, cleanupWorktree, err := createWorktree(eff, root, branch)
	if err != nil {
		return "", nil, err
	}
	return worktreeRoot, cleanupWorktree, nil
}

func prepareDraftPR(eff Effects, worktreeRoot, branch, version string, releaseDate time.Time, stopBeforePush bool) (string, string, error) {
	err := validateReleaseFiles(eff, worktreeRoot, version)
	if err != nil {
		return "", "", err
	}

	releaseNotes, err := updateReleaseFiles(eff, worktreeRoot, version, releaseDate)
	if err != nil {
		return "", "", err
	}

	prURL, err := createDraftPR(eff, worktreeRoot, branch, version, stopBeforePush)
	if err != nil {
		return "", "", err
	}

	return releaseNotes, prURL, nil
}

func validateReleaseFiles(eff Effects, worktreeRoot, newVersion string) error {
	goMod, err := eff.readFile(filepath.Join(worktreeRoot, "go.mod"))
	if err != nil {
		return err
	}
	err = validateGoMod(goMod)
	if err != nil {
		return err
	}

	versionGo, err := eff.readFile(filepath.Join(worktreeRoot, "internal", "version.go"))
	if err != nil {
		return err
	}
	currentVersion, err := extractSDKVersion(versionGo)
	if err != nil {
		return err
	}
	err = validateVersionIncrease(currentVersion, newVersion)
	if err != nil {
		return err
	}

	changelog, err := eff.readFile(filepath.Join(worktreeRoot, "CHANGELOG.md"))
	if err != nil {
		return err
	}
	err = validateChangelog(changelog, currentVersion)
	if err != nil {
		return err
	}

	return nil
}

func updateReleaseFiles(eff Effects, worktreeRoot, version string, releaseDate time.Time) (string, error) {
	changelogPath := filepath.Join(worktreeRoot, "CHANGELOG.md")
	versionPath := filepath.Join(worktreeRoot, "internal", "version.go")

	updatedChangelog, err := updateFile(eff, changelogPath, func(text string) (string, error) {
		return updateChangelog(text, version, releaseDate)
	})
	if err != nil {
		return "", err
	}

	releaseNotes, err := prepareReleaseNotes(updatedChangelog, version)
	if err != nil {
		return "", err
	}

	_, err = updateFile(eff, versionPath, func(text string) (string, error) {
		return replaceSDKVersion(text, version)
	})
	if err != nil {
		return "", err
	}

	return releaseNotes, nil
}

func createDraftPR(eff Effects, worktreeRoot, branch, version string, stopBeforePush bool) (string, error) {

	err := commitRelease(eff, worktreeRoot, version)
	if err != nil {
		return "", err
	}
	if stopBeforePush {
		return "", errors.New("stopped before pushing release branch (--stop-before-push)")
	}

	err = pushBranch(eff, worktreeRoot, branch)
	if err != nil {
		return "", err
	}

	prURL, err := openDraftPR(eff, worktreeRoot, branch, version)
	if err != nil {
		return "", err
	}

	return prURL, nil
}

// EFFECTFUL HELPERS

// fetchMain fetches origin/main.
func fetchMain(eff Effects, root string) error {
	_, err := eff.runCommand(root, "git", "fetch", "origin", "main")
	if err != nil {
		return fmt.Errorf("fetch main: %w", err)
	}
	return nil
}

// createWorktree creates a worktree for the given branch and returns its path and a cleanup function.
func createWorktree(eff Effects, root, branch string) (string, func() error, error) {
	worktreeRoot, err := eff.mkdirTemp("", "prepare-go-release-")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary worktree: %w", err)
	}
	_, err = eff.runCommand(root, "git", "worktree", "add", "-b", branch, worktreeRoot, "origin/main")
	if err != nil {
		return "", nil, fmt.Errorf("create worktree: %w", err)
	}
	head, err := eff.runCommand(worktreeRoot, "git", "log", "-1", "--format=%s (%h)")
	if err != nil {
		return "", nil, fmt.Errorf("describe worktree: %w", err)
	}
	eff.printf("Created worktree: %s at HEAD: %s\n", worktreeRoot, strings.TrimSpace(head))
	cleanup := func() error {
		_, err := eff.runCommand(root, "git", "worktree", "remove", "--force", worktreeRoot)
		if err != nil {
			return fmt.Errorf("remove worktree: %w", err)
		}
		eff.printf("Cleaned up worktree.\n")
		return nil
	}
	return worktreeRoot, cleanup, nil
}

// updateFile reads the file at the given path, runs the update function on its contents,
// writes the result to the file, and returns the updated contents.
func updateFile(eff Effects, path string, update func(string) (string, error)) (string, error) {
	data, err := eff.readFile(path)
	if err != nil {
		return "", err
	}
	updated, err := update(data)
	if err != nil {
		return "", err
	}
	err = eff.writeFile(path, updated)
	if err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return updated, nil
}

// commitRelease commits the release files with a message indicating the version.
func commitRelease(eff Effects, root, version string) error {
	args := append([]string{"commit", "-m", "Prepare release " + version, "--"}, releaseFiles...)
	_, err := eff.runCommand(root, "git", args...)
	if err != nil {
		return fmt.Errorf("commit release files: %w", err)
	}
	return nil
}

// pushBranch pushes the branch to origin and sets the upstream.
func pushBranch(eff Effects, root, branch string) error {
	_, err := eff.runCommand(root, "git", "push", "--set-upstream", "origin", branch)
	if err != nil {
		return fmt.Errorf("push branch: %w", err)
	}
	return nil
}

// openDraftPR creates a draft pull request whose HEAD is the given branch.
func openDraftPR(eff Effects, root, branch, version string) (string, error) {
	url, err := eff.runCommand(root, "gh", "pr", "create", "--draft", "--base", "main", "--head", branch,
		"--title", "Prepare release "+version, "--body", "Prepare Go SDK release "+version+".")
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	return strings.TrimSpace(url), nil
}

// createDraftRelease creates a draft release with the given version and release notes.
func createDraftRelease(eff Effects, root, version, releaseNotes string) (string, error) {
	tag := "v" + version
	releaseNotesPath := filepath.Join(root, "prepare-release-notes.md")
	err := eff.writeFile(releaseNotesPath, releaseNotes)
	if err != nil {
		return "", fmt.Errorf("write release notes to %s: %w", releaseNotesPath, err)
	}
	eff.printf("Release notes: %s\n", releaseNotesPath)

	url, err := eff.runCommand(root, "gh", "release", "create", tag, "--draft", "--title", tag,
		"--notes-file", releaseNotesPath, "--generate-notes")
	if err != nil {
		return "", fmt.Errorf("create draft release: %w", err)
	}
	return strings.TrimSpace(url), nil
}
