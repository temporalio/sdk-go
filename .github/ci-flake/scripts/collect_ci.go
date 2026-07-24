package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const githubAPIRoot = "https://api.github.com"

var (
	includedEvents     = valueSet("push", "pull_request")
	failureConclusions = valueSet("failure", "timed_out")
)

type githubClient struct {
	token  string
	client *http.Client
}

type apiRun struct {
	ID             int64  `json:"id"`
	RunAttempt     int    `json:"run_attempt"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	Event          string `json:"event"`
	Status         string `json:"status"`
	Conclusion     string `json:"conclusion"`
	HTMLURL        string `json:"html_url"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	HeadBranch     string `json:"head_branch"`
	HeadSHA        string `json:"head_sha"`
	HeadRepository *struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Actor *struct {
		Login string `json:"login"`
	} `json:"actor"`
	PullRequests []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
		Head   *struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base *struct {
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_requests"`
}

type apiJob struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Status          string   `json:"status"`
	Conclusion      string   `json:"conclusion"`
	HTMLURL         string   `json:"html_url"`
	StartedAt       string   `json:"started_at"`
	CompletedAt     string   `json:"completed_at"`
	RunnerName      string   `json:"runner_name"`
	RunnerGroupName string   `json:"runner_group_name"`
	Labels          []string `json:"labels"`
	Steps           []struct {
		Number     int    `json:"number"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	} `json:"steps"`
}

type apiIssue struct {
	Number      int       `json:"number"`
	State       string    `json:"state"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	UpdatedAt   string    `json:"updated_at"`
	PullRequest *struct{} `json:"pull_request"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type collectCIOptions struct {
	Repository    string
	Workflow      string
	LookbackHours int
	MaxRuns       int
	MaxFailedJobs int
	MaxLogBytes   int
	Output        string
}

func runCollectCI(args []string) error {
	flags := flag.NewFlagSet("collect-ci", flag.ContinueOnError)
	repository := requiredString(flags, "repository", "OWNER/REPOSITORY")
	workflow := requiredString(flags, "workflow", "workflow name")
	lookbackHours := flags.Int("lookback-hours", 0, "lookback in hours")
	maxRuns := flags.Int("max-runs", 0, "maximum runs")
	maxFailedJobs := flags.Int("max-failed-jobs", 0, "maximum failed jobs")
	maxLogBytes := flags.Int("max-log-bytes", 0, "maximum log bytes")
	output := requiredString(flags, "output", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"repository": *repository,
		"workflow":   *workflow,
		"output":     *output,
	}); err != nil {
		return err
	}
	if *lookbackHours < 1 || *lookbackHours > 720 {
		return fmt.Errorf("--lookback-hours must be between 1 and 720")
	}
	if *maxRuns < 1 || *maxRuns > 500 {
		return fmt.Errorf("--max-runs must be between 1 and 500")
	}
	if *maxFailedJobs < 1 || *maxFailedJobs > 100 {
		return fmt.Errorf("--max-failed-jobs must be between 1 and 100")
	}
	if *maxLogBytes < 1 || *maxLogBytes > 64*1024*1024 {
		return fmt.Errorf("--max-log-bytes must be between 1 and 64 MiB")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("GH_TOKEN is required")
	}
	client := githubClient{token: token, client: &http.Client{Timeout: 2 * time.Minute}}
	result, err := collectCI(context.Background(), client, collectCIOptions{
		Repository:    *repository,
		Workflow:      *workflow,
		LookbackHours: *lookbackHours,
		MaxRuns:       *maxRuns,
		MaxFailedJobs: *maxFailedJobs,
		MaxLogBytes:   *maxLogBytes,
		Output:        *output,
	})
	if err != nil {
		return err
	}
	fmt.Printf(
		"Collected %d runs, %d failed jobs, and %d log bytes.\n",
		result.Coverage.RunsInspected,
		result.Coverage.FailedJobsInspected,
		result.Coverage.LogBytesInspected,
	)
	return nil
}

func collectCI(ctx context.Context, client githubClient, options collectCIOptions) (snapshot, error) {
	if !validRepository(options.Repository) {
		return snapshot{}, fmt.Errorf("repository must be OWNER/REPOSITORY")
	}
	logDir := filepath.Join(options.Output, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return snapshot{}, err
	}
	generatedAt := time.Now().UTC()
	cutoff := generatedAt.Add(-time.Duration(options.LookbackHours) * time.Hour)
	allRuns, err := listRecentRuns(ctx, client, options.Repository, cutoff)
	if err != nil {
		return snapshot{}, err
	}
	runs, limited := selectRuns(allRuns, options.Workflow, cutoff, options.MaxRuns)
	records := make([]runRecord, 0, len(runs))
	for _, run := range runs {
		records = append(records, makeRunRecord(run))
	}
	var limitsReached []string
	if limited {
		limitsReached = append(limitsReached, "max-runs")
	}

	failedJobsInspected := 0
	logBytesInspected := 0
	logErrors := 0
	for runIndex := range records {
		record := &records[runIndex]
		if !inSet(failureConclusions, record.Conclusion) {
			continue
		}
		jobs, err := listRunJobs(ctx, client, options.Repository, record.ID)
		if err != nil {
			return snapshot{}, err
		}
		for _, job := range jobs {
			if !inSet(failureConclusions, job.Conclusion) {
				continue
			}
			if failedJobsInspected >= options.MaxFailedJobs {
				addUnique(&limitsReached, "max-failed-jobs")
				break
			}
			jobResult := makeJobRecord(job)
			remaining := options.MaxLogBytes - logBytesInspected
			if remaining <= 0 {
				message := "Log byte limit reached before this job"
				jobResult.LogError = &message
				addUnique(&limitsReached, "max-log-bytes")
			} else {
				raw, err := client.getBytes(
					ctx,
					fmt.Sprintf("repos/%s/actions/jobs/%d/logs", options.Repository, job.ID),
					int64(remaining)+1,
					"text/plain",
				)
				if err != nil {
					message := truncate(err.Error(), 500)
					jobResult.LogError = &message
					logErrors++
					addUnique(&limitsReached, "log-unavailable")
				} else {
					truncated := len(raw) > remaining
					if truncated {
						raw = raw[:remaining]
						addUnique(&limitsReached, "max-log-bytes")
					}
					relativePath := fmt.Sprintf("logs/job-%d.txt", job.ID)
					if err := os.WriteFile(filepath.Join(options.Output, relativePath), raw, 0o600); err != nil {
						return snapshot{}, err
					}
					jobResult.LogPath = pointer(".ci-flake-runtime/input/" + relativePath)
					jobResult.LogBytes = len(raw)
					jobResult.LogTruncated = truncated
					logBytesInspected += len(raw)
				}
			}
			record.FailedJobs = append(record.FailedJobs, jobResult)
			failedJobsInspected++
		}
	}

	dedupCandidates, err := listDedupCandidates(ctx, client, options.Repository)
	if err != nil {
		return snapshot{}, err
	}
	repositoryLabels, err := listRepositoryLabels(ctx, client, options.Repository)
	if err != nil {
		return snapshot{}, err
	}
	failedRuns := 0
	for _, record := range records {
		if inSet(failureConclusions, record.Conclusion) {
			failedRuns++
		}
	}
	result := snapshot{
		SchemaVersion:     "1",
		GeneratedAt:       generatedAt.Format(time.RFC3339Nano),
		Repository:        options.Repository,
		WorkflowAllowlist: []string{options.Workflow},
		ExcludedWorkflows: []string{"CI flake investigator"},
		Search: snapshotSearch{
			Cutoff:         cutoff.Format(time.RFC3339Nano),
			LookbackHours:  options.LookbackHours,
			IncludedEvents: []string{"push", "pull_request"},
		},
		Limits: snapshotLimits{
			MaxRuns:       options.MaxRuns,
			MaxFailedJobs: options.MaxFailedJobs,
			MaxLogBytes:   options.MaxLogBytes,
		},
		Coverage: snapshotCoverage{
			RunsInspected:       len(records),
			FailedRunsInspected: failedRuns,
			FailedJobsInspected: failedJobsInspected,
			LogBytesInspected:   logBytesInspected,
			LogErrors:           logErrors,
			LimitsReached:       nonNilSlice(limitsReached),
		},
		Runs:             nonNilSlice(records),
		DedupCandidates:  nonNilSlice(dedupCandidates),
		RepositoryLabels: nonNilSlice(repositoryLabels),
	}
	if err := writeJSON(filepath.Join(options.Output, "snapshot.json"), result); err != nil {
		return snapshot{}, err
	}
	return result, nil
}

func selectRuns(all []apiRun, workflow string, cutoff time.Time, maxRuns int) ([]apiRun, bool) {
	var selected []apiRun
	for _, run := range all {
		createdAt, err := time.Parse(time.RFC3339, run.CreatedAt)
		if err != nil ||
			run.Name != workflow ||
			!inSet(includedEvents, run.Event) ||
			run.Status != "completed" ||
			createdAt.Before(cutoff) {
			continue
		}
		if run.Event != "pull_request" &&
			(run.HeadRepository == nil || run.HeadRepository.FullName != run.Repository.FullName) {
			continue
		}
		selected = append(selected, run)
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return selected[left].CreatedAt > selected[right].CreatedAt
	})
	limited := len(selected) > maxRuns
	if limited {
		selected = selected[:maxRuns]
	}
	return selected, limited
}

func makeRunRecord(run apiRun) runRecord {
	record := runRecord{
		ID:           run.ID,
		Attempt:      run.RunAttempt,
		Name:         run.Name,
		Path:         run.Path,
		Event:        run.Event,
		Status:       run.Status,
		Conclusion:   run.Conclusion,
		HTMLURL:      run.HTMLURL,
		CreatedAt:    run.CreatedAt,
		UpdatedAt:    run.UpdatedAt,
		HeadBranch:   run.HeadBranch,
		HeadSHA:      run.HeadSHA,
		PullRequests: []pullRequestRecord{},
		FailedJobs:   []jobRecord{},
	}
	if run.HeadRepository != nil {
		record.HeadRepository = &run.HeadRepository.FullName
	}
	if run.Actor != nil {
		record.Actor = &run.Actor.Login
	}
	for _, pull := range run.PullRequests {
		item := pullRequestRecord{Number: pull.Number, URL: pull.URL}
		if pull.Head != nil {
			item.HeadSHA = &pull.Head.SHA
		}
		if pull.Base != nil {
			item.BaseSHA = &pull.Base.SHA
		}
		record.PullRequests = append(record.PullRequests, item)
	}
	return record
}

func makeJobRecord(job apiJob) jobRecord {
	record := jobRecord{
		ID:              job.ID,
		Name:            job.Name,
		Status:          job.Status,
		Conclusion:      job.Conclusion,
		HTMLURL:         job.HTMLURL,
		StartedAt:       job.StartedAt,
		CompletedAt:     job.CompletedAt,
		RunnerName:      job.RunnerName,
		RunnerGroupName: job.RunnerGroupName,
		Labels:          nonNilSlice(job.Labels),
		Steps:           []stepRecord{},
	}
	for _, step := range job.Steps {
		record.Steps = append(record.Steps, stepRecord{
			Number:     step.Number,
			Name:       step.Name,
			Status:     step.Status,
			Conclusion: step.Conclusion,
		})
	}
	return record
}

func listRecentRuns(ctx context.Context, client githubClient, repository string, cutoff time.Time) ([]apiRun, error) {
	var runs []apiRun
	for page := 1; page <= 5; page++ {
		query := url.Values{
			"status":   {"completed"},
			"created":  {">=" + cutoff.Format(time.RFC3339)},
			"per_page": {"100"},
			"page":     {fmt.Sprint(page)},
		}
		var payload struct {
			WorkflowRuns []apiRun `json:"workflow_runs"`
		}
		if err := client.getJSON(ctx, "repos/"+repository+"/actions/runs?"+query.Encode(), &payload); err != nil {
			return nil, err
		}
		runs = append(runs, payload.WorkflowRuns...)
		if len(payload.WorkflowRuns) < 100 {
			break
		}
	}
	return runs, nil
}

func listRunJobs(ctx context.Context, client githubClient, repository string, runID int64) ([]apiJob, error) {
	var jobs []apiJob
	for page := 1; page <= 3; page++ {
		var payload struct {
			Jobs []apiJob `json:"jobs"`
		}
		apiPath := fmt.Sprintf(
			"repos/%s/actions/runs/%d/jobs?filter=all&per_page=100&page=%d",
			repository,
			runID,
			page,
		)
		if err := client.getJSON(ctx, apiPath, &payload); err != nil {
			return nil, err
		}
		jobs = append(jobs, payload.Jobs...)
		if len(payload.Jobs) < 100 {
			break
		}
	}
	return jobs, nil
}

func listDedupCandidates(ctx context.Context, client githubClient, repository string) ([]dedupCandidate, error) {
	var issues []apiIssue
	if err := client.getJSON(
		ctx,
		"repos/"+repository+"/issues?state=all&sort=updated&direction=desc&per_page=100",
		&issues,
	); err != nil {
		return nil, err
	}
	candidates := make([]dedupCandidate, 0, len(issues))
	for _, issue := range issues {
		kind := "issue"
		if issue.PullRequest != nil {
			kind = "pull-request"
		}
		labels := make([]string, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			labels = append(labels, truncate(label.Name, 100))
		}
		candidates = append(candidates, dedupCandidate{
			Number:    issue.Number,
			Kind:      kind,
			State:     issue.State,
			Title:     truncate(issue.Title, 500),
			Body:      truncate(issue.Body, 14000),
			HTMLURL:   issue.HTMLURL,
			UpdatedAt: issue.UpdatedAt,
			Labels:    nonNilSlice(labels),
		})
	}
	return candidates, nil
}

func listRepositoryLabels(ctx context.Context, client githubClient, repository string) ([]string, error) {
	var labels []apiLabel
	for page := 1; page <= 3; page++ {
		var payload []apiLabel
		apiPath := fmt.Sprintf(
			"repos/%s/labels?per_page=100&page=%d",
			repository,
			page,
		)
		if err := client.getJSON(ctx, apiPath, &payload); err != nil {
			return nil, err
		}
		labels = append(labels, payload...)
		if len(payload) < 100 {
			break
		}
	}
	result := make([]string, 0, len(labels))
	for _, label := range labels {
		result = append(result, truncate(label.Name, 100))
	}
	return result, nil
}

func (client githubClient) getJSON(ctx context.Context, path string, value any) error {
	data, err := client.getBytes(ctx, path, 16*1024*1024, "application/vnd.github+json")
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func (client githubClient) getBytes(ctx context.Context, path string, maxBytes int64, accept string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIRoot+"/"+strings.TrimPrefix(path, "/"), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "sdk-go-ci-flake-investigator")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API %s returned %d", path, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return data, nil
	}
	return data, nil
}

func validRepository(repository string) bool {
	parts := strings.Split(repository, "/")
	return len(parts) == 2 && validRepositoryPart(parts[0]) && validRepositoryPart(parts[1])
}

func validRepositoryPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') &&
			!(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') &&
			character != '_' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}

func addUnique(values *[]string, value string) {
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func nonNilSlice[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
