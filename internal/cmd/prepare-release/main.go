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

const usage = "usage: prepare-release [--release-date YYYY-MM-DD] [--stop-before-push] [MODULE] VERSION\n" +
	"optional argument MODULE is a slash-separated path to a contrib module directory like 'contrib/envconfig'; " +
	"omit MODULE to release the Go SDK"

type app struct {
	out            io.Writer
	eff            effects
	repoRoot       string
	target         releaseTarget
	version        semver
	releaseDate    time.Time
	stopBeforePush bool
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
	return a.prepareRelease()
}

func newApp(out, errOut io.Writer, args []string) (*app, error) {
	// Declare flags
	flags := flag.NewFlagSet("prepare-release", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), usage)
		flags.PrintDefaults()
	}
	releaseDate := flags.String(
		"release-date",
		time.Now().Format(time.DateOnly),
		"release date in YYYY-MM-DD format",
	)
	stopBeforePush := flags.Bool(
		"stop-before-push",
		false,
		"abort the script before pushing anything to GitHub",
	)
	err := flags.Parse(args)
	if err != nil {
		return nil, err
	}

	// Store flags and positional args
	a := &app{out: out, eff: realWorld{out: out}}

	a.releaseDate, err = time.Parse(time.DateOnly, *releaseDate)
	if err != nil {
		return nil, fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", *releaseDate, err)
	}
	a.stopBeforePush = *stopBeforePush

	positional := flags.Args()
	switch len(positional) {
	case 1:
		a.target = sdkTarget()
		a.version, err = parseVersion(positional[0])
		if err != nil {
			return nil, err
		}
	case 2:
		a.target, err = contribTarget(positional[0])
		if err != nil {
			return nil, err
		}
		a.version, err = parseVersion(positional[1])
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New(usage)
	}

	a.repoRoot, err = repoRoot()
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CORE LOGIC

func (app *app) prepareRelease() (retErr error) {
	fmt.Fprintf(app.out, "Preparing %s %s\n\n", app.target.modulePath(), app.version)

	fmt.Fprintln(app.out, "[1/5] Fetch main")
	err := app.fetchMain()
	if err != nil {
		return err
	}

	fmt.Fprintln(app.out, "[2/5] Create release worktree")
	branchName := app.target.releaseBranchName(app.version)
	worktree, err := app.createWorktree(branchName)
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
	err = worktree.validateEverything()
	if err != nil {
		return err
	}

	fmt.Fprintln(app.out, "[3/5] Commit release files")
	releaseNotes, err := worktree.updateReleaseFiles()
	if err != nil {
		return err
	}
	err = worktree.commitRelease()
	if err != nil {
		return err
	}
	if app.stopBeforePush {
		return errors.New("stopped before pushing release branch (--stop-before-push)")
	}

	fmt.Fprintln(app.out, "[4/5] Publish draft PR and draft release")
	err = worktree.pushBranch()
	if err != nil {
		return err
	}
	prURL, err := worktree.openDraftPR()
	if err != nil {
		return err
	}
	_, err = worktree.createDraftRelease(releaseNotes)
	if err != nil {
		return err
	}

	fmt.Fprintln(app.out, "[5/5] Clean up release worktree")
	err = worktree.cleanup()
	if err != nil {
		return err
	}
	printDetail(app.out, "Done.")

	fmt.Fprint(app.out, "\nTo roll back this release, close the PR and delete the draft release:\n")
	fmt.Fprintf(app.out, "  %s\n", formatCommand("gh", "pr", "close", prURL, "--delete-branch"))
	fmt.Fprintf(app.out, "  %s\n", formatCommand("gh", "release", "delete", app.target.tag(app.version), "--yes"))

	return nil
}

func (app *app) fetchMain() error {
	_, err := app.eff.runCommand(app.repoRoot, "git", "fetch", "--tags", "origin", "main")
	if err != nil {
		return fmt.Errorf("fetch main: %w", err)
	}
	return nil
}

func (app *app) createWorktree(branch string) (*worktree, error) {
	root, err := app.eff.mkdirTemp("", "prepare-go-release-")
	if err != nil {
		return nil, fmt.Errorf("create temporary worktree: %w", err)
	}
	_, err = app.eff.runCommand(app.repoRoot, "git", "worktree", "add", "-b", branch, root, "origin/main")
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}
	head, err := app.eff.runCommand(root, "git", "log", "-1", "--format=%s (%h)")
	if err != nil {
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
		target:       app.target,
		version:      app.version,
		releaseDate:  app.releaseDate,
		repoRoot:     app.repoRoot,
		worktreeRoot: root,
		branch:       branch,
	}
}

func printDetail(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "      "+format+"\n", args...)
}

// repoRoot locates the git repo that contains this script.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not locate prepare-release source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../..")), nil
}
