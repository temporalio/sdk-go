package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var outcomeBanners = map[string]string{
	"no-failures":           "➖ No PR — no failed monitored jobs occurred",
	"no-credible-flake":     "➖ No PR — no credible flake was found",
	"credible-flake-no-fix": "🔎 No PR — a credible flake needs more evidence or a safer fix",
	"patch-ready":           "🛠️ Manual fix handoff ready — publication is disabled",
	"draft-pr-opened":       "✅ Draft PR opened",
	"partial":               "⚠️ Partial investigation — review the missing coverage",
	"error":                 "❌ Automation error — the investigation result was not usable",
}

var manualBranchPattern = regexp.MustCompile(
	`^automation/ci-flake/[A-Za-z0-9][A-Za-z0-9._/-]*$`,
)

type renderMetadata struct {
	BaseSHA         string
	CodexConclusion string
	PublicationMode string
	RunID           int64
	RunAttempt      int
	RunURL          string
	TriggerMode     string
	RunbookVersion  string
}

type outcomeSummary struct {
	Runs                    int
	PublishEnabledCompleted int
	PRs                     int
	NoPR                    int
	ReportOnly              int
	Prototype               int
	PartialOrError          int
	Handoffs                int
	LinkedHumanPRs          int
}

func runRender(args []string) error {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	result := requiredString(flags, "result", "investigator result JSON")
	snapshot := requiredString(flags, "snapshot", "CI snapshot JSON")
	patch := requiredString(flags, "patch", "candidate patch")
	changes := requiredString(flags, "changes", "changed-files JSON")
	output := requiredString(flags, "output", "output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"result":   *result,
		"snapshot": *snapshot,
		"patch":    *patch,
		"changes":  *changes,
		"output":   *output,
	}); err != nil {
		return err
	}
	return renderCurrent(*result, *snapshot, *patch, *changes, *output)
}

func runStats(args []string) error {
	flags := flag.NewFlagSet("stats", flag.ContinueOnError)
	current := requiredString(flags, "current", "current outcome JSON")
	history := requiredString(flags, "history", "prior outcome directory")
	limit := flags.Int("limit", 0, "maximum outcome records")
	output := requiredString(flags, "output", "statistics Markdown")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireFlags(map[string]string{
		"current": *current,
		"history": *history,
		"output":  *output,
	}); err != nil {
		return err
	}
	if *limit < 1 || *limit > 200 {
		return fmt.Errorf("--limit must be between 1 and 200")
	}
	return renderStatistics(*current, *history, *limit, *output)
}

func fallbackResult(reason string) investigationResult {
	return investigationResult{
		SchemaVersion: "1",
		Outcome:       "error",
		Headline:      "The investigator did not produce a usable result.",
		Confidence:    "none",
		Decision: decision{
			ReasonCode:  "investigator-error",
			Explanation: sanitizeText(reason, 1600),
		},
		PrimarySignature: signature{},
		Findings:         []finding{},
		RecommendedFix: recommendedFix{
			Summary:         "",
			AffectedFiles:   []string{},
			AffectedSymbols: []string{},
		},
		MissingEvidence: []string{
			"A valid structured investigator result was unavailable.",
		},
		NextExperiment: nextExperiment{
			Summary:  "Inspect the failed investigator and evidence-collection steps, then rerun.",
			Commands: []string{},
		},
		Verification: []verification{},
		ManualFix: manualFix{
			Labels:        []string{},
			ReviewerNotes: []string{},
		},
	}
}

func fallbackSnapshot() snapshot {
	return snapshot{
		SchemaVersion:     "1",
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		Repository:        "unknown/unknown",
		WorkflowAllowlist: []string{},
		ExcludedWorkflows: []string{},
		Search: snapshotSearch{
			LookbackHours:  0,
			IncludedEvents: []string{},
		},
		Limits: snapshotLimits{},
		Coverage: snapshotCoverage{
			LimitsReached: []string{"snapshot-unavailable"},
		},
		Runs:             []runRecord{},
		DedupCandidates:  []dedupCandidate{},
		RepositoryLabels: []string{},
	}
}

func readSnapshot(file string) (snapshot, error) {
	var value snapshot
	if err := readJSONStrict(file, &value, 16*1024*1024); err != nil {
		return snapshot{}, err
	}
	if value.SchemaVersion != "1" ||
		!validRepository(value.Repository) ||
		value.GeneratedAt == "" ||
		value.Search.LookbackHours < 1 ||
		value.Limits.MaxRuns < 1 ||
		value.Limits.MaxFailedJobs < 1 ||
		value.Limits.MaxLogBytes < 1 ||
		value.Coverage.RunsInspected < 0 ||
		value.Coverage.FailedRunsInspected < 0 ||
		value.Coverage.FailedJobsInspected < 0 ||
		value.Coverage.LogBytesInspected < 0 ||
		value.Coverage.LogErrors < 0 ||
		value.Coverage.LimitsReached == nil {
		return snapshot{}, fmt.Errorf(
			"snapshot.json is missing required deterministic coverage fields",
		)
	}
	return value, nil
}

func readChanges(file string) (changedFiles, error) {
	var value changedFiles
	if err := readJSONStrict(file, &value, 4*1024*1024); err != nil {
		return changedFiles{}, err
	}
	if value.SchemaVersion != "1" ||
		value.Files == nil ||
		value.ProtectedFiles == nil ||
		value.Bytes < 0 {
		return changedFiles{}, fmt.Errorf("changed-files.json is invalid")
	}
	for _, file := range append(append([]string{}, value.Files...), value.ProtectedFiles...) {
		if !safeRelativePath(file) {
			return changedFiles{}, fmt.Errorf("changed-files.json contains an unsafe path")
		}
	}
	return value, nil
}

func coherenceErrors(
	result investigationResult,
	snapshot snapshot,
	changes changedFiles,
	patch []byte,
) []string {
	var errors []string
	patchReady := result.Outcome == "patch-ready"
	if patchReady != result.ManualFix.Eligible {
		errors = append(errors, "patch-ready and manual_fix.eligible must agree")
	}
	if patchReady != changes.HasChanges {
		errors = append(errors, "repository changes must exist only for a patch-ready result")
	}
	if changes.Bytes != len(patch) {
		errors = append(errors, "candidate patch size does not match changed-files metadata")
	}
	if changes.HasChanges != (len(patch) > 0) {
		errors = append(errors, "candidate patch presence does not match changed-files metadata")
	}
	if len(changes.ProtectedFiles) > 0 {
		errors = append(
			errors,
			"protected automation files changed: "+strings.Join(changes.ProtectedFiles, ", "),
		)
	}
	if changes.Oversized {
		errors = append(errors, "candidate patch is oversized")
	}
	if containsSecretLike(string(patch)) {
		errors = append(errors, "candidate patch contains a secret-like value")
	}
	if result.Outcome == "no-failures" && snapshot.Coverage.FailedRunsInspected != 0 {
		errors = append(
			errors,
			"no-failures conflicts with the deterministic failed-run count",
		)
	}
	if (result.PrimarySignature.Normalized == nil) !=
		(result.PrimarySignature.SHA256 == nil) {
		errors = append(
			errors,
			"normalized signature and signature hash must either both be present or both be null",
		)
	}
	if result.PrimarySignature.Normalized != nil &&
		result.PrimarySignature.SHA256 != nil {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(*result.PrimarySignature.Normalized)))
		if hash != *result.PrimarySignature.SHA256 {
			errors = append(
				errors,
				"primary_signature.sha256 does not match the normalized signature",
			)
		}
	}
	for _, item := range result.Findings {
		urls := append([]string{}, item.RelevantURLs...)
		if item.LastGoodURL != nil {
			urls = append(urls, *item.LastGoodURL)
		}
		if item.FirstBadURL != nil {
			urls = append(urls, *item.FirstBadURL)
		}
		for _, candidate := range urls {
			if !safeGitHubURL(candidate) {
				errors = append(errors, "finding URLs must point to github.com")
				break
			}
		}
	}

	if patchReady {
		if result.Confidence != "high" {
			errors = append(errors, "patch-ready requires high confidence")
		}
		if result.Decision.ReasonCode != "patch-ready" {
			errors = append(
				errors,
				"patch-ready requires the patch-ready decision reason",
			)
		}
		if result.PrimarySignature.SHA256 == nil {
			errors = append(
				errors,
				"patch-ready requires a normalized signature hash",
			)
		}
		if result.ManualFix.BranchName == nil ||
			!manualBranchPattern.MatchString(*result.ManualFix.BranchName) ||
			strings.Contains(*result.ManualFix.BranchName, "..") {
			errors = append(
				errors,
				"manual branch name must be a safe automation/ci-flake/ branch",
			)
		}
		if result.ManualFix.CommitMessage == nil ||
			*result.ManualFix.CommitMessage == "" {
			errors = append(errors, "manual_fix.commit_message is required for patch-ready")
		}
		if result.ManualFix.PRTitle == nil || *result.ManualFix.PRTitle == "" {
			errors = append(errors, "manual_fix.pr_title is required for patch-ready")
		}
		if result.ManualFix.PRBody == nil || *result.ManualFix.PRBody == "" {
			errors = append(errors, "manual_fix.pr_body is required for patch-ready")
		}
		repeatedTest := false
		checkPassed := false
		for _, verification := range result.Verification {
			if verification.Passed != nil &&
				*verification.Passed &&
				verification.Attempts >= 5 &&
				(strings.Contains(verification.Command, "unit-test") ||
					strings.Contains(verification.Command, "integration-test")) {
				repeatedTest = true
			}
			if verification.Passed != nil &&
				*verification.Passed &&
				strings.Contains(verification.Command, "go run . check") {
				checkPassed = true
			}
		}
		if !repeatedTest {
			errors = append(
				errors,
				"patch-ready requires a focused test to pass at least five times",
			)
		}
		if !checkPassed {
			errors = append(errors, "patch-ready requires go run . check to pass")
		}
		affected := valueSet(result.RecommendedFix.AffectedFiles...)
		var omitted []string
		for _, file := range changes.Files {
			if !inSet(affected, file) {
				omitted = append(omitted, file)
			}
		}
		if len(omitted) > 0 {
			errors = append(
				errors,
				"affected_files omits changed paths: "+strings.Join(omitted, ", "),
			)
		}
		knownLabels := valueSet(snapshot.RepositoryLabels...)
		var unknownLabels []string
		for _, label := range result.ManualFix.Labels {
			if !inSet(knownLabels, label) {
				unknownLabels = append(unknownLabels, label)
			}
		}
		if len(unknownLabels) > 0 {
			errors = append(
				errors,
				"manual fix suggests labels that do not exist: "+
					strings.Join(unknownLabels, ", "),
			)
		}
	} else {
		if result.ManualFix.BranchName != nil {
			errors = append(errors, "manual_fix.branch_name must be null without a patch")
		}
		if result.ManualFix.CommitMessage != nil {
			errors = append(errors, "manual_fix.commit_message must be null without a patch")
		}
		if result.ManualFix.PRTitle != nil {
			errors = append(errors, "manual_fix.pr_title must be null without a patch")
		}
		if result.ManualFix.PRBody != nil {
			errors = append(errors, "manual_fix.pr_body must be null without a patch")
		}
		if len(result.ManualFix.Labels) > 0 ||
			len(result.ManualFix.ReviewerNotes) > 0 ||
			result.ManualFix.ChangelogRequired {
			errors = append(errors, "manual fix metadata must be empty without a patch")
		}
	}
	return errors
}

func safeRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	cleaned := filepath.Clean(value)
	return cleaned == value &&
		cleaned != "." &&
		cleaned != ".." &&
		!strings.HasPrefix(cleaned, "../")
}

func sanitizeInvestigationResult(result investigationResult) investigationResult {
	result.SchemaVersion = sanitizeText(result.SchemaVersion, 1)
	result.Outcome = sanitizeText(result.Outcome, 40)
	result.Headline = sanitizeText(result.Headline, 240)
	result.Confidence = sanitizeText(result.Confidence, 20)
	result.Decision.ReasonCode = sanitizeText(result.Decision.ReasonCode, 60)
	result.Decision.Explanation = sanitizeText(result.Decision.Explanation, 1600)
	sanitizeOptional(result.PrimarySignature.Normalized, 1200)
	sanitizeOptional(result.PrimarySignature.SHA256, 64)
	for index := range result.Findings {
		item := &result.Findings[index]
		item.Title = sanitizeText(item.Title, 240)
		item.Signature = sanitizeText(item.Signature, 1200)
		item.Classification = sanitizeText(item.Classification, 40)
		item.Confidence = sanitizeText(item.Confidence, 20)
		item.RootCause = sanitizeText(item.RootCause, 2400)
		sanitizeStrings(item.Facts, 800)
		sanitizeStrings(item.Inferences, 800)
		sanitizeStrings(item.EvidenceForFlake, 800)
		sanitizeStrings(item.EvidenceAgainstFlake, 800)
		sanitizeOptional(item.LastGoodURL, 500)
		sanitizeOptional(item.FirstBadURL, 500)
		sanitizeStrings(item.RelevantURLs, 500)
	}
	result.RecommendedFix.Summary = sanitizeText(
		result.RecommendedFix.Summary,
		2400,
	)
	sanitizeStrings(result.RecommendedFix.AffectedFiles, 300)
	sanitizeStrings(result.RecommendedFix.AffectedSymbols, 300)
	sanitizeStrings(result.MissingEvidence, 800)
	result.NextExperiment.Summary = sanitizeText(
		result.NextExperiment.Summary,
		1600,
	)
	sanitizeStrings(result.NextExperiment.Commands, 800)
	for index := range result.Verification {
		item := &result.Verification[index]
		item.Command = sanitizeText(item.Command, 800)
		item.Summary = sanitizeText(item.Summary, 800)
	}
	sanitizeOptional(result.ManualFix.BranchName, 200)
	sanitizeOptional(result.ManualFix.CommitMessage, 200)
	sanitizeOptional(result.ManualFix.PRTitle, 240)
	sanitizeOptional(result.ManualFix.PRBody, 3000)
	sanitizeStrings(result.ManualFix.Labels, 100)
	sanitizeStrings(result.ManualFix.ReviewerNotes, 800)
	return result
}

func sanitizeOptional(value *string, max int) {
	if value != nil {
		*value = sanitizeText(*value, max)
	}
}

func sanitizeStrings(values []string, max int) {
	for index := range values {
		values[index] = sanitizeText(values[index], max)
	}
}

func inlineText(value string, max int) string {
	return strings.Join(strings.Fields(sanitizeText(value, max)), " ")
}

func bulletLines(items []string, limit int) []string {
	if len(items) == 0 {
		return []string{"- None recorded."}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+inlineText(item, 800))
	}
	return lines
}

func linkLines(urls []string) []string {
	seen := make(map[string]struct{})
	var lines []string
	for _, candidate := range urls {
		if !safeGitHubURL(candidate) {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		lines = append(lines, "- "+candidate)
		if len(lines) == 8 {
			break
		}
	}
	if len(lines) == 0 {
		return []string{"- None recorded."}
	}
	return lines
}

func renderSummary(
	result investigationResult,
	snapshot snapshot,
	metadata renderMetadata,
	handoffReady bool,
) string {
	banner, ok := outcomeBanners[result.Outcome]
	if !ok {
		banner = outcomeBanners["error"]
	}
	limitsReached := "none"
	if len(snapshot.Coverage.LimitsReached) > 0 {
		items := make([]string, 0, len(snapshot.Coverage.LimitsReached))
		for _, item := range snapshot.Coverage.LimitsReached {
			items = append(items, "`"+tableCell(item, 80)+"`")
		}
		limitsReached = strings.Join(items, ", ")
	}
	lines := []string{
		"# CI flake investigation",
		"",
		banner,
		"",
		"| Context | Value |",
		"| --- | --- |",
		"| Trigger | " + tableCell(metadata.TriggerMode, 40) + " |",
		"| Publication | " + tableCell(metadata.PublicationMode, 40) + " |",
		"| Search window | " + strconv.Itoa(snapshot.Search.LookbackHours) + " hours |",
		"| Base SHA | `" + tableCell(metadata.BaseSHA, 80) + "` |",
		"| Runbook | v" + tableCell(metadata.RunbookVersion, 20) + " |",
		"",
		"## Deterministic coverage",
		"",
		"| Measure | Inspected | Limit |",
		"| --- | ---: | ---: |",
		fmt.Sprintf(
			"| Workflow runs | %d | %d |",
			snapshot.Coverage.RunsInspected,
			snapshot.Limits.MaxRuns,
		),
		fmt.Sprintf(
			"| Failed jobs | %d | %d |",
			snapshot.Coverage.FailedJobsInspected,
			snapshot.Limits.MaxFailedJobs,
		),
		fmt.Sprintf(
			"| Log bytes | %d | %d |",
			snapshot.Coverage.LogBytesInspected,
			snapshot.Limits.MaxLogBytes,
		),
		fmt.Sprintf(
			"| Unavailable job logs | %d | 0 |",
			snapshot.Coverage.LogErrors,
		),
		"",
		"Limits reached: " + limitsReached + ".",
		"",
		"## AI assessment",
		"",
		"**" + inlineText(result.Headline, 240) + "**",
		"",
		"Confidence: **" + inlineText(result.Confidence, 20) + "**",
		"",
		inlineText(result.Decision.Explanation, 1600),
	}
	findings := result.Findings
	if len(findings) > 3 {
		findings = findings[:3]
	}
	for _, item := range findings {
		lines = append(
			lines,
			"",
			"### "+inlineText(item.Title, 240),
			"",
			"Classification: `"+tableCell(item.Classification, 40)+
				"`; confidence: `"+tableCell(item.Confidence, 20)+"`.",
			"",
			"Root cause assessment: "+inlineText(item.RootCause, 1800),
			"",
			"Strongest facts:",
			"",
		)
		lines = append(lines, bulletLines(item.Facts, 4)...)
		lines = append(lines, "", "Evidence for flakiness:", "")
		lines = append(lines, bulletLines(item.EvidenceForFlake, 4)...)
		lines = append(lines, "", "Evidence against flakiness:", "")
		lines = append(lines, bulletLines(item.EvidenceAgainstFlake, 3)...)
	}
	if result.RecommendedFix.Summary != "" {
		lines = append(
			lines,
			"",
			"## Recommended fix",
			"",
			inlineText(result.RecommendedFix.Summary, 2000),
		)
		if len(result.RecommendedFix.AffectedFiles) > 0 {
			files := result.RecommendedFix.AffectedFiles
			if len(files) > 12 {
				files = files[:12]
			}
			formatted := make([]string, 0, len(files))
			for _, file := range files {
				formatted = append(formatted, "`"+tableCell(file, 300)+"`")
			}
			lines = append(
				lines,
				"",
				"Affected files: "+strings.Join(formatted, ", "),
			)
		}
	}
	if handoffReady {
		lines = append(
			lines,
			"",
			fmt.Sprintf(
				"The inspectable artifact `ci-flake-manual-fix-%d-%d` contains the candidate patch, validation record, and draft PR text.",
				metadata.RunID,
				metadata.RunAttempt,
			),
		)
	}
	var relevantURLs []string
	for _, item := range result.Findings {
		if item.LastGoodURL != nil {
			relevantURLs = append(relevantURLs, *item.LastGoodURL)
		}
		if item.FirstBadURL != nil {
			relevantURLs = append(relevantURLs, *item.FirstBadURL)
		}
		relevantURLs = append(relevantURLs, item.RelevantURLs...)
	}
	lines = append(lines, "", "## Missing evidence", "")
	lines = append(lines, bulletLines(result.MissingEvidence, 8)...)
	nextExperiment := inlineText(result.NextExperiment.Summary, 1600)
	if nextExperiment == "" {
		nextExperiment = "None required."
	}
	lines = append(
		lines,
		"",
		"## Best next experiment",
		"",
		nextExperiment,
		"",
		"## Relevant links",
		"",
	)
	lines = append(lines, linkLines(relevantURLs)...)
	lines = append(
		lines,
		"",
		"_Coverage and workflow status above are deterministic. Findings and recommendations are model-produced assessments._",
		"",
	)
	return strings.Join(lines, "\n") + "\n"
}

func renderDetailedReport(
	result investigationResult,
	snapshot snapshot,
	metadata renderMetadata,
	handoffReady bool,
) string {
	lines := []string{
		"# Detailed CI flake investigation report",
		"",
		"- Repository: `" + tableCell(snapshot.Repository, 300) + "`",
		"- Base SHA: `" + tableCell(metadata.BaseSHA, 80) + "`",
		"- Workflow run: " + metadata.RunURL,
		"- Trigger: `" + tableCell(metadata.TriggerMode, 40) + "`",
		"- Publication: `" + tableCell(metadata.PublicationMode, 40) + "`",
		"- Outcome: `" + tableCell(result.Outcome, 40) + "`",
		"- Manual handoff: " + map[bool]string{true: "produced", false: "not produced"}[handoffReady],
		"",
		"## Decision",
		"",
		inlineText(result.Decision.Explanation, 1600),
	}
	for index, item := range result.Findings {
		lines = append(
			lines,
			"",
			fmt.Sprintf("## Finding %d: %s", index+1, inlineText(item.Title, 240)),
			"",
			"- Classification: `"+tableCell(item.Classification, 40)+"`",
			"- Confidence: `"+tableCell(item.Confidence, 20)+"`",
			"- Signature: `"+tableCell(item.Signature, 1200)+"`",
			"",
			"### Root cause",
			"",
			inlineText(item.RootCause, 2400),
			"",
			"### Facts",
			"",
		)
		lines = append(lines, bulletLines(item.Facts, 12)...)
		lines = append(lines, "", "### Inferences", "")
		lines = append(lines, bulletLines(item.Inferences, 8)...)
		lines = append(lines, "", "### Evidence for flakiness", "")
		lines = append(lines, bulletLines(item.EvidenceForFlake, 12)...)
		lines = append(lines, "", "### Evidence against flakiness", "")
		lines = append(lines, bulletLines(item.EvidenceAgainstFlake, 12)...)
	}
	fixSummary := inlineText(result.RecommendedFix.Summary, 2400)
	if fixSummary == "" {
		fixSummary = "No specific safe fix was recommended."
	}
	lines = append(lines, "", "## Recommended fix", "", fixSummary, "", "## Verification", "")
	if len(result.Verification) == 0 {
		lines = append(lines, "- No verification command was completed.")
	} else {
		for _, item := range result.Verification {
			lines = append(
				lines,
				fmt.Sprintf(
					"- `%s` — %s; %d attempt(s). %s",
					tableCell(item.Command, 800),
					verificationStatus(item.Passed),
					item.Attempts,
					inlineText(item.Summary, 800),
				),
			)
		}
	}
	lines = append(lines, "", "## Missing evidence", "")
	lines = append(lines, bulletLines(result.MissingEvidence, 12)...)
	nextExperiment := inlineText(result.NextExperiment.Summary, 1600)
	if nextExperiment == "" {
		nextExperiment = "None required."
	}
	lines = append(lines, "", "## Next experiment", "", nextExperiment, "")
	lines = append(lines, bulletLines(result.NextExperiment.Commands, 8)...)
	lines = append(lines, "")
	return strings.Join(lines, "\n") + "\n"
}

func verificationStatus(value *bool) string {
	if value == nil {
		return "not run"
	}
	if *value {
		return "passed"
	}
	return "failed"
}

func createManualHandoff(
	result investigationResult,
	snapshot snapshot,
	metadata renderMetadata,
	patch []byte,
	output string,
) error {
	handoff := filepath.Join(output, "manual-fix")
	if err := os.MkdirAll(handoff, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(handoff, "candidate.patch"), patch, 0o600); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(handoff, "result.json"), result); err != nil {
		return err
	}
	provenance := fmt.Sprintf(
		"\n## Automation provenance\n\n- Investigator workflow: %s\n- Base SHA: `%s`\n- Normalized signature: `%s`\n- Runbook version: `%s`\n",
		metadata.RunURL,
		tableCell(metadata.BaseSHA, 80),
		tableCell(dereference(result.PrimarySignature.SHA256), 64),
		tableCell(metadata.RunbookVersion, 20),
	)
	prBody := sanitizeText(dereference(result.ManualFix.PRBody), 3000)
	if err := os.WriteFile(
		filepath.Join(handoff, "draft-pr.md"),
		[]byte(prBody+"\n"+provenance+"\n"),
		0o600,
	); err != nil {
		return err
	}
	var verificationLines []string
	for _, item := range result.Verification {
		verificationLines = append(
			verificationLines,
			fmt.Sprintf(
				"- `%s` — %s; %d attempt(s). %s",
				tableCell(item.Command, 800),
				verificationStatus(item.Passed),
				item.Attempts,
				inlineText(item.Summary, 800),
			),
		)
	}
	if len(verificationLines) == 0 {
		verificationLines = []string{"- None recorded."}
	}
	labels := "none"
	if len(result.ManualFix.Labels) > 0 {
		items := make([]string, 0, len(result.ManualFix.Labels))
		for _, label := range result.ManualFix.Labels {
			items = append(items, "`"+tableCell(label, 100)+"`")
		}
		labels = strings.Join(items, ", ")
	}
	lines := []string{
		"# Manual CI flake fix handoff",
		"",
		"Review the diagnosis and patch before applying anything. CI validation does not replace local review or normal pull request checks.",
		"",
		"## Identity",
		"",
		"- Repository: `" + tableCell(snapshot.Repository, 300) + "`",
		"- Base SHA: `" + tableCell(metadata.BaseSHA, 80) + "`",
		"- Investigator run: " + metadata.RunURL,
		"- Signature: `" + tableCell(dereference(result.PrimarySignature.SHA256), 64) + "`",
		"- Confidence: `" + tableCell(result.Confidence, 20) + "`",
		"",
		"## Suggested change",
		"",
		inlineText(result.RecommendedFix.Summary, 2400),
		"",
		"- Suggested branch: `" + tableCell(dereference(result.ManualFix.BranchName), 200) + "`",
		"- Suggested commit: `" + tableCell(dereference(result.ManualFix.CommitMessage), 200) + "`",
		"- Suggested PR title: `" + tableCell(dereference(result.ManualFix.PRTitle), 240) + "`",
		"- Changelog required: " + map[bool]string{true: "yes", false: "no"}[result.ManualFix.ChangelogRequired],
		"- Existing labels: " + labels,
		"",
		"## Verification recorded in CI",
		"",
	}
	lines = append(lines, verificationLines...)
	lines = append(lines, "", "## Reviewer attention", "")
	lines = append(lines, bulletLines(result.ManualFix.ReviewerNotes, 12)...)
	lines = append(
		lines,
		"",
		"## Manual workflow",
		"",
		"1. Start from a clean checkout and create a branch from an up-to-date `main`.",
		"2. Confirm the recorded base SHA is still relevant.",
		"3. Inspect `candidate.patch`, then check it with `git apply --check candidate.patch`.",
		"4. Apply the patch only after review, inspect the resulting diff, and rerun the recorded verification.",
		"5. Use `draft-pr.md` as a starting point and preserve its automation provenance.",
		"6. Commit and open the pull request under your own identity.",
		"",
	)
	return os.WriteFile(
		filepath.Join(handoff, "README.md"),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	)
}

func renderCurrent(
	resultFile string,
	snapshotFile string,
	patchFile string,
	changesFile string,
	output string,
) error {
	if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	runID, runIDError := strconv.ParseInt(os.Getenv("CI_FLAKE_RUN_ID"), 10, 64)
	runAttempt, runAttemptError := strconv.Atoi(os.Getenv("CI_FLAKE_RUN_ATTEMPT"))
	metadata := renderMetadata{
		BaseSHA:         environmentValue("CI_FLAKE_BASE_SHA", "unknown"),
		CodexConclusion: environmentValue("CI_FLAKE_CODEX_CONCLUSION", "unknown"),
		PublicationMode: environmentValue("CI_FLAKE_PUBLICATION_MODE", "report-only"),
		RunID:           runID,
		RunAttempt:      runAttempt,
		RunURL:          os.Getenv("CI_FLAKE_RUN_URL"),
		TriggerMode:     environmentValue("CI_FLAKE_TRIGGER_MODE", "manual-reconciliation"),
		RunbookVersion:  environmentValue("CI_FLAKE_RUNBOOK_VERSION", "1"),
	}
	var problems []string
	if runIDError != nil || metadata.RunID < 1 {
		problems = append(problems, "workflow run ID is invalid")
	}
	if runAttemptError != nil || metadata.RunAttempt < 1 {
		problems = append(problems, "workflow run attempt is invalid")
	}
	if !safeGitHubURL(metadata.RunURL) {
		problems = append(problems, "workflow run URL is invalid")
	}
	if !inSet(triggerModes, metadata.TriggerMode) {
		problems = append(problems, "trigger mode is invalid")
	}
	if !inSet(publicationModes, metadata.PublicationMode) {
		problems = append(problems, "publication mode is invalid")
	}

	snapshotValue, err := readSnapshot(snapshotFile)
	if err != nil {
		problems = append(problems, err.Error())
		snapshotValue = fallbackSnapshot()
	}
	changesValue := changedFiles{
		SchemaVersion:  "1",
		Files:          []string{},
		ProtectedFiles: []string{},
	}
	if readValue, err := readChanges(changesFile); err != nil {
		problems = append(problems, err.Error())
	} else {
		changesValue = readValue
	}
	patch, err := os.ReadFile(patchFile)
	if err != nil {
		problems = append(
			problems,
			"candidate patch is unavailable: "+sanitizeText(err.Error(), 800),
		)
		patch = []byte{}
	}
	var result investigationResult
	if err := readJSONStrict(resultFile, &result, 128*1024); err != nil {
		problems = append(
			problems,
			"investigator result is unavailable: "+sanitizeText(err.Error(), 800),
		)
	} else {
		problems = append(problems, validateInvestigationResult(result)...)
	}
	if metadata.CodexConclusion != "success" {
		problems = append(
			problems,
			"Codex step conclusion was "+sanitizeText(metadata.CodexConclusion, 40),
		)
	}
	if len(problems) == 0 {
		problems = append(
			problems,
			coherenceErrors(result, snapshotValue, changesValue, patch)...,
		)
	}
	payloadValid := len(problems) == 0
	if !payloadValid {
		result = fallbackResult(strings.Join(problems, "; "))
	}
	result = sanitizeInvestigationResult(result)
	handoffReady := payloadValid && result.Outcome == "patch-ready"
	if err := writeJSON(resultFile, result); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(output, "summary.md"),
		[]byte(renderSummary(result, snapshotValue, metadata, handoffReady)),
		0o600,
	); err != nil {
		return err
	}
	if err := os.WriteFile(
		filepath.Join(output, "detailed-report.md"),
		[]byte(renderDetailedReport(result, snapshotValue, metadata, handoffReady)),
		0o600,
	); err != nil {
		return err
	}
	if handoffReady {
		if err := createManualHandoff(
			result,
			snapshotValue,
			metadata,
			patch,
			output,
		); err != nil {
			return err
		}
	}
	outcome := outcomeRecord{
		SchemaVersion:            "1",
		WorkflowRunID:            metadata.RunID,
		WorkflowRunAttempt:       metadata.RunAttempt,
		WorkflowRunURL:           metadata.RunURL,
		CreatedAt:                time.Now().UTC().Format(time.RFC3339Nano),
		TriggerMode:              metadata.TriggerMode,
		PublicationMode:          metadata.PublicationMode,
		Outcome:                  result.Outcome,
		PROpened:                 false,
		PRURL:                    nil,
		ManualFixHandoffProduced: handoffReady,
		NormalizedSignatureHash:  result.PrimarySignature.SHA256,
		PayloadValid:             payloadValid,
		FallbackUsed:             !payloadValid,
		LinkedHumanPRURL:         nil,
	}
	if errors := validateOutcomeRecord(outcome); len(errors) > 0 {
		return fmt.Errorf(
			"renderer produced an invalid outcome: %s",
			strings.Join(errors, "; "),
		)
	}
	if err := writeJSON(filepath.Join(output, "outcome.json"), outcome); err != nil {
		return err
	}
	if err := appendOutput(map[string]string{
		"handoff_ready":    strconv.FormatBool(handoffReady),
		"outcome":          result.Outcome,
		"payload_valid":    strconv.FormatBool(payloadValid),
		"pr_opened":        "false",
		"pr_url":           "",
		"publication_mode": metadata.PublicationMode,
		"trigger_mode":     metadata.TriggerMode,
	}); err != nil {
		return err
	}
	if !payloadValid || result.Outcome == "error" {
		message := strings.Join(problems, "; ")
		if message == "" {
			message = "investigator reported an automation error"
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func environmentValue(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func uniqueLatestRecords(records []outcomeRecord, limit int) []outcomeRecord {
	var valid []outcomeRecord
	for _, record := range records {
		if len(validateOutcomeRecord(record)) == 0 {
			valid = append(valid, record)
		}
	}
	sort.SliceStable(valid, func(left, right int) bool {
		leftTime, _ := time.Parse(time.RFC3339Nano, valid[left].CreatedAt)
		rightTime, _ := time.Parse(time.RFC3339Nano, valid[right].CreatedAt)
		if !leftTime.Equal(rightTime) {
			return leftTime.After(rightTime)
		}
		if valid[left].WorkflowRunID != valid[right].WorkflowRunID {
			return valid[left].WorkflowRunID > valid[right].WorkflowRunID
		}
		return valid[left].WorkflowRunAttempt > valid[right].WorkflowRunAttempt
	})
	seen := make(map[int64]struct{})
	result := make([]outcomeRecord, 0, min(len(valid), limit))
	for _, record := range valid {
		if _, ok := seen[record.WorkflowRunID]; ok {
			continue
		}
		seen[record.WorkflowRunID] = struct{}{}
		result = append(result, record)
		if len(result) == limit {
			break
		}
	}
	return result
}

func summarizeRecords(records []outcomeRecord) outcomeSummary {
	summary := outcomeSummary{Runs: len(records)}
	for _, record := range records {
		if record.PublicationMode == "github-app" &&
			record.Outcome != "partial" &&
			record.Outcome != "error" {
			summary.PublishEnabledCompleted++
			if record.PROpened {
				summary.PRs++
			}
		}
		if record.PublicationMode == "report-only" {
			summary.ReportOnly++
		}
		if record.PublicationMode == "github-token-prototype" {
			summary.Prototype++
		}
		if record.Outcome == "partial" || record.Outcome == "error" {
			summary.PartialOrError++
		}
		if record.ManualFixHandoffProduced {
			summary.Handoffs++
			if record.LinkedHumanPRURL != nil {
				summary.LinkedHumanPRs++
			}
		}
	}
	summary.NoPR = summary.PublishEnabledCompleted - summary.PRs
	return summary
}

func statisticsRow(label string, records []outcomeRecord) string {
	summary := summarizeRecords(records)
	return fmt.Sprintf(
		"| %s | %d | %d | %d | %s | %d | %d | %d |",
		label,
		summary.Runs,
		summary.PublishEnabledCompleted,
		summary.PRs,
		formatPercent(summary.PRs, summary.PublishEnabledCompleted),
		summary.NoPR,
		summary.ReportOnly,
		summary.PartialOrError,
	)
}

func recordsForTrigger(records []outcomeRecord, trigger string) []outcomeRecord {
	var result []outcomeRecord
	for _, record := range records {
		if record.TriggerMode == trigger {
			result = append(result, record)
		}
	}
	return result
}

func renderStatistics(
	currentFile string,
	historyDirectory string,
	limit int,
	output string,
) error {
	var current outcomeRecord
	if err := readJSONStrict(currentFile, &current, maxOutcomeRecordBytes); err != nil {
		return fmt.Errorf("current outcome is invalid: %w", err)
	}
	if errors := validateOutcomeRecord(current); len(errors) > 0 {
		return fmt.Errorf("current outcome is invalid: %s", strings.Join(errors, "; "))
	}
	candidates := []outcomeRecord{current}
	invalidFiles := 0
	var collection *outcomeCollection
	entries, err := os.ReadDir(historyDirectory)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(historyDirectory, entry.Name())
		if entry.Name() == "collection.json" {
			var value outcomeCollection
			if err := readJSONStrict(path, &value, maxOutcomeRecordBytes); err != nil ||
				value.SchemaVersion != "1" {
				invalidFiles++
			} else {
				collection = &value
			}
			continue
		}
		var record outcomeRecord
		if err := readJSONStrict(path, &record, maxOutcomeRecordBytes); err != nil ||
			len(validateOutcomeRecord(record)) > 0 {
			invalidFiles++
			continue
		}
		candidates = append(candidates, record)
	}
	records := uniqueLatestRecords(candidates, limit)
	overall := summarizeRecords(records)
	sampleRange := ""
	if len(records) > 0 {
		sampleRange = fmt.Sprintf(
			", from %s through %s",
			inlineText(records[len(records)-1].CreatedAt, 40),
			inlineText(records[0].CreatedAt, 40),
		)
	}
	lines := []string{
		"",
		"## Rolling outcomes",
		"",
		fmt.Sprintf(
			"Sample: **%d of %d requested runs**%s.",
			len(records),
			limit,
			sampleRange,
		),
		"",
		"| Trigger | Runs | Publish-enabled completed | PRs | PR-open rate | No PR | Report-only | Partial or error |",
		"| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
		statisticsRow("All", records),
		statisticsRow(
			"Daily reconciliation",
			recordsForTrigger(records, "daily-reconciliation"),
		),
		statisticsRow(
			"Main-failure fast path",
			recordsForTrigger(records, "main-failure-fast-path"),
		),
		statisticsRow(
			"Manual reconciliation",
			recordsForTrigger(records, "manual-reconciliation"),
		),
		"",
		fmt.Sprintf(
			"Automated PR rate excludes report-only and `GITHUB_TOKEN` prototype runs. Prototype records in this sample: **%d**.",
			overall.Prototype,
		),
		"",
		"### Manual handoff conversion",
		"",
		fmt.Sprintf("- Manual fix handoffs: **%d**", overall.Handoffs),
		fmt.Sprintf("- Linked human-authored PRs: **%d**", overall.LinkedHumanPRs),
		fmt.Sprintf(
			"- Linked handoff-to-PR rate: **%s**",
			formatPercent(overall.LinkedHumanPRs, overall.Handoffs),
		),
	}
	if collection == nil {
		lines = append(
			lines,
			"",
			"Prior outcome collection was unavailable; this table may contain only the current run.",
		)
	} else if collection.Invalid+invalidFiles > 0 {
		lines = append(
			lines,
			"",
			fmt.Sprintf(
				"Ignored **%d** invalid or unreadable prior outcome record(s).",
				collection.Invalid+invalidFiles,
			),
		)
	}
	lines = append(lines, "")
	return os.WriteFile(output, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
