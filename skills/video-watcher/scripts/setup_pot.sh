#!/usr/bin/env bash
#
# setup_pot.sh — Set up the bgutil PO token provider for YouTube auto-captions.
#
# WHY: Many YouTube videos expose auto-generated captions only behind a
# "proof-of-origin" (PO) token. Plain yt-dlp can't produce that token, so it
# silently drops those captions. This script installs a PO token provider
# (bgutil) that get_transcript.py picks up automatically.
#
# PORTABLE & IDEMPOTENT:
#   * Everything is installed under this skill's own vendor/ dir (or wherever
#     $VIDEO_WATCHER_POT_REPO points), so it works the same on any machine.
#   * No hardcoded absolute paths — paths are resolved relative to this script.
#   * Safe to re-run. Use --force to rebuild from scratch.
#
# REQUIRES: git + a JS runtime (Node.js >= 20  OR  Deno >= 2.0).
#
# After setup, no config is needed — get_transcript.py auto-detects it.
#
# Usage:
#   bash setup_pot.sh            # set up under <skill>/vendor
#   bash setup_pot.sh --force    # rebuild from scratch
#   VIDEO_WATCHER_POT_REPO=/x bash setup_pot.sh   # install into a custom dir

set -euo pipefail

# --- Config --------------------------------------------------------------
REPO_URL="https://github.com/Brainicism/bgutil-ytdlp-pot-provider.git"
VERSION="${VIDEO_WATCHER_POT_VERSION:-1.3.1}"

# --- Portable path resolution -------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if [ -n "${VIDEO_WATCHER_POT_REPO:-}" ]; then
  REPO_DIR="$VIDEO_WATCHER_POT_REPO"
else
  REPO_DIR="$SKILL_DIR/vendor/bgutil-ytdlp-pot-provider"
fi
SERVER_DIR="$REPO_DIR/server"

log() { printf '\033[1;34m[#]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[!]\033[0m %s\n' "$*" >&2; }

# --- Options -------------------------------------------------------------
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

# --- Prereqs -------------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

have git || { err "git is required to clone the provider."; exit 1; }

RUNTIME=""
if have node; then
  NODE_MAJOR="$(node -p 'parseInt(process.versions.node, 10)' 2>/dev/null || echo 0)"
  if [ "${NODE_MAJOR:-0}" -ge 20 ]; then
    RUNTIME="node"
  fi
fi
if [ -z "$RUNTIME" ] && have deno; then
  RUNTIME="deno"
fi
if [ -z "$RUNTIME" ]; then
  err "Need Node.js >= 20 or Deno >= 2.0 to build the PO token provider."
  err "Install one (e.g. 'brew install node') and re-run."
  exit 1
fi
log "Using JS runtime: $RUNTIME"

# --- Idempotency ---------------------------------------------------------
MARKER="$SERVER_DIR/.setup_pot_done_${RUNTIME}_${VERSION}"
if [ "$FORCE" -ne 1 ] && [ -d "$REPO_DIR/.git" ] && [ -f "$MARKER" ]; then
  log "Already set up ($RUNTIME @ $VERSION). Nothing to do."
  log "Rebuild with: bash $0 --force"
  exit 0
fi

# --- Clone ---------------------------------------------------------------
if [ "$FORCE" -eq 1 ]; then
  rm -rf "$REPO_DIR"
fi

if [ -d "$REPO_DIR/.git" ] && [ -d "$REPO_DIR/plugin/yt_dlp_plugins" ]; then
  log "Reusing existing checkout at $REPO_DIR"
else
  mkdir -p "$(dirname "$REPO_DIR")"
  rm -rf "$REPO_DIR"
  log "Cloning bgutil-ytdlp-pot-provider @ $VERSION ..."
  if ! git clone --single-branch --branch "$VERSION" "$REPO_URL" "$REPO_DIR"; then
    err "git clone failed. If you're on a restricted network, set a proxy and retry, e.g.:"
    err "  https_proxy=http://127.0.0.1:7890 bash $0 --force"
    exit 1
  fi
fi

# --- Build the JS server -------------------------------------------------
cd "$SERVER_DIR"
if [ "$RUNTIME" = "node" ]; then
  have npm || { err "npm not found (required for the Node build)."; exit 1; }
  log "npm ci (installing JS deps) ..."
  npm ci
  log "npx tsc (transpiling) ..."
  npx tsc
else
  log "deno install (installing JS deps, frozen) ..."
  deno install --allow-scripts=npm:canvas --frozen
fi

# --- Verify --------------------------------------------------------------
if [ -d "$REPO_DIR/plugin/yt_dlp_plugins" ]; then
  touch "$MARKER"
  log "OK — PO token provider ready at: $REPO_DIR"
  log "get_transcript.py will auto-detect it (no config needed). Try:"
  echo "    python3 $SCRIPT_DIR/get_transcript.py 'https://www.youtube.com/watch?v=<id>'"
  if [ "$REPO_DIR" != "$SKILL_DIR/vendor/bgutil-ytdlp-pot-provider" ]; then
    echo "    (custom location — make sure \${VIDEO_WATCHER_POT_REPO}=\$REPO_DIR is set"
    echo "     in your shell, or pass --pot-repo $REPO_DIR to get_transcript.py)"
  fi
else
  err "Setup finished but the plugin dir was not found under:"
  err "  $REPO_DIR/plugin/yt_dlp_plugins"
  exit 1
fi