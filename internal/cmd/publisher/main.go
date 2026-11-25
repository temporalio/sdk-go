// Command publisher prepares PRs and Github Releases for a Go SDK release.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

const usage = `usage: publisher (gate|steward|stage|vet|tag)

  gate   Verify changed modules have changelog fragments in a non-release PR
  steward Find modules that changed since their last release
  stage  Create a draft release PR
  vet    Find mistakes in a release commit
  tag    Create draft GitHub releases

Windows is not supported. Use Linux, macOS, or WSL.
The command assumes you have cargo and gh on your PATH.

The command guides you through the release process. Start with "publisher steward".
If anything goes wrong, publisher prints an audit log with the commands it ran`

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
	if runtime.GOOS == "windows" {
		return errors.New("publisher does not support Windows; run it on Linux, macOS, or in WSL")
	}
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
	case "gate":
		return newGateApp(eff, args[1:])
	case "steward":
		return newStewardApp(eff, args[1:])
	case "stage":
		return newStageApp(eff, args[1:])
	case "vet":
		return newVetApp(eff, args[1:])
	case "tag":
		return newTagApp(eff, args[1:])
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

// GATE

type gateApp struct {
	app
	baseCommit    string
	skipChangelog bool
}

func newGateApp(eff effects, args []string) (*gateApp, error) {
	const usage = "usage: publisher gate [-skip-changelog] BASE_COMMIT"
	c := &gateApp{}
	flags := flag.NewFlagSet("publisher gate", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.BoolVar(&c.skipChangelog, "skip-changelog", false, "do not require a new changelog fragment")
	flags.Usage = func() {
		eff.err("%s\n", usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 1 {
		return nil, errors.New(usage)
	}
	r, err := newRepo(eff)
	if err != nil {
		return nil, err
	}
	c.app, c.baseCommit = app{repo: r}, flags.Arg(0)
	return c, nil
}

func (c *gateApp) run() error {
	baseCommit, err := c.resolveCommit(c.baseCommit)
	if err != nil {
		return err
	}
	coreTool, err := newCoreTool(c.eff, c.repoRoot)
	if err != nil {
		return err
	}
	contribModules, err := listContribModules(c.eff, c.repoRoot)
	if err != nil {
		return err
	}
	modules := append([]pure.Module{pure.SDKModule()}, contribModules...)
	for _, module := range modules {
		requireNew := false
		if !c.skipChangelog {
			requireNew, err = c.moduleChanged(module, baseCommit, contribModules)
			if err != nil {
				return err
			}
		}
		if err := coreTool.checkChangelog(c.repoRoot, module, baseCommit, requireNew); err != nil {
			return fmt.Errorf("check changelog for %s: %w", module.Name(), err)
		}
	}
	return nil
}

// STEWARD

type stewardApp struct {
	app
}

func newStewardApp(eff effects, args []string) (*stewardApp, error) {
	const usage = "usage: publisher steward"
	flags := flag.NewFlagSet("publisher steward", flag.ContinueOnError)
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
	return &stewardApp{app: app{repo: r}}, nil
}

func (c *stewardApp) run() error {
	batches, err := c.findReleases()
	if err != nil {
		return err
	}

	switch {
	case len(batches) <= 1:
	default:
		printLine(c.eff, "Releasing the modules in %d batches:", len(batches))
		for i, releases := range batches {
			printDetail(c.eff, "Batch %d: %s", i+1, pure.FormatVersionedModules(releases))
		}
		printDetail(c.eff, "")
	}

	for _, releases := range batches {
		var moduleArgs []string
		for _, release := range releases {
			moduleArgs = append(moduleArgs, release.Module.Name()+"="+release.Version.String())
		}

		printLine(c.eff, "Create a release PR\n")
		branch := pure.ReleaseBranchName(c.eff.today(), c.eff.randomSuffix())
		err = c.offerCommand("go", append([]string{"run", "./internal/cmd/publisher", "stage", "-branch", branch}, moduleArgs...)...)
		if err != nil {
			return err
		}

		printLine(c.eff, "After you've reviewed the PR and made changes, validate the PR\n")
		err = c.offerCommand("go", append([]string{"run", "./internal/cmd/publisher", "vet", "origin/" + branch}, moduleArgs...)...)
		if err != nil {
			return err
		}

		// merge-loop and get the commit hash
		printLine(c.eff, "Merge the PR if it passes checks\n")
		err = c.offerCommand("gh", "pr", "merge", "origin/"+branch, "--merge")
		if err != nil {
			return err
		}
		// TODO Parse stdout to get the merge commit, maybe rewriting the command above
		var commit string

		// tag-loop
		printLine(c.eff, "Create draft GitHub releases\n")
		err = c.offerCommand("go", append([]string{"run", "./internal/cmd/publisher", "tag", commit}, moduleArgs...)...)
		if err != nil {
			return err
		}

		// mark tag as ready or skip
		printLine(c.eff, "Mark the draft releases as ready for review\n")
		// TODO
	}
	return nil
}

func (c *stewardApp) findReleases() (_ [][]pure.VersionedModule, retErr error) {
	if err := c.fetchOrigin(); err != nil {
		return nil, err
	}
	w, err := c.createWorktree("origin/main")
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, w.cleanup())
	}()

	contribModules, err := listContribModules(c.eff, w.worktreeRoot)
	if err != nil {
		return nil, err
	}
	modules := append([]pure.Module{pure.SDKModule()}, contribModules...)

	// 1. FIND MODULES TO RELEASE

	var toRelease []pure.VersionedModule
	for _, module := range modules {
		tags, err := w.listTags(module.TagPattern())
		if err != nil {
			return nil, err
		}
		latestVersion := pure.LatestReleasedVersion(tags, module.TagPrefix())
		ref := "HEAD"
		if latestVersion != nil {
			ref = module.Tag(*latestVersion) + "..HEAD"
		}

		pathspecs := modulePathspecs(module, contribModules, modulePathspecOptions{
			includeGoMod:  true,
			includeGoSum:  true,
			includeReadme: true,
		})
		changed, err := w.hasCommits(ref, pathspecs...)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}

		nextVersion, err := c.chooseVersion(w, module, latestVersion, pathspecs)
		if err != nil {
			return nil, err
		}
		if nextVersion != nil {
			toRelease = append(toRelease, pure.VersionedModule{Module: module, Version: *nextVersion})
		}
		printLine(c.eff, "")
	}
	if len(toRelease) == 0 {
		return nil, nil
	}

	// 2. COLLECT MODULES THAT DON'T DEPEND ON EACH OTHER INTO BATCHES

	var goMods []pure.GoMod
	byPath := make(map[string]pure.VersionedModule, len(toRelease))
	for _, module := range toRelease {
		file := module.Module.GoMod(w.worktreeRoot)
		contents, err := w.eff.readFile(file)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file, err)
		}
		goMod, err := pure.NewValidatedGoMod(contents, module.Module.ModulePath())
		if err != nil {
			return nil, fmt.Errorf("validate %s: %w", file, err)
		}
		goMods = append(goMods, goMod)
		byPath[goMod.ModulePath] = module
	}
	batchedModules, err := pure.SortGoMods(goMods)
	if err != nil {
		return nil, fmt.Errorf("sort modules for release: %w", err)
	}
	var batches [][]pure.VersionedModule
	for _, batch := range batchedModules {
		var releases []pure.VersionedModule
		for _, module := range batch {
			releases = append(releases, byPath[module.ModulePath])
		}
		batches = append(batches, releases)
	}
	return batches, nil
}

func (c *stewardApp) chooseVersion(w *worktree, module pure.Module, latestVersion *pure.Version, pathspecs []string) (*pure.Version, error) {
	var latest pure.Version
	var baseline string
	if latestVersion != nil {
		latest = *latestVersion
		baseline = "latest release: " + latest.String()
	} else {
		latest = pure.Version{0, 0, 0}
		baseline = "never released"
	}

	name := module.Name()
	printLine(c.eff, "%s — %s", name, baseline)
	printLine(c.eff, "Make a minor release [m], patch release [p], view commit log [c], view diff [d], or skip [s]. ")
	for {
		answer, err := c.eff.readCharacter()
		if err != nil {
			return nil, fmt.Errorf("read release choice for %s: %w", name, err)
		}
		switch answer {
		case 'm':
			return &pure.Version{latest[0], latest[1] + 1, 0}, nil
		case 'p':
			return &pure.Version{latest[0], latest[1], latest[2] + 1}, nil
		case 's', '\r', '\n':
			return nil, nil
		case 'c':
			ref := "HEAD"
			if latestVersion != nil {
				ref = module.Tag(*latestVersion) + "..HEAD"
			}
			if err := w.viewCommits(ref, pathspecs...); err != nil {
				return nil, fmt.Errorf("view commits for %s: %w", name, err)
			}
		case 'd':
			// Compare never-released modules against the empty tree.
			base := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
			if latestVersion != nil {
				base = module.Tag(*latestVersion)
			}
			if err := w.viewDiff(base, pathspecs...); err != nil {
				return nil, fmt.Errorf("view diff for %s: %w", name, err)
			}
		default:
			printLine(c.eff, "Invalid choice; enter m, p, c, d, or s.")
		}
	}
}

func (c *stewardApp) offerCommand(name string, args ...string) error {
	printDetail(c.eff, "%s", formatCommand(name, args...))
	printLine(c.eff, "\n[Enter] Run the command for me. ")
	for {
		answer, err := c.eff.readCharacter()
		if err != nil {
			return err
		}
		switch answer {
		case '\n':
			stdout, err := c.eff.runCommand(c.repoRoot, name, args...)
			if err != nil {
				printDetail(c.eff, "%v", err)
				err = nil
				continue
			}
			return c.eff.page(stdout)
		default:
			printLine(c.eff, "Invalid choice.")
		}
	}
}

// STAGE

type stageApp struct {
	app
	modules []pure.VersionedModule
	branch  string
}

func newStageApp(eff effects, args []string) (*stageApp, error) {
	const usage = "usage: publisher stage [-branch BRANCH] MODULE=VERSION [MODULE=VERSION ...]\n" +
		"MODULE is 'sdk' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"
	var branch string
	flags := flag.NewFlagSet("publisher stage", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.StringVar(&branch, "branch", "", "use this new branch instead of generating a unique release branch")
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
	return &stageApp{
		app:     app{repo: r},
		modules: modules,
		branch:  branch,
	}, nil
}

func (c *stageApp) run() (retErr error) {
	printLine(c.eff, "Preparing release PR\n")

	printLine(c.eff, "[1/3] Fetch origin, prepare changelog tool, and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	branchName := c.branch
	if branchName == "" {
		branchName = pure.ReleaseBranchName(c.eff.today(), c.eff.randomSuffix())
	}
	coreTool, err := newCoreTool(c.eff, c.repoRoot)
	if err != nil {
		return err
	}
	worktree, err := c.createWorktreeAndBranch("origin/main", branchName)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, worktree.cleanup())
	}()

	printLine(c.eff, "[2/3] Update release files")
	for _, module := range c.modules {
		err := c.goReleaseFiles(worktree, module)
		if err != nil {
			return err
		}
	}
	for _, module := range c.modules {
		if err := coreTool.prepareChangelog(worktree.worktreeRoot, module, c.eff.today()); err != nil {
			return err
		}
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
	printLine(c.eff, "1. Vet the release branch:")
	printDetail(c.eff, "go run ./internal/cmd/publisher vet origin/%s %s", branchName, pure.FormatVersionedModules(c.modules))
	printLine(c.eff, "2. Fix errors and mark the PR as \"ready for review\".")
	printLine(c.eff, "3. Merge the PR and copy the commit hash.")
	printLine(c.eff, "4. Create draft github releases:")
	printDetail(c.eff, "go run ./internal/cmd/publisher tag <YOUR_COMMIT_HASH> %s", pure.FormatVersionedModules(c.modules))

	printLine(c.eff, "\nAlternatively, you can close the PR and delete the branch:")
	printDetail(c.eff, formatCommand("gh", "pr", "close", prURL, "--delete-branch"))

	return nil
}

func (c *stageApp) goReleaseFiles(w *worktree, module pure.VersionedModule) error {
	if versionFile, ok := module.Module.VersionFile(w.worktreeRoot); ok {
		text, err := c.eff.readFile(versionFile)
		if err != nil {
			return err
		}
		text, err = pure.ComputeNewVersionFile(text, module.Version)
		if err != nil {
			return fmt.Errorf("%s: %w", versionFile, err)
		}
		err = c.eff.writeFile(versionFile, text)
		if err != nil {
			return err
		}
	}
	return nil
}

// VET

type vetApp struct {
	app
	modules []pure.VersionedModule
	commit  string
}

func newVetApp(eff effects, args []string) (*vetApp, error) {
	const usage = "usage: publisher vet COMMIT MODULE=VERSION [MODULE=VERSION ...]"
	flags := flag.NewFlagSet("publisher vet", flag.ContinueOnError)
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
	return &vetApp{
		app:     app{repo: r},
		modules: modules,
		commit:  commit,
	}, nil
}

func (c *vetApp) run() (retErr error) {
	printLine(c.eff, "Checking release readiness\n")

	printLine(c.eff, "[1/2] Fetch origin, prepare changelog tool, and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	coreTool, err := newCoreTool(c.eff, c.repoRoot)
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
	err = c.vetModules(worktree, c.modules, coreTool)
	if err != nil {
		return err
	}
	return nil
}

// TAG

type tagApp struct {
	app
	modules     []pure.VersionedModule
	commit      string
	vetOverride bool
}

func newTagApp(eff effects, args []string) (*tagApp, error) {
	const usage = "usage: publisher tag [-vet-override] COMMIT MODULE=VERSION [MODULE=VERSION ...]\n" +
		"MODULE is 'sdk' for the Go SDK or a slash-separated contrib module directory like 'contrib/envconfig'"
	var vetOverride bool
	flags := flag.NewFlagSet("publisher tag", flag.ContinueOnError)
	flags.SetOutput(errorWriter{eff: eff})
	flags.BoolVar(&vetOverride, "vet-override", false, "skip the entire vet step before creating draft releases")
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
	return &tagApp{
		app:         app{repo: r},
		modules:     modules,
		commit:      flags.Arg(0),
		vetOverride: vetOverride,
	}, nil
}

func (c *tagApp) run() (retErr error) {
	printLine(c.eff, "Creating draft releases for %s\n", pure.FormatVersionedModules(c.modules))

	printLine(c.eff, "[1/4] Fetch origin, prepare changelog tool, and create worktree")
	err := c.fetchOrigin()
	if err != nil {
		return err
	}
	coreTool, err := newCoreTool(c.eff, c.repoRoot)
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

	printLine(c.eff, "[2/4] Vet released modules")
	if c.vetOverride {
		printDetail(c.eff, "Skipped (-vet-override)")
	} else {
		err = c.vetModules(worktree, c.modules, coreTool)
		if err != nil {
			return err
		}
	}

	printLine(c.eff, "[3/4] Get release notes")
	releaseNotes := make([]string, len(c.modules))
	for i, module := range c.modules {
		releaseNotes[i], err = coreTool.readReleaseNotes(worktree.worktreeRoot, module)
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
			printDetail(c.eff, formatCommand("gh", "release", "delete", module.Module.Tag(module.Version), "--yes"))
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

func (c *app) vetModules(w *worktree, modules []pure.VersionedModule, coreTool *coreTool) error {
	var problems []error
	ready := make(map[pure.VersionedModule]bool)
	for _, module := range modules {
		err := c.vetModule(w, module, coreTool)
		if err != nil {
			ready[module] = false
			problems = append(problems, err)
		} else {
			ready[module] = true
		}
	}
	for _, module := range modules {
		if ready[module] {
			printDetail(c.eff, "%s: passed inspection ✔", module.Module.ModulePath())
		} else {
			printDetail(c.eff, "%s: failed inspection ✘", module.Module.ModulePath())
		}
	}
	return errors.Join(problems...)
}

func (c *app) vetModule(w *worktree, module pure.VersionedModule, coreTool *coreTool) error {
	m := module.Module
	modulePath := m.ModulePath()
	var problems []error

	printDetail(c.eff, "%s: validate new version number", modulePath)
	tags, err := w.listTags(m.TagPattern())
	if err != nil {
		problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
	} else {
		var latest pure.Version
		if version := pure.LatestReleasedVersion(tags, m.TagPrefix()); version != nil {
			latest = *version
		}
		major := pure.Version{latest[0] + 1, 0, 0}
		minor := pure.Version{latest[0], latest[1] + 1, 0}
		patch := pure.Version{latest[0], latest[1], latest[2] + 1}
		if module.Version != major && module.Version != minor && module.Version != patch {
			problems = append(problems, fmt.Errorf("%s: proposed version %s must be a major, minor, or patch increment of %s; choose %s, %s, or %s",
				modulePath, module.Version, latest, major, minor, patch))
		}
	}

	printDetail(c.eff, "%s: validate go.mod", modulePath)
	contents, err := w.eff.readFile(m.GoMod(w.worktreeRoot))
	var goMod pure.GoMod
	if err == nil {
		goMod, err = pure.NewValidatedGoMod(contents, modulePath)
	}
	if err != nil {
		problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
	} else {
		printDetail(c.eff, "%s: verify that go.mod requirements are published", modulePath)
		for _, requirement := range goMod.Requirements {
			published, err := w.eff.checkModulePublished(requirement.ModulePath, requirement.Version)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
			} else if !published {
				problems = append(problems, fmt.Errorf("%s: %s %s is not published on the Go module proxy",
					modulePath, requirement.ModulePath, requirement.Version))
			}
		}
	}
	if versionFile, ok := m.VersionFile(w.worktreeRoot); ok {
		printDetail(c.eff, "%s: validate version.go", modulePath)
		text, err := w.eff.readFile(versionFile)
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
		} else if err := pure.ValidateVersionConstant(text, m, module.Version); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
		}
	}

	printDetail(c.eff, "%s: validate changelog", modulePath)
	if err := coreTool.validateReleaseChangelog(w.worktreeRoot, module, c.eff.today()); err != nil {
		problems = append(problems, fmt.Errorf("%s: %w", modulePath, err))
	}

	return errors.Join(problems...)
}
