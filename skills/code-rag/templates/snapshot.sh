#!/bin/bash
# Code Expert — Snapshot Manager (generic)
#
# 核心设计：
#   - frozen/ 是冻结的"镜像"，永远不被修改
#   - 每次使用时 fork 出一份到 runtime/
#   - runtime/ 是可变的工作副本，随便问，随便脏
#
# 用法：
#   ./snapshot.sh start    — 从冻结镜像 fork 出工作副本，启动 expert
#   ./snapshot.sh ask "Q"  — 提问
#   ./snapshot.sh stop     — 停止（保留工作副本，可 resume）
#   ./snapshot.sh clean    — 清除工作副本（下次 start 会重新 fork）
#   ./snapshot.sh freeze   — 用当前 session 重建冻结镜像（wiki 更新后用）
#   ./snapshot.sh status   — 查看状态
#   ./snapshot.sh reset    — 清除冻结镜像 + 工作副本（完全重来）

set -euo pipefail

# ─── 项目配置（使用前修改这两行） ───
PROJECT_NAME="<PROJECT>"              # 例: cse, tidb, myapp
EXPERT_LABEL="<PROJECT> Code Expert"  # 例: CSE Code Expert

# ─── 自动推导路径 ───
WIKI_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SNAPSHOT_DIR="$WIKI_DIR/snapshot"
FREEZE_DIR="$SNAPSHOT_DIR/frozen"
RUNTIME_DIR="$SNAPSHOT_DIR/runtime"
ID_FILE="$SNAPSHOT_DIR/expert.id"
TMUX_SESSION="${PROJECT_NAME}-expert"
TMUX_SESSION_FREEZE="${PROJECT_NAME}-expert-freeze"

cd "$WIKI_DIR"

# ─── freeze：构建冻结镜像 ───
do_freeze() {
    echo "🔬 构建冻结镜像..."
    mkdir -p "$FREEZE_DIR"

    do_stop 2>/dev/null || true

    rm -rf "$FREEZE_DIR"
    mkdir -p "$FREEZE_DIR"

    echo "   启动临时 agent 加载 wiki（约 3-5 分钟）..."

    local tmp_id_file="/tmp/${PROJECT_NAME}-expert-freeze-$$.id"
    rm -f "$tmp_id_file"

    tmux kill-session -t "$TMUX_SESSION_FREEZE" 2>/dev/null || true
    tmux new-session -d -s "$TMUX_SESSION_FREEZE" \
        "ai serve \
            --input-file $SNAPSHOT_DIR/bootstrap.md \
            --session $FREEZE_DIR \
            --name '${PROJECT_NAME}-expert-freeze' \
            --id-file $tmp_id_file \
            --timeout 10m"

    local agent_id=""
    for i in $(seq 1 30); do
        if [ -f "$tmp_id_file" ] && [ -s "$tmp_id_file" ]; then
            agent_id=$(cat "$tmp_id_file")
            break
        fi
        sleep 1
    done

    if [ -z "$agent_id" ]; then
        echo "❌ 临时 agent 启动失败"
        tmux kill-session -t "$TMUX_SESSION_FREEZE" 2>/dev/null || true
        return 1
    fi

    echo "   等待 wiki 加载完成..."
    ai send --id "$agent_id" --wait --timeout 8m --summary "请确认已就绪" 2>&1 | tail -5

    ai kill --id "$agent_id" 2>/dev/null || true
    tmux kill-session -t "$TMUX_SESSION_FREEZE" 2>/dev/null || true
    rm -f "$tmp_id_file"

    local msg_count=$(wc -l < "$FREEZE_DIR/messages.jsonl" | tr -d ' ')
    local size=$(du -h "$FREEZE_DIR/messages.jsonl" | cut -f1)

    echo "✅ 冻结镜像已创建"
    echo "   位置: $FREEZE_DIR"
    echo "   消息数: $msg_count, 大小: $size"
    echo "   ⚠️  不要手动修改此目录"
}

# ─── start：fork 冻结镜像 → 启动 ───
do_start() {
    if [ -f "$ID_FILE" ]; then
        local old_id=$(cat "$ID_FILE" 2>/dev/null)
        if ai ls 2>/dev/null | grep -q "$old_id"; then
            echo "✅ ${EXPERT_LABEL} 已在运行 (ID: $old_id)"
            echo "   用 '$0 ask \"你的问题\"' 来提问"
            return 0
        fi
    fi

    if [ ! -f "$FREEZE_DIR/messages.jsonl" ]; then
        echo "⚠️  冻结镜像不存在，先构建..."
        do_freeze
    fi

    tmux kill-session -t "$TMUX_SESSION" 2>/dev/null || true
    rm -f "$ID_FILE"

    if [ -f "$RUNTIME_DIR/messages.jsonl" ] && [ -s "$RUNTIME_DIR/messages.jsonl" ]; then
        echo "📎 恢复已有工作 session"
    else
        echo "🍴 从冻结镜像 fork 工作副本..."
        rm -rf "$RUNTIME_DIR"
        mkdir -p "$RUNTIME_DIR"
        cp "$FREEZE_DIR/messages.jsonl" "$RUNTIME_DIR/messages.jsonl"
        [ -f "$FREEZE_DIR/meta.json" ] && cp "$FREEZE_DIR/meta.json" "$RUNTIME_DIR/meta.json"
        local freeze_count=$(wc -l < "$FREEZE_DIR/messages.jsonl" | tr -d ' ')
        echo "   已复制 ${freeze_count} 条消息"
        fi

    echo "🚀 启动 ${EXPERT_LABEL}..."

    local system_prompt="$SNAPSHOT_DIR/system-prompt.md"
    local prompt_arg=""
    if [ -f "$system_prompt" ]; then
        prompt_arg="--system-prompt @${system_prompt}"
    fi

    tmux new-session -d -s "$TMUX_SESSION" \
        "ai serve \
            --session $RUNTIME_DIR \
            --name '${PROJECT_NAME}-expert' \
            --id-file $ID_FILE \
            $prompt_arg \
            --timeout 0"

    for i in $(seq 1 30); do
        if [ -f "$ID_FILE" ] && [ -s "$ID_FILE" ]; then
            local agent_id=$(cat "$ID_FILE")
            echo "✅ ${EXPERT_LABEL} 已启动 (ID: $agent_id)"
            echo "   用 '$0 ask \"你的问题\"' 来提问"
            return 0
        fi
        sleep 1
    done

    echo "❌ 启动超时，请检查: tmux attach -t $TMUX_SESSION"
    return 1
}

# ─── ask：提问 ───
do_ask() {
    local question="$1"
    if [ ! -f "$ID_FILE" ]; then
        echo "❌ Expert 未运行，请先执行: $0 start"
        return 1
    fi
    local agent_id=$(cat "$ID_FILE")
        ai send --id "$agent_id" --wait --timeout 10m "$question"
}

# ─── stop：停止（保留工作副本） ───
do_stop() {
    if [ -f "$ID_FILE" ]; then
        local agent_id=$(cat "$ID_FILE")
        ai kill --id "$agent_id" 2>/dev/null || true
    fi
    tmux kill-session -t "$TMUX_SESSION" 2>/dev/null || true
    rm -f "$ID_FILE"

    if [ -f "$RUNTIME_DIR/messages.jsonl" ]; then
        local lines=$(wc -l < "$RUNTIME_DIR/messages.jsonl" | tr -d ' ')
        echo "✅ ${EXPERT_LABEL} 已停止（工作副本保留，${lines} 条消息）"
        echo "   $0 start   → 恢复对话"
        echo "   $0 clean   → 丢弃工作副本，重新 fork"
    else
        echo "✅ ${EXPERT_LABEL} 已停止"
    fi
}

# ─── clean：清除工作副本 ───
do_clean() {
    do_stop 2>/dev/null || true
    rm -rf "$RUNTIME_DIR"
    echo "✅ 工作副本已清除"
    echo "   下次 $0 start 会从冻结镜像重新 fork"
}

# ─── reset：完全清除 ───
do_reset() {
    do_stop 2>/dev/null || true
    rm -rf "$RUNTIME_DIR" "$FREEZE_DIR"
    echo "✅ 冻结镜像 + 工作副本已全部清除"
    echo "   下次 $0 start 会先 freeze（重新加载 wiki），再 fork"
}

# ─── status ───
do_status() {
    echo "=== ${EXPERT_LABEL} Status ==="
    echo ""

    if [ -f "$FREEZE_DIR/messages.jsonl" ]; then
        local freeze_lines=$(wc -l < "$FREEZE_DIR/messages.jsonl" | tr -d ' ')
        local freeze_size=$(du -h "$FREEZE_DIR/messages.jsonl" | cut -f1)
        local freeze_date=$(stat -f "%Sm" -t "%Y-%m-%d %H:%M" "$FREEZE_DIR/messages.jsonl" 2>/dev/null || stat -c "%y" "$FREEZE_DIR/messages.jsonl" 2>/dev/null | cut -d. -f1)
        echo "📦 冻结镜像: ${freeze_lines} 条消息, ${freeze_size} (${freeze_date})"
    else
        echo "📦 冻结镜像: ❌ 不存在（需要 freeze）"
    fi

    if [ -f "$RUNTIME_DIR/messages.jsonl" ]; then
        local rt_lines=$(wc -l < "$RUNTIME_DIR/messages.jsonl" | tr -d ' ')
        local rt_size=$(du -h < "$RUNTIME_DIR/messages.jsonl" | cut -f1)
        echo "🔧 工作副本: ${rt_lines} 条消息, ${rt_size}"
    else
        echo "🔧 工作副本: （空）"
    fi

    if [ -f "$ID_FILE" ] && [ -s "$ID_FILE" ]; then
        local agent_id=$(cat "$ID_FILE")
        if ai ls 2>/dev/null | grep -q "$agent_id"; then
            echo "🟢 Agent: 运行中 (ID: $agent_id)"
        else
            echo "⚪ Agent: 已停止 (ID: $agent_id)"
        fi
    else
        echo "⚪ Agent: 未启动"
    fi
}

# ─── main ───
case "${1:-help}" in
    start)  do_start ;;
    ask)    [ -z "${2:-}" ] && echo "用法: $0 ask \"你的问题\"" && exit 1; do_ask "$2" ;;
    stop)   do_stop ;;
    clean)  do_clean ;;
    freeze) do_freeze ;;
    reset)  do_reset ;;
    status) do_status ;;
    help|*)
        echo "${EXPERT_LABEL} — Snapshot Manager"
        echo ""
        echo "  $0 start     Fork 冻结镜像 → 启动 expert（或恢复已有工作副本）"
        echo "  $0 ask \"Q\"   向 expert 提问"
        echo "  $0 stop      停止 expert（保留工作副本，可 resume）"
        echo "  $0 clean     丢弃工作副本（下次 start 重新 fork）"
        echo "  $0 freeze    重建冻结镜像（wiki 更新后执行）"
        echo "  $0 reset     清除一切（冻结 + 工作副本）"
        echo "  $0 status    查看状态"
        ;;
esac