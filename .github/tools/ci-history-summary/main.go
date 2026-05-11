package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWorkflowFile  = "ci.yml"
	defaultEvent         = "push"
	defaultMaxFailedJobs = 20
	apiVersion           = "2022-11-28"
	userAgent            = "temporalio-sdk-go-ci-history"
)

type config struct {
	token        string
	repository   string
	branch       string
	summaryPath  string
	workflowFile string
	event        string
	serverURL    string
	runID        string
	maxFailed    int
}

type apiClient struct {
	root       string
	token      string
	httpClient *http.Client
}

type workflowRun struct {
	ID        int64  `json:"id"`
	HeadSHA   string `json:"head_sha"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type workflowRunsResponse struct {
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowJob struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Conclusion   string `json:"conclusion"`
	HTMLURL      string `json:"html_url"`
	CompletedAt  string `json:"completed_at"`
	RunID        int64
	RunSHA       string
	RunCreatedAt string
	RunUpdatedAt string
}

type workflowJobsResponse struct {
	Jobs []workflowJob `json:"jobs"`
}

type jobSummary struct {
	Name           string
	SevenRuns      int
	SevenFailures  int
	SevenPassRate  string
	ThirtyRuns     int
	ThirtyFailures int
	ThirtyPassRate string
	JobOrder       int
	OSOrder        int
	GoOrder        int
	FIPSOrder      int
}

type recentFailure struct {
	Name      string
	Completed string
	Line      string
}

func main() {
	ctx := context.Background()
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := run(ctx, cfg); err != nil {
		_ = appendErrorSummary(cfg.summaryPath, "See the CI History Summary job logs for details.")
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{
		token:        os.Getenv("GITHUB_TOKEN"),
		repository:   os.Getenv("GITHUB_REPOSITORY"),
		branch:       os.Getenv("CI_HISTORY_TARGET_BRANCH"),
		summaryPath:  os.Getenv("GITHUB_STEP_SUMMARY"),
		workflowFile: cmp.Or(os.Getenv("CI_HISTORY_WORKFLOW_FILE"), defaultWorkflowFile),
		event:        cmp.Or(os.Getenv("CI_HISTORY_EVENT"), defaultEvent),
		serverURL:    cmp.Or(os.Getenv("GITHUB_SERVER_URL"), "https://github.com"),
		runID:        os.Getenv("GITHUB_RUN_ID"),
		maxFailed:    defaultMaxFailedJobs,
	}

	for name, value := range map[string]string{
		"GITHUB_TOKEN":             cfg.token,
		"GITHUB_REPOSITORY":        cfg.repository,
		"CI_HISTORY_TARGET_BRANCH": cfg.branch,
		"GITHUB_STEP_SUMMARY":      cfg.summaryPath,
	} {
		if value == "" {
			return config{}, fmt.Errorf("%s is required", name)
		}
	}
	if !strings.Contains(cfg.repository, "/") {
		return config{}, fmt.Errorf("GITHUB_REPOSITORY must be owner/repo, got %q", cfg.repository)
	}
	if value := os.Getenv("CI_HISTORY_MAX_FAILED_JOBS"); value != "" {
		maxFailed, err := strconv.Atoi(value)
		if err != nil || maxFailed < 0 {
			return config{}, fmt.Errorf("CI_HISTORY_MAX_FAILED_JOBS must be a non-negative integer")
		}
		cfg.maxFailed = maxFailed
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config) error {
	owner, repo, _ := strings.Cut(cfg.repository, "/")
	client := apiClient{
		root:       fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo),
		token:      cfg.token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	since := time.Now().UTC().AddDate(0, 0, -30)
	runs, err := fetchWorkflowRuns(ctx, client, cfg, since)
	if err != nil {
		return err
	}

	var jobs []workflowJob
	for _, run := range runs {
		runJobs, err := fetchRunJobs(ctx, client, run)
		if err != nil {
			return err
		}
		jobs = append(jobs, runJobs...)
	}

	return appendSummary(cfg.summaryPath, renderSummary(cfg, jobs, time.Now().UTC()))
}

func fetchWorkflowRuns(ctx context.Context, client apiClient, cfg config, since time.Time) ([]workflowRun, error) {
	var runs []workflowRun
	currentRunID := cfg.runID

	for page := 1; ; page++ {
		query := url.Values{}
		query.Set("branch", cfg.branch)
		query.Set("event", cfg.event)
		query.Set("status", "completed")
		query.Set("created", ">="+since.Format(time.RFC3339))
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))

		path := fmt.Sprintf("/actions/workflows/%s/runs?%s", url.PathEscape(cfg.workflowFile), query.Encode())
		var response workflowRunsResponse
		if err := client.get(ctx, path, &response); err != nil {
			return nil, err
		}

		for _, run := range response.WorkflowRuns {
			if strconv.FormatInt(run.ID, 10) != currentRunID {
				runs = append(runs, run)
			}
		}
		if len(response.WorkflowRuns) < 100 {
			break
		}
	}

	return runs, nil
}

func fetchRunJobs(ctx context.Context, client apiClient, run workflowRun) ([]workflowJob, error) {
	var jobs []workflowJob
	for page := 1; ; page++ {
		path := fmt.Sprintf("/actions/runs/%d/jobs?per_page=100&page=%d", run.ID, page)
		var response workflowJobsResponse
		if err := client.get(ctx, path, &response); err != nil {
			return nil, err
		}

		for _, job := range response.Jobs {
			job.RunID = run.ID
			job.RunSHA = run.HeadSHA
			job.RunCreatedAt = run.CreatedAt
			job.RunUpdatedAt = cmp.Or(run.UpdatedAt, run.CreatedAt)
			jobs = append(jobs, job)
		}
		if len(response.Jobs) < 100 {
			break
		}
	}
	return jobs, nil
}

func (c apiClient) get(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.root+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github api GET %s failed: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode github api response for %s: %w", path, err)
	}
	return nil
}

func renderSummary(cfg config, jobs []workflowJob, now time.Time) string {
	rows := buildJobSummaries(jobs, now)
	var b strings.Builder
	fmt.Fprintf(&b, "## CI History for %s\n\n", cfg.branch)
	b.WriteString("## Job History\n\n")
	b.WriteString(renderJobStatsTable(rows))
	b.WriteString("\n\n## Most Failure-Prone Jobs, 30 days\n\n")
	b.WriteString(renderMostFailureProneJobs(rows))
	b.WriteString("\n\n## Recent Failed Jobs\n\n")
	b.WriteString(renderRecentFailedJobs(cfg, jobs))
	fmt.Fprintf(&b, "\n\n_Source: %s runs for `%s` on `%s`._\n", cfg.event, cfg.workflowFile, cfg.branch)
	return b.String()
}

func renderJobStatsTable(rows []jobSummary) string {
	if len(rows) == 0 {
		return "No completed jobs found in the last 30 days."
	}

	var b strings.Builder
	b.WriteString("| Job | 7d Runs | 7d Failures | 7d Pass Rate | 30d Runs | 30d Failures | 30d Pass Rate |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %d | %d | %s |\n",
			row.Name,
			row.SevenRuns,
			row.SevenFailures,
			row.SevenPassRate,
			row.ThirtyRuns,
			row.ThirtyFailures,
			row.ThirtyPassRate,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderMostFailureProneJobs(rows []jobSummary) string {
	failureRows := rows[:0]
	for _, row := range rows {
		if row.ThirtyFailures > 0 {
			failureRows = append(failureRows, row)
		}
	}
	rows = failureRows
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].ThirtyFailures != rows[j].ThirtyFailures {
			return rows[i].ThirtyFailures > rows[j].ThirtyFailures
		}
		return rows[i].Name < rows[j].Name
	})

	if len(rows) == 0 {
		return "No failed jobs found in the last 30 days."
	}

	var b strings.Builder
	b.WriteString("| Job | 30d Runs | 30d Failures | 30d Pass Rate |\n")
	b.WriteString("|---|---:|---:|---:|\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %d | %d | %s |\n",
			row.Name,
			row.ThirtyRuns,
			row.ThirtyFailures,
			row.ThirtyPassRate,
		)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderRecentFailedJobs(cfg config, jobs []workflowJob) string {
	failures := buildRecentFailures(cfg, jobs)
	if len(failures) == 0 {
		return "No recent failed jobs found."
	}

	var b strings.Builder
	shown := 0
	currentGroup := ""
	for _, failure := range failures {
		if shown >= cfg.maxFailed {
			break
		}
		if failure.Name != currentGroup {
			currentGroup = failure.Name
			if shown > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "### %s\n\n", currentGroup)
		}
		b.WriteString(failure.Line)
		b.WriteString("\n")
		shown++
	}
	return strings.TrimRight(b.String(), "\n")
}

func buildJobSummaries(jobs []workflowJob, now time.Time) []jobSummary {
	thirtyCutoff := now.AddDate(0, 0, -30)
	sevenCutoff := now.AddDate(0, 0, -7)
	collapsed := collapseJobs(jobs)
	byName := map[string][]workflowJob{}

	for _, job := range collapsed {
		if !reportable(job) || job.Conclusion == "skipped" {
			continue
		}
		byName[job.Name] = append(byName[job.Name], job)
	}

	rows := make([]jobSummary, 0, len(byName))
	for name, jobs := range byName {
		sevenRuns, sevenFailures := countRuns(jobs, sevenCutoff)
		thirtyRuns, thirtyFailures := countRuns(jobs, thirtyCutoff)
		rows = append(rows, jobSummary{
			Name:           name,
			SevenRuns:      sevenRuns,
			SevenFailures:  sevenFailures,
			SevenPassRate:  passRate(sevenRuns, sevenFailures),
			ThirtyRuns:     thirtyRuns,
			ThirtyFailures: thirtyFailures,
			ThirtyPassRate: passRate(thirtyRuns, thirtyFailures),
			JobOrder:       jobOrder(name),
			OSOrder:        osOrder(name),
			GoOrder:        goOrder(name),
			FIPSOrder:      fipsOrder(name),
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		switch {
		case left.JobOrder != right.JobOrder:
			return left.JobOrder < right.JobOrder
		case left.OSOrder != right.OSOrder:
			return left.OSOrder < right.OSOrder
		case left.GoOrder != right.GoOrder:
			return left.GoOrder < right.GoOrder
		case left.FIPSOrder != right.FIPSOrder:
			return left.FIPSOrder < right.FIPSOrder
		default:
			return left.Name < right.Name
		}
	})

	return rows
}

func buildRecentFailures(cfg config, jobs []workflowJob) []recentFailure {
	collapsed := collapseJobs(jobs)
	var failures []recentFailure
	for _, job := range collapsed {
		if !reportable(job) || !failed(job) {
			continue
		}
		completed := completedAt(job)
		dateOnly := completed
		if len(dateOnly) > 10 {
			dateOnly = dateOnly[:10]
		}
		shortSHA := job.RunSHA
		if len(shortSHA) > 7 {
			shortSHA = shortSHA[:7]
		}
		jobURL := job.HTMLURL
		if jobURL == "" {
			jobURL = fmt.Sprintf("%s/%s/actions/runs/%d", cfg.serverURL, cfg.repository, job.RunID)
		}
		failures = append(failures, recentFailure{
			Name:      job.Name,
			Completed: completed,
			Line:      fmt.Sprintf("- [%s `%s`](%s)", dateOnly, shortSHA, jobURL),
		})
	}

	sort.SliceStable(failures, func(i, j int) bool {
		if failures[i].Name != failures[j].Name {
			return failures[i].Name < failures[j].Name
		}
		return failures[i].Completed > failures[j].Completed
	})
	return failures
}

func collapseJobs(jobs []workflowJob) []workflowJob {
	groups := map[string][]workflowJob{}
	for _, job := range jobs {
		job.Name = normalizedName(job.Name)
		key := fmt.Sprintf("%d\x00%s", job.RunID, job.Name)
		groups[key] = append(groups[key], job)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	collapsed := make([]workflowJob, 0, len(groups))
	for _, key := range keys {
		group := groups[key]
		chosen := group[0]
		for _, job := range group {
			if failed(job) {
				chosen = job
				chosen.Conclusion = "failure"
				break
			}
		}
		collapsed = append(collapsed, chosen)
	}
	return collapsed
}

func countRuns(jobs []workflowJob, cutoff time.Time) (runs int, failures int) {
	for _, job := range jobs {
		completed, ok := parseGitHubTime(completedAt(job))
		if ok && completed.Before(cutoff) {
			continue
		}
		runs++
		if failed(job) {
			failures++
		}
	}
	return runs, failures
}

func appendSummary(path, content string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(content)
	return err
}

func appendErrorSummary(path, message string) error {
	var b strings.Builder
	b.WriteString("## CI History\n\n")
	b.WriteString("Failed to build CI history summary.\n\n")
	b.WriteString("```\n")
	b.WriteString(message)
	b.WriteString("\n```\n\n")
	return appendSummary(path, b.String())
}

func completedAt(job workflowJob) string {
	return cmp.Or(job.CompletedAt, job.RunUpdatedAt, job.RunCreatedAt)
}

func parseGitHubTime(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func failed(job workflowJob) bool {
	return job.Conclusion != "" &&
		job.Conclusion != "success" &&
		job.Conclusion != "skipped" &&
		job.Conclusion != "neutral"
}

func reportable(job workflowJob) bool {
	return job.Name != "CI History Summary"
}

func normalizedName(name string) string {
	if before, _, ok := strings.Cut(name, " / "); ok {
		return before
	}
	return name
}

func passRate(runs, failures int) string {
	if runs == 0 {
		return "n/a"
	}
	rate := float64(runs-failures) * 100 / float64(runs)
	rate = math.Round(rate*10) / 10
	return strconv.FormatFloat(rate, 'f', -1, 64) + "%"
}

func jobOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "unit-test"):
		return 0
	case strings.HasPrefix(name, "integration-test-no-cache"):
		return 1
	case strings.HasPrefix(name, "integration-test-with-cache"):
		return 2
	case name == "docker-compose-test":
		return 3
	case strings.HasPrefix(name, "cloud-test"):
		return 4
	case strings.HasPrefix(name, "features-test"):
		return 5
	default:
		return 6
	}
}

func osOrder(name string) int {
	switch {
	case strings.Contains(name, "(ubuntu-latest,"):
		return 0
	case strings.Contains(name, "(macos-intel,"):
		return 1
	case strings.Contains(name, "(macos-arm,"):
		return 2
	case strings.Contains(name, "(windows-latest,"):
		return 3
	default:
		return 9
	}
}

func goOrder(name string) int {
	switch {
	case strings.Contains(name, "oldstable"):
		return 0
	case strings.Contains(name, "stable"):
		return 1
	default:
		return 9
	}
}

func fipsOrder(name string) int {
	switch {
	case strings.Contains(name, ", true)"):
		return 0
	case strings.Contains(name, ", false)"):
		return 1
	default:
		return 9
	}
}
