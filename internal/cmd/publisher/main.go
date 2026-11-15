// Command publisher prepares PRs and Github Releases for a Go SDK release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

const usage = "usage: publisher (find|pr|lint|release)"

type command interface {
	run() error
}

type app struct {
	repo
}

// COMMAND-LINE WRAPPER

func main() {
	eff := &realWorld{stdout: os.Stdout, stderr: os.Stderr}
	err := run(eff, os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		eff.err("\n%v\n", err)
		os.Exit(1)
	}
}

func run(eff *realWorld, args []string) (retErr error) {
	audit, err := os.CreateTemp("", "publisher-*.log")
	if err != nil {
		return fmt.Errorf("create audit log: %w", err)
	}
	defer func() {
		printLine(eff, "Audit log: %s", audit.Name())
		if err := audit.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close audit log %s: %w", audit.Name(), err))
		}
	}()
	eff.audit = audit
	command, err := newCommand(eff, args)
	if err != nil {
		return err
	}
	return command.run()
}

func newCommand(eff effects, args []string) (command, error) {
	if len(args) == 0 {
		return nil, errors.New(usage)
	}
	name := args[0]
	if name == "-h" || name == "--help" {
		eff.err("%s\n", usage)
		return nil, flag.ErrHelp
	}
	switch name {
	case "find":
		return newFindApp(eff, args[1:])
	case "pr":
		return newPrApp(eff, args[1:])
	case "lint":
		return newLintApp(eff, args[1:])
	case "release":
		return newReleaseApp(eff, args[1:])
	default:
		return nil, fmt.Errorf("unknown command %q\n%s", name, usage)
	}
}

func printLine(eff effects, format string, args ...any) {
	eff.out(format+"\n", args...)
}

func printDetail(eff effects, format string, args ...any) {
	eff.out("      "+format+"\n", args...)
}

// FIND

type findApp struct {
	app
}

func newFindApp(eff effects, args []string) (*findApp, error) {
	const usage = "usage: publisher find"
	flags := flag.NewFlagSet("publisher find", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.Usage = func() { eff.err("%s\n", usage) }
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, errors.New(usage)
	}
	r, err := newRepo(eff)
	if err != nil {
		return nil, err
	}
	return &findApp{app: app{repo: r}}, nil
}

func (c *findApp) run() error {
	if err := c.fetchOrigin(); err != nil {
		return err
	}
	head, err := c.resolveCommit("origin/main")
	if err != nil {
		return err
	}

	modules := []pure.Module{pure.SDKModule()}
	modules = append(modules, c.contribModules...)

	var toRelease []pure.VersionedModule
	for _, module := range modules {
		latestVersion, changed, err := c.changesSinceLastRelease(module, head)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		nextVersion, err := c.chooseVersion(module, latestVersion, head)
		if err != nil {
			return err
		}
		if nextVersion != nil {
			toRelease = append(toRelease, pure.VersionedModule{Module: module, Version: *nextVersion})
		}
		printLine(c.eff, "")
	}
	if len(toRelease) > 0 {
		printLine(c.eff, "To create a release PR, run the command:")
		printDetail(c.eff, "publisher pr %s\n", pure.FormatVersionedModules(toRelease))
	}
	return nil
}

func (c *findApp) chooseVersion(module pure.Module, latestVersion *pure.Version, head string) (*pure.Version, error) {
	var latest pure.Version
	var baseline string
	if latestVersion != nil {
		latest = *latestVersion
		baseline = "latest release: " + latest.String()
	} else {
		latest = pure.Version{0, 0, 0}
		baseline = "never released"
	}

	name := module.Dir
	if name == "" {
		name = "sdk"
	}
	printLine(c.eff, "%s — %s", name, baseline)
	printLine(c.eff, "Make a minor release [m], patch release [p], view commit log [c], view diff [d], or skip [s]. ")
	pathspecs := c.modulePathspecs(module)
	for {
		answer, err := c.eff.readCharacter()
		if err != nil {
			return nil, fmt.Errorf("read release choice for %s: %w", name, err)
		}
		switch answer {
		case 'm', 'M':
			return &pure.Version{latest[0], latest[1] + 1, 0}, nil
		case 'p', 'P':
			return &pure.Version{latest[0], latest[1], latest[2] + 1}, nil
		case 's', 'S', '\r', '\n':
			return nil, nil
		case 'c', 'C':
			ref := head
			if latestVersion != nil {
				ref = module.Tag(*latestVersion) + ".." + head
			}
			if err := c.viewCommits(ref, pathspecs...); err != nil {
				return nil, fmt.Errorf("view commits for %s: %w", name, err)
			}
		case 'd', 'D':
			// Compare never-released modules against the empty tree.
			base := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
			if latestVersion != nil {
				base = module.Tag(*latestVersion)
			}
			if err := c.viewDiff(base, head, pathspecs...); err != nil {
				return nil, fmt.Errorf("view diff for %s: %w", name, err)
			}
		default:
			printLine(c.eff, "Invalid choice; enter m, p, c, d, or s.")
		}
	}
}

// PR

type prApp struct {
	app
	modules []pure.VersionedModule
}

func newPrApp(eff effects, args []string) (*prApp, error) {
	const usage = "usage: publisher pr MODULE=VERSION [MODULE=VERSION ...]\n" +
		"MODULE is 'sdk' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"
	flags := flag.NewFlagSet("publisher pr", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.Usage = func() {
		eff.err("%s\n", usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() == 0 {
		return nil, errors.New(usage)
	}
	modules, err := pure.ParseVersionedModules(flags.Args())
	if err != nil {
		return nil, err
	}
	r, err := newRepo(eff)
	if err != nil {
		return nil, err
	}
	return &prApp{
		app:     app{repo: r},
		modules: modules,
	}, nil
}

func (c *prApp) run() (retErr error) {
	printLine(c.eff, "Preparing release PR\n")

	printLine(c.eff, "[1/3] Fetch origin and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	branchName := pure.ReleaseBranchName(c.eff.today())
	worktree, err := c.createWorktreeAndBranch("origin/main", branchName)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, worktree.cleanup())
	}()

	printLine(c.eff, "[2/3] Update release files")
	var files []releaseFile
	for _, module := range c.modules {
		updated, err := worktree.newReleaseFiles(module)
		if err != nil {
			return err
		}
		files = append(files, updated...)
	}
	err = worktree.writeReleaseFiles(files)
	if err != nil {
		return err
	}

	printLine(c.eff, "[3/3] Commit and publish draft PR")
	err = worktree.commitRelease(c.modules)
	if err != nil {
		return err
	}
	err = worktree.pushBranch()
	if err != nil {
		return err
	}
	prURL, err := worktree.openDraftPR(c.modules)
	if err != nil {
		return err
	}

	printLine(c.eff, "\nNext steps:")
	printLine(c.eff, "1. Lint the release branch:")
	printDetail(c.eff, "publisher lint %s %s", branchName, pure.FormatVersionedModules(c.modules))
	printLine(c.eff, "2. Fix errors and mark the PR as \"ready for review\".")
	printLine(c.eff, "3. Merge the PR and copy the commit hash.")
	printLine(c.eff, "4. Create draft github releases:")
	printDetail(c.eff, "publisher release <YOUR_COMMIT_HASH> %s", pure.FormatVersionedModules(c.modules))

	printLine(c.eff, "\nAlternatively, you can close the PR and delete the branch:")
	printDetail(c.eff, FormatCommand("gh", "pr", "close", prURL, "--delete-branch"))

	return nil
}

// LINT

type lintApp struct {
	app
	modules []pure.VersionedModule
	commit  string
}

func newLintApp(eff effects, args []string) (*lintApp, error) {
	const usage = "usage: publisher lint COMMIT MODULE=VERSION [MODULE=VERSION ...]"
	flags := flag.NewFlagSet("publisher lint", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.Usage = func() {
		eff.err("%s\n", usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() < 2 {
		return nil, errors.New(usage)
	}
	commit := flags.Arg(0)
	modules, err := pure.ParseVersionedModules(flags.Args()[1:])
	if err != nil {
		return nil, err
	}
	r, err := newRepo(eff)
	if err != nil {
		return nil, err
	}
	return &lintApp{
		app:     app{repo: r},
		modules: modules,
		commit:  commit,
	}, nil
}

func (c *lintApp) run() (retErr error) {
	printLine(c.eff, "Checking release readiness\n")

	printLine(c.eff, "[1/2] Fetch origin and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	commit, err := c.resolveCommit(c.commit)
	if err != nil {
		return err
	}
	worktree, err := c.createWorktree(commit)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, worktree.cleanup())
	}()

	printLine(c.eff, "[2/2] Validate release files")
	var problems []error
	ready := make(map[pure.VersionedModule]bool)
	for _, module := range c.modules {
		err := c.lintModule(worktree, module)
		if err != nil {
			ready[module] = false
			problems = append(problems, err)
		} else {
			ready[module] = true
		}
	}
	for _, module := range c.modules {
		if ready[module] {
			printDetail(c.eff, "%s: passed inspection ✔", module.Module.ModulePath())
		} else {
			printDetail(c.eff, "%s: failed inspection ✘", module.Module.ModulePath())
		}
	}
	return errors.Join(problems...)
}

// RELEASE

type releaseApp struct {
	app
	modules []pure.VersionedModule
	commit  string
}

func newReleaseApp(eff effects, args []string) (*releaseApp, error) {
	const usage = "usage: publisher release COMMIT MODULE=VERSION [MODULE=VERSION ...]\n" +
		"MODULE is 'sdk' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"
	flags := flag.NewFlagSet("publisher release", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.Usage = func() {
		eff.err("%s\n", usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() < 2 {
		return nil, errors.New(usage)
	}
	modules, err := pure.ParseVersionedModules(flags.Args()[1:])
	if err != nil {
		return nil, err
	}
	r, err := newRepo(eff)
	if err != nil {
		return nil, err
	}
	return &releaseApp{
		app:     app{repo: r},
		modules: modules,
		commit:  flags.Arg(0),
	}, nil
}

func (c *releaseApp) run() (retErr error) {
	printLine(c.eff, "Creating draft releases for %s\n", pure.FormatVersionedModules(c.modules))

	printLine(c.eff, "[1/4] Fetch origin and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	commit, err := c.resolveCommit(c.commit)
	if err != nil {
		return err
	}
	worktree, err := c.createWorktree(commit)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, worktree.cleanup())
	}()

	printLine(c.eff, "[2/4] Lint released modules")
	var problems []error
	for _, module := range c.modules {
		err = c.lintModule(worktree, module)
		if err != nil {
			problems = append(problems, err)
		}
	}
	err = errors.Join(problems...)
	if err != nil {
		return fmt.Errorf("lint failed: %w", err)
	}
	printDetail(c.eff, "Modules passed inspection ✔")

	printLine(c.eff, "[3/4] Get release notes")
	releaseNotes := make([]string, len(c.modules))
	for i, module := range c.modules {
		releaseNotes[i], err = worktree.readReleaseNotes(module)
		if err != nil {
			return err
		}
	}

	printLine(c.eff, "[4/4] Create draft release(s)")
	releaseCount := 0
	defer func() {
		if releaseCount == 0 {
			return
		}
		printLine(c.eff, "\nTo roll back successful draft releases, run:")
		for _, module := range c.modules[:releaseCount] {
			printDetail(c.eff, FormatCommand("gh", "release", "delete", module.Module.Tag(module.Version), "--yes"))
		}
	}()
	for i, module := range c.modules {
		_, err = worktree.createDraftRelease(module, commit, releaseNotes[i])
		if err != nil {
			return err
		}
		releaseCount++
	}
	return nil
}

// VALIDATION

func (c *app) lintModule(w *worktree, module pure.VersionedModule) error {
	m := module.Module
	modulePath := m.ModulePath()

	printDetail(c.eff, "%s: validate go.mod", modulePath)
	goMod, err := w.eff.readFile(w.pathToGoMod(m))
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	requirements, err := pure.ValidateGoMod(goMod, modulePath)
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	printDetail(c.eff, "%s: verify that go.mod requirements are published", modulePath)
	for _, requirement := range requirements {
		published, err := w.eff.checkModulePublished(requirement.ModulePath, requirement.Version)
		if err != nil {
			return fmt.Errorf("%s: %w", modulePath, err)
		}
		if !published {
			return fmt.Errorf("%s: %s %s is not published on the Go module proxy",
				modulePath, requirement.ModulePath, requirement.Version)
		}
	}
	if m.VersionFile != "" {
		printDetail(c.eff, "%s: validate version.go", modulePath)
		text, err := w.eff.readFile(w.pathToVersionFile(m))
		if err != nil {
			return fmt.Errorf("%s: %w", modulePath, err)
		}
		if err := pure.ValidateVersionConstant(text, m, module.Version); err != nil {
			return fmt.Errorf("%s: %w", modulePath, err)
		}
	}

	printDetail(c.eff, "%s: validate changelog", modulePath)
	changelog, err := w.eff.readFile(w.pathToChangelog(m))
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}
	err = pure.ValidateChangelog(changelog, module.Version, c.eff.today())
	if err != nil {
		return fmt.Errorf("%s: %w", modulePath, err)
	}

	return nil
}
