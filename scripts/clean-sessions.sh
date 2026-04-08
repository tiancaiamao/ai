#!/bin/bash

# Session Cleaner for .ai/sessions
# 清理空的或短的 session 目录

set -euo pipefail

# 默认配置
SESSIONS_BASE="${1:-/Users/genius/.ai/sessions}"
MIN_LINES="${2:-3}"           # 最小行数阈值，低于此值视为短 session
MIN_SIZE="${3:-1000}"         # 最小文件大小阈值（字节），低于此值视为短 session
DRY_RUN="${DRY_RUN:-true}"    # 默认 dry-run，设置 DRY_RUN=false 执行实际删除
DAYS_OLD="${DAYS_OLD:-}"      # 可选：只清理 N 天前的 session

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

log_delete() {
    echo -e "${RED}[DELETE]${NC} $1"
}

# 检查会话目录
check_session() {
    local session_dir="$1"
    local msg_file="$session_dir/messages.jsonl"

    # 检查 messages.jsonl 是否存在
    if [[ ! -f "$msg_file" ]]; then
        log_warn "No messages.jsonl in $(basename "$session_dir")"
        return
    fi

    # 获取行数
    local lines
    lines=$(wc -l < "$msg_file" 2>/dev/null || echo 0)

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
        local session_name=$(basename "$session_dir")
        if [[ "$DRY_RUN" == "true" ]]; then
            log_delete "$session_name ($reason)"
        else
            log_delete "$session_name ($reason) - REMOVING"
            rm -rf "$session_dir"
        fi
        return 1  # 返回非0表示被删除
    fi

    return 0  # 返回0表示保留
}

# 遍历所有会话目录
scan_sessions() {
    local total_count=0
    local delete_count=0
    local total_size=0
    local delete_size=0

    log_info "Scanning sessions in: $SESSIONS_BASE"
    log_info "Thresholds: < $MIN_LINES lines OR < $MIN_SIZE bytes"
    if [[ -n "$DAYS_OLD" ]]; then
        log_info "Age filter: > $DAYS_OLD days old"
    fi
    log_info "Dry run: $DRY_RUN"
    echo ""

    # 查找所有 session 目录（session-id 目录，在 cwd-hash 之下）
    while IFS= read -r -d '' session_dir; do
        # 跳过 current.json 等非目录文件
        if [[ ! -d "$session_dir" ]]; then
            continue
        fi

        # 跳过 meta.json 等特殊文件
        local dirname=$(basename "$session_dir")
        if [[ "$dirname" =~ ^(current\.json|.*\.meta\.json)$ ]]; then
            continue
        fi

        total_count=$((total_count + 1))

        # 获取目录大小
        local dir_size=0
        if [[ "$OSTYPE" == "darwin"* ]]; then
            dir_size=$(du -sk "$session_dir" 2>/dev/null | cut -f1 || echo 0)
        else
            dir_size=$(du -sk "$session_dir" 2>/dev/null | cut -f1 || echo 0)
        fi
        total_size=$((total_size + dir_size))

        # 检查会话
        if ! check_session "$session_dir"; then
            delete_count=$((delete_count + 1))
            delete_size=$((delete_size + dir_size))
        fi

    done < <(find "$SESSIONS_BASE" -mindepth 2 -maxdepth 2 -type d -print0)

    echo ""
    log_info "Summary:"
    echo "  Total sessions: $total_count"
    echo "  Would delete: $delete_count sessions"
    echo "  Total size: ${total_size}KB"
    echo "  Would free: ${delete_size}KB"
}

# 显示使用说明
show_usage() {
    cat << EOF
Usage: $0 [sessions_base] [min_lines] [min_size]

Arguments:
  sessions_base    Sessions directory path (default: /Users/genius/.ai/sessions)
  min_lines        Minimum line count threshold (default: 3)
  min_size         Minimum file size threshold in bytes (default: 1000)

Environment Variables:
  DRY_RUN          Set to 'false' to actually delete (default: true)
  DAYS_OLD         Only delete sessions older than N days

Examples:
  # Dry run (show what would be deleted)
  $0

  # Dry run with custom thresholds
  $0 /Users/genius/.ai/sessions 5 2000

  # Actually delete sessions with < 5 lines AND < 2000 bytes
  DRY_RUN=false $0 /Users/genius/.ai/sessions 5 2000

  # Only delete sessions older than 7 days
  DAYS_OLD=7 $0 /Users/genius/.ai/sessions 3 1000

  # Actually delete sessions > 7 days old with < 3 lines
  DRY_RUN=false DAYS_OLD=7 $0

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

# 运行主函数
main "$@"