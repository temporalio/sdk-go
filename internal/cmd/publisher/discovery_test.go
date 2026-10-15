package main

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangedModulesWithoutReleases(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/envconfig", "contrib/tools/workflowcheck", "test", "internal/cmd/build", ".github/scripts/cloud-namespace")
	writeDiscoveryFile(t, w, "contrib/untracked/go.mod", "module go.temporal.io/sdk/contrib/untracked\n")

	// No changelogs exist, but readiness problems must not hide release candidates.
	checkChangedModules(t, w, `
		.: unreleased
		contrib/envconfig: unreleased
		contrib/tools/workflowcheck: unreleased
	`)
}

func TestChangedModulesUsesLatestStableTags(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/envconfig", "contrib/envconfig/nested")
	runDiscoveryGit(t, w, "tag", "v1.9.0")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.9.0")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/nested/v9.0.0")

	writeDiscoveryFile(t, w, "file.txt", "SDK changes\n")
	writeDiscoveryFile(t, w, "contrib/envconfig/file.txt", "contrib changes\n")
	runDiscoveryGit(t, w, "add", ".")
	runDiscoveryGit(t, w, "commit", "-m", "Release SDK and contrib changes")
	runDiscoveryGit(t, w, "tag", "v1.10.0")
	runDiscoveryGit(t, w, "tag", "-a", "contrib/envconfig/v0.10.0", "-m", "Contrib release")
	checkChangedModules(t, w, "")

	writeDiscoveryFile(t, w, "contrib/envconfig/file.txt", "contrib prerelease changes\n")
	runDiscoveryGit(t, w, "add", ".")
	runDiscoveryGit(t, w, "commit", "-m", "Prepare a contrib prerelease")
	runDiscoveryGit(t, w, "tag", "v2.0.0-rc.1")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.11.0-rc.1")
	checkChangedModules(t, w, `
		contrib/envconfig: 0.10.0
	`)
}

func TestChangedModulesTracksLocalEdits(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/envconfig", "contrib/tally")
	runDiscoveryGit(t, w, "tag", "v1.0.0")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.1.0")
	runDiscoveryGit(t, w, "tag", "contrib/tally/v0.1.0")
	writeDiscoveryFile(t, w, "untracked.txt", "ignored until staged\n")
	checkChangedModules(t, w, "")

	writeDiscoveryFile(t, w, "file.txt", "staged SDK changes\n")
	runDiscoveryGit(t, w, "add", "file.txt")
	writeDiscoveryFile(t, w, "contrib/envconfig/file.txt", "unstaged contrib changes\n")
	err := os.Remove(filepath.Join(w.worktreeRoot, "contrib", "tally", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	writeDiscoveryFile(t, w, "contrib/new/go.mod", "module go.temporal.io/sdk/contrib/new\n")
	runDiscoveryGit(t, w, "add", "contrib/new/go.mod")

	before := runDiscoveryGit(t, w, "status", "--porcelain", "-z")
	checkChangedModules(t, w, `
		.: 1.0.0
		contrib/envconfig: 0.1.0
		contrib/new: unreleased
		contrib/tally: 0.1.0
	`)
	after := runDiscoveryGit(t, w, "status", "--porcelain", "-z")
	testEqual(t, after, before)
}

func TestChangedModulesRespectsModuleBoundaries(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/aws/s3driver", "contrib/aws/s3driver/awssdkv2", "contrib/aws/s3driver-extra", "test", "internal/tools [test]", ".github/scripts/cloud-namespace")
	runDiscoveryGit(t, w, "tag", "v1.0.0")
	runDiscoveryGit(t, w, "tag", "contrib/aws/s3driver/v0.1.0")
	runDiscoveryGit(t, w, "tag", "contrib/aws/s3driver/awssdkv2/v0.1.0")
	runDiscoveryGit(t, w, "tag", "contrib/aws/s3driver-extra/v0.1.0")

	writeDiscoveryFile(t, w, "contrib/aws/s3driver/awssdkv2/file.txt", "nested module changes\n")
	writeDiscoveryFile(t, w, "test/file.txt", "integration test module changes\n")
	writeDiscoveryFile(t, w, "internal/tools [test]/file.txt", "build module changes\n")
	writeDiscoveryFile(t, w, ".github/scripts/cloud-namespace/file.txt", "operational module changes\n")
	checkChangedModules(t, w, `
		contrib/aws/s3driver/awssdkv2: 0.1.0
	`)

	writeDiscoveryFile(t, w, "contrib/aws/s3driver-extra/file.txt", "sibling module changes\n")
	checkChangedModules(t, w, `
		contrib/aws/s3driver-extra: 0.1.0
		contrib/aws/s3driver/awssdkv2: 0.1.0
	`)
}

func TestChangedModulesIgnoresDependencyOnlyChanges(t *testing.T) {
	for _, files := range [][]string{{"go.mod"}, {"go.sum"}, {"go.mod", "go.sum"}} {
		for _, state := range []string{"unstaged", "staged", "committed"} {
			t.Run(strings.Join(files, "+")+"/"+state, func(t *testing.T) {
				w := newDiscoveryWorktree(t, "contrib/envconfig")
				for _, dir := range []string{"", "contrib/envconfig"} {
					writeDiscoveryFile(t, w, path.Join(dir, "go.sum"), "initial checksums\n")
				}
				runDiscoveryGit(t, w, "add", ".")
				runDiscoveryGit(t, w, "commit", "-m", "Add go.sum files")
				runDiscoveryGit(t, w, "tag", "v1.0.0")
				runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.1.0")
				for _, dir := range []string{"", "contrib/envconfig"} {
					for _, file := range files {
						writeDiscoveryFile(t, w, path.Join(dir, file), "updated dependency metadata\n")
					}
				}
				if state != "unstaged" {
					runDiscoveryGit(t, w, "add", ".")
				}
				if state == "committed" {
					runDiscoveryGit(t, w, "commit", "-m", "Update dependency files")
				}
				checkChangedModules(t, w, "")

				writeDiscoveryFile(t, w, "file.txt", "SDK changes\n")
				writeDiscoveryFile(t, w, "contrib/envconfig/file.txt", "contrib changes\n")
				checkChangedModules(t, w, `
					.: 1.0.0
					contrib/envconfig: 0.1.0
				`)
			})
		}
	}
}

func TestChangedModulesTracksMovesAcrossModules(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/envconfig", "contrib/tally")
	runDiscoveryGit(t, w, "tag", "v1.0.0")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.1.0")
	runDiscoveryGit(t, w, "tag", "contrib/tally/v0.1.0")

	runDiscoveryGit(t, w, "mv", "contrib/envconfig/file.txt", "contrib/tally/moved.txt")
	checkChangedModules(t, w, `
		contrib/envconfig: 0.1.0
		contrib/tally: 0.1.0
	`)

	runDiscoveryGit(t, w, "mv", "contrib/tally/moved.txt", "moved.txt")
	checkChangedModules(t, w, `
		.: 1.0.0
		contrib/envconfig: 0.1.0
	`)
}

func TestLintRealCheckoutAndSnapshot(t *testing.T) {
	w := newDiscoveryWorktree(t, "contrib/envconfig")
	writeDiscoveryFile(t, w, "contrib/envconfig/CHANGELOG.md", "## [Unreleased]\n")
	runDiscoveryGit(t, w, "add", ".")
	runDiscoveryGit(t, w, "commit", "-m", "Add changelog")
	runDiscoveryGit(t, w, "branch", "-M", "main")
	runDiscoveryGit(t, w, "remote", "add", "origin", w.worktreeRoot)
	runDiscoveryGit(t, w, "tag", "v1.0.0")
	runDiscoveryGit(t, w, "tag", "contrib/envconfig/v0.1.0")
	runDiscoveryGit(t, w, "checkout", "-b", "release-pr")
	writeDiscoveryFile(t, w, "contrib/envconfig/file.txt", "An unreleased fix.\n")

	var out strings.Builder
	a := &app{
		out:         &out,
		eff:         realWorld{out: &out},
		repoRoot:    w.worktreeRoot,
		releaseDate: time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC),
	}
	err := a.lint()
	if err == nil || !strings.Contains(err.Error(), "appears to be empty") {
		t.Fatalf("expected empty Unreleased error, got %v", err)
	}
	writeDiscoveryFile(t, w, "contrib/envconfig/CHANGELOG.md", "## [Unreleased]\n\n- An unreleased fix.\n")
	err = a.lint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "publisher pr contrib/envconfig=0.2.0") {
		t.Fatalf("missing minor suggestion:\n%s", out.String())
	}

	// Prepare and commit the release on an unmerged PR branch.
	w.releaseDate = a.releaseDate
	files, err := w.computeReleaseFiles(releaseSpec{target: contribEnvconfig(t), version: semver{0, 1, 1}})
	if err != nil {
		t.Fatal(err)
	}
	err = w.writeReleaseFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	runDiscoveryGit(t, w, "add", ".")
	runDiscoveryGit(t, w, "commit", "-m", "Prepare release")
	a.commit = strings.TrimSpace(runDiscoveryGit(t, w, "rev-parse", "HEAD"))
	writeDiscoveryFile(t, w, "contrib/envconfig/CHANGELOG.md", "Broken local edits.\n")
	before := runDiscoveryGit(t, w, "status", "--porcelain", "-z")
	worktrees := runDiscoveryGit(t, w, "worktree", "list", "--porcelain")
	out.Reset()

	err = a.lint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "contrib/envconfig=0.1.1: ready for release") {
		t.Fatalf("missing prepared release report:\n%s", out.String())
	}
	testEqual(t, runDiscoveryGit(t, w, "status", "--porcelain", "-z"), before)
	testEqual(t, runDiscoveryGit(t, w, "worktree", "list", "--porcelain"), worktrees)
}

func newDiscoveryWorktree(t *testing.T, moduleDirs ...string) *worktree {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_EDITOR", "true")
	root := t.TempDir()
	w := &worktree{out: io.Discard, eff: realWorld{out: io.Discard}, repoRoot: root, worktreeRoot: root}
	runDiscoveryGit(t, w, "init", "--quiet")
	runDiscoveryGit(t, w, "config", "user.name", "Release test")
	runDiscoveryGit(t, w, "config", "user.email", "release-test@example.com")
	for _, dir := range append([]string{""}, moduleDirs...) {
		writeDiscoveryFile(t, w, path.Join(dir, "go.mod"), "module "+path.Join(sdkModulePath, dir)+"\n")
		writeDiscoveryFile(t, w, path.Join(dir, "file.txt"), "contents of "+dir+"\n")
	}
	runDiscoveryGit(t, w, "add", ".")
	runDiscoveryGit(t, w, "commit", "-m", "Add modules")
	return w
}

func runDiscoveryGit(t *testing.T, w *worktree, args ...string) string {
	t.Helper()
	output, err := w.eff.runCommand(w.worktreeRoot, "git", args...)
	if err != nil {
		t.Fatalf("%s: %v", formatCommand("git", args...), err)
	}
	return output
}

func writeDiscoveryFile(t *testing.T, w *worktree, file, contents string) {
	t.Helper()
	file = filepath.Join(w.worktreeRoot, filepath.FromSlash(file))
	err := os.MkdirAll(filepath.Dir(file), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	err = w.eff.writeFile(file, contents)
	if err != nil {
		t.Fatal(err)
	}
}

func checkChangedModules(t *testing.T, w *worktree, want string) {
	t.Helper()
	candidates, err := w.changedModules()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, candidate := range candidates {
		module := candidate.target.dir
		if module == "" {
			module = "."
		}
		version := "unreleased"
		if candidate.oldVersion != nil {
			version = candidate.oldVersion.String()
		}
		got = append(got, module+": "+version)
	}
	testEqual(t, strings.Join(got, "\n"), want)
}
