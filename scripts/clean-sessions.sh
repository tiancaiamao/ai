#!/bin/bash

# Session Cleaner for .ai/sessions
# Cleans old, empty, short, or stale session directories.
#
# Supports two session formats:
#   - Legacy: <uuid>.jsonl + <uuid>.meta.json files directly under project dir
#   - New: <uuid>/ directories containing messages.jsonl, checkpoints/, etc.
#
# Cleanup modes (can be combined):
#   1. Empty sessions: no messages.jsonl or 0 lines
#   2. Short sessions: few lines AND small file size
#   3. Old sessions: older than N days (regardless of size)
#   4. Stale sessions: old AND short (most practical default)
#   5. Orphaned files: legacy .jsonl/.meta.json with no matching directory

set -euo pipefail

# ============================================================================
# Configuration
# ============================================================================

SESSIONS_BASE="${SESSIONS_BASE:-/Users/genius/.ai/sessions}"
DRY_RUN="${DRY_RUN:-true}"

# Short session thresholds (used in short/stale modes)
MIN_LINES="${MIN_LINES:-10}"
MIN_SIZE="${MIN_SIZE:-5000}"    # bytes

# Age threshold in days (used in old/stale modes)
DAYS_OLD="${DAYS_OLD:-}"

# Cleanup mode: empty|short|old|stale|all
#   empty  - only empty sessions (no messages or 0 lines)
#   short  - empty + short sessions (few lines AND small size)
#   old    - sessions older than DAYS_OLD days (any size)
#   stale  - sessions older than DAYS_OLD AND short (recommended for cron)
#   all    - delete everything old OR short (aggressive)
MODE="${MODE:-short}"

# ============================================================================
# Colors (all output goes to stderr so stdout is clean for data)
# ============================================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
DIM='\033[2m'
NC='\033[0m'

log_info()  { echo -e "${BLUE}[INFO]${NC} $1" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $1" >&2; }
log_ok()    { echo -e "${GREEN}[OK]${NC} $1" >&2; }
log_del()   { echo -e "${RED}[DEL]${NC} $1" >&2; }
log_skip()  { echo -e "${CYAN}[SKIP]${NC} $1" >&2; }
log_dim()   { echo -e "${DIM}$1${NC}" >&2; }

# ============================================================================
# Platform helpers
# ============================================================================

get_size() {
    if [[ "$OSTYPE" == "darwin"* ]]; then
        stat -f%z "$1" 2>/dev/null || echo 0
    else
        stat -c%s "$1" 2>/dev/null || echo 0
    fi
}

get_mtime() {
    if [[ "$OSTYPE" == "darwin"* ]]; then
        stat -f%m "$1" 2>/dev/null || echo 0
    else
        stat -c%Y "$1" 2>/dev/null || echo 0
    fi
}

get_dir_size_kb() {
    du -sk "$1" 2>/dev/null | cut -f1 || echo 0
}

days_since() {
    local now
    now=$(date +%s)
    echo $(( (now - $1) / 86400 ))
}

human_size() {
    local kb=$1
    if [[ "$kb" -ge 1048576 ]]; then
        printf "%.1fGB" "$(echo "scale=1; $kb/1048576" | bc)"
    elif [[ "$kb" -ge 1024 ]]; then
        printf "%.1fMB" "$(echo "scale=1; $kb/1024" | bc)"
    else
        echo "${kb}KB"
    fi
}

# ============================================================================
# Session analysis
# ============================================================================

# Analyze a session directory (new format: <uuid>/)
# Prints: "lines:size_bytes:days_old:format" to stdout
analyze_session_dir() {
    local session_dir="$1"
    local msg_file="$session_dir/messages.jsonl"

    if [[ ! -f "$msg_file" ]]; then
        local mtime
        mtime=$(get_mtime "$session_dir")
        local days
        days=$(days_since "$mtime")
        echo "0:0:$days:dir-empty"
        return
    fi

    local lines
    lines=$(wc -l < "$msg_file" 2>/dev/null || echo 0)
    lines=$(echo "$lines" | tr -d ' ')

    local size
    size=$(get_size "$msg_file")

    local mtime
    mtime=$(get_mtime "$session_dir")
    local days
    days=$(days_since "$mtime")

    echo "$lines:$size:$days:dir"
}

# Analyze a legacy session file (<uuid>.jsonl)
# Prints: "lines:size_bytes:days_old:legacy" to stdout
analyze_legacy_file() {
    local jsonl_file="$1"

    local lines
    lines=$(wc -l < "$jsonl_file" 2>/dev/null || echo 0)
    lines=$(echo "$lines" | tr -d ' ')

    local size
    size=$(get_size "$jsonl_file")

    local mtime
    mtime=$(get_mtime "$jsonl_file")
    local days
    days=$(days_since "$mtime")

    echo "$lines:$size:$days:legacy"
}

# ============================================================================
# Deletion decision
# ============================================================================

# decide_delete "lines:size:days:format"
# Returns 0 if should delete, prints reason to stdout
# Returns 1 if keep, prints nothing
decide_delete() {
    local IFS=':'
    read -r lines size days fmt <<< "$1"

    local reason=""

    case "$MODE" in
        empty)
            if [[ "$lines" -eq 0 ]] && [[ "$fmt" == "dir-empty" ]]; then
                reason="empty (no messages.jsonl)"
            fi
            ;;
        short)
            if [[ "$lines" -eq 0 ]] && [[ "$fmt" == "dir-empty" ]]; then
                reason="empty (no messages.jsonl)"
            elif [[ "$lines" -lt "$MIN_LINES" ]] && [[ "$size" -lt "$MIN_SIZE" ]]; then
                reason="short (${lines} lines, ${size} bytes)"
            fi
            ;;
        old)
            if [[ -n "$DAYS_OLD" ]] && [[ "$days" -ge "$DAYS_OLD" ]]; then
                reason="old (${days}d, ${lines} lines)"
            fi
            ;;
        stale)
            if [[ "$lines" -eq 0 ]] && [[ "$fmt" == "dir-empty" ]]; then
                reason="empty (no messages.jsonl)"
            elif [[ -n "$DAYS_OLD" ]] && [[ "$days" -ge "$DAYS_OLD" ]] && \
                 [[ "$lines" -lt "$MIN_LINES" ]] && [[ "$size" -lt "$MIN_SIZE" ]]; then
                reason="stale (${days}d old, ${lines} lines, ${size}B)"
            fi
            ;;
        all)
            if [[ "$lines" -eq 0 ]] && [[ "$fmt" == "dir-empty" ]]; then
                reason="empty (no messages.jsonl)"
            elif [[ "$lines" -lt "$MIN_LINES" ]] && [[ "$size" -lt "$MIN_SIZE" ]]; then
                reason="short (${lines} lines, ${size} bytes)"
            elif [[ -n "$DAYS_OLD" ]] && [[ "$days" -ge "$DAYS_OLD" ]]; then
                reason="old (${days}d, ${lines} lines)"
            fi
            ;;
        *)
            echo "ERROR: Unknown mode '$MODE'" >&2
            return 1
            ;;
    esac

    if [[ -n "$reason" ]]; then
        echo "$reason"
        return 0
    fi
    return 1
}

# ============================================================================
# Core scan logic
# ============================================================================

# scan_project scans one project directory.
# Prints result to stdout: "count:delete:size_kb:delete_size_kb"
# All logging goes to stderr.
scan_project() {
    local project_dir="$1"
    local project_name
    project_name=$(basename "$project_dir")

    local p_count=0
    local p_delete=0
    local p_size=0
    local p_del_size=0

    # ---- Scan new-format session directories ----
    while IFS= read -r -d '' session_dir; do
        [[ ! -d "$session_dir" ]] && continue

        local dirname
        dirname=$(basename "$session_dir")

        # Skip special dirs
        [[ "$dirname" == "checkpoints" || "$dirname" == "current" || \
           "$dirname" == "llm-context" || "$dirname" == "current.json" ]] && continue

        p_count=$((p_count + 1))

        local dir_size
        dir_size=$(get_dir_size_kb "$session_dir")
        p_size=$((p_size + dir_size))

        local info
        info=$(analyze_session_dir "$session_dir")

        local reason
        reason=$(decide_delete "$info") || true

        if [[ -n "$reason" ]]; then
            p_delete=$((p_delete + 1))
            p_del_size=$((p_del_size + dir_size))

            if [[ "$DRY_RUN" == "true" ]]; then
                log_del "$project_name/$dirname ($reason)"
            else
                log_del "$project_name/$dirname ($reason) - REMOVING"
                rm -rf "$session_dir"
            fi
        fi

    done < <(find "$project_dir" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)

    # ---- Scan legacy format files (<uuid>.jsonl) ----
    while IFS= read -r -d '' jsonl_file; do
        p_count=$((p_count + 1))

        local file_size_b
        file_size_b=$(get_size "$jsonl_file")
        local file_size_kb=$(( (file_size_b + 512) / 1024 ))
        [[ $file_size_kb -lt 1 ]] && file_size_kb=1
        p_size=$((p_size + file_size_kb))

        local info
        info=$(analyze_legacy_file "$jsonl_file")

        local reason
        reason=$(decide_delete "$info") || true

        local fname
        fname=$(basename "$jsonl_file")

        if [[ -n "$reason" ]]; then
            p_delete=$((p_delete + 1))
            p_del_size=$((p_del_size + file_size_kb))

            if [[ "$DRY_RUN" == "true" ]]; then
                log_del "$project_name/$fname ($reason)"
            else
                log_del "$project_name/$fname ($reason) - REMOVING"
                rm -f "$jsonl_file"
                rm -f "${jsonl_file%.jsonl}.meta.json"
                rm -f "${jsonl_file%.jsonl}.jsonl.lock"
            fi
        fi

    done < <(find "$project_dir" -mindepth 1 -maxdepth 1 -name '*.jsonl' -type f -print0 2>/dev/null)

    # ---- Clean orphaned .meta.json files ----
    while IFS= read -r -d '' meta_file; do
        local base
        base=$(basename "$meta_file" .meta.json)

        if [[ ! -d "$project_dir/$base" ]] && \
           [[ ! -f "$project_dir/${base}.jsonl" ]]; then
            p_delete=$((p_delete + 1))

            if [[ "$DRY_RUN" == "true" ]]; then
                log_del "$project_name/$(basename "$meta_file") (orphaned .meta.json)"
            else
                log_del "$project_name/$(basename "$meta_file") (orphaned) - REMOVING"
                rm -f "$meta_file"
            fi
        fi
    done < <(find "$project_dir" -mindepth 1 -maxdepth 1 -name '*.meta.json' -type f -print0 2>/dev/null)

    # Output result (only line to stdout)
    echo "${p_count}:${p_delete}:${p_size}:${p_del_size}"
}

# ============================================================================
# Commands
# ============================================================================

cmd_scan_all() {
    local total_projects=0
    local total_sessions=0
    local total_delete=0
    local total_size=0
    local total_delete_size=0

    log_info "Scanning: $SESSIONS_BASE"
    log_info "Mode: $MODE | MIN_LINES=$MIN_LINES | MIN_SIZE=${MIN_SIZE}B"
    if [[ -n "$DAYS_OLD" ]]; then
        log_info "Age filter: > $DAYS_OLD days"
    fi
    log_info "Dry run: $DRY_RUN"
    echo "" >&2

    while IFS= read -r -d '' project_dir; do
        [[ ! -d "$project_dir" ]] && continue

        local project_name
        project_name=$(basename "$project_dir")

        total_projects=$((total_projects + 1))

        # scan_project prints one line to stdout: count:delete:size:delsize
        local result
        result=$(scan_project "$project_dir")

        local IFS=':'
        read -r p_count p_delete p_size p_del_size <<< "$result"

        total_sessions=$((total_sessions + p_count))
        total_delete=$((total_delete + p_delete))
        total_size=$((total_size + p_size))
        total_delete_size=$((total_delete_size + p_del_size))

        # Show per-project summary if there are deletions
        if [[ "$p_delete" -gt 0 ]]; then
            log_info "$project_name: $p_delete/$p_count sessions deletable ($(human_size "$p_del_size"))"
        fi

    done < <(find "$SESSIONS_BASE" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)

    echo "" >&2
    echo "============================================" >&2
    log_info "Summary"
    echo "  Projects scanned: $total_projects" >&2
    echo "  Total sessions:   $total_sessions" >&2
    echo "  To delete:        $total_delete sessions" >&2
    echo "  Total size:       $(human_size "$total_size")" >&2
    if [[ "$DRY_RUN" == "true" ]]; then
        echo "  Would free:       $(human_size "$total_delete_size")" >&2
    else
        echo "  Freed:            $(human_size "$total_delete_size")" >&2
    fi
    echo "============================================" >&2

    if [[ "$total_delete" -eq 0 ]]; then
        log_ok "Nothing to clean!"
    fi
}

cmd_scan_single() {
    local target="$1"

    # If it's a full path, use directly; otherwise try as project name
    if [[ -d "$target" ]]; then
        :
    else
        target="$SESSIONS_BASE/--${target}--"
        if [[ ! -d "$target" ]]; then
            echo "Error: Project not found: $target" >&2
            echo "Hint: Use --list to see available projects" >&2
            exit 1
        fi
    fi

    log_info "Scanning: $target"
    log_info "Mode: $MODE | MIN_LINES=$MIN_LINES | MIN_SIZE=${MIN_SIZE}B"
    if [[ -n "$DAYS_OLD" ]]; then
        log_info "Age filter: > $DAYS_OLD days"
    fi
    log_info "Dry run: $DRY_RUN"
    echo "" >&2

    local result
    result=$(scan_project "$target")

    local IFS=':'
    read -r p_count p_delete p_size p_del_size <<< "$result"

    echo "" >&2
    echo "============================================" >&2
    log_info "Summary"
    echo "  Total sessions: $p_count" >&2
    echo "  To delete:      $p_delete sessions" >&2
    echo "  Total size:     $(human_size "$p_size")" >&2
    if [[ "$DRY_RUN" == "true" ]]; then
        echo "  Would free:     $(human_size "$p_del_size")" >&2
    else
        echo "  Freed:          $(human_size "$p_del_size")" >&2
    fi
    echo "============================================" >&2
}

cmd_list() {
    printf "%-10s %-10s %-10s %s\n" "SIZE" "SESSIONS" "EMPTY" "PROJECT" >&2
    echo "------------------------------------------------------------" >&2

    # Collect all projects first, then process to avoid nested find fd leaks
    declare -a project_dirs=()
    while IFS= read -r -d '' project_dir; do
        [[ ! -d "$project_dir" ]] && continue
        project_dirs+=("$project_dir")
    done < <(find "$SESSIONS_BASE" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)

    # Collect entries
    declare -a entries=()
    for project_dir in "${project_dirs[@]}"; do
        local project_name
        project_name=$(basename "$project_dir")

        local dir_count
        dir_count=$(find "$project_dir" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')
        local file_count
        file_count=$(find "$project_dir" -mindepth 1 -maxdepth 1 -name '*.jsonl' -type f 2>/dev/null | wc -l | tr -d ' ')
        local count=$((dir_count + file_count))

        local size
        size=$(get_dir_size_kb "$project_dir")

        # Count empty sessions (no messages.jsonl)
        local empty=0
        for sd in "$project_dir"/*/; do
            [[ ! -d "$sd" ]] && continue
            if [[ ! -f "$sd/messages.jsonl" ]]; then
                empty=$((empty + 1))
            fi
        done

        entries+=("${size}|${count}|${empty}|${project_name}")
    done

    # Sort by size descending
    IFS=$'\n' sorted=($(for e in "${entries[@]}"; do echo "$e"; done | sort -t'|' -k1 -rn)); unset IFS

    for entry in "${sorted[@]}"; do
        IFS='|' read -r s c e n <<< "$entry"
        printf "%-10s %-10s %-10s %s\n" "$(human_size "$s")" "$c" "$e" "$n"
    done
}

# ============================================================================
# Usage
# ============================================================================

show_usage() {
    cat << 'EOF'
Usage: clean-sessions.sh [command] [options]

Commands:
  scan [project]     Scan and show what would be deleted (default)
  clean [project]    Actually delete sessions
  list               List all projects with stats
  help               Show this help

Options (environment variables):
  MODE               Cleanup mode (default: short)
                       empty  - only empty sessions
                       short  - empty + short (< MIN_LINES AND < MIN_SIZE)
                       old    - all sessions older than DAYS_OLD (any size)
                       stale  - old AND short (safe for cron)
                       all    - anything old OR short (aggressive)
  MIN_LINES          Line count threshold (default: 10)
  MIN_SIZE           Size in bytes threshold (default: 5000)
  DAYS_OLD           Age in days for "old" modes
  SESSIONS_BASE      Base directory (default: ~/.ai/sessions)

Examples:
  # Preview: delete empty and short sessions
  ./clean-sessions.sh scan

  # Preview: all sessions older than 30 days
  DAYS_OLD=30 MODE=old ./clean-sessions.sh scan

  # Preview: old AND short (safe for cron)
  DAYS_OLD=30 MODE=stale ./clean-sessions.sh scan

  # Actually delete short sessions
  ./clean-sessions.sh clean

  # Actually delete everything older than 14 days
  DAYS_OLD=14 MODE=old ./clean-sessions.sh clean

  # Clean a specific project
  ./clean-sessions.sh scan Users-genius-project-ai

  # List all projects
  ./clean-sessions.sh list
EOF
}

# ============================================================================
# Main
# ============================================================================

main() {
    local cmd="${1:-scan}"
    shift 2>/dev/null || true

    case "$cmd" in
        -h|--help|help)
            show_usage
            exit 0
            ;;
        list)
            cmd_list
            ;;
        scan)
            DRY_RUN=true
            if [[ -n "${1:-}" ]]; then
                cmd_scan_single "$1"
            else
                cmd_scan_all
            fi
            ;;
        clean)
            DRY_RUN=false
            log_warn "REAL DELETE MODE - this cannot be undone!"
            log_warn "Mode: $MODE | MIN_LINES=$MIN_LINES | MIN_SIZE=${MIN_SIZE}B | DAYS_OLD=${DAYS_OLD:-none}"
            echo "" >&2
            read -p "Type 'yes' to continue: " confirm
            if [[ "$confirm" != "yes" ]]; then
                echo "Aborted."
                exit 0
            fi
            if [[ -n "${1:-}" ]]; then
                cmd_scan_single "$1"
            else
                cmd_scan_all
            fi
            ;;
        *)
            echo "Unknown command: $cmd" >&2
            echo "Use: scan, clean, list, or help" >&2
            exit 1
            ;;
    esac
}

main "$@"
