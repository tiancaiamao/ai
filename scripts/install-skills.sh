#!/usr/bin/env bash
# install-skills.sh — ai 技能安装/备份脚本（个人维护版）
#
# 用法:
#   ./scripts/install-skills.sh            # 安装: 建受管技能 symlink; 有快照则恢复外部技能
#   ./scripts/install-skills.sh --backup   # 备份: 打包外部技能为快照 ~/.ai/skills-external.tar.gz
#
# 设计:
#   - 受管技能 (仓库 skills/ 下的目录): 在 ~/.ai/skills 建 symlink 指向仓库，改仓库即生效
#   - 外部技能 (lark-*、市场下载的): 不进 git，物理目录 + tar 快照兜底 (市场技能可能下架)
#   - 幂等: 重复运行安全，不破坏已有配置
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKILLS_SRC="$REPO_DIR/skills"
DEST="$HOME/.ai/skills"
SNAPSHOT="${AI_SKILLS_SNAPSHOT:-$HOME/.ai/skills-external.tar.gz}"

backup() {
    echo "== 备份外部技能 -> $SNAPSHOT"
    [ -d "$DEST" ] || { echo "  目录不存在: $DEST，无可备份"; exit 0; }
        # 只打包物理目录 (find 不跟随 symlink，受管技能自动排除; -mindepth 1 排除 DEST 自身)
    dirs="$(find "$DEST" -mindepth 1 -maxdepth 1 -type d | sed "s|$DEST/||" | sort)"
    if [ -z "$dirs" ]; then
        echo "  没有外部技能 (全是 symlink)，跳过"
        exit 0
    fi
    tar -czf "$SNAPSHOT" -C "$DEST" $dirs
    echo "  完成: $(echo "$dirs" | wc -l | tr -d ' ') 个技能目录"
}

install() {
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

    echo "== 外部技能 (快照恢复)"
    if [ -f "$SNAPSHOT" ]; then
        echo "  从 $SNAPSHOT 恢复"
        tar -xzf "$SNAPSHOT" -C "$DEST"
        echo "  完成"
    else
        echo "  无快照 ($SNAPSHOT)，跳过。可用 --backup 生成"
    fi

    total="$(ls "$DEST" | wc -l | tr -d ' ')"
    links="$(find "$DEST" -maxdepth 1 -type l | wc -l | tr -d ' ')"
    echo "== 结果: ~/.ai/skills 共 $total 项 (symlink $links + 物理 $((total - links)))"
}

case "${1:-}" in
    --backup) backup ;;
    --help|-h) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//' ;;
    *) install ;;
esac