#!/bin/bash

# Session Cleaner for .ai/sessions
# Clean empty, short, stale, or abandoned session directories

set -euo pipefail

# Default configuration
SESSIONS_BASE="${1:-/Users/genius/.ai/sessions}"
MIN_LINES="${MIN_LINES:-3}"
MIN_SIZE="${MIN_SIZE:-1000}"
DRY_RUN="${DRY_RUN:-true}"
DAYS_OLD="${DAYS_OLD:-}"
CLEAN_EMPTY="${CLEAN_EMPTY:-true}"
CLEAN_EMPTY_HASH="${CLEAN_EMPTY_HASH:-false}"
KEEP_RECENT="${KEEP_RECENT:-0}"
MODE="${MODE:-short}"

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { printf "${BLUE}[INFO]${NC} %s\n" "$1" >&2; }
log_warn() { printf "${YELLOW}[WARN]${NC} %s\n" "$1" >&2; }
log_delete() { printf "${RED}[DELETE]${NC} %s\n" "$1" >&2; }
log_keep() { printf "${GREEN}[KEEP]${NC} %s\n" "$1" >&2; }

# Counters
_total=0 _delete=0 _kept=0 _empty_hash=0

# Check if a directory name is a session-id (UUID format)
is_session_id_dir() {
    [[ "$1" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]
}

is_hash_dir() {
    [[ "$1" == --* ]]
}

# Check a single session directory. Returns 0=keep, 1=delete
check_session() {
    local session_dir="$1"
    local lines="$2"
    local size="$3"
    local days_old="$4"
    local msg_file="$session_dir/messages.jsonl"

    # No messages.jsonl → empty session directory
    if [[ ! -f "$msg_file" ]]; then
        if [[ "$CLEAN_EMPTY" == "true" ]]; then
            log_delete "$(basename "$session_dir") (no messages.jsonl)"
            [[ "$DRY_RUN" != "true" ]] && rm -rf "$session_dir"
            return 1
        fi
        return 0
    fi

    # Check age filter first (applies to all modes)
    if [[ -n "$DAYS_OLD" ]] && [[ "$days_old" -lt "$DAYS_OLD" ]]; then
        return 0
    fi

    local should_delete=false
    local reason=""

    case "$MODE" in
        short)
            if [[ "$lines" -eq 0 ]]; then
                should_delete=true
                reason="empty"
            elif [[ "$lines" -lt "$MIN_LINES" ]] && [[ "$size" -lt "$MIN_SIZE" ]]; then
                should_delete=true
                reason="short ($lines lines, $size bytes)"
            elif ! grep -q '"role":"assistant"' "$msg_file" 2>/dev/null; then
                should_delete=true
                reason="no assistant reply ($lines lines)"
            fi
            ;;
        stale)
            local stale_days="${STALE_DAYS:-7}"
            local stale_lines="${STALE_LINES:-50}"
            local stale_size="${STALE_SIZE:-50000}"
            if [[ "$days_old" -ge "$stale_days" ]]; then
                if [[ "$lines" -lt "$stale_lines" ]] || [[ "$size" -lt "$stale_size" ]]; then
                    should_delete=true
                    reason="stale ($days_old days old, $lines lines, $size bytes)"
                fi
            fi
            ;;
        all)
            should_delete=true
            reason="all mode cleanup ($days_old days old, $lines lines)"
            ;;
        *)
            echo "Error: Unknown MODE '$MODE'. Use: short | stale | all" >&2
            exit 1
            ;;
    esac

    if $should_delete; then
        log_delete "$(basename "$session_dir") ($reason)"
        [[ "$DRY_RUN" != "true" ]] && rm -rf "$session_dir"
        return 1
    fi

    return 0
}

# Process sessions under a parent directory using batch-collected metadata
process_sessions_in() {
    local parent_dir="$1"

        # Batch collect all metadata in ONE find pass to avoid fork bomb
    # Format: mtime<tab>lines<tab>size<tab>path
    local data_file
    data_file=$(mktemp) || { echo "Error: mktemp failed" >&2; return 1; }

    # Use explicit cleanup via a subshell wrapper instead of nested trap
    _cleanup() {
        [[ -n "${data_file:-}" ]] && rm -f "$data_file"
    }

    # Single pass: find all messages.jsonl + compute mtime of parent dir
    # For sessions without messages.jsonl, we also need the dir mtime
    {
        # Sessions WITH messages.jsonl: get line count, size, and dir mtime
        find "$parent_dir" -mindepth 2 -maxdepth 2 -name 'messages.jsonl' -type f -exec sh -c '
            for f; do
                dir=$(dirname "$f")
                lines=$(wc -l < "$f" 2>/dev/null || echo 0)
                lines=$(echo "$lines" | tr -d " ")
                size=$(stat -f%z "$f" 2>/dev/null || stat -c%s "$f" 2>/dev/null || echo 0)
                mtime=$(stat -f%m "$dir" 2>/dev/null || stat -c%Y "$dir" 2>/dev/null || echo 0)
                printf "%s\t%s\t%s\t%s\n" "$mtime" "$lines" "$size" "$dir"
            done
        ' _ {} +

        # Sessions WITHOUT messages.jsonl: just need dir path (mark with -1 lines)
        find "$parent_dir" -mindepth 1 -maxdepth 1 -type d | while IFS= read -r dir; do
            name=$(basename "$dir")
            is_session_id_dir "$name" || continue
            [[ -f "$dir/messages.jsonl" ]] && continue
            mtime=$(stat -f%m "$dir" 2>/dev/null || stat -c%Y "$dir" 2>/dev/null || echo 0)
            printf "%s\t-1\t0\t%s\n" "$mtime" "$dir"
        done
    } > "$data_file"

    # Read collected data and process
    local now
    now=$(date +%s)

    # Sort by mtime descending for KEEP_RECENT support
    local sorted_data
    if [[ "$KEEP_RECENT" -gt 0 ]]; then
        sorted_data=$(sort -t$'\t' -k1 -rn "$data_file")
    else
        sorted_data=$(cat "$data_file")
    fi

    local kept=0
    while IFS=$'\t' read -r mtime lines size path; do
        [[ -z "$path" ]] && continue

        local name
        name=$(basename "$path")
        is_session_id_dir "$name" || continue

        _total=$((_total + 1))

        # KEEP_RECENT: skip the N newest
        if [[ "$KEEP_RECENT" -gt 0 ]] && [[ "$kept" -lt "$KEEP_RECENT" ]]; then
            log_keep "$name (recent #$((kept+1)))"
            kept=$((kept + 1))
            _kept=$((_kept + 1))
            continue
        fi

        local days_old=0
        if [[ "$mtime" -gt 0 ]]; then
            days_old=$(( (now - mtime) / 86400 ))
        fi

        # -1 lines means no messages.jsonl
        if [[ "$lines" -eq -1 ]]; then
            lines=0
            if [[ "$CLEAN_EMPTY" == "true" ]]; then
                log_delete "$name (no messages.jsonl)"
                [[ "$DRY_RUN" != "true" ]] && rm -rf "$path"
                _delete=$((_delete + 1))
            fi
            continue
        fi

                if check_session "$path" "$lines" "$size" "$days_old"; then
            :
        else
            _delete=$((_delete + 1))
        fi
    done <<< "$sorted_data"

    _cleanup
}

# Main scan logic
scan_sessions() {
    log_info "Scanning sessions in: $SESSIONS_BASE"
    log_info "Mode: $MODE"
    case "$MODE" in
        short)
            log_info "Short threshold: < $MIN_LINES lines AND < $MIN_SIZE bytes"
            log_info "Also cleaning: sessions with no assistant reply"
            ;;
        stale)
            log_info "Stale threshold: >= ${STALE_DAYS:-7} days old AND (< ${STALE_LINES:-50} lines OR < ${STALE_SIZE:-50000} bytes)"
            ;;
        all)
            log_info "Deleting all sessions"
            ;;
    esac
    log_info "Clean empty session dirs: $CLEAN_EMPTY"
    log_info "Clean empty hash dirs: $CLEAN_EMPTY_HASH"
    [[ -n "$DAYS_OLD" ]] && log_info "Age filter: only sessions > $DAYS_OLD days old"
    [[ "$KEEP_RECENT" -gt 0 ]] && log_info "Keep recent: $KEEP_RECENT most recent sessions"
    log_info "Dry run: $DRY_RUN"
    echo "" >&2

    local base_name
    base_name=$(basename "$SESSIONS_BASE")

    if is_hash_dir "$base_name"; then
        process_sessions_in "$SESSIONS_BASE"
    else
        local hash_dirs=()
        while IFS= read -r -d '' hash_dir; do
            hash_dirs+=("$hash_dir")
        done < <(find "$SESSIONS_BASE" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)

        for hash_dir in "${hash_dirs[@]}"; do
            process_sessions_in "$hash_dir"

            if [[ "$CLEAN_EMPTY_HASH" == "true" ]]; then
                local hash_name
                hash_name=$(basename "$hash_dir")
                local session_count=0
                while IFS= read -r -d '' entry; do
                    local n
                    n=$(basename "$entry")
                    is_session_id_dir "$n" && session_count=$((session_count + 1))
                done < <(find "$hash_dir" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)
                if [[ "$session_count" -eq 0 ]]; then
                    log_delete "$hash_name/ (empty hash dir)"
                    [[ "$DRY_RUN" != "true" ]] && rm -rf "$hash_dir"
                    _empty_hash=$((_empty_hash + 1))
                fi
            fi
        done
    fi

    echo "" >&2
    log_info "Summary:"
    echo "  Total sessions:       $_total"
    echo "  Would delete:         $_delete sessions"
    [[ "$KEEP_RECENT" -gt 0 ]] && echo "  Kept (recent):        $_kept sessions"
    [[ "$CLEAN_EMPTY_HASH" == "true" ]] && echo "  Empty hash dirs:      $_empty_hash"
}

show_usage() {
    cat << EOF
Usage: $0 [sessions_base]

Clean up session directories under .ai/sessions.

Arguments:
  sessions_base    Sessions directory path (default: /Users/genius/.ai/sessions)
                   Can be top-level sessions dir or a specific workspace dir.

Environment Variables:
  MODE             Cleanup mode (default: short):
                     short  - Delete empty, short (< MIN_LINES AND < MIN_SIZE),
                              or sessions with no assistant reply
                     stale  - Delete old AND small sessions (safe bulk cleanup)
                     all    - Delete everything (respects DAYS_OLD, KEEP_RECENT)

  DRY_RUN          Set to 'false' to actually delete (default: true)

  # short mode options:
  MIN_LINES        Min line count threshold (default: 3)
  MIN_SIZE         Min file size threshold in bytes (default: 1000)

  # stale mode options:
  STALE_DAYS       Min age in days to consider stale (default: 7)
  STALE_LINES     Max line count for stale (default: 50)
  STALE_SIZE       Max file size for stale in bytes (default: 50000)

  # General options:
  DAYS_OLD         Only delete sessions older than N days (applies to all modes)
  KEEP_RECENT      Keep the N most recent sessions (default: 0 = keep all)
  CLEAN_EMPTY      Clean session dirs without messages.jsonl (default: true)
  CLEAN_EMPTY_HASH Clean empty cwd-hash directories (default: false)

Session matching:
  Only directories with UUID names (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
  are treated as session directories. Internal directories like current/,
  checkpoints/, llm-context/, working-memory/ etc. are always skipped.

Examples:
  # Dry run with default (short) mode
  $0

  # Dry run stale mode - see what's old and small
  MODE=stale $0 /Users/genius/.ai/sessions

  # Delete sessions older than 7 days that are small (<50 lines or <50KB)
  MODE=stale DRY_RUN=false $0

  # Delete ALL sessions but keep the 20 most recent
  MODE=all KEEP_RECENT=20 DRY_RUN=false $0

  # Scan a specific workspace
  $0 /Users/genius/.ai/sessions/--Users-genius-project-ai--

  # Aggressive: delete empty + empty hash dirs older than 7 days
  CLEAN_EMPTY=true CLEAN_EMPTY_HASH=true DAYS_OLD=7 DRY_RUN=false $0

  # Custom stale thresholds
  MODE=stale STALE_DAYS=3 STALE_LINES=20 STALE_SIZE=20000 DRY_RUN=false $0

EOF
}

main() {
    if [[ "${1:-}" == "-h" ]] || [[ "${1:-}" == "--help" ]]; then
        show_usage
        exit 0
    fi

    if [[ ! -d "$SESSIONS_BASE" ]]; then
        echo "Error: Sessions directory not found: $SESSIONS_BASE"
        exit 1
    fi

    if [[ "$DRY_RUN" == "false" ]]; then
        log_warn "REAL DELETE MODE enabled - this cannot be undone!"
        read -p "Continue? (yes/no): " confirm
        if [[ "$confirm" != "yes" ]]; then
            echo "Aborted."
            exit 0
        fi
    fi

    scan_sessions
}

main "$@"