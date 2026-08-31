#!/usr/bin/env python3
"""
Video Watcher - Universal transcript fetcher for YouTube and Bilibili
Supports: YouTube (youtube.com, youtu.be) and Bilibili (bilibili.com, b23.tv)
"""
import argparse
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from urllib.parse import urlparse


class BotDetectedError(Exception):
    """Raised when YouTube returns bot detection / sign-in challenge."""
    pass


class VideoUnavailableError(Exception):
    """Raised when the video is not available or region-restricted."""
    pass


def detect_proxy() -> str | None:
    """Auto-detect local proxy by probing common ports."""
    import socket
    common_proxies = [
        (1180, 'socks5://127.0.0.1:1180'),
        (7890, 'http://127.0.0.1:7890'),
        (7891, 'http://127.0.0.1:7891'),
        (1080, 'socks5://127.0.0.1:1080'),
        (1087, 'http://127.0.0.1:1087'),
    ]
    for port, proxy_url in common_proxies:
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=0.5):
                return proxy_url
        except (ConnectionRefusedError, OSError, TimeoutError):
            continue
    return None


def detect_platform(url: str) -> str:
    """Detect video platform from URL."""
    domain = urlparse(url).netloc.lower()
    if any(d in domain for d in ['youtube.com', 'youtu.be', 'youtube-nocookie.com']):
        return 'youtube'
    elif any(d in domain for d in ['bilibili.com', 'b23.tv']):
        return 'bilibili'
    else:
        return 'unknown'


def clean_vtt(content: str) -> str:
    """Clean WebVTT content to plain text. Removes headers, timestamps, and duplicates."""
    lines = content.splitlines()
    text_lines = []
    timestamp_pattern = re.compile(
        r'\d{2}:\d{2}:\d{2}\.\d{3}\s-->\s\d{2}:\d{2}:\d{2}\.\d{3}'
    )
    for line in lines:
        line = line.strip()
        if not line or line == 'WEBVTT' or line.isdigit():
            continue
        if timestamp_pattern.match(line):
            continue
        if line.startswith('NOTE') or line.startswith('STYLE'):
            continue
        if line.startswith('Kind:') or line.startswith('Language:'):
            continue
        if text_lines and text_lines[-1] == line:
            continue
        line = re.sub(r'<[^>]+>', '', line)
        text_lines.append(line)
    return '\n'.join(text_lines)


def clean_srt(content: str) -> str:
    """Clean SRT content to plain text. Removes sequence numbers and timestamps."""
    lines = content.splitlines()
    text_lines = []
    timestamp_pattern = re.compile(
        r'\d{2}:\d{2}:\d{2},\d{3}\s-->\s\d{2}:\d{2}:\d{2},\d{3}'
    )
    for line in lines:
        line = line.strip()
        if not line:
            continue
        if line.isdigit():
            continue
        if timestamp_pattern.match(line):
            continue
        if text_lines and text_lines[-1] == line:
            continue
        line = re.sub(r'<[^>]+>', '', line)
        text_lines.append(line)
    return '\n'.join(text_lines)


def find_pot_repo() -> str | None:
    """Locate a built bgutil-ytdlp-pot-provider checkout for YouTube auto-captions.

    Portable / no machine-specific paths:
      1. $VIDEO_WATCHER_POT_REPO override (absolute or ~ path)
      2. a vendor checkout bundled next to this skill (created by setup_pot.sh)
    Returns None when unavailable; callers then degrade gracefully.
    """
    if os.environ.get('VIDEO_WATCHER_POT_REPO_DISABLE'):
        return None
    override = os.environ.get('VIDEO_WATCHER_POT_REPO')
    if override:
        d = Path(override).expanduser()
        return str(d) if (d / 'plugin' / 'yt_dlp_plugins').is_dir() else None
    skill_dir = Path(__file__).resolve().parent.parent  # scripts/ -> skill root
    cand = skill_dir / 'vendor' / 'bgutil-ytdlp-pot-provider'
    return str(cand) if (cand / 'plugin' / 'yt_dlp_plugins').is_dir() else None


def _pot_args(platform: str, pot_repo: str | None) -> list:
    """Extra yt-dlp args enabling the bgutil PO token provider (YouTube only).

    Needed for videos whose auto-captions are gated behind a proof-of-origin
    token. Works with any yt-dlp install via --plugin-dirs. No-op otherwise.
    """
    if platform == 'youtube' and pot_repo:
        return [
            '--plugin-dirs', pot_repo,
            '--extractor-args', f'youtubepot-bgutilscript:server_home={pot_repo}/server',
        ]
    return []


def _setup_hint() -> str:
    """Path to the setup script, for actionable error messages."""
    return str(Path(__file__).resolve().parent / 'setup_pot.sh')


def _build_cmd(platform: str, url: str, sub_lang: str | None,
               proxy: str | None, cookies_from_browser: str | None,
               pot_repo: str | None = None) -> list:
    """Build the yt-dlp command list."""
    cmd = [
        "yt-dlp",
        "--write-subs",
        "--write-auto-subs",
        "--skip-download",
        "--output", "subs",
    ]
    if sub_lang:
        cmd.extend(["--sub-lang", sub_lang])
    if proxy:
        cmd.extend(["--proxy", proxy])
    if cookies_from_browser:
        cmd.extend(["--cookies-from-browser", cookies_from_browser])
    if platform == 'youtube':
        cmd.extend(["--remote-components", "ejs:github"])
        cmd.extend(_pot_args(platform, pot_repo))
    if platform == 'bilibili':
        cmd.extend([
            "--add-header", "User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
            "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            "--add-header", "Referer: https://www.bilibili.com/",
        ])
    cmd.append(url)
    return cmd


def _list_available_subs(platform: str, url: str, proxy: str | None,
                         cookies_from_browser: str | None,
                         pot_repo: str | None = None) -> tuple:
    """Return (available subtitle language codes, po_token_blocked).

    Returns ([], False) on any error.

    yt-dlp prints the list to **stdout** in this format:
        [info] Available automatic captions for VIDEO_ID:
        Language Formats
        zh-Hans  vtt
        en       vtt
        VIDEO_ID has no subtitles
    `po_token_blocked` is True when yt-dlp reports captions were discarded
    because a PO token was not provided.
    """
    cmd = ["yt-dlp", "--no-update", "--list-subs", "--skip-download"]
    if proxy:
        cmd.extend(["--proxy", proxy])
    if cookies_from_browser:
        cmd.extend(["--cookies-from-browser", cookies_from_browser])
    if platform == 'youtube':
        cmd.extend(["--remote-components", "ejs:github"])
        cmd.extend(_pot_args(platform, pot_repo))
    cmd.append(url)
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
    except (subprocess.TimeoutExpired, OSError, Exception):
        return ([], False)

    combined = ((result.stderr or '') + (result.stdout or '')).lower()
    # Only the real 'not provided' warning counts. The provider's own
    # success lines ('Generating/Retrieved a ... PO Token') must not match.
    po_blocked = 'po token' in combined and 'was not provided' in combined

    langs = []
    in_section = False
    for line in result.stdout.splitlines():
        line = line.strip()
        if "Available" in line and ("caption" in line.lower() or "subtitle" in line.lower()):
            in_section = True
            continue
        if not in_section:
            continue
        if not line or line.startswith("["):
            break
        if "Formats" in line:
            continue
        parts = line.split()
        # Accept exactly 2 columns: "lang format" (e.g. "zh-Hans  vtt")
        if len(parts) == 2 and len(parts[0]) >= 2:
            langs.append(parts[0])
    return langs, po_blocked


def _pick_best_language(requested: str, available: list) -> str | None:
    """Pick the best matching language. Tries exact, then prefix, then any."""
    if not available:
        return None
    if requested in available:
        return requested
    # Prefix: requested matches start of available (e.g. "en" -> "en-US")
    for lang in available:
        if lang.lower().startswith(requested.lower()):
            return lang
    # Reverse prefix: available matches start of requested
    for lang in available:
        if requested.lower().startswith(lang.lower()):
            return lang
    # Fallback: first available
    return available[0]


def get_transcript(url: str, language: str | None = None, proxy: str | None = None,
                   cookies_from_browser: str | None = None, no_auto_proxy: bool = False):
    platform = detect_platform(url)
    if platform == 'unknown':
        print("Error: Unsupported URL format. Please use YouTube or Bilibili URLs.",
              file=sys.stderr)
        sys.exit(1)

    if language is None:
        language = 'zh-CN' if platform == 'bilibili' else 'en'

    if platform == 'youtube' and proxy is None and not no_auto_proxy:
        proxy = detect_proxy()
        if proxy:
            print(f"# Auto-detected proxy: {proxy}", file=sys.stderr)

    pot_repo = find_pot_repo() if platform == 'youtube' else None
    if pot_repo:
        print(f"# PO token provider: {pot_repo}", file=sys.stderr)

    effective_cookies = cookies_from_browser

    with tempfile.TemporaryDirectory() as temp_dir:
        temp_path = Path(temp_dir)

        def _try_download(sub_lang: str | None) -> bool:
            """Attempt download. Returns True if subtitle files appear."""
            for f in temp_path.glob("*.vtt"):
                f.unlink()
            for f in temp_path.glob("*.srt"):
                f.unlink()
            cmd = _build_cmd(platform, url, sub_lang, proxy, effective_cookies, pot_repo)
            try:
                subprocess.run(cmd, cwd=temp_dir, check=True, capture_output=True)
            except subprocess.CalledProcessError as e:
                error_msg = e.stderr.decode()
                err_lower = error_msg.lower()
                if "sign in to confirm" in err_lower or "not a bot" in err_lower:
                    raise BotDetectedError(error_msg)
                if "unavailable" in err_lower or "not available" in err_lower:
                    raise VideoUnavailableError(error_msg)
                raise
            return bool(list(temp_path.glob("*.vtt")) + list(temp_path.glob("*.srt")))

        def _try_language_fallback() -> bool:
            """Discover available languages and try to download the best match."""
            nonlocal language
            print(f"# No subtitles for '{language}'. "
                  f"Discovering available subtitle languages...", file=sys.stderr)
            avail = _list_available_subs(
                platform, url, proxy, effective_cookies, pot_repo)[0]
            if not avail:
                return False
            best = _pick_best_language(language, avail)
            print(f"# Available: {', '.join(avail)} -> using '{best}'", file=sys.stderr)
            language = best
            has_files = _try_download(language)
            if not has_files:
                print("# Still no files, trying without language filter...", file=sys.stderr)
                has_files = _try_download(None)
            if not has_files:
                print(f"Error: Subtitles exist ({', '.join(avail)}) but download failed. "
                      f"Try manually: --lang <code>", file=sys.stderr)
                sys.exit(1)
            return True

        try:
            has_files = _try_download(language)
            if not has_files:
                _try_language_fallback()

        except BotDetectedError:
            if effective_cookies:
                print(f"Error: Bot detection even with cookies ({effective_cookies}). "
                      f"Try manually: --cookies-from-browser <browser>", file=sys.stderr)
                sys.exit(1)
            print("# Bot detection triggered. Retrying with browser cookies...",
                  file=sys.stderr)
            succeeded = False
            for browser in ("chrome", "safari", "firefox"):
                print(f"# Trying cookies from: {browser}", file=sys.stderr)
                effective_cookies = browser
                try:
                    has_files = _try_download(language)
                    succeeded = True
                    break
                except BotDetectedError:
                    continue
                except subprocess.CalledProcessError:
                    has_files = False
                    succeeded = True
                    break
            if not succeeded:
                print("Error: Bot detection and all cookie retries failed. "
                      "Try manually: --cookies-from-browser <browser>", file=sys.stderr)
                sys.exit(1)
            if not has_files:
                _try_language_fallback()

        except VideoUnavailableError as e:
            print(f"Error: Video not available or region-restricted. {e}", file=sys.stderr)
            sys.exit(1)
        except FileNotFoundError:
            print("Error: yt-dlp not found. Please install it:\n"
                  "  pip install yt-dlp\n  or: brew install yt-dlp", file=sys.stderr)
            sys.exit(1)

        subtitle_files = list(temp_path.glob("*.vtt")) + list(temp_path.glob("*.srt"))
        if not subtitle_files:
            avail, po_blocked = _list_available_subs(
                platform, url, proxy, effective_cookies, pot_repo)
            if avail:
                print(f"Error: Subtitles exist ({', '.join(avail)}) but download failed. "
                      f"Try manually with --lang <code>.", file=sys.stderr)
            elif po_blocked:
                print("Error: Auto-generated captions exist but are gated behind a PO "
                      "token. Enable the PO token provider and retry:\n"
                      f"  bash {_setup_hint()}", file=sys.stderr)
            elif platform == 'youtube' and not pot_repo:
                print("Error: No subtitles found. If this video has auto-generated "
                      "captions, they may require a PO token provider that isn't set up "
                      "yet. Enable it and retry:\n"
                      f"  bash {_setup_hint()}", file=sys.stderr)
            else:
                print("Error: No subtitles found. The video may not have subtitles "
                      "available.", file=sys.stderr)
            sys.exit(1)

        subtitle_file = subtitle_files[0]
        content = subtitle_file.read_text(encoding='utf-8')

        if subtitle_file.suffix.lower() == '.vtt':
            clean_text = clean_vtt(content)
        else:
            clean_text = clean_srt(content)

        print(f"# Platform: {platform.title()}")
        print(f"# Language: {language}")
        if effective_cookies:
            print(f"# Cookies: {effective_cookies}")
        print(f"# URL: {url}")
        print()
        print(clean_text)


def main():
    parser = argparse.ArgumentParser(
        description="Fetch video transcripts from YouTube or Bilibili."
    )
    parser.add_argument("url", help="Video URL (YouTube or Bilibili)")
    parser.add_argument(
        "--lang", "-l",
        help="Subtitle language code (default: zh-CN for Bilibili, en for YouTube)",
        default=None
    )
    parser.add_argument(
        "--proxy", "-p",
        help="Proxy URL (e.g. socks5://127.0.0.1:1080)",
        default=None
    )
    parser.add_argument(
        "--cookies-from-browser", "-c",
        help="Browser to extract cookies from (e.g. chrome, safari, firefox)",
        default=None
    )
    parser.add_argument(
        "--no-auto-proxy",
        action="store_true",
        help="Disable automatic proxy detection for YouTube",
    )
    parser.add_argument(
        "--pot-repo",
        help="Path to a built bgutil-ytdlp-pot-provider checkout "
             "(overrides $VIDEO_WATCHER_POT_REPO and bundled vendor/)",
        default=None,
    )
    parser.add_argument(
        "--no-pot",
        action="store_true",
        help="Disable the PO token provider (fall back to plain auto-captions)",
    )
    args = parser.parse_args()
    if args.pot_repo:
        os.environ['VIDEO_WATCHER_POT_REPO'] = args.pot_repo
    if args.no_pot:
        os.environ['VIDEO_WATCHER_POT_REPO_DISABLE'] = '1'
    get_transcript(args.url, args.lang, args.proxy, args.cookies_from_browser,
                   args.no_auto_proxy)


if __name__ == "__main__":
    main()