#!/usr/bin/env bash
# install-skills.sh — ai 技能安装/同步脚本（个人维护版）
#
# 用法:
#   ./scripts/install-skills.sh            # 同步: 建受管技能 symlink + 刷新外部技能快照 (= 本机最新状态)
#   ./scripts/install-skills.sh --restore  # 恢复: 建受管技能 symlink + 从快照恢复外部技能 (新机器首次安装)
#
# 设计:
#   - 受管技能 (仓库 skills/ 下的目录): 在 ~/.ai/skills 建 symlink 指向仓库，改仓库即生效
#   - 外部技能 (lark-*、市场下载的): 不进 git，物理目录 + tar 快照兜底 (市场技能可能下架)
#   - 同步方向明确: install = 本机→快照 (快照永远最新); --restore = 快照→本机 (新机器用)
#   - 幂等: 重复运行安全，不破坏已有配置
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKILLS_SRC="$REPO_DIR/skills"
DEST="$HOME/.ai/skills"
SNAPSHOT="${AI_SKILLS_SNAPSHOT:-$HOME/.ai/skills-external.tar.gz}"

# 链接受管技能 (幂等: 已就位跳过, 同名外部物理目录不动)
link_managed() {
    echo "== 受管技能 (symlink -> 仓库 skills/)"
    mkdir -p "$DEST"
    for skill_dir in "$SKILLS_SRC"/*/; do
        [ -d "$skill_dir" ] || continue
        name="$(basename "$skill_dir")"
        link="$DEST/$name"
        if [ -L "$link" ] && [ "$(readlink "$link")" = "$SKILLS_SRC/$name" ]; then
            continue  # 已就位
        fi
        if [ -d "$link" ] && [ ! -L "$link" ]; then
            echo "  跳过(外部物理目录): $name"
            continue
        fi
        ln -sfn "$SKILLS_SRC/$name" "$link"
        echo "  链接: $name"
    done
}

# 本机外部技能 (物理目录) 打包为快照。无外部技能则不动。
refresh_snapshot() {
    [ -d "$DEST" ] || return 0
    # find 不跟随 symlink，受管技能自动排除; -mindepth 1 排除 DEST 自身
    dirs="$(find "$DEST" -mindepth 1 -maxdepth 1 -type d | sed "s|$DEST/||" | sort)"
    [ -z "$dirs" ] && return 0
    tar -czf "$SNAPSHOT" -C "$DEST" $dirs
    echo "  快照已刷新: $(echo "$dirs" | wc -l | tr -d ' ') 个技能目录 -> $SNAPSHOT"
}

summary() {
    total="$(ls "$DEST" | wc -l | tr -d ' ')"
    links="$(find "$DEST" -maxdepth 1 -type l | wc -l | tr -d ' ')"
    echo "== 结果: ~/.ai/skills 共 $total 项 (symlink $links + 物理 $((total - links)))"
}

# 同步: 本机 → 快照
install() {
    link_managed
    echo "== 刷新外部技能快照 (本机 -> 快照)"
    refresh_snapshot
    summary
}

# 恢复: 快照 → 本机 (新机器首次安装)
restore() {
    link_managed
    echo "== 从快照恢复外部技能 (快照 -> 本机)"
    if [ -f "$SNAPSHOT" ]; then
        tar -xzf "$SNAPSHOT" -C "$DEST"
        echo "  完成"
    else
        echo "  无快照 ($SNAPSHOT)。先在旧机器跑 install 生成，再拷贝过来"
    fi
    summary
}

case "${1:-}" in
    --restore) restore ;;
    --help|-h) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//' ;;
    *) install ;;
esac