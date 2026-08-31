# Video Watcher

Fetch transcripts from **YouTube** and **Bilibili** videos to enable summarization, QA, and content extraction.

## Supported Platforms

- ✅ **YouTube** (youtube.com, youtu.be)
- ✅ **Bilibili** (bilibili.com, b23.tv)

## Quick Start

```bash
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL"
```

**That's it.** For YouTube, proxy is auto-detected from common local ports (1180, 7890, 7891, 1080, 1087).

## Proxy Behavior

| Platform | Auto Proxy |
|----------|-----------|
| YouTube  | ✅ Automatically probes common SOCKS5/HTTP proxy ports |
| Bilibili | Not needed (direct access) |

- **YouTube 在国内必须走 proxy**，脚本会自动检测本地常见代理端口
- 如果自动检测的不对，可手动指定：`--proxy socks5://127.0.0.1:1180`
- 如果不需要代理（如海外环境）：`--no-auto-proxy`

## Auto-Retry Behavior

The script handles two common failure modes automatically:

| Failure | Auto-Recovery |
|---------|---------------|
| **Bot detection** ("Sign in to confirm you're not a bot") | Retries with browser cookies in order: chrome → safari → firefox |
| **Language not found** (e.g. default `en` but video only has `zh-Hans`) | Runs `--list-subs` to discover available languages, picks the best match, retries. Falls back to any available language if no close match. |

Both retries log progress to stderr (prefixed with `#`) so you can see what happened.

## YouTube Auto-Captions & PO Token

Many YouTube videos only expose **auto-generated captions** behind a *proof-of-origin*
(PO) token that plain `yt-dlp` cannot produce. Without it, those captions are silently
dropped and the script reports "No subtitles found" even though captions exist.

To fetch them, install the bundled **PO token provider** (bgutil) **once per machine**:

```bash
bash {baseDir}/scripts/setup_pot.sh
```

- **Portable & idempotent** — installs everything under this skill's `vendor/` dir
  (git-ignored), using paths resolved relative to the script. Safe to re-run;
  use `--force` to rebuild.
- **Requires** `git` + a JS runtime: **Node.js >= 20** or **Deno >= 2.0**.
- After setup, `get_transcript.py` **auto-detects** the provider — no flags needed.
- The provider forwards your proxy to token generation, so it works over a proxy too.

Override / disable (no config file, all env/CLI):

```bash
# Point at a custom checkout (also honored by setup_pot.sh):
export VIDEO_WATCHER_POT_REPO=/path/to/checkout
# Or per-run:
python3 {baseDir}/scripts/get_transcript.py "URL" --pot-repo /path/to/checkout
# Disable the provider (fall back to plain auto-captions):
python3 {baseDir}/scripts/get_transcript.py "URL" --no-pot
```

If the provider isn't set up but a video's captions are PO-gated, the script prints an
actionable hint telling you to run `setup_pot.sh`. If a video genuinely has no captions,
it simply reports "No subtitles found."

## Examples

### YouTube (auto proxy + auto cookies + auto language)
```bash
python3 {baseDir}/scripts/get_transcript.py "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
```
One-liner is enough for most cases — proxy, cookies, and language are all auto-detected.

### YouTube with specific language
```bash
# Force Chinese subtitles
python3 {baseDir}/scripts/get_transcript.py "https://youtube.com/watch?v=..." --lang zh-CN
```

### Bilibili
```bash
python3 {baseDir}/scripts/get_transcript.py "https://www.bilibili.com/video/BV1xx411c7mD"
```

### Manual proxy override
```bash
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL" --proxy socks5://127.0.0.1:1180
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL" --proxy http://127.0.0.1:7890
```

### Manual cookie source (skip auto-retry)
```bash
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL" --cookies-from-browser safari
```

## Default Languages

| Platform | Default Language | Fallback |
|----------|-----------------|----------|
| YouTube  | `en` (English)  | Auto-discovers and picks best match from available subs |
| Bilibili | `zh-CN` (Chinese) | Same fallback logic |

## Notes

- Requires `yt-dlp` in PATH
- YouTube: auto proxy + auto cookie retry + auto language fallback — just pass the URL
- YouTube auto-captions gated by a PO token: run `scripts/setup_pot.sh` once (see above)
- If all retries fail, error message lists available subtitle languages so you can retry with `--lang`