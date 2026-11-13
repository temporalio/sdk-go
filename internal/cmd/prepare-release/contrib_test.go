package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContribPRBody(t *testing.T) {
	root := t.TempDir()
	eff := realWorld{out: io.Discard}
	git := func(args ...string) string {
		t.Helper()
		out, err := eff.runCommand(root, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	write := func(name, contents string) {
		t.Helper()
		name = filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(subject string) string {
		t.Helper()
		git("add", ".")
		git("commit", "-m", subject)
		return git("rev-parse", "HEAD")
	}
	git("init")
	git("config", "user.name", "Release test")
	git("config", "user.email", "release@example.com")
	git("config", "commit.gpgsign", "false")
	modules := []string{"contrib/a", "contrib/a/nested", "contrib/b", "contrib/deleted"}
	for _, dir := range modules {
		write(dir+"/go.mod", "module example.com/"+dir)
		write(dir+"/CHANGELOG.md", "Initial release")
	}
	commit("Released changes")
	for _, dir := range modules {
		git("tag", dir+"/v1.0.0")
	}
	write("contrib/a/old.go", "Older unreleased change")
	older := commit("Unreleased before SDK release")
	git("tag", "v1.0.0")
	write("contrib/b/released.go", "Already released")
	commit("Contrib release after SDK release")
	git("tag", "contrib/b/v1.1.0")
	write("contrib/b/go.mod", "module example.com/contrib/b\ngo 1.26.0")
	write("contrib/b/go.sum", "Dependency update")
	commit("Dependency-only change")
	write("contrib/a/test/go.sum", "Test dependency update")
	commit("Test dependency-only change")
	write("contrib/a/go.mod", "module example.com/contrib/a\ngo 1.26.0")
	write("contrib/a/mixed.go", "Source change alongside dependency update")
	write("contrib/b/go.sum", "Another dependency update")
	mixed := commit("Source and dependency change")
	write("contrib/a/nested/new.go", "Nested change")
	write("contrib/a/test/go.mod", "module example.com/test")
	write("contrib/a/test/test.go", "Test change")
	shared := commit("Change parent and nested module")
	git("tag", "contrib/a/v9.0.0-rc.1")
	write("contrib/new/go.mod", "module example.com/new")
	write("contrib/new/CHANGELOG.md", "Unreleased")
	newModule := commit("Add new module")
	git("mv", "contrib/a/old.go", "contrib/a/nested/moved.go")
	renamed := commit("Move between modules")
	write("contrib/a/nested/only.go", "Nested only change")
	nestedOnly := commit("Change only nested module")
	git("rm", "-r", "contrib/deleted")
	commit("Remove module")
	mainBranch := git("branch", "--show-current")
	git("branch", "unrelated")
	write("main.txt", "Main branch change outside contrib")
	commit("Main branch change outside contrib")
	git("switch", "unrelated")
	write("side.txt", "Side branch change outside contrib")
	commit("Side branch change outside contrib")
	git("switch", mainBranch)
	git("merge", "--no-ff", "-m", "Merge unrelated changes", "unrelated")
	w := worktree{eff: eff, target: sdkTarget(), version: semver{1, 1, 0}, worktreeRoot: root}
	body, err := w.draftPRBody()
	if err != nil {
		t.Fatal(err)
	}
	entry := func(sha, subject string) string {
		return "  - [" + sha[:7] + "](https://github.com/temporalio/sdk-go/commit/" + sha + ") " + subject
	}
	testEqual(t, body, "Prepare release 1.1.0\n\n### Contrib packages with changes since their last release:\n\n**NOTE:** Dependency-only changes (go.mod and go.sum) are excluded. These updates may still require a contrib release.\n\n"+
		"Consider releasing these packages after this Go SDK release.\n\n"+
		"- `contrib/a` (since `contrib/a/v1.0.0`)\n"+
		entry(renamed, "Move between modules")+"\n"+
		entry(shared, "Change parent and nested module")+"\n"+
		entry(mixed, "Source and dependency change")+"\n"+
		entry(older, "Unreleased before SDK release")+"\n"+
		"- `contrib/a/nested` (since `contrib/a/nested/v1.0.0`)\n"+
		entry(nestedOnly, "Change only nested module")+"\n"+
		entry(renamed, "Move between modules")+"\n"+
		entry(shared, "Change parent and nested module")+"\n"+
		"- `contrib/new` (Never released)\n"+entry(newModule, "Add new module"))

	git("update-ref", "refs/remotes/origin/main", "HEAD")
	write("contrib/b/local.go", "Local branch change")
	commit("Local branch only")
	summary, err := w.contribSummary("origin/main")
	if err != nil {
		t.Fatal(err)
	}
	_, want, _ := strings.Cut(body, "Consider releasing these packages after this Go SDK release.\n\n")
	testEqual(t, summary, want)
}

func TestDraftPRBodyWithoutContribChanges(t *testing.T) {
	a, _ := newMockApp(app{target: sdkTarget(), version: semver{1, 2, 3}}, nil)
	body, err := a.worktreeAt("/worktree", "release").draftPRBody()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, body, "Prepare release 1.2.3\n\n### Contrib packages with changes since their last release:\n\n**NOTE:** Dependency-only changes (go.mod and go.sum) are excluded. These updates may still require a contrib release.\n\n"+
		"Consider releasing these packages after this Go SDK release.\n\nNone.")
}

func TestDraftPRBodyForContrib(t *testing.T) {
	a, eff := newMockApp(app{target: contribEnvconfig(t), version: semver{1, 2, 3}}, nil)
	body, err := a.worktreeAt("/worktree", "release").draftPRBody()
	if err != nil {
		t.Fatal(err)
	}
	testEqual(t, body, "Prepare contrib/envconfig release 1.2.3")
	testEqual(t, eff.commands.String(), "")
}

func TestDraftPRBodyGitFailures(t *testing.T) {
	for _, command := range []string{"git tag", "git ls-tree", "git log"} {
		t.Run(command, func(t *testing.T) {
			failure := errors.New("git failed")
			a, _ := newMockApp(app{target: sdkTarget()}, func(cmd string) (string, error) {
				if strings.HasPrefix(cmd, command) {
					return "", failure
				}
				if strings.HasPrefix(cmd, "git ls-tree") {
					return "contrib/a/go.mod\x00contrib/a/CHANGELOG.md\x00", nil
				}
				return "", nil
			})
			_, err := a.worktreeAt("/worktree", "release").draftPRBody()
			if !errors.Is(err, failure) {
				t.Fatalf("expected Git failure, got %v", err)
			}
		})
	}
}

func TestContribCheck(t *testing.T) {
	a, eff := newMockApp(app{}, func(cmd string) (string, error) {
		switch cmd {
		case "git tag --list contrib/*":
			return "contrib/a/v1.0.0\n", nil
		case "git ls-tree -r --name-only -z origin/main -- contrib/":
			return "contrib/a/go.mod\x00contrib/a/CHANGELOG.md\x00", nil
		case "git log --full-history --topo-order --no-renames --format=%H%x09%s contrib/a/v1.0.0..origin/main -- contrib/a/ :(exclude,glob)contrib/a/**/go.mod :(exclude,glob)contrib/a/**/go.sum":
			return "abcdef0123456789\tUnreleased change\n", nil
		}
		return "", nil
	})
	if err := a.checkContrib(); err != nil {
		t.Fatal(err)
	}
	testEqual(t, eff.commands.String(), `
		/repo: git fetch --tags origin main
		/repo: git tag --list contrib/*
		/repo: git ls-tree -r --name-only -z origin/main -- contrib/
		/repo: git log --full-history --topo-order --no-renames --format=%H%x09%s contrib/a/v1.0.0..origin/main -- contrib/a/ :(exclude,glob)contrib/a/**/go.mod :(exclude,glob)contrib/a/**/go.sum
	`)
	if !strings.Contains(eff.output.String(), "- `contrib/a` (since `contrib/a/v1.0.0`)\n  - [abcdef0]") {
		t.Fatalf("missing contrib summary: %s", eff.output.String())
	}
	if !strings.Contains(eff.output.String(), "**NOTE:** Dependency-only changes (go.mod and go.sum) are excluded. These updates may still require a contrib release.") {
		t.Fatalf("missing dependency warning: %s", eff.output.String())
	}
	if len(eff.files) != 0 {
		t.Fatalf("unexpected file writes: %v", eff.files)
	}
}

func TestContribCheckFetchFailure(t *testing.T) {
	failure := errors.New("fetch failed")
	a, eff := newMockApp(app{}, func(string) (string, error) { return "", failure })
	if err := a.checkContrib(); !errors.Is(err, failure) {
		t.Fatalf("expected fetch failure, got %v", err)
	}
	testEqual(t, eff.commands.String(), "/repo: git fetch --tags origin main")
}

func TestContribCheckRejectsArguments(t *testing.T) {
	err := run(io.Discard, io.Discard, []string{"contrib-check", "1.2.3"})
	if err == nil || !strings.Contains(err.Error(), "contrib-check takes no arguments") {
		t.Fatalf("expected contrib-check usage error, got %v", err)
	}
}
