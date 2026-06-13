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

## Examples

### YouTube (auto proxy)
```bash
python3 {baseDir}/scripts/get_transcript.py "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
```

### YouTube with cookies (bypass bot detection)
```bash
python3 {baseDir}/scripts/get_transcript.py "https://youtube.com/watch?v=..." --cookies-from-browser chrome
```

### Bilibili
```bash
python3 {baseDir}/scripts/get_transcript.py "https://www.bilibili.com/video/BV1xx411c7mD"
```

### Specify language
```bash
# Chinese subtitles for YouTube
python3 {baseDir}/scripts/get_transcript.py "https://youtube.com/watch?v=..." --lang zh-CN
```

### Manual proxy override
```bash
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL" --proxy socks5://127.0.0.1:1180
python3 {baseDir}/scripts/get_transcript.py "VIDEO_URL" --proxy http://127.0.0.1:7890
```

## Default Languages

| Platform | Default Language |
|----------|-----------------|
| YouTube  | `en` (English)  |
| Bilibili | `zh-CN` (Chinese) |

## Notes

- Requires `yt-dlp` in PATH
- YouTube: auto proxy + `--cookies-from-browser chrome` recommended for best results
- If no subtitles available, the script will error with a clear message