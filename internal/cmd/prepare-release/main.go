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
	"strconv"
	"strings"
	"time"
)

var (
	// Matches release versions such as "1.48.0".
	versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	// Matches tagged Go module release versions such as "v1.48.0".
	// Shouldn't match pseudo-versions such as "v1.48.0-0.20260804123456-abcdef123456".
	taggedGoVersionRE = regexp.MustCompile(`^v[0-9]\.[0-9]+\.[0-9]+$`)
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
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// run parses command-line options and executes the release workflow.
func run(args []string) error {
	flags := flag.NewFlagSet("prepare-release", flag.ContinueOnError)
	date := flags.String("date", time.Now().Format(time.DateOnly), "release date in YYYY-MM-DD format")
	dryRun := flags.Bool("dry-run", false, "print file updates and commands without executing them")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: prepare-release [--date YYYY-MM-DD] [--dry-run] VERSION")
	}
	version, err := validateVersion(flags.Arg(0))
	if err != nil {
		return err
	}
	releaseDate, err := time.Parse(time.DateOnly, *date)
	if err != nil {
		return fmt.Errorf("invalid release date %q; expected YYYY-MM-DD: %w", *date, err)
	}

	var eff Effects = RealWorld{}
	if *dryRun {
		tempDir, err := os.MkdirTemp("", "prepare-release-")
		if err != nil {
			return fmt.Errorf("create dry-run directory: %w", err)
		}
		eff = DryRun{Output: os.Stdout, TempDir: tempDir}
	}
	root, err := eff.repoRoot()
	if err != nil {
		return err
	}
	if err := prepareRelease(eff, root, version, releaseDate); err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("Dry run completed for release %s dated %s; no changes were made\n", version, releaseDate.Format(time.DateOnly))
	} else {
		fmt.Printf("Prepared release %s dated %s and opened a PR\n", version, releaseDate.Format(time.DateOnly))
	}
	return nil
}

// CORE LOGIC

func prepareRelease(eff Effects, root, version string, releaseDate time.Time) error {

	// Validate git state and create a release branch

	if err := ensureCleanWorktree(eff, root); err != nil {
		return err
	}
	if _, err := eff.runCommand(root, "git", "fetch", "origin", "main"); err != nil {
		return err
	}
	branch := "chore/release-" + version
	if _, err := eff.runCommand(root, "git", "switch", "--create", branch, "origin/main"); err != nil {
		return err
	}

	// Validate and update files

	goMod, err := eff.readFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fmt.Errorf("read go.mod: %w", err)
	}
	if err := validateGoMod(goMod); err != nil {
		return err
	}
	changelogPath := filepath.Join(root, "CHANGELOG.md")
	if err := eff.updateFile(changelogPath, func(text string) (string, error) {
		return updateChangelog(text, version, releaseDate)
	}); err != nil {
		return err
	}
	versionPath := filepath.Join(root, "internal", "version.go")
	if err := eff.updateFile(versionPath, func(text string) (string, error) {
		return replaceSDKVersion(text, version)
	}); err != nil {
		return err
	}

	// Commit and push changes, then open a PR

	commitArgs := append([]string{"commit", "-m", "Prepare release " + version, "--"}, releaseFiles...)
	if _, err := eff.runCommand(root, "git", commitArgs...); err != nil {
		return err
	}
	if _, err := eff.runCommand(root, "git", "push", "--set-upstream", "origin", branch); err != nil {
		return err
	}
	if _, err := eff.runCommand(root, "gh", "pr", "create", "--base", "main", "--head", branch,
		"--title", "Prepare release "+version, "--body", "Prepare Go SDK release "+version+"."); err != nil {
		return err
	}
	return nil
}

// PURE HELPERS

// validateVersion accepts release versions with a semantic version core.
func validateVersion(version string) (string, error) {
	if !versionRE.MatchString(version) {
		return "", fmt.Errorf("invalid version %q; expected a version like '1.48.0'", version)
	}
	return version, nil
}

// validateGoMod requires go.temporal.io/api to use a tagged version (1.XX.YY format)
// instead of a git snapshot (1.XX.YY-0.YYYYMMDDHHMMSS-abcdef123456 format).
func validateGoMod(data string) error {
	match := apiVersionRE.FindStringSubmatch(data)
	if match == nil {
		return errors.New("could not find go.temporal.io/api in go.mod")
	}
	if !taggedGoVersionRE.MatchString(match[1]) {
		return fmt.Errorf("go.temporal.io/api must use an official release, found %q", match[1])
	}
	return nil
}

// replaceSDKVersion updates the sole SDKVersion declaration in version.go.
func replaceSDKVersion(text, version string) (string, error) {
	if _, err := validateVersion(version); err != nil {
		return "", err
	}
	if len(sdkVersionRE.FindAllStringIndex(text, -1)) != 1 {
		return "", errors.New("could not find exactly one SDKVersion declaration in internal/version.go")
	}
	return sdkVersionRE.ReplaceAllString(text, "${1}"+version+"${2}"), nil
}

// updateChangelog moves Unreleased entries into a dated version section.
func updateChangelog(text, version string, releaseDate time.Time) (string, error) {
	if _, err := validateVersion(version); err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if _, _, _, ok := findVersionSection(lines, version); ok {
		return "", fmt.Errorf("changelog already has a section for %q", version)
	}
	heading, start, end, ok := findVersionSection(lines, "Unreleased")
	if !ok {
		return "", errors.New("could not find changelog section for 'Unreleased'")
	}
	unreleased := stripEmptyChangelogHeaders(stripOuterBlankLines(lines[start:end]))
	if len(unreleased) == 0 {
		return "", errors.New("changelog section for 'Unreleased' appears to be empty")
	}
	next := append([]string{}, lines[:heading]...)
	next = append(next, seededUnreleasedLines()...)
	next = append(next, "## ["+version+"] - "+releaseDate.Format(time.DateOnly), "")
	next = append(next, unreleased...)
	next = append(next, "")
	next = append(next, lines[end:]...)
	return strings.Join(collapseBlankLines(next), "\n") + "\n", nil
}

// findVersionSection returns the heading and content bounds for a changelog version.
func findVersionSection(lines []string, version string) (heading, start, end int, ok bool) {
	for i, line := range lines {
		match := changelogHeadingRE.FindStringSubmatch(line)
		if match == nil || match[1] != version {
			continue
		}
		end = len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") {
				end = j
				break
			}
		}
		return i, i + 1, end, true
	}
	return 0, 0, 0, false
}

func seededUnreleasedLines() []string {
	lines := []string{"## [Unreleased]", ""}
	for _, header := range changelogHeaders {
		lines = append(lines, "### "+header, "")
	}
	return lines
}

// stripEmptyChangelogHeaders removes recognized sections that contain no content.
func stripEmptyChangelogHeaders(lines []string) []string {
	var filtered []string
	for i := 0; i < len(lines); {
		match := changelogHeaderRE.FindStringSubmatch(lines[i])
		if match == nil || !contains(changelogHeaders, match[1]) {
			filtered = append(filtered, lines[i])
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "### ") {
			j++
		}
		content := lines[i+1 : j]
		if hasNonblank(content) {
			filtered = append(filtered, lines[i])
			filtered = append(filtered, content...)
		}
		i = j
	}
	return stripOuterBlankLines(filtered)
}

func stripOuterBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// collapseBlankLines reduces consecutive blank lines and trims the outer ones.
func collapseBlankLines(lines []string) []string {
	var collapsed []string
	previousBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && previousBlank {
			continue
		}
		collapsed = append(collapsed, line)
		previousBlank = blank
	}
	return stripOuterBlankLines(collapsed)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func hasNonblank(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// formatCommand renders a command with quoting suitable for logs and dry runs.
func formatCommand(name string, args ...string) string {
	parts := []string{name}
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			arg = strconv.Quote(arg)
		}
		parts = append(parts, arg)
	}
	return strings.Join(parts, " ")
}

// EFFECTFUL HELPERS

// changedFiles returns paths reported as changed by git status --porcelain.
func changedFiles(eff Effects, root string) ([]string, error) {
	output, err := eff.runCommand(root, "git", "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if len(line) >= 4 {
			files = append(files, line[3:])
		}
	}
	return files, nil
}

func ensureCleanWorktree(eff Effects, root string) error {
	files, err := changedFiles(eff, root)
	if err != nil {
		return err
	}
	if len(files) != 0 {
		return fmt.Errorf("release preparation requires a clean worktree; found changes in %s", strings.Join(files, ", "))
	}
	return nil
}
