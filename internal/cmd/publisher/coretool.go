package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/internal/cmd/publisher/pure"
)

// coreTool is a handle to the changelog-release-notes tool in sdk-rust.
// The tool is used to validate and assemble changelog fragments into a release.
type coreTool struct {
	eff      effects
	checkout string
}

// sdkRustRevision is the pinned sdk-rust revision located in ./sdk-rust-revision.
//
//go:embed sdk-rust-revision
var sdkRustRevision string

// newCoreTool locates sdk-rust, verifies its pinned revision, and builds and starts
// the tool with --help before release work begins.
func newCoreTool(eff effects, repoRoot string) (*coreTool, error) {
	checkout := os.Getenv("PUBLISHER_SDK_RUST")
	if checkout == "" {
		checkout = filepath.Join(filepath.Dir(repoRoot), "sdk-rust")
	}
	checkout, err := filepath.Abs(checkout)
	if err != nil {
		return nil, fmt.Errorf("resolve sdk-rust checkout %q: %w", checkout, err)
	}
	revision, err := eff.runCommand(checkout, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("locate sdk-rust checkout (set PUBLISHER_SDK_RUST): %w", err)
	}
	if strings.TrimSpace(revision) != strings.TrimSpace(sdkRustRevision) {
		return nil, fmt.Errorf("sdk-rust checkout %s must be at %s, found %s",
			checkout, strings.TrimSpace(sdkRustRevision), strings.TrimSpace(revision))
	}
	tool := &coreTool{eff: eff, checkout: checkout}
	if _, err := tool.run(repoRoot, "--help"); err != nil {
		return nil, fmt.Errorf("build and start changelog-tool (requires Rust and Cargo): %w", err)
	}
	return tool, nil
}

// prepareChangelog validates the module's changelog fragment layout (e.g.
// checking for unexpected subdirectories, non-markdown files, etc) and
// moves them into the module's CHANGELOG.md with the given date and version.
func (t *coreTool) prepareChangelog(root string, module pure.VersionedModule, date time.Time) error {
	_, err := t.run(root, "prepare",
		"--version", module.Version.String(), "--date", date.Format(time.DateOnly),
		"--changelog", module.Module.Changelog(""), "--fragments", module.Module.Fragments(""))
	return err
}

// validateReleaseChangelog checks that the module's CHANGELOG.md is well-formed and
// ends with a release entry for the given version and date.
func (t *coreTool) validateReleaseChangelog(root string, module pure.VersionedModule, date time.Time) error {
	_, err := t.run(root, "release-notes",
		"--version", module.Version.String(), "--date", date.Format(time.DateOnly),
		"--changelog", module.Module.Changelog(""))
	return err
}

// readReleaseNotes generates release notes from the module by reading its
// CHANGELOG.md under the heading for the given version and date.
func (t *coreTool) readReleaseNotes(root string, module pure.VersionedModule) (string, error) {
	return t.run(root, "release-notes",
		"--version", module.Version.String(), "--changelog", module.Module.Changelog(""))
}

// run executes a shared changelog tool command through Cargo in the verified
// checkout, returning the tool's stdout.
func (t *coreTool) run(root string, command string, args ...string) (string, error) {
	cargoArgs := []string{
		"run", "--quiet", "--locked",
		"--manifest-path", filepath.Join(t.checkout, "crates/changelog-release-notes/Cargo.toml"),
		"--bin", "changelog-tool", "--", command, "--repo", root,
	}
	output, err := t.eff.runCommand(t.checkout, "cargo", append(cargoArgs, args...)...)
	if err != nil {
		return "", fmt.Errorf("changelog-tool %s: %w", command, err)
	}
	return output, nil
}
