// Command prepare-release prepares PRs and Github Releases for a Go SDK release.
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

const usage = "usage: prepare-release pr [--release-date YYYY-MM-DD] MODULE=VERSION\n" +
	"       prepare-release release COMMIT MODULE=VERSION\n" +
	"MODULE is '.' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"

type app struct {
	out         io.Writer
	eff         effects
	repoRoot    string
	command     string
	release     releaseSpec
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
	if command != "pr" && command != "release" {
		return nil, fmt.Errorf("unknown command %q\n%s", command, usage)
	}

	// Declare flags
	flags := flag.NewFlagSet("prepare-release "+command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() {
		printLine(flags.Output(), "%s", usage)
		flags.PrintDefaults()
	}
	var releaseDate string
	if command == "pr" {
		flags.StringVar(
			&releaseDate,
			"release-date",
			time.Now().Format(time.DateOnly),
			"release date in YYYY-MM-DD format",
		)
	}
	err := flags.Parse(args[1:])
	if err != nil {
		return nil, err
	}

	// Store flags and positional args
	a := &app{out: out, eff: realWorld{out: out}, command: command}
	if command == "pr" {
		a.releaseDate, err = time.Parse(time.DateOnly, releaseDate)
		if err != nil {
			return nil, fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", releaseDate, err)
		}
	}

	positional := flags.Args()
	if command == "release" {
		if len(positional) != 2 {
			return nil, errors.New(usage)
		}
		a.commit = positional[0]
		positional = positional[1:]
	}
	if len(positional) != 1 {
		return nil, errors.New(usage)
	}
	a.release, err = parseReleaseSpec(positional[0])
	if err != nil {
		return nil, err
	}

	a.repoRoot, err = repoRoot()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CORE LOGIC

func (app *app) preparePR() (retErr error) {
	printLine(app.out, "Preparing %s %s\n", app.release.target.modulePath(), app.release.version)

	printLine(app.out, "[1/5] Fetch main")
	err := app.fetchMain()
	if err != nil {
		return err
	}

	printLine(app.out, "[2/5] Create release worktree")
	branchName := app.release.target.releaseBranchName(app.release.version)
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
	err = worktree.validateEverything(app.release, "pr")
	if err != nil {
		return err
	}

	printLine(app.out, "[3/5] Commit release files")
	err = worktree.updateReleaseFiles(app.release)
	if err != nil {
		return err
	}
	err = worktree.commitRelease(app.release)
	if err != nil {
		return err
	}

	printLine(app.out, "[4/5] Publish draft PR")
	err = worktree.pushBranch()
	if err != nil {
		return err
	}
	prURL, err := worktree.openDraftPR(app.release)
	if err != nil {
		return err
	}

	printLine(app.out, "[5/5] Clean up release worktree")
	err = worktree.cleanup()
	if err != nil {
		return err
	}
	printDetail(app.out, "Done.")

	printLine(app.out, "\nTo close the PR and delete the branch:")
	printLine(app.out, "  %s", formatCommand("gh", "pr", "close", prURL, "--delete-branch"))

	return nil
}

func (app *app) prepareRelease() (retErr error) {
	printLine(app.out, "Creating draft release for %s %s\n", app.release.target.modulePath(), app.release.version)

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
	err = worktree.validateEverything(app.release, "release")
	if err != nil {
		return err
	}

	printLine(app.out, "[4/4] Create draft release")
	_, err = worktree.createDraftRelease(app.release, commit)
	if err != nil {
		return err
	}
	printLine(app.out, "\nTo roll back this release, delete the draft release:")
	printLine(app.out, "  %s", formatCommand("gh", "release", "delete", app.release.target.tag(app.release.version), "--yes"))
	return nil
}

func (app *app) fetchMain() error {
	_, err := app.eff.runCommand(app.repoRoot, "git", "fetch", "--tags", "origin", "main")
	if err != nil {
		return fmt.Errorf("fetch main: %w", err)
	}
	return nil
}

func (app *app) resolveReleaseCommit() (string, error) {
	commit, err := app.eff.runCommand(app.repoRoot, "git", "rev-parse", "--verify", "--end-of-options", app.commit+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve release commit %q: %w", app.commit, err)
	}
	commit = strings.TrimSpace(commit)
	_, err = app.eff.runCommand(app.repoRoot, "git", "merge-base", "--is-ancestor", commit, "origin/main")
	if err != nil {
		return "", fmt.Errorf("release commit %s must be reachable from origin/main: %w", commit, err)
	}
	return commit, nil
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
		return "", errors.New("could not locate prepare-release source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}
