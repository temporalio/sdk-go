package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	maxOutcomeArtifactBytes = 256 * 1024
	maxOutcomeRecordBytes   = 16 * 1024
)

type apiArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Expired     bool   `json:"expired"`
	SizeInBytes int64  `json:"size_in_bytes"`
	CreatedAt   string `json:"created_at"`
	WorkflowRun *struct {
		HeadBranch string `json:"head_branch"`
	} `json:"workflow_run"`
}

type outcomeCollection struct {
	SchemaVersion string `json:"schema_version"`
	CollectedAt   string `json:"collected_at"`
	Candidates    int    `json:"candidates"`
	Valid         int    `json:"valid"`
	Invalid       int    `json:"invalid"`
}

type collectOutcomesOptions struct {
	Repository    string
	Branch        string
	Prefix        string
	MaxCandidates int
	Output        string
}

func runCollectOutcomes(args []string) error {
	flags := flag.NewFlagSet("collect-outcomes", flag.ContinueOnError)
	repository := requiredString(flags, "repository", "OWNER/REPOSITORY")
	branch := requiredString(flags, "branch", "workflow branch")
	prefix := requiredString(flags, "prefix", "artifact name prefix")
	maxCandidates := flags.Int("max-candidates", 0, "maximum candidate artifacts")
	output := requiredString(flags, "output", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"repository": *repository,
		"branch":     *branch,
		"prefix":     *prefix,
		"output":     *output,
	}); err != nil {
		return err
	}
	if *maxCandidates < 1 || *maxCandidates > 200 {
		return fmt.Errorf("--max-candidates must be between 1 and 200")
	}
	if !validRepository(*repository) {
		return fmt.Errorf("repository must be OWNER/REPOSITORY")
	}
	if len(*branch) > 255 {
		return fmt.Errorf("--branch exceeds 255 characters")
	}
	if len(*prefix) > 100 {
		return fmt.Errorf("--prefix exceeds 100 characters")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("GH_TOKEN is required")
	}
	client := githubClient{token: token, client: defaultHTTPClient()}
	status, err := collectOutcomes(
		context.Background(),
		client,
		collectOutcomesOptions{
			Repository:    *repository,
			Branch:        *branch,
			Prefix:        *prefix,
			MaxCandidates: *maxCandidates,
			Output:        *output,
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf(
		"Collected %d valid prior outcomes; skipped %d invalid outcomes.\n",
		status.Valid,
		status.Invalid,
	)
	return nil
}

func defaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Minute}
}

func collectOutcomes(
	ctx context.Context,
	client githubClient,
	options collectOutcomesOptions,
) (outcomeCollection, error) {
	if err := os.MkdirAll(options.Output, 0o700); err != nil {
		return outcomeCollection{}, err
	}
	allArtifacts, err := listOutcomeArtifacts(ctx, client, options.Repository)
	if err != nil {
		return outcomeCollection{}, err
	}
	artifacts := selectOutcomeArtifacts(
		allArtifacts,
		options.Branch,
		options.Prefix,
		options.MaxCandidates,
	)
	status := outcomeCollection{
		SchemaVersion: "1",
		CollectedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Candidates:    len(artifacts),
	}
	var records []outcomeRecord
	for _, artifact := range artifacts {
		record, err := downloadOutcomeRecord(
			ctx,
			client,
			options.Repository,
			artifact,
		)
		if err != nil {
			status.Invalid++
			fmt.Fprintf(
				os.Stderr,
				"Skipped outcome artifact %d: %s\n",
				artifact.ID,
				sanitizeText(err.Error(), 800),
			)
			continue
		}
		records = append(records, record)
		status.Valid++
	}
	if len(records) > 0 {
		candidates, err := listDedupCandidates(ctx, client, options.Repository)
		if err != nil {
			return outcomeCollection{}, err
		}
		for index := range records {
			linkHumanAuthoredPR(&records[index], candidates)
		}
	}
	for _, record := range records {
		name := fmt.Sprintf(
			"%d-%d.json",
			record.WorkflowRunID,
			record.WorkflowRunAttempt,
		)
		if err := writeJSON(filepath.Join(options.Output, name), record); err != nil {
			return outcomeCollection{}, err
		}
	}
	if err := writeJSON(filepath.Join(options.Output, "collection.json"), status); err != nil {
		return outcomeCollection{}, err
	}
	return status, nil
}

func linkHumanAuthoredPR(
	record *outcomeRecord,
	candidates []dedupCandidate,
) {
	if !record.ManualFixHandoffProduced ||
		record.NormalizedSignatureHash == nil ||
		record.LinkedHumanPRURL != nil {
		return
	}
	for _, candidate := range candidates {
		if candidate.Kind != "pull-request" ||
			!safeGitHubURL(candidate.HTMLURL) ||
			!strings.Contains(candidate.Body, *record.NormalizedSignatureHash) ||
			!strings.Contains(candidate.Body, record.WorkflowRunURL) {
			continue
		}
		record.LinkedHumanPRURL = pointer(candidate.HTMLURL)
		return
	}
}

func listOutcomeArtifacts(
	ctx context.Context,
	client githubClient,
	repository string,
) ([]apiArtifact, error) {
	var artifacts []apiArtifact
	for page := 1; page <= 3; page++ {
		var payload struct {
			Artifacts []apiArtifact `json:"artifacts"`
		}
		apiPath := fmt.Sprintf(
			"repos/%s/actions/artifacts?per_page=100&page=%d",
			repository,
			page,
		)
		if err := client.getJSON(ctx, apiPath, &payload); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, payload.Artifacts...)
		if len(payload.Artifacts) < 100 {
			break
		}
	}
	return artifacts, nil
}

func selectOutcomeArtifacts(
	artifacts []apiArtifact,
	branch string,
	prefix string,
	maxCandidates int,
) []apiArtifact {
	selected := make([]apiArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Expired ||
			artifact.SizeInBytes > maxOutcomeArtifactBytes ||
			!strings.HasPrefix(artifact.Name, prefix) ||
			artifact.WorkflowRun == nil ||
			artifact.WorkflowRun.HeadBranch != branch {
			continue
		}
		selected = append(selected, artifact)
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return selected[left].CreatedAt > selected[right].CreatedAt
	})
	if len(selected) > maxCandidates {
		selected = selected[:maxCandidates]
	}
	return selected
}

func downloadOutcomeRecord(
	ctx context.Context,
	client githubClient,
	repository string,
	artifact apiArtifact,
) (outcomeRecord, error) {
	archive, err := client.getBytes(
		ctx,
		fmt.Sprintf(
			"repos/%s/actions/artifacts/%d/zip",
			repository,
			artifact.ID,
		),
		maxOutcomeArtifactBytes,
		"application/vnd.github+json",
	)
	if err != nil {
		return outcomeRecord{}, err
	}
	if len(archive) > maxOutcomeArtifactBytes {
		return outcomeRecord{}, fmt.Errorf("downloaded archive exceeded the artifact size limit")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return outcomeRecord{}, err
	}
	names := make([]string, 0, len(reader.File))
	files := make(map[string]*zip.File, len(reader.File))
	for _, file := range reader.File {
		names = append(names, file.Name)
		files[file.Name] = file
	}
	entry, err := selectOutcomeEntry(names)
	if err != nil {
		return outcomeRecord{}, err
	}
	file := files[entry]
	if file.UncompressedSize64 > maxOutcomeRecordBytes {
		return outcomeRecord{}, fmt.Errorf("outcome record exceeded the size limit")
	}
	stream, err := file.Open()
	if err != nil {
		return outcomeRecord{}, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, maxOutcomeRecordBytes+1))
	if err != nil {
		return outcomeRecord{}, err
	}
	if len(data) > maxOutcomeRecordBytes {
		return outcomeRecord{}, fmt.Errorf("outcome record exceeded the size limit")
	}
	var record outcomeRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return outcomeRecord{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return outcomeRecord{}, fmt.Errorf("outcome record contains trailing JSON")
	}
	if errors := validateOutcomeRecord(record); len(errors) > 0 {
		return outcomeRecord{}, fmt.Errorf("%s", strings.Join(errors, "; "))
	}
	return record, nil
}

func selectOutcomeEntry(entries []string) (string, error) {
	var matches []string
	for _, entry := range entries {
		segments := strings.Split(entry, "/")
		if len(entry) > 300 ||
			strings.HasPrefix(entry, "/") ||
			slices.Contains(segments, "..") ||
			segments[len(segments)-1] != "outcome.json" {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("artifact must contain exactly one safe outcome.json entry")
	}
	return matches[0], nil
}
