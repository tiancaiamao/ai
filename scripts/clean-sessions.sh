#!/bin/bash

# Session Cleaner for .ai/sessions
# 清理空的、短的或废弃的 session 目录

set -euo pipefail

# 默认配置
SESSIONS_BASE="${1:-/Users/genius/.ai/sessions}"
MIN_LINES="${2:-3}"           # 最小行数阈值，低于此值视为短 session
MIN_SIZE="${3:-1000}"         # 最小文件大小阈值（字节），低于此值视为短 session
DRY_RUN="${DRY_RUN:-true}"    # 默认 dry-run，设置 DRY_RUN=false 执行实际删除
DAYS_OLD="${DAYS_OLD:-}"      # 可选：只清理 N 天前的 session
CLEAN_EMPTY="${CLEAN_EMPTY:-true}"         # 清理没有 messages.jsonl 的空 session-id 目录
CLEAN_EMPTY_HASH="${CLEAN_EMPTY_HASH:-false}" # 清理空的 cwd-hash 目录（更激进）

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

log_info() { echo -e "${BLUE}[INFO]${NC} $1" >&2; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1" >&2; }
log_delete() { echo -e "${RED}[DELETE]${NC} $1" >&2; }

# 计数器（用文件避免子 shell 变量隔离问题）
_COUNT_FILE=""

init_counters() {
    _COUNT_FILE=$(mktemp)
    echo "0 0 0" > "$_COUNT_FILE"
}

cleanup_counters() {
    [[ -n "$_COUNT_FILE" ]] && rm -f "$_COUNT_FILE"
}

get_counters() {
    cat "$_COUNT_FILE"
}

inc_total() { read t d e < "$_COUNT_FILE"; echo "$((t+1)) $d $e" > "$_COUNT_FILE"; }
inc_delete() { read t d e < "$_COUNT_FILE"; echo "$t $((d+1)) $e" > "$_COUNT_FILE"; }
inc_empty_hash() { read t d e < "$_COUNT_FILE"; echo "$t $d $((e+1))" > "$_COUNT_FILE"; }

# 判断一个目录名是否像 session-id（UUID 格式）
is_session_id_dir() {
    local name="$1"
    [[ "$name" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$ ]]
}

# 判断一个目录名是否是 cwd-hash 目录
is_hash_dir() {
    local name="$1"
    [[ "$name" == --* ]]
}

# 检查单个 session 目录，返回 0=保留, 1=删除
check_session() {
    local session_dir="$1"
    local msg_file="$session_dir/messages.jsonl"

    # 没有 messages.jsonl → 空 session 目录
    if [[ ! -f "$msg_file" ]]; then
        if [[ "$CLEAN_EMPTY" == "true" ]]; then
            log_delete "$(basename "$session_dir") (no messages.jsonl)"
            if [[ "$DRY_RUN" != "true" ]]; then
                rm -rf "$session_dir"
            fi
            return 1
        else
            log_warn "No messages.jsonl in $(basename "$session_dir")"
            return 0
        fi
    fi

    # 获取行数
    local lines
    lines=$(wc -l < "$msg_file" 2>/dev/null || echo 0)
    lines=$(echo "$lines" | tr -d ' ')

    # 获取文件大小
    local size
    size=$(stat -f%z "$msg_file" 2>/dev/null || stat -c%s "$msg_file" 2>/dev/null || echo 0)

    # 获取修改时间
    local mtime
    if [[ "$OSTYPE" == "darwin"* ]]; then
        mtime=$(stat -f%m "$session_dir" 2>/dev/null || echo 0)
    else
        mtime=$(stat -c%Y "$session_dir" 2>/dev/null || echo 0)
    fi

    local days_old=0
    if [[ -n "$DAYS_OLD" ]] && [[ "$mtime" -gt 0 ]]; then
        local now
        now=$(date +%s)
        days_old=$(( (now - mtime) / 86400 ))
    fi

    # 判断是否需要清理
    local should_delete=false
    local reason=""

    if [[ "$lines" -eq 0 ]]; then
        should_delete=true
        reason="empty"
    elif [[ "$lines" -lt "$MIN_LINES" ]] && [[ "$size" -lt "$MIN_SIZE" ]]; then
        should_delete=true
        reason="short ($lines lines, $size bytes)"
    fi

    # 检查时间限制
    if [[ -n "$DAYS_OLD" ]] && [[ "$days_old" -lt "$DAYS_OLD" ]]; then
        should_delete=false
    fi

    if $should_delete; then
        log_delete "$(basename "$session_dir") ($reason)"
        if [[ "$DRY_RUN" != "true" ]]; then
            rm -rf "$session_dir"
        fi
        return 1
    fi

    return 0
}

# 处理一个目录下的 session-id 子目录
process_sessions_in() {
    local parent_dir="$1"

    while IFS= read -r -d '' entry; do
        [[ ! -d "$entry" ]] && continue

        local name
        name=$(basename "$entry")

        # 只处理 UUID 格式的目录名
        is_session_id_dir "$name" || continue

        inc_total

        if check_session "$entry"; then
            :
        else
            inc_delete
        fi
    done < <(find "$parent_dir" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)
}

# 主扫描逻辑
scan_sessions() {
    init_counters
    trap cleanup_counters EXIT

    log_info "Scanning sessions in: $SESSIONS_BASE"
    log_info "Thresholds: < $MIN_LINES lines AND < $MIN_SIZE bytes"
    log_info "Clean empty session dirs: $CLEAN_EMPTY"
    log_info "Clean empty hash dirs: $CLEAN_EMPTY_HASH"
    if [[ -n "$DAYS_OLD" ]]; then
        log_info "Age filter: > $DAYS_OLD days old"
    fi
    log_info "Dry run: $DRY_RUN"
    echo "" >&2

    local base_name
    base_name=$(basename "$SESSIONS_BASE")

    if is_hash_dir "$base_name"; then
        # 直接指定了某个 workspace 目录
        process_sessions_in "$SESSIONS_BASE"
    else
        # 顶层 sessions 目录，遍历所有 cwd-hash
        while IFS= read -r -d '' hash_dir; do
            process_sessions_in "$hash_dir"

            # 清理空的 cwd-hash 目录
            if [[ "$CLEAN_EMPTY_HASH" == "true" ]]; then
                local hash_name
                hash_name=$(basename "$hash_dir")
                local session_count
                session_count=$(find "$hash_dir" -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2>/dev/null | while read -r n; do is_session_id_dir "$n" && echo y; done | wc -l | tr -d ' ')
                if [[ "$session_count" -eq 0 ]]; then
                    log_delete "$hash_name/ (empty hash dir)"
                    if [[ "$DRY_RUN" != "true" ]]; then
                        rm -rf "$hash_dir"
                    fi
                    inc_empty_hash
                fi
            fi
        done < <(find "$SESSIONS_BASE" -mindepth 1 -maxdepth 1 -type d -print0)
    fi

    # 输出汇总
    echo "" >&2
    local total del empty_hash
    read total del empty_hash < "$_COUNT_FILE"
    log_info "Summary:"
    echo "  Total sessions:       $total"
    echo "  Would delete:         $del sessions"
    if [[ "$CLEAN_EMPTY_HASH" == "true" ]]; then
        echo "  Empty hash dirs:      $empty_hash"
    fi
}

# 显示使用说明
show_usage() {
    cat << EOF
Usage: $0 [sessions_base] [min_lines] [min_size]

Arguments:
  sessions_base    Sessions directory path (default: /Users/genius/.ai/sessions)
                   Can be top-level sessions dir or a specific workspace dir.
  min_lines        Minimum line count threshold (default: 3)
  min_size         Minimum file size threshold in bytes (default: 1000)

Environment Variables:
  DRY_RUN              Set to 'false' to actually delete (default: true)
  DAYS_OLD             Only delete sessions older than N days
  CLEAN_EMPTY          Clean session dirs without messages.jsonl (default: true)
  CLEAN_EMPTY_HASH     Clean empty cwd-hash directories (default: false)

Session matching:
  Only directories with UUID names (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)
  are treated as session directories. Internal directories like current/,
  checkpoints/, llm-context/, working-memory/ etc. are always skipped.

Examples:
  # Dry run (show what would be deleted)
  $0

  # Dry run with custom thresholds
  $0 /Users/genius/.ai/sessions 5 2000

  # Scan a specific workspace
  $0 /Users/genius/.ai/sessions/--Users-genius-project-ai--

  # Actually delete sessions with < 5 lines AND < 2000 bytes
  DRY_RUN=false $0 /Users/genius/.ai/sessions 5 2000

  # Only delete sessions older than 7 days
  DAYS_OLD=7 $0 /Users/genius/.ai/sessions 3 1000

  # Delete empty session dirs + empty hash dirs (aggressive cleanup)
  CLEAN_EMPTY=true CLEAN_EMPTY_HASH=true DRY_RUN=false DAYS_OLD=7 $0

EOF
}

# 主函数
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