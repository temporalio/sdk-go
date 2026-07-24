package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxPatchBytes = 2 * 1024 * 1024

var protectedPaths = []string{
	".github/ci-flake/",
	".github/workflows/ci-flake-investigator.yml",
	"AGENTS.md",
	"CLAUDE.md",
}

func runCapturePatch(args []string) error {
	flags := flag.NewFlagSet("capture-patch", flag.ContinueOnError)
	output := requiredString(flags, "output", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{"output": *output}); err != nil {
		return err
	}
	metadata, err := capturePatch(*output)
	if err != nil {
		return err
	}
	fmt.Printf(
		"Captured %d changed files and %d patch bytes.\n",
		len(metadata.Files),
		metadata.Bytes,
	)
	return nil
}

func capturePatch(output string) (changedFiles, error) {
	if err := os.MkdirAll(output, 0o700); err != nil {
		return changedFiles{}, err
	}
	if _, err := gitOutput(
		"add", "-N", "--", ".", ":(exclude).ci-flake-runtime/**",
	); err != nil {
		return changedFiles{}, err
	}
	rawFiles, err := gitOutput(
		"diff", "--no-ext-diff", "--name-only", "-z", "HEAD", "--",
		".", ":(exclude).ci-flake-runtime/**",
	)
	if err != nil {
		return changedFiles{}, err
	}
	files := splitNUL(rawFiles)
	patch, err := gitOutput(
		"diff", "--no-ext-diff", "--binary", "--full-index", "HEAD", "--",
		".", ":(exclude).ci-flake-runtime/**",
	)
	if err != nil {
		return changedFiles{}, err
	}
	metadata := changedFiles{
		SchemaVersion:  "1",
		Files:          nonNilSlice(files),
		ProtectedFiles: nonNilSlice(findProtectedFiles(files)),
		Bytes:          len(patch),
		HasChanges:     len(patch) > 0,
		Oversized:      len(patch) > maxPatchBytes,
	}
	if err := os.WriteFile(filepath.Join(output, "candidate.patch"), patch, 0o600); err != nil {
		return changedFiles{}, err
	}
	if err := writeJSON(filepath.Join(output, "changed-files.json"), metadata); err != nil {
		return changedFiles{}, err
	}
	if metadata.Oversized {
		return metadata, fmt.Errorf("candidate patch exceeds %d bytes", maxPatchBytes)
	}
	if len(metadata.ProtectedFiles) > 0 {
		return metadata, fmt.Errorf(
			"candidate patch modifies protected files: %s",
			strings.Join(metadata.ProtectedFiles, ", "),
		)
	}
	return metadata, nil
}

func gitOutput(args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Env = append(
		os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return nil, fmt.Errorf(
			"git %s failed: %s",
			strings.Join(args, " "),
			sanitizeText(string(exitError.Stderr), 800),
		)
	}
	return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
}

func splitNUL(value []byte) []string {
	parts := strings.Split(string(value), "\x00")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func findProtectedFiles(files []string) []string {
	var protected []string
	for _, file := range files {
		for _, protectedPath := range protectedPaths {
			if strings.HasSuffix(protectedPath, "/") && strings.HasPrefix(file, protectedPath) ||
				file == protectedPath {
				protected = append(protected, file)
				break
			}
		}
	}
	return protected
}
