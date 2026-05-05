#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
: "${CI_HISTORY_TARGET_BRANCH:?CI_HISTORY_TARGET_BRANCH is required}"
: "${GITHUB_STEP_SUMMARY:?GITHUB_STEP_SUMMARY is required}"

CI_HISTORY_WORKFLOW_FILE="${CI_HISTORY_WORKFLOW_FILE:-ci.yml}"
CI_HISTORY_EVENT="${CI_HISTORY_EVENT:-push}"
CI_HISTORY_MAX_FAILED_JOBS="${CI_HISTORY_MAX_FAILED_JOBS:-20}"
GITHUB_SERVER_URL="${GITHUB_SERVER_URL:-https://github.com}"
GITHUB_RUN_ID="${GITHUB_RUN_ID:-}"

OWNER="${GITHUB_REPOSITORY%%/*}"
REPO="${GITHUB_REPOSITORY#*/}"
API_ROOT="https://api.github.com/repos/${OWNER}/${REPO}"
WORK_DIR="$(mktemp -d)"
RUNS_FILE="${WORK_DIR}/runs.ndjson"
JOBS_FILE="${WORK_DIR}/jobs.ndjson"

cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

touch "${RUNS_FILE}" "${JOBS_FILE}"

urlencode() {
  jq -rn --arg value "$1" '$value | @uri'
}

api_get() {
  local path="$1"
  curl --fail --silent --show-error \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    -H "User-Agent: temporalio-sdk-go-ci-history" \
    "${API_ROOT}${path}"
}

iso_days_ago() {
  date -u -d "$1 days ago" +"%Y-%m-%dT%H:%M:%SZ"
}

append_error_summary() {
  {
    echo "## CI History"
    echo
    echo "Failed to build CI history summary."
    echo
    echo '```'
    echo "$1"
    echo '```'
    echo
  } >>"${GITHUB_STEP_SUMMARY}"
}

main() {
  local branch workflow since encoded_branch encoded_workflow encoded_created page runs_count response
  branch="${CI_HISTORY_TARGET_BRANCH}"
  workflow="${CI_HISTORY_WORKFLOW_FILE}"
  since="$(iso_days_ago 30)"
  encoded_branch="$(urlencode "${branch}")"
  encoded_workflow="$(urlencode "${workflow}")"
  encoded_created="$(urlencode ">=${since}")"

  page=1
  while true; do
    response="$(api_get "/actions/workflows/${encoded_workflow}/runs?branch=${encoded_branch}&event=${CI_HISTORY_EVENT}&status=completed&created=${encoded_created}&per_page=100&page=${page}")"

    jq -c --arg current_run_id "${GITHUB_RUN_ID}" \
      '.workflow_runs[]? | select((.id | tostring) != $current_run_id)' \
      <<<"${response}" >>"${RUNS_FILE}"

    runs_count="$(jq '.workflow_runs | length' <<<"${response}")"
    if [[ "${runs_count}" -lt 100 ]]; then
      break
    fi
    page=$((page + 1))
  done

  while IFS= read -r run; do
    [[ -n "${run}" ]] || continue

    local run_id run_sha run_created_at run_updated_at job_page job_response jobs_count
    run_id="$(jq -r '.id' <<<"${run}")"
    run_sha="$(jq -r '.head_sha // ""' <<<"${run}")"
    run_created_at="$(jq -r '.created_at' <<<"${run}")"
    run_updated_at="$(jq -r '.updated_at // .created_at' <<<"${run}")"

    job_page=1
    while true; do
      job_response="$(api_get "/actions/runs/${run_id}/jobs?per_page=100&page=${job_page}")"

      jq -c \
        --arg run_id "${run_id}" \
        --arg run_sha "${run_sha}" \
        --arg run_created_at "${run_created_at}" \
        --arg run_updated_at "${run_updated_at}" \
        '.jobs[]? + {
          run_id: $run_id,
          run_sha: $run_sha,
          run_created_at: $run_created_at,
          run_updated_at: $run_updated_at
        }' <<<"${job_response}" >>"${JOBS_FILE}"

      jobs_count="$(jq '.jobs | length' <<<"${job_response}")"
      if [[ "${jobs_count}" -lt 100 ]]; then
        break
      fi
      job_page=$((job_page + 1))
    done
  done <"${RUNS_FILE}"

  render_summary
}

render_summary() {
  local seven_cutoff thirty_cutoff branch_label
  seven_cutoff="$(iso_days_ago 7)"
  thirty_cutoff="$(iso_days_ago 30)"
  branch_label="${CI_HISTORY_TARGET_BRANCH}"

  {
    echo "## CI History for ${branch_label}"
    echo
    echo "## Job History"
    echo
    job_stats_table
    echo
    echo "## Most Failure-Prone Jobs, 30 days"
    echo
    most_failure_prone_jobs
    echo
    echo "## Recent Failed Jobs"
    echo
    recent_failed_jobs
    echo
    echo "_Source: ${CI_HISTORY_EVENT} runs for \`${CI_HISTORY_WORKFLOW_FILE}\` on \`${CI_HISTORY_TARGET_BRANCH}\`._"
  } >>"${GITHUB_STEP_SUMMARY}"
}

job_stats_table() {
  local rows
  rows="$(
    jq -rs --arg seven_cutoff "$(iso_days_ago 7)" --arg thirty_cutoff "$(iso_days_ago 30)" '
      def failed: .conclusion != null and (.conclusion | IN("success", "skipped", "neutral") | not);
      def reportable: .name != "CI History Summary";
      def normalized_name: .name | sub(" / .+$"; "");
      def job_order:
        if .name | startswith("unit-test") then 10
        elif .name | startswith("integration-test-no-cache") then 20
        elif .name | startswith("integration-test-with-cache") then 30
        elif .name == "docker-compose-test" then 40
        elif .name | startswith("cloud-test") then 50
        elif .name | startswith("features-test") then 60
        else 90 end;
      def os_order:
        if .name | contains("(ubuntu-latest,") then 0
        elif .name | contains("(macos-intel,") then 1
        elif .name | contains("(macos-arm,") then 2
        elif .name | contains("(windows-latest,") then 3
        else 9 end;
      def go_order:
        if .name | contains("oldstable") then 0
        elif .name | contains("stable") then 1
        else 9 end;
      def fips_order:
        if .name | contains(", true)") then 0
        elif .name | contains(", false)") then 1
        else 9 end;
      def pass_rate($runs; $failures):
        if $runs == 0 then "n/a" else (((($runs - $failures) * 100 / $runs) * 10 | round / 10 | tostring) + "%") end;
      def stats($jobs; $cutoff):
        ($jobs | map(select((.completed_at // .run_updated_at // .run_created_at) >= $cutoff and .conclusion != "skipped"))) as $jobs |
        {
          runs: ($jobs | length),
          failures: ($jobs | map(select(failed)) | length)
        };

      map(select(reportable and .conclusion != "skipped"))
      | map(. + {name: normalized_name})
      | group_by([.run_id, .name])
      | map(.[0] + {conclusion: (if (map(select(failed)) | length) > 0 then "failure" else .[0].conclusion end)})
      | group_by(.name)
      | map(
          stats(.; $seven_cutoff) as $seven |
          stats(.; $thirty_cutoff) as $thirty |
          {
            name: .[0].name,
            job_order: (.[0] | job_order),
            os_order: (.[0] | os_order),
            go_order: (.[0] | go_order),
            fips_order: (.[0] | fips_order),
            seven_runs: $seven.runs,
            seven_failures: $seven.failures,
            seven_pass_rate: pass_rate($seven.runs; $seven.failures),
            thirty_runs: $thirty.runs,
            thirty_failures: $thirty.failures,
            thirty_pass_rate: pass_rate($thirty.runs; $thirty.failures)
          }
        )
      | sort_by([.job_order, .os_order, .go_order, .fips_order, .name])
      | .[]
      | "| \(.name) | \(.seven_runs) | \(.seven_failures) | \(.seven_pass_rate) | \(.thirty_runs) | \(.thirty_failures) | \(.thirty_pass_rate) |"
    ' "${JOBS_FILE}"
  )"

  if [[ -z "${rows}" ]]; then
    echo "No completed jobs found in the last 30 days."
    return
  fi

  echo "| Job | 7d Runs | 7d Failures | 7d Pass Rate | 30d Runs | 30d Failures | 30d Pass Rate |"
  echo "|---|---:|---:|---:|---:|---:|---:|"
  echo "${rows}"
}

most_failure_prone_jobs() {
  local rows
  rows="$(
    jq -rs --arg thirty_cutoff "$(iso_days_ago 30)" '
      def failed: .conclusion != null and (.conclusion | IN("success", "skipped", "neutral") | not);
      def reportable: .name != "CI History Summary";
      def normalized_name: .name | sub(" / .+$"; "");
      def pass_rate($runs; $failures):
        if $runs == 0 then "n/a" else (((($runs - $failures) * 100 / $runs) * 10 | round / 10 | tostring) + "%") end;

      map(select(reportable and (.completed_at // .run_updated_at // .run_created_at) >= $thirty_cutoff and .conclusion != "skipped"))
      | map(. + {name: normalized_name})
      | group_by([.run_id, .name])
      | map(.[0] + {conclusion: (if (map(select(failed)) | length) > 0 then "failure" else .[0].conclusion end)})
      | group_by(.name)
      | map({
          name: .[0].name,
          runs: length,
          failures: (map(select(failed)) | length)
        })
      | map(select(.failures > 0))
      | sort_by([-.failures, .name])
      | .[]
      | "| \(.name) | \(.runs) | \(.failures) | \(pass_rate(.runs; .failures)) |"
    ' "${JOBS_FILE}"
  )"

  if [[ -z "${rows}" ]]; then
    echo "No failed jobs found in the last 30 days."
    return
  fi

  echo "| Job | 30d Runs | 30d Failures | 30d Pass Rate |"
  echo "|---|---:|---:|---:|"
  echo "${rows}"
}

recent_failed_jobs() {
  local rows_file
  rows_file="${WORK_DIR}/failed-jobs.tsv"

  jq -rs \
    --arg server_url "${GITHUB_SERVER_URL}" \
    --arg repository "${GITHUB_REPOSITORY}" '
    def failed: .conclusion != null and (.conclusion | IN("success", "skipped", "neutral") | not);
    def reportable: .name != "CI History Summary";
    def normalized_name: .name | sub(" / .+$"; "");
    def short_sha: .run_sha[0:7];
    def completed: .completed_at // .run_updated_at // .run_created_at;
    def date_only: completed[0:10];
    def job_url: .html_url // "\($server_url)/\($repository)/actions/runs/\(.run_id)";

    map(select(reportable) | . + {name: normalized_name})
    | group_by([.run_id, .name])
    | map(if (map(select(failed)) | length) > 0 then (map(select(failed))[0]) else .[0] end)
    | map(select(failed))
    | map([
        .name,
        completed,
        "- [\(date_only) `\(short_sha)`](\(job_url))"
      ])
    | .[]
    | @tsv
  ' "${JOBS_FILE}" | sort -t $'\t' -k1,1 -k2,2r >"${rows_file}"

  if [[ ! -s "${rows_file}" ]]; then
    echo "No recent failed jobs found."
    return
  fi

  awk -F '\t' -v limit="${CI_HISTORY_MAX_FAILED_JOBS}" '
    shown >= limit {
      next
    }
    $1 != current_group {
      current_group = $1
      print "### " current_group
      print ""
    }
    {
      print $3
      shown += 1
    }
  ' "${rows_file}"
}

if ! main; then
  append_error_summary "See the CI History Summary job logs for details."
  exit 1
fi
