// Command yeet prepares PRs and Github Releases for a Go SDK release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const usage = "usage: yeet lint [COMMIT]\n" +
	"       yeet pr [--release-date YYYY-MM-DD] MODULE=VERSION [MODULE=VERSION ...]\n" +
	"       yeet release COMMIT MODULE=VERSION [MODULE=VERSION ...]\n" +
	"MODULE is '.' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"

type app struct {
	out         io.Writer
	eff         effects
	repoRoot    string
	command     string
	releases    []releaseSpec
	commit      string
	releaseDate time.Time
}

// COMMAND-LINE WRAPPER

func main() {
	log.SetFlags(0) // No need to log the time here
	err := run(os.Stdout, os.Stderr, os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr)
		log.Print(err)
		os.Exit(1)
	}
}

func run(out, errOut io.Writer, args []string) error {
	a, err := newApp(out, errOut, args)
	if err != nil {
		return err
	}
	if a.command == "lint" {
		return a.lint()
	}
	if a.command == "pr" {
		return a.preparePR()
	}
	return a.prepareRelease()
}

func newApp(out, errOut io.Writer, args []string) (*app, error) {
	if len(args) == 0 {
		return nil, errors.New(usage)
	}
	command := args[0]
	if command == "-h" || command == "--help" {
		printLine(errOut, "%s", usage)
		return nil, flag.ErrHelp
	}
	if command != "lint" && command != "pr" && command != "release" {
		return nil, fmt.Errorf("unknown command %q\n%s", command, usage)
	}

	// Declare flags
	flags := flag.NewFlagSet("yeet "+command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() {
		printLine(flags.Output(), "%s", usage)
		flags.PrintDefaults()
	}
	releaseDate := time.Now().Format(time.DateOnly)
	if command == "pr" {
		flags.StringVar(
			&releaseDate,
			"release-date",
			releaseDate,
			"release date in YYYY-MM-DD format",
		)
	}
	err := flags.Parse(args[1:])
	if err != nil {
		return nil, err
	}

	// Store flags and positional args
	a := &app{out: out, eff: realWorld{out: out}, command: command}
	if command == "pr" || command == "lint" {
		a.releaseDate, err = time.Parse(time.DateOnly, releaseDate)
		if err != nil {
			return nil, fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", releaseDate, err)
		}
	}

	positional := flags.Args()
	if command == "lint" {
		if len(positional) > 1 {
			return nil, errors.New(usage)
		}
		if len(positional) == 1 {
			a.commit = positional[0]
		}
	} else {
		if command == "release" {
			if len(positional) < 2 {
				return nil, errors.New(usage)
			}
			a.commit = positional[0]
			positional = positional[1:]
		}
		if len(positional) == 0 {
			return nil, errors.New(usage)
		}
		a.releases, err = parseReleaseSpecs(positional)
		if err != nil {
			return nil, err
		}
	}

	a.repoRoot, err = repoRoot()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CORE LOGIC

func (app *app) preparePR() (retErr error) {
	printLine(app.out, "Preparing %s\n", formatReleaseSpecs(app.releases))

	printLine(app.out, "[1/5] Fetch main")
	err := app.fetchMain()
	if err != nil {
		return err
	}

	printLine(app.out, "[2/5] Create release worktree")
	branchName := releaseBranchName(app.releaseDate)
	worktree, err := app.createWorktree(branchName, "origin/main")
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
				worktree.worktreeRoot,
				formatCommand("git", "worktree", "remove", "--force", worktree.worktreeRoot),
				formatCommand("git", "branch", "--delete", "--force", worktree.branch),
			)
		}
	}()
	var files []releaseFile
	for _, release := range app.releases {
		err = worktree.validatePreparation(release, "pr")
		if err != nil {
			return err
		}
		updated, err := worktree.computeReleaseFiles(release)
		if err != nil {
			return err
		}
		files = append(files, updated...)
	}

	printLine(app.out, "[3/5] Commit release files")
	err = worktree.writeReleaseFiles(files)
	if err != nil {
		return err
	}
	err = worktree.commitRelease(app.releases)
	if err != nil {
		return err
	}

	printLine(app.out, "[4/5] Publish draft PR")
	err = worktree.pushBranch()
	if err != nil {
		return err
	}
	prURL, err := worktree.openDraftPR(app.releases)
	if err != nil {
		return err
	}

	printLine(app.out, "[5/5] Clean up release worktree")
	err = worktree.cleanup()
	if err != nil {
		return err
	}
	printDetail(app.out, "Done.")

	printLine(app.out, "\nAfter merging the PR, create draft releases with:")
	printLine(app.out, "  yeet release <MERGE_COMMIT> %s", formatReleaseSpecs(app.releases))
	printLine(app.out, "\nTo close the PR and delete the branch:")
	printLine(app.out, "  %s", formatCommand("gh", "pr", "close", prURL, "--delete-branch"))

	return nil
}

func (app *app) prepareRelease() (retErr error) {
	releaseCount := 0
	defer func() {
		if releaseCount == 0 {
			return
		}
		printLine(app.out, "\nTo roll back these releases, run:")
		for _, release := range app.releases[:releaseCount] {
			printLine(app.out, "  %s", formatCommand("gh", "release", "delete", release.target.tag(release.version), "--yes"))
		}
	}()

	printLine(app.out, "Creating draft releases for %s\n", formatReleaseSpecs(app.releases))

	printLine(app.out, "[1/4] Fetch main")
	err := app.fetchMain()
	if err != nil {
		return err
	}

	printLine(app.out, "[2/4] Check out release commit")
	commit, err := app.resolveReleaseCommit()
	if err != nil {
		return err
	}
	worktree, err := app.createWorktree("", commit)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, worktree.cleanup())
	}()

	printLine(app.out, "[3/4] Validate release files")
	releaseNotes := make([]string, len(app.releases))
	for i, release := range app.releases {
		err = worktree.validateEverything(release, "release")
		if err != nil {
			return err
		}
		releaseNotes[i], err = worktree.readReleaseNotes(release)
		if err != nil {
			return err
		}
	}

	printLine(app.out, "[4/4] Create draft releases")
	for i, release := range app.releases {
		_, err = worktree.createDraftRelease(release, commit, releaseNotes[i])
		if err != nil {
			return err
		}
		releaseCount++
	}
	return nil
}

func (app *app) lint() (retErr error) {
	printLine(app.out, "Checking release readiness\n")

	printLine(app.out, "[1/3] Fetch main")
	err := app.fetchMain()
	if err != nil {
		return err
	}

	printLine(app.out, "[2/3] Inspect checkout")
	var worktree *worktree
	if app.commit == "" {
		worktree = app.worktreeAt(app.repoRoot, "")
		printDetail(app.out, "Worktree: %s (including local edits)", app.repoRoot)
	} else {
		commit, err := app.resolveCommit()
		if err != nil {
			return err
		}
		worktree, err = app.createWorktree("", commit)
		if err != nil {
			return err
		}
		defer func() {
			retErr = errors.Join(retErr, worktree.cleanup())
		}()
	}
	candidates, err := worktree.changedModules()
	if err != nil {
		return err
	}

	printLine(app.out, "[3/3] Lint changed modules")
	if len(candidates) == 0 {
		printDetail(app.out, "No changed modules.")
		return nil
	}
	var proposed []releaseSpec
	var problems []error
	for _, candidate := range candidates {
		release, command, err := worktree.lintRelease(candidate)
		if command == "pr" {
			proposed = append(proposed, release)
		}
		if err != nil {
			printDetail(app.out, "%s: blocked for %s", formatReleaseSpecs([]releaseSpec{release}), command)
			problems = append(problems, err)
			continue
		}
		printDetail(app.out, "%s: ready for %s", formatReleaseSpecs([]releaseSpec{release}), command)
	}
	if len(proposed) > 0 {
		printLine(app.out, "\nSuggested PR command:")
		printLine(app.out, "  yeet pr %s", formatReleaseSpecs(proposed))
	}
	return errors.Join(problems...)
}

func (app *app) fetchMain() error {
	_, err := app.eff.runCommand(app.repoRoot, "git", "fetch", "--tags", "origin", "main")
	if err != nil {
		return fmt.Errorf("fetch main: %w", err)
	}
	return nil
}

func (app *app) resolveReleaseCommit() (string, error) {
	commit, err := app.resolveCommit()
	if err != nil {
		return "", err
	}
	_, err = app.eff.runCommand(app.repoRoot, "git", "merge-base", "--is-ancestor", commit, "origin/main")
	if err != nil {
		return "", fmt.Errorf("release commit %s must be reachable from origin/main: %w", commit, err)
	}
	return commit, nil
}

func (app *app) resolveCommit() (string, error) {
	commit, err := app.eff.runCommand(app.repoRoot, "git", "rev-parse", "--verify", "--end-of-options", app.commit+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve commit %q: %w", app.commit, err)
	}
	return strings.TrimSpace(commit), nil
}

func (app *app) createWorktree(branch, commit string) (*worktree, error) {
	root, err := app.eff.mkdirTemp("", "prepare-go-release-")
	if err != nil {
		return nil, fmt.Errorf("create temporary worktree: %w", err)
	}
	gitArgs := []string{"worktree", "add"}
	if branch == "" {
		gitArgs = append(gitArgs, "--detach")
	} else {
		gitArgs = append(gitArgs, "-b", branch)
	}
	gitArgs = append(gitArgs, root, commit)
	_, err = app.eff.runCommand(app.repoRoot, "git", gitArgs...)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	head, err := app.eff.runCommand(root, "git", "log", "-1", "--format=%s (%h)")
	if err != nil {
		if branch == "" {
			err = errors.Join(err, app.worktreeAt(root, branch).cleanup())
		}
		return nil, fmt.Errorf("describe worktree: %w", err)
	}
	printDetail(app.out, "Worktree: %s", root)
	printDetail(app.out, "HEAD: %s", strings.TrimSpace(head))
	return app.worktreeAt(root, branch), nil
}

func (app *app) worktreeAt(root, branch string) *worktree {
	return &worktree{
		out:          app.out,
		eff:          app.eff,
		releaseDate:  app.releaseDate,
		repoRoot:     app.repoRoot,
		worktreeRoot: root,
		branch:       branch,
	}
}

// printLine writes a line of progress.
func printLine(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format+"\n", args...)
}

// printDetail writes an indented line of progress output.
func printDetail(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, "      "+format+"\n", args...)
}

// repoRoot locates the git repo that contains this script.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate yeet source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}
