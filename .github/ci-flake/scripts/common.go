package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	investigationOutcomes = valueSet(
		"no-failures",
		"no-credible-flake",
		"credible-flake-no-fix",
		"patch-ready",
		"partial",
		"error",
	)
	outcomeRecordOutcomes = valueSet(
		"no-failures",
		"no-credible-flake",
		"credible-flake-no-fix",
		"patch-ready",
		"draft-pr-opened",
		"partial",
		"error",
	)
	triggerModes = valueSet(
		"daily-reconciliation",
		"main-failure-fast-path",
		"manual-reconciliation",
	)
	publicationModes = valueSet(
		"report-only",
		"github-token-prototype",
		"github-app",
	)
	confidenceValues  = valueSet("none", "low", "medium", "high")
	findingConfidence = valueSet("low", "medium", "high")
	classifications   = valueSet(
		"credible-flake",
		"deterministic",
		"infrastructure",
		"duplicate",
		"insufficient-evidence",
	)
	reasonCodes = valueSet(
		"no-failures",
		"deterministic-failures",
		"known-infrastructure",
		"duplicate",
		"insufficient-evidence",
		"fix-not-safe",
		"patch-ready",
		"limit-reached",
		"investigator-error",
	)
	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{12,}\b`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{12,}\b`),
		regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{12,}\b`),
		regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]{12,}=*\b`),
	}
	urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>()\]]+`)
)

type investigationResult struct {
	SchemaVersion    string         `json:"schema_version"`
	Outcome          string         `json:"outcome"`
	Headline         string         `json:"headline"`
	Confidence       string         `json:"confidence"`
	Decision         decision       `json:"decision"`
	PrimarySignature signature      `json:"primary_signature"`
	Findings         []finding      `json:"findings"`
	RecommendedFix   recommendedFix `json:"recommended_fix"`
	MissingEvidence  []string       `json:"missing_evidence"`
	NextExperiment   nextExperiment `json:"next_experiment"`
	Verification     []verification `json:"verification"`
	ManualFix        manualFix      `json:"manual_fix"`
}

type decision struct {
	ReasonCode  string `json:"reason_code"`
	Explanation string `json:"explanation"`
}

type signature struct {
	Normalized *string `json:"normalized"`
	SHA256     *string `json:"sha256"`
}

type finding struct {
	Title                string   `json:"title"`
	Signature            string   `json:"signature"`
	Classification       string   `json:"classification"`
	Confidence           string   `json:"confidence"`
	RootCause            string   `json:"root_cause"`
	Facts                []string `json:"facts"`
	Inferences           []string `json:"inferences"`
	EvidenceForFlake     []string `json:"evidence_for_flake"`
	EvidenceAgainstFlake []string `json:"evidence_against_flake"`
	LastGoodURL          *string  `json:"last_good_url"`
	FirstBadURL          *string  `json:"first_bad_url"`
	RelevantURLs         []string `json:"relevant_urls"`
}

type recommendedFix struct {
	Summary         string   `json:"summary"`
	AffectedFiles   []string `json:"affected_files"`
	AffectedSymbols []string `json:"affected_symbols"`
}

type nextExperiment struct {
	Summary  string   `json:"summary"`
	Commands []string `json:"commands"`
}

type verification struct {
	Command  string `json:"command"`
	Attempts int    `json:"attempts"`
	Passed   *bool  `json:"passed"`
	Summary  string `json:"summary"`
}

type manualFix struct {
	Eligible          bool     `json:"eligible"`
	BranchName        *string  `json:"branch_name"`
	CommitMessage     *string  `json:"commit_message"`
	PRTitle           *string  `json:"pr_title"`
	PRBody            *string  `json:"pr_body"`
	Labels            []string `json:"labels"`
	ChangelogRequired bool     `json:"changelog_required"`
	ReviewerNotes     []string `json:"reviewer_notes"`
}

type outcomeRecord struct {
	SchemaVersion            string  `json:"schema_version"`
	WorkflowRunID            int64   `json:"workflow_run_id"`
	WorkflowRunAttempt       int     `json:"workflow_run_attempt"`
	WorkflowRunURL           string  `json:"workflow_run_url"`
	CreatedAt                string  `json:"created_at"`
	TriggerMode              string  `json:"trigger_mode"`
	PublicationMode          string  `json:"publication_mode"`
	Outcome                  string  `json:"outcome"`
	PROpened                 bool    `json:"pr_opened"`
	PRURL                    *string `json:"pr_url"`
	ManualFixHandoffProduced bool    `json:"manual_fix_handoff_produced"`
	NormalizedSignatureHash  *string `json:"normalized_signature_hash"`
	PayloadValid             bool    `json:"payload_valid"`
	FallbackUsed             bool    `json:"fallback_used"`
	LinkedHumanPRURL         *string `json:"linked_human_pr_url"`
}

type snapshot struct {
	SchemaVersion     string           `json:"schema_version"`
	GeneratedAt       string           `json:"generated_at"`
	Repository        string           `json:"repository"`
	WorkflowAllowlist []string         `json:"workflow_allowlist"`
	ExcludedWorkflows []string         `json:"excluded_workflows"`
	Search            snapshotSearch   `json:"search"`
	Limits            snapshotLimits   `json:"limits"`
	Coverage          snapshotCoverage `json:"coverage"`
	Runs              []runRecord      `json:"runs"`
	DedupCandidates   []dedupCandidate `json:"dedup_candidates"`
	RepositoryLabels  []string         `json:"repository_labels"`
}

type snapshotSearch struct {
	Cutoff         string   `json:"cutoff"`
	LookbackHours  int      `json:"lookback_hours"`
	IncludedEvents []string `json:"included_events"`
}

type snapshotLimits struct {
	MaxRuns       int `json:"max_runs"`
	MaxFailedJobs int `json:"max_failed_jobs"`
	MaxLogBytes   int `json:"max_log_bytes"`
}

type snapshotCoverage struct {
	RunsInspected       int      `json:"runs_inspected"`
	FailedRunsInspected int      `json:"failed_runs_inspected"`
	FailedJobsInspected int      `json:"failed_jobs_inspected"`
	LogBytesInspected   int      `json:"log_bytes_inspected"`
	LogErrors           int      `json:"log_errors"`
	LimitsReached       []string `json:"limits_reached"`
}

type runRecord struct {
	ID             int64               `json:"id"`
	Attempt        int                 `json:"attempt"`
	Name           string              `json:"name"`
	Path           string              `json:"path"`
	Event          string              `json:"event"`
	Status         string              `json:"status"`
	Conclusion     string              `json:"conclusion"`
	HTMLURL        string              `json:"html_url"`
	CreatedAt      string              `json:"created_at"`
	UpdatedAt      string              `json:"updated_at"`
	HeadBranch     string              `json:"head_branch"`
	HeadSHA        string              `json:"head_sha"`
	HeadRepository *string             `json:"head_repository"`
	Actor          *string             `json:"actor"`
	PullRequests   []pullRequestRecord `json:"pull_requests"`
	FailedJobs     []jobRecord         `json:"failed_jobs"`
}

type pullRequestRecord struct {
	Number  int     `json:"number"`
	URL     string  `json:"url"`
	HeadSHA *string `json:"head_sha"`
	BaseSHA *string `json:"base_sha"`
}

type jobRecord struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	Status          string       `json:"status"`
	Conclusion      string       `json:"conclusion"`
	HTMLURL         string       `json:"html_url"`
	StartedAt       string       `json:"started_at"`
	CompletedAt     string       `json:"completed_at"`
	RunnerName      string       `json:"runner_name"`
	RunnerGroupName string       `json:"runner_group_name"`
	Labels          []string     `json:"labels"`
	Steps           []stepRecord `json:"steps"`
	LogPath         *string      `json:"log_path"`
	LogBytes        int          `json:"log_bytes"`
	LogTruncated    bool         `json:"log_truncated"`
	LogError        *string      `json:"log_error"`
}

type stepRecord struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type dedupCandidate struct {
	Number    int      `json:"number"`
	Kind      string   `json:"kind"`
	State     string   `json:"state"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	HTMLURL   string   `json:"html_url"`
	UpdatedAt string   `json:"updated_at"`
	Labels    []string `json:"labels"`
}

type changedFiles struct {
	SchemaVersion  string   `json:"schema_version"`
	Files          []string `json:"files"`
	ProtectedFiles []string `json:"protected_files"`
	Bytes          int      `json:"bytes"`
	HasChanges     bool     `json:"has_changes"`
	Oversized      bool     `json:"oversized"`
}

func valueSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func inSet(set map[string]struct{}, value string) bool {
	_, ok := set[value]
	return ok
}

func readJSONStrict(file string, value any, maxBytes int64) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", file, maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%s contains trailing JSON", file)
	}
	return nil
}

func writeJSON(file string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(file, data, 0o600)
}

func pointer[T any](value T) *T {
	return &value
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value bool) *bool {
	return &value
}

func truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

func sanitizeText(value string, max int) string {
	value = strings.Map(func(r rune) rune {
		if (r >= 0 && r <= 8) || r == 11 || r == 12 || (r >= 14 && r <= 31) || r == 127 {
			return ' '
		}
		return r
	}, value)
	value = urlPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if safeGitHubURL(candidate) {
			return candidate
		}
		return "[external URL removed]"
	})
	value = strings.ReplaceAll(value, "![", `\![`)
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = escapeMarkdownBrackets(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED]")
	}
	return truncate(strings.TrimSpace(value), max)
}

func escapeMarkdownBrackets(value string) string {
	var output strings.Builder
	for index := 0; index < len(value); index++ {
		current := value[index]
		if (current == '[' || current == ']') && (index == 0 || value[index-1] != '\\') {
			output.WriteByte('\\')
		}
		output.WriteByte(current)
	}
	return output.String()
}

func containsSecretLike(value string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func safeGitHubURL(value string) bool {
	if value == "" || len(value) > 500 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil &&
		parsed.Scheme == "https" &&
		strings.EqualFold(parsed.Hostname(), "github.com") &&
		parsed.User == nil &&
		parsed.Port() == ""
}

func tableCell(value string, max int) string {
	value = sanitizeText(value, max)
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return strings.ReplaceAll(value, "|", `\|`)
}

func validateInvestigationResult(result investigationResult) []string {
	var errors []string
	checkString(&errors, "schema_version", result.SchemaVersion, 1, valueSet("1"))
	checkString(&errors, "outcome", result.Outcome, 40, investigationOutcomes)
	checkString(&errors, "headline", result.Headline, 240, nil)
	checkString(&errors, "confidence", result.Confidence, 20, confidenceValues)
	checkString(&errors, "decision.reason_code", result.Decision.ReasonCode, 60, reasonCodes)
	checkString(&errors, "decision.explanation", result.Decision.Explanation, 1600, nil)
	checkOptionalString(&errors, "primary_signature.normalized", result.PrimarySignature.Normalized, 1200)
	checkOptionalString(&errors, "primary_signature.sha256", result.PrimarySignature.SHA256, 64)
	if result.PrimarySignature.SHA256 != nil && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(*result.PrimarySignature.SHA256) {
		errors = append(errors, "primary_signature.sha256 must be a lowercase SHA-256")
	}
	if len(result.Findings) > 5 {
		errors = append(errors, "findings exceeds 5 items")
	}
	for index, finding := range result.Findings {
		prefix := "findings[" + strconv.Itoa(index) + "]"
		checkString(&errors, prefix+".title", finding.Title, 240, nil)
		checkString(&errors, prefix+".signature", finding.Signature, 1200, nil)
		checkString(&errors, prefix+".classification", finding.Classification, 40, classifications)
		checkString(&errors, prefix+".confidence", finding.Confidence, 20, findingConfidence)
		checkString(&errors, prefix+".root_cause", finding.RootCause, 2400, nil)
		checkStringSlice(&errors, prefix+".facts", finding.Facts, 12, 800)
		checkStringSlice(&errors, prefix+".inferences", finding.Inferences, 8, 800)
		checkStringSlice(&errors, prefix+".evidence_for_flake", finding.EvidenceForFlake, 12, 800)
		checkStringSlice(&errors, prefix+".evidence_against_flake", finding.EvidenceAgainstFlake, 12, 800)
		checkOptionalString(&errors, prefix+".last_good_url", finding.LastGoodURL, 500)
		checkOptionalString(&errors, prefix+".first_bad_url", finding.FirstBadURL, 500)
		checkStringSlice(&errors, prefix+".relevant_urls", finding.RelevantURLs, 12, 500)
	}
	checkString(&errors, "recommended_fix.summary", result.RecommendedFix.Summary, 2400, nil)
	checkStringSlice(&errors, "recommended_fix.affected_files", result.RecommendedFix.AffectedFiles, 20, 300)
	checkStringSlice(&errors, "recommended_fix.affected_symbols", result.RecommendedFix.AffectedSymbols, 20, 300)
	checkStringSlice(&errors, "missing_evidence", result.MissingEvidence, 12, 800)
	checkString(&errors, "next_experiment.summary", result.NextExperiment.Summary, 1600, nil)
	checkStringSlice(&errors, "next_experiment.commands", result.NextExperiment.Commands, 8, 800)
	if len(result.Verification) > 12 {
		errors = append(errors, "verification exceeds 12 items")
	}
	for index, verification := range result.Verification {
		prefix := "verification[" + strconv.Itoa(index) + "]"
		checkString(&errors, prefix+".command", verification.Command, 800, nil)
		if verification.Attempts < 0 || verification.Attempts > 100 {
			errors = append(errors, prefix+".attempts must be between 0 and 100")
		}
		checkString(&errors, prefix+".summary", verification.Summary, 800, nil)
	}
	checkOptionalString(&errors, "manual_fix.branch_name", result.ManualFix.BranchName, 200)
	checkOptionalString(&errors, "manual_fix.commit_message", result.ManualFix.CommitMessage, 200)
	checkOptionalString(&errors, "manual_fix.pr_title", result.ManualFix.PRTitle, 240)
	checkOptionalString(&errors, "manual_fix.pr_body", result.ManualFix.PRBody, 3000)
	if result.ManualFix.PRBody != nil && len(strings.Fields(*result.ManualFix.PRBody)) > 300 {
		errors = append(errors, "manual_fix.pr_body exceeds 300 words")
	}
	checkStringSlice(&errors, "manual_fix.labels", result.ManualFix.Labels, 8, 100)
	checkStringSlice(&errors, "manual_fix.reviewer_notes", result.ManualFix.ReviewerNotes, 12, 800)
	return errors
}

func validateOutcomeRecord(record outcomeRecord) []string {
	var errors []string
	checkString(&errors, "schema_version", record.SchemaVersion, 1, valueSet("1"))
	if record.WorkflowRunID < 1 {
		errors = append(errors, "workflow_run_id must be positive")
	}
	if record.WorkflowRunAttempt < 1 {
		errors = append(errors, "workflow_run_attempt must be positive")
	}
	if !safeGitHubURL(record.WorkflowRunURL) {
		errors = append(errors, "workflow_run_url must be a GitHub URL")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.CreatedAt); err != nil || len(record.CreatedAt) > 40 {
		errors = append(errors, "created_at must be an ISO timestamp")
	}
	checkString(&errors, "trigger_mode", record.TriggerMode, 40, triggerModes)
	checkString(&errors, "publication_mode", record.PublicationMode, 40, publicationModes)
	checkString(&errors, "outcome", record.Outcome, 40, outcomeRecordOutcomes)
	if record.PRURL != nil && !safeGitHubURL(*record.PRURL) {
		errors = append(errors, "pr_url must be a GitHub URL or null")
	}
	if record.PROpened && record.PRURL == nil {
		errors = append(errors, "pr_url is required when pr_opened is true")
	}
	if record.NormalizedSignatureHash != nil && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(*record.NormalizedSignatureHash) {
		errors = append(errors, "normalized_signature_hash must be a lowercase SHA-256")
	}
	if record.LinkedHumanPRURL != nil && !safeGitHubURL(*record.LinkedHumanPRURL) {
		errors = append(errors, "linked_human_pr_url must be a GitHub URL or null")
	}
	return errors
}

func checkString(errors *[]string, name, value string, max int, choices map[string]struct{}) {
	if utf8.RuneCountInString(value) > max {
		*errors = append(*errors, fmt.Sprintf("%s exceeds %d characters", name, max))
	}
	if choices != nil && !inSet(choices, value) {
		*errors = append(*errors, name+" has an unsupported value")
	}
}

func checkOptionalString(errors *[]string, name string, value *string, max int) {
	if value != nil {
		checkString(errors, name, *value, max, nil)
	}
}

func checkStringSlice(errors *[]string, name string, values []string, maxItems, maxLength int) {
	if len(values) > maxItems {
		*errors = append(*errors, fmt.Sprintf("%s exceeds %d items", name, maxItems))
	}
	for index, value := range values {
		checkString(errors, fmt.Sprintf("%s[%d]", name, index), value, maxLength, nil)
	}
}

func appendOutput(values map[string]string) error {
	outputFile := os.Getenv("GITHUB_OUTPUT")
	if outputFile == "" {
		return nil
	}
	file, err := os.OpenFile(outputFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(writer, "%s=%s\n", key, values[key]); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func formatPercent(numerator, denominator int) string {
	if denominator == 0 {
		return "N/A"
	}
	return fmt.Sprintf("%.1f%%", float64(numerator)*100/float64(denominator))
}
