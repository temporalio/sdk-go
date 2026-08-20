// Command prepare-release prepares PRs and Github Releases for a Go SDK release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"
)

// versionCore matches a three-part semantic version with no leading "v" and no
// prerelease or build suffix.
const versionCore = `(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)`

var (
	// Matches release versions such as "1.48.0" or "0.2.1".
	versionRE = regexp.MustCompile(`^` + versionCore + `$`)
	// Matches tagged Go module release versions such as "v1.48.0".
	// Shouldn't match pseudo-versions such as "v1.48.0-0.20260804123456-abcdef123456".
	goVersionRE = regexp.MustCompile(`^v` + versionCore + `$`)
	// Matches contrib module directories such as "contrib/envconfig" and
	// "contrib/aws/s3driver/awssdkv2".
	contribModuleRE = regexp.MustCompile(`^contrib(/[a-z0-9][a-z0-9._-]*)+$`)
	// Matches changelog headings such as "## [1.48.0] - 2026-08-04".
	changelogHeadingRE = regexp.MustCompile(`^## \[([^]]+)](?:\s+-\s+.*)?\s*$`)
	// Matches changelog section headers such as "### :boom: Breaking Changes".
	changelogHeaderRE = regexp.MustCompile(`^### (.+?)\s*$`)
	// Matches the SDK version declaration: SDKVersion = "1.48.0".
	sdkVersionRE = regexp.MustCompile(`(?m)^(\s*SDKVersion\s*=\s*")[^"]+("\s*)$`)
	// Matches the module declaration in a go.mod.
	moduleDeclarationRE = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)
)

var changelogHeaders = []string{
	"Added",
	"Changed",
	"Deprecated",
	":boom: Breaking Changes",
	"Fixed",
	"Security",
}

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
	commandArgs, err := parseArgs(args)
	if err != nil {
		return err
	}
	return prepareEverything(RealWorld{}, commandArgs)
}

// commandArgs is one parsed command line: which module to release, and how.
type commandArgs struct {
	target         releaseTarget
	version        string
	releaseDate    time.Time
	stopBeforePush bool
}

func parseArgs(args []string) (commandArgs, error) {
	var commandArgs commandArgs

	// Parse flags
	flags := flag.NewFlagSet("prepare-release", flag.ContinueOnError)
	date := flags.String("date", time.Now().Format(time.DateOnly), "release date in YYYY-MM-DD format")
	stopBeforePush := flags.Bool("stop-before-push", false, "stop after committing the release files but before pushing")
	err := flags.Parse(args)
	if err != nil {
		return commandArgs, err
	}
	commandArgs.releaseDate, err = time.Parse(time.DateOnly, *date)
	if err != nil {
		return commandArgs, fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", *date, err)
	}
	commandArgs.stopBeforePush = *stopBeforePush

	// Parse positional arguments
	if flags.NArg() < 1 || flags.NArg() > 2 {
		return commandArgs, errors.New("usage: prepare-release [--date YYYY-MM-DD] [--stop-before-push] [MODULE] VERSION\n" +
			"optional argument MODULE is a contrib module directory like 'contrib/envconfig'; omit it to release the Go SDK")
	}
	if flags.NArg() == 1 {
		commandArgs.target = sdkTarget()
		commandArgs.version, err = validateVersion(flags.Arg(1))
		if err != nil {
			return commandArgs, err
		}
	}
	if flags.NArg() == 2 {
		commandArgs.target, err = contribTarget(flags.Arg(0))
		if err != nil {
			return commandArgs, err
		}
		commandArgs.version, err = validateVersion(flags.Arg(2))
		if err != nil {
			return commandArgs, err
		}
	}

	return commandArgs, nil
}

// CORE LOGIC

func prepareEverything(eff Effects, args commandArgs) (retErr error) {
	branch := args.target.releaseBranch(args.version)

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

	releaseNotes, prURL, err := prepareDraftPR(eff, args, worktreeRoot, branch)
	if err != nil {
		return err
	}
	eff.printf("PR: %s\n", prURL)

	draftReleaseURL, err := createDraftRelease(eff, args, worktreeRoot, releaseNotes)
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

func prepareDraftPR(eff Effects, args commandArgs, worktreeRoot, branch string) (string, string, error) {
	err := validateRelease(eff, args, worktreeRoot)
	if err != nil {
		return "", "", err
	}

	releaseNotes, err := updateReleaseFiles(eff, args, worktreeRoot)
	if err != nil {
		return "", "", err
	}

	prURL, err := createDraftPR(eff, args, worktreeRoot, branch)
	if err != nil {
		return "", "", err
	}

	return releaseNotes, prURL, nil
}

// validateRelease confirms the module exists, depends on released Temporal code, has
// not already been released at newVersion, and has a changelog ready to cut.
func validateRelease(eff Effects, args commandArgs, worktreeRoot string) error {
	target := args.target
	newVersion := args.version
	goMod, err := eff.readFile(target.filePath(worktreeRoot, target.goModFile()))
	if err != nil {
		return fmt.Errorf("%s is not a Go module in this repository: %w", target.name(), err)
	}
	err = validateModulePath(goMod, target.modulePath)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name(), err)
	}
	err = validateDependency(goMod, target.dependency)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name(), err)
	}

	tags, err := listTags(eff, worktreeRoot, target.tagPattern())
	if err != nil {
		return err
	}
	newTag := target.tag(newVersion)
	if contains(tags, newTag) {
		return fmt.Errorf("%s %s has already been released; tag %s exists", target.name(), newVersion, newTag)
	}

	currentVersion, err := currentReleasedVersion(eff, args, worktreeRoot, tags)
	if err != nil {
		return err
	}
	err = validateVersionIncrease(currentVersion, newVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name(), err)
	}

	changelog, err := eff.readFile(target.filePath(worktreeRoot, target.changelogFile()))
	if err != nil {
		return fmt.Errorf("%s has no changelog to release: %w", target.name(), err)
	}
	err = validateChangelog(changelog, currentVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", target.name(), err)
	}

	if currentVersion == "" {
		eff.printf("Preparing the first release of %s: %s (tag %s).\n", target.name(), newVersion, newTag)
	} else {
		eff.printf("Preparing %s %s, following %s (tag %s).\n", target.name(), newVersion, currentVersion, newTag)
	}
	return nil
}

// currentReleasedVersion returns the module's current version, or "" if it has never
// been released. Modules that embed their version declare it; the rest are versioned
// only by their Go module tags.
func currentReleasedVersion(eff Effects, args commandArgs, worktreeRoot string, tags []string) (string, error) {
	target := args.target
	if target.versionFile == "" {
		return latestReleasedVersion(tags, target.tagPrefix), nil
	}
	versionGo, err := eff.readFile(target.filePath(worktreeRoot, target.versionFile))
	if err != nil {
		return "", err
	}
	return extractSDKVersion(versionGo)
}

func updateReleaseFiles(eff Effects, args commandArgs, worktreeRoot string) (string, error) {
	target := args.target
	version := args.version
	updatedChangelog, err := updateFile(eff, target.filePath(worktreeRoot, target.changelogFile()), func(text string) (string, error) {
		return updateChangelog(text, version, args.releaseDate, target.seededHeaders)
	})
	if err != nil {
		return "", err
	}

	releaseNotes, err := prepareReleaseNotes(updatedChangelog, version)
	if err != nil {
		return "", err
	}

	if target.versionFile != "" {
		_, err = updateFile(eff, target.filePath(worktreeRoot, target.versionFile), func(text string) (string, error) {
			return replaceSDKVersion(text, version)
		})
		if err != nil {
			return "", err
		}
	}

	return releaseNotes, nil
}

func createDraftPR(eff Effects, args commandArgs, worktreeRoot, branch string) (string, error) {

	err := commitRelease(eff, args, worktreeRoot)
	if err != nil {
		return "", err
	}
	if args.stopBeforePush {
		return "", errors.New("stopped before pushing release branch (--stop-before-push)")
	}

	err = pushBranch(eff, worktreeRoot, branch)
	if err != nil {
		return "", err
	}

	prURL, err := openDraftPR(eff, args, worktreeRoot, branch)
	if err != nil {
		return "", err
	}

	return prURL, nil
}

// EFFECTFUL HELPERS

// fetchMain fetches origin/main along with the tags that version every module.
func fetchMain(eff Effects, root string) error {
	_, err := eff.runCommand(root, "git", "fetch", "--tags", "origin", "main")
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

// listTags returns the repository's tags matching the given glob pattern.
func listTags(eff Effects, root, pattern string) ([]string, error) {
	output, err := eff.runCommand(root, "git", "tag", "--list", pattern)
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

// commitRelease commits the release files with a message indicating the module and version.
func commitRelease(eff Effects, commandArgs commandArgs, root string) error {
	target := commandArgs.target
	gitArgs := append([]string{"commit", "-m", "Prepare " + target.releaseSubject(commandArgs.version), "--"}, target.releaseFiles()...)
	_, err := eff.runCommand(root, "git", gitArgs...)
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
func openDraftPR(eff Effects, args commandArgs, root, branch string) (string, error) {
	target := args.target
	url, err := eff.runCommand(root, "gh", "pr", "create", "--draft", "--base", "main", "--head", branch,
		"--title", "Prepare "+target.releaseSubject(args.version),
		"--body", "Prepare "+target.modulePath+" release "+args.version+".")
	if err != nil {
		return "", fmt.Errorf("create draft PR: %w", err)
	}
	return strings.TrimSpace(url), nil
}

// createDraftRelease creates a draft release for the module's Go tag. Publishing the
// draft is what creates the tag, so the release PR must be merged first.
func createDraftRelease(eff Effects, commandArgs commandArgs, root, releaseNotes string) (string, error) {
	target := commandArgs.target
	tag := target.tag(commandArgs.version)
	ghArgs := []string{"release", "create", tag, "--draft", "--title", tag, "--notes", releaseNotes}
	if target.generateNotes {
		ghArgs = append(ghArgs, "--generate-notes")
	}
	if !target.markLatest {
		// Only the main SDK module may own GitHub's "Latest" badge.
		ghArgs = append(ghArgs, "--latest=false")
	}
	url, err := eff.runCommand(root, "gh", ghArgs...)
	if err != nil {
		return "", fmt.Errorf("create draft release: %w", err)
	}
	return strings.TrimSpace(url), nil
}
