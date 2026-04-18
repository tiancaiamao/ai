#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

AG_BIN="${AG_BIN:-$HOME/.ai/skills/ag/ag}"
IMPLEMENTER_PROMPT="${IMPLEMENTER_PROMPT:-$HOME/.ai/skills/implement/prompts/implementer.md}"
SPEC_REVIEWER_PROMPT="${SPEC_REVIEWER_PROMPT:-$HOME/.ai/skills/implement/prompts/spec-reviewer.md}"
QUALITY_REVIEWER_PROMPT="${QUALITY_REVIEWER_PROMPT:-$HOME/.ai/skills/implement/prompts/quality-reviewer.md}"

COMMAND="run"
PLAN_FILE=""
SPEC_FILE=""
TEAM_ID=""
TEAM_DESCRIPTION="workflow implement phase"
WORKERS=2
WORKDIR="$PWD"
RESULTS_DIR=""
MAX_ROUNDS=2
IMPLEMENT_TIMEOUT=900
REVIEW_TIMEOUT=600
FORCE_CLEANUP=0
DRY_RUN=0
SKIP_IMPORT=0

usage() {
  cat <<'EOF'
implement-team.sh — TEAM mode executor for workflow implement phase

Usage:
  implement-team.sh run [options]
  implement-team.sh status [--team TEAM_ID]
  implement-team.sh cleanup [--team TEAM_ID] [--force]
  implement-team.sh help

Run Options:
  --plan FILE                 PLAN.yml path (required for run)
  --spec FILE                 SPEC.md path (optional, preferred)
  --team TEAM_ID              Team ID (default: feat-<cwd-name>)
  --team-description TEXT     Team description (default: workflow implement phase)
  --workers N                 Number of worker loops (default: 2)
  --cwd DIR                   Working directory for agent execution (default: current dir)
  --results-dir DIR           Task report output dir (default: <plan-dir>/impl-results)
  --max-rounds N              Max implement+review repair rounds per task (default: 2)
  --implement-timeout SEC     Timeout per implementer run (default: 900)
  --review-timeout SEC        Timeout per review run (default: 600)
  --skip-import               Skip PLAN import even when queue is empty
  --force-cleanup             Cleanup team after successful completion
  --dry-run                   Do not spawn agents; mark tasks done with synthetic reports

Environment:
  AG_BIN
  IMPLEMENTER_PROMPT
  SPEC_REVIEWER_PROMPT
  QUALITY_REVIEWER_PROMPT

Examples:
  implement-team.sh run \
    --plan .workflow/artifacts/feature/PLAN.yml \
    --spec .workflow/artifacts/feature/SPEC.md \
    --team feat-user-auth \
    --workers 3

  implement-team.sh status --team feat-user-auth
  implement-team.sh cleanup --team feat-user-auth --force
EOF
}

log() {
  printf '[implement-team] %s\n' "$*" >&2
}

fail() {
  log "ERROR: $*"
  exit 1
}

ag() {
  "$AG_BIN" "$@"
}

sanitize_team_id() {
  local raw="$1"
  raw="$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')"
  raw="$(printf '%s' "$raw" | tr -cd 'a-z0-9._-')"
  if [[ -z "$raw" ]]; then
    raw="feat-team"
  fi
  printf '%s' "$raw"
}

task_count_by_status() {
  local status="$1"
  local out
  out="$(ag task list --status "$status" 2>/dev/null || true)"
  if [[ -z "$out" ]] || grep -q '^No tasks$' <<<"$out"; then
    echo 0
    return
  fi
  awk 'NR>1 && NF>0 {c++} END {print c+0}' <<<"$out"
}

task_count_total() {
  local out
  out="$(ag task list 2>/dev/null || true)"
  if [[ -z "$out" ]] || grep -q '^No tasks$' <<<"$out"; then
    echo 0
    return
  fi
  awk 'NR>1 && NF>0 {c++} END {print c+0}' <<<"$out"
}

extract_verdict() {
  local file="$1"
  local last
  last="$(grep -Eo 'APPROVED|CHANGES_REQUESTED|REJECTED' "$file" | tail -n1 || true)"
  case "$last" in
    APPROVED) echo "APPROVED" ;;
    CHANGES_REQUESTED|REJECTED) echo "CHANGES_REQUESTED" ;;
    *) echo "CHANGES_REQUESTED" ;;
  esac
}

spawn_and_collect() {
  local agent_id="$1"
  local prompt_file="$2"
  local input_file="$3"
  local timeout_sec="$4"
  local output_file="$5"

  ag spawn \
    --id "$agent_id" \
    --system "$prompt_file" \
    --input "$input_file" \
    --cwd "$WORKDIR" \
    --timeout "${timeout_sec}s" >/dev/null

  if ! ag wait "$agent_id" --timeout "$timeout_sec" >/dev/null; then
    ag rm -f "$agent_id" >/dev/null 2>&1 || true
    return 1
  fi

  ag output "$agent_id" >"$output_file"
  ag rm "$agent_id" >/dev/null 2>&1 || true
  return 0
}

process_task() {
  local worker_id="$1"
  local task_id="$2"
  local claimed_desc="$3"

  local task_info task_desc task_spec
  task_info="$(ag task show "$task_id" || true)"
  task_desc="$(sed -n 's/^description: //p' <<<"$task_info" | head -n1)"
  if [[ -z "$task_desc" ]]; then
    task_desc="$claimed_desc"
  fi
  task_spec="$(sed -n 's/^spec: //p' <<<"$task_info" | head -n1)"
  if [[ -z "$task_spec" ]]; then
    task_spec="$SPEC_FILE"
  fi

  local feedback=""
  local round verdict
  local task_report_dir="$RESULTS_DIR/$task_id"
  mkdir -p "$task_report_dir"

  for ((round=1; round<=MAX_ROUNDS; round++)); do
    local stamp impl_id spec_id qual_id
    stamp="$(date +%s%N)"
    impl_id="${worker_id}-${task_id}-impl-r${round}-${stamp}"
    spec_id="${worker_id}-${task_id}-spec-r${round}-${stamp}"
    qual_id="${worker_id}-${task_id}-quality-r${round}-${stamp}"

    local impl_input impl_out spec_input spec_out qual_input qual_out
    impl_input="$task_report_dir/impl-input-r${round}.md"
    impl_out="$task_report_dir/impl-output-r${round}.md"
    spec_input="$task_report_dir/spec-review-input-r${round}.md"
    spec_out="$task_report_dir/spec-review-output-r${round}.md"
    qual_input="$task_report_dir/quality-review-input-r${round}.md"
    qual_out="$task_report_dir/quality-review-output-r${round}.md"

    cat >"$impl_input" <<EOF
TASK_ID: $task_id
TASK_DESCRIPTION: $task_desc
WORKER: $worker_id

WORKDIR: $WORKDIR
PLAN_FILE: $PLAN_FILE
SPEC_FILE: ${task_spec:-"(none)"}

CONTEXT:
- Implement exactly this task.
- Edit code in the current repository at WORKDIR.
- Run relevant tests for changed behavior.
- Provide concise summary of code changes and test results.

${feedback:+PREVIOUS_REVIEW_FEEDBACK:
$feedback
}
EOF

    if [[ "$DRY_RUN" -eq 1 ]]; then
      cat >"$impl_out" <<EOF
DRY RUN implement output for $task_id (round $round)
EOF
    else
      if ! spawn_and_collect "$impl_id" "$IMPLEMENTER_PROMPT" "$impl_input" "$IMPLEMENT_TIMEOUT" "$impl_out"; then
        ag task fail "$task_id" --error "implementer timeout/failure (round $round)" >/dev/null
        return 1
      fi
    fi

    cat >"$spec_input" <<EOF
## What Was Requested
$task_desc

## Implementer Output
$(cat "$impl_out")

## Verification Task
Review the real code in WORKDIR and verify specification compliance.
End with APPROVED or CHANGES_REQUESTED.
EOF

    if [[ "$DRY_RUN" -eq 1 ]]; then
      echo "APPROVED" >"$spec_out"
    else
      if ! spawn_and_collect "$spec_id" "$SPEC_REVIEWER_PROMPT" "$spec_input" "$REVIEW_TIMEOUT" "$spec_out"; then
        ag task fail "$task_id" --error "spec-review timeout/failure (round $round)" >/dev/null
        return 1
      fi
    fi

    verdict="$(extract_verdict "$spec_out")"
    if [[ "$verdict" != "APPROVED" ]]; then
      feedback="$(cat "$spec_out")"
      if (( round == MAX_ROUNDS )); then
        ag task fail "$task_id" --error "spec review rejected after $MAX_ROUNDS round(s)" >/dev/null
        return 1
      fi
      continue
    fi

    cat >"$qual_input" <<EOF
## Task
$task_desc

## Implementer Output
$(cat "$impl_out")

## Spec Review Result
$(cat "$spec_out")

## Verification Task
Review code quality, correctness, and safety in WORKDIR.
End with APPROVED or CHANGES_REQUESTED.
EOF

    if [[ "$DRY_RUN" -eq 1 ]]; then
      echo "APPROVED" >"$qual_out"
    else
      if ! spawn_and_collect "$qual_id" "$QUALITY_REVIEWER_PROMPT" "$qual_input" "$REVIEW_TIMEOUT" "$qual_out"; then
        ag task fail "$task_id" --error "quality-review timeout/failure (round $round)" >/dev/null
        return 1
      fi
    fi

    verdict="$(extract_verdict "$qual_out")"
    if [[ "$verdict" != "APPROVED" ]]; then
      feedback="$(cat "$qual_out")"
      if (( round == MAX_ROUNDS )); then
        ag task fail "$task_id" --error "quality review rejected after $MAX_ROUNDS round(s)" >/dev/null
        return 1
      fi
      continue
    fi

    local summary_file="$task_report_dir/summary.md"
    cat >"$summary_file" <<EOF
# Task $task_id Summary

## Description
$task_desc

## Worker
$worker_id

## Round
$round

## Implementer Output
$(cat "$impl_out")

## Spec Review
$(cat "$spec_out")

## Quality Review
$(cat "$qual_out")
EOF

    ag task done "$task_id" --output "$summary_file" >/dev/null
    log "[$worker_id] task $task_id done"
    return 0
  done

  ag task fail "$task_id" --error "exhausted rounds unexpectedly" >/dev/null
  return 1
}

worker_loop() {
  local worker_id="$1"
  local idle_wait=2
  log "[$worker_id] started"

  while true; do
    local claimed task_id task_desc
    if claimed="$(ag task next --as "$worker_id" 2>/dev/null)"; then
      task_id="$(awk -F'\t' '{print $1}' <<<"$claimed")"
      task_desc="$(awk -F'\t' '{print $2}' <<<"$claimed")"
      if [[ -z "$task_id" ]]; then
        log "[$worker_id] received malformed claim output: $claimed"
        return 1
      fi
      log "[$worker_id] claimed $task_id"
      if ! process_task "$worker_id" "$task_id" "$task_desc"; then
        log "[$worker_id] failed task $task_id"
        return 1
      fi
      continue
    fi

    local pending claimed_count
    pending="$(task_count_by_status pending)"
    if (( pending == 0 )); then
      log "[$worker_id] no pending tasks; exiting"
      return 0
    fi
    claimed_count="$(task_count_by_status claimed)"
    if (( claimed_count == 0 )); then
      log "[$worker_id] pending tasks remain but none are claimable (blocked/deadlock)"
      return 2
    fi
    sleep "$idle_wait"
  done
}

ensure_run_prereqs() {
  [[ -x "$AG_BIN" ]] || fail "AG_BIN not executable: $AG_BIN"
  [[ -f "$PLAN_FILE" ]] || fail "PLAN file not found: $PLAN_FILE"
  [[ -f "$IMPLEMENTER_PROMPT" ]] || fail "missing implementer prompt: $IMPLEMENTER_PROMPT"
  [[ -f "$SPEC_REVIEWER_PROMPT" ]] || fail "missing spec reviewer prompt: $SPEC_REVIEWER_PROMPT"
  [[ -f "$QUALITY_REVIEWER_PROMPT" ]] || fail "missing quality reviewer prompt: $QUALITY_REVIEWER_PROMPT"
  [[ "$WORKERS" =~ ^[0-9]+$ ]] || fail "--workers must be integer"
  (( WORKERS >= 1 )) || fail "--workers must be >= 1"
  [[ "$MAX_ROUNDS" =~ ^[0-9]+$ ]] || fail "--max-rounds must be integer"
  (( MAX_ROUNDS >= 1 )) || fail "--max-rounds must be >= 1"
}

run_command() {
  ensure_run_prereqs

  if [[ -z "$TEAM_ID" ]]; then
    TEAM_ID="feat-$(sanitize_team_id "$(basename "$WORKDIR")")"
  else
    TEAM_ID="$(sanitize_team_id "$TEAM_ID")"
  fi
  if [[ -z "$RESULTS_DIR" ]]; then
    RESULTS_DIR="$(dirname "$PLAN_FILE")/impl-results"
  fi
  mkdir -p "$RESULTS_DIR"

  if ag team use "$TEAM_ID" >/dev/null 2>&1; then
    log "using existing team: $TEAM_ID"
  else
    ag team init "$TEAM_ID" --description "$TEAM_DESCRIPTION" >/dev/null
    log "initialized team: $TEAM_ID"
  fi

  local total_before
  total_before="$(task_count_total)"
  if (( total_before == 0 )) && (( SKIP_IMPORT == 0 )); then
    if [[ -n "$SPEC_FILE" ]]; then
      ag task import-plan "$PLAN_FILE" --spec "$SPEC_FILE" >/dev/null
    else
      ag task import-plan "$PLAN_FILE" >/dev/null
    fi
    log "imported tasks from plan: $PLAN_FILE"
  else
    log "task queue already has $total_before task(s); skipping import"
  fi

  local pids=()
  local i
  for ((i=1; i<=WORKERS; i++)); do
    worker_loop "worker-$i" &
    pids+=("$!")
  done

  local failed=0
  for pid in "${pids[@]}"; do
    if ! wait "$pid"; then
      failed=1
    fi
  done

  if (( failed == 1 )); then
    log "one or more workers failed"
    ag task list || true
    exit 1
  fi

  ag team done --team "$TEAM_ID" >/dev/null || true
  log "team marked done: $TEAM_ID"
  ag task list || true

  if (( FORCE_CLEANUP == 1 )); then
    ag team cleanup --team "$TEAM_ID" --force >/dev/null || true
    log "team cleaned up: $TEAM_ID"
  fi
}

status_command() {
  if [[ -n "$TEAM_ID" ]]; then
    ag team use "$TEAM_ID" >/dev/null
  fi
  ag team current
  ag team list
  ag task list || true
}

cleanup_command() {
  if [[ -n "$TEAM_ID" ]]; then
    if (( FORCE_CLEANUP == 1 )); then
      ag team cleanup --team "$TEAM_ID" --force
    else
      ag team cleanup --team "$TEAM_ID"
    fi
    return
  fi
  if (( FORCE_CLEANUP == 1 )); then
    ag team cleanup --force
  else
    ag team cleanup
  fi
}

parse_args() {
  if (( $# == 0 )); then
    usage
    exit 1
  fi

  case "${1:-}" in
    run|status|cleanup|help)
      COMMAND="$1"
      shift
      ;;
  esac

  while (( $# > 0 )); do
    case "$1" in
      --plan) PLAN_FILE="$2"; shift 2 ;;
      --spec) SPEC_FILE="$2"; shift 2 ;;
      --team) TEAM_ID="$2"; shift 2 ;;
      --team-description) TEAM_DESCRIPTION="$2"; shift 2 ;;
      --workers) WORKERS="$2"; shift 2 ;;
      --cwd) WORKDIR="$2"; shift 2 ;;
      --results-dir) RESULTS_DIR="$2"; shift 2 ;;
      --max-rounds) MAX_ROUNDS="$2"; shift 2 ;;
      --implement-timeout) IMPLEMENT_TIMEOUT="$2"; shift 2 ;;
      --review-timeout) REVIEW_TIMEOUT="$2"; shift 2 ;;
      --skip-import) SKIP_IMPORT=1; shift ;;
      --force-cleanup) FORCE_CLEANUP=1; shift ;;
      --force) FORCE_CLEANUP=1; shift ;;
      --dry-run) DRY_RUN=1; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
  done
}

main() {
  parse_args "$@"
  case "$COMMAND" in
    run)
      [[ -n "$PLAN_FILE" ]] || fail "--plan is required for run"
      run_command
      ;;
    status)
      status_command
      ;;
    cleanup)
      cleanup_command
      ;;
    help)
      usage
      ;;
    *)
      fail "unsupported command: $COMMAND"
      ;;
  esac
}

main "$@"
