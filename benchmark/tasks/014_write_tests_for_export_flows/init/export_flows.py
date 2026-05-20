#!/usr/bin/env python3
"""Export mitmproxy flow files into per-flow directories with request.json, request_headers.txt, and response_raw.txt."""

import argparse
import glob
import hashlib
import json
import os
import re
import shutil
from mitmproxy.io import FlowReader, FlowWriter
from mitmproxy.exceptions import FlowReadException
from mitmproxy.http import HTTPFlow

SYSTEM_PROMPT_INDEX = {
    "cc": 2,
    "vix": 0,
}

MODEL_PRICING = {
    "claude-opus-4-6":   {"input": 5e-6,    "cache_write": 6.25e-6,  "cache_read": 0.50e-6,  "output": 25e-6},
    "claude-opus-4-5":   {"input": 5e-6,    "cache_write": 6.25e-6,  "cache_read": 0.50e-6,  "output": 25e-6},
    "claude-opus-4-1":   {"input": 15e-6,   "cache_write": 18.75e-6, "cache_read": 1.50e-6,  "output": 75e-6},
    "claude-opus-4":     {"input": 15e-6,   "cache_write": 18.75e-6, "cache_read": 1.50e-6,  "output": 75e-6},
    "claude-sonnet-4-6": {"input": 3e-6,    "cache_write": 3.75e-6,  "cache_read": 0.30e-6,  "output": 15e-6},
    "claude-sonnet-4-5": {"input": 3e-6,    "cache_write": 3.75e-6,  "cache_read": 0.30e-6,  "output": 15e-6},
    "claude-sonnet-4":   {"input": 3e-6,    "cache_write": 3.75e-6,  "cache_read": 0.30e-6,  "output": 15e-6},
    "claude-haiku-4-5":  {"input": 1e-6,    "cache_write": 1.25e-6,  "cache_read": 0.10e-6,  "output": 5e-6},
    "claude-haiku-3-5":  {"input": 0.80e-6, "cache_write": 1e-6,     "cache_read": 0.08e-6,  "output": 4e-6},
}

# Sort prefixes longest-first so "claude-opus-4-5" matches before "claude-opus-4"
_SORTED_PREFIXES = sorted(MODEL_PRICING.keys(), key=len, reverse=True)


def count_whitespace_stats(text):
    """Count line returns and unnecessary spaces in text.

    Returns dict with:
      - line_returns_count: number of \\n characters
      - unnecessary_space_count: for each run of 2+ spaces, count len(run) - 1
      - total_chars: total character count of the text
    """
    line_returns = text.count("\n")
    unnecessary_spaces = sum(len(m) - 1 for m in re.findall(r" {2,}", text))
    return {
        "line_returns_count": line_returns,
        "unnecessary_space_count": unnecessary_spaces,
        "total_chars": len(text),
    }


def parse_request_body(request_path: str):
    """Parse the JSON body from a request.json file. Returns the parsed dict or None."""
    try:
        with open(request_path, "r") as f:
            return json.load(f)
    except (json.JSONDecodeError, ValueError):
        return None


def get_canonical_model(model_id: str):
    """Return the canonical model name (without date suffix) or None if unknown."""
    for prefix in _SORTED_PREFIXES:
        if model_id.startswith(prefix):
            return prefix
    return None


def get_pricing(model_id: str):
    """Match a model ID (possibly with date suffix) to pricing. Returns pricing dict or None."""
    canonical = get_canonical_model(model_id)
    if canonical:
        return MODEL_PRICING[canonical]
    return None


def sanitize_path(path: str) -> str:
    """Turn a URL path into a safe directory name component."""
    path = path.split("?")[0]
    path = path.replace("/", "_")
    path = path.strip("_")
    path = re.sub(r"[^a-zA-Z0-9_\-]", "", path)
    return path[:80]


_REDACTED_HEADERS = {b"x-api-key", b"authorization"}


def format_headers(headers) -> str:
    lines = []
    for k, v in headers.fields:
        if k.lower() in _REDACTED_HEADERS:
            lines.append(f"{k}: [REDACTED]")
        else:
            lines.append(f"{k}: {v}")
    return "\n".join(lines)


def write_request(flow: HTTPFlow, directory: str):
    req = flow.request
    # Write headers file (method+URL line, blank line, headers)
    header_lines = [
        f"{req.method} {req.pretty_url}",
        "",
        format_headers(req.headers),
    ]
    with open(os.path.join(directory, "request_headers.txt"), "w") as f:
        f.write("\n".join(header_lines))
    # Write body as JSON (pretty-printed if valid JSON, raw fallback)
    body = req.get_text(strict=False)
    if body:
        try:
            parsed = json.loads(body)
            body_out = json.dumps(parsed, indent=2)
        except (json.JSONDecodeError, ValueError):
            body_out = body
        with open(os.path.join(directory, "request.json"), "w") as f:
            f.write(body_out)
            f.write("\n")



def write_response(flow: HTTPFlow, directory: str):
    resp = flow.response
    if resp is None:
        return
    lines = [
        f"{resp.status_code} {resp.reason}",
        "",
        format_headers(resp.headers),
    ]
    body = resp.get_text(strict=False)
    if body:
        lines += ["", body]
    with open(os.path.join(directory, "response_raw.txt"), "w") as f:
        f.write("\n".join(lines))


def extract_stop_reason(response_raw: str) -> str:
    """Extract stop_reason from SSE message_delta event or non-streaming JSON response."""
    # Try SSE streaming format
    for line in response_raw.splitlines():
        line = line.strip()
        if not line.startswith("data: "):
            continue
        payload = line[len("data: "):].strip()
        if '"type":"message_delta"' not in payload and '"type": "message_delta"' not in payload:
            continue
        try:
            data = json.loads(payload)
            delta = data.get("delta", {})
            reason = delta.get("stop_reason")
            if reason:
                return reason
        except json.JSONDecodeError:
            continue

    # Fallback: non-streaming JSON response
    for line in reversed(response_raw.splitlines()):
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            data = json.loads(line)
            if data.get("type") == "message":
                reason = data.get("stop_reason")
                if reason:
                    return reason
        except json.JSONDecodeError:
            continue

    return "unknown"


def redact_flow_files(directory: str):
    """Redact sensitive headers (API keys) from .flow files in-place."""
    flow_files = sorted(glob.glob(os.path.join(directory, "*.flow")))
    if not flow_files:
        return

    count = 0
    for filepath in flow_files:
        flows = []
        modified = False
        try:
            with open(filepath, "rb") as f:
                reader = FlowReader(f)
                for flow in reader.stream():
                    if isinstance(flow, HTTPFlow):
                        for header in _REDACTED_HEADERS:
                            if flow.request.headers.get(header):
                                flow.request.headers[header] = "[REDACTED]"
                                modified = True
                    flows.append(flow)
        except (FlowReadException, Exception) as e:
            print(f"  Skipping {os.path.basename(filepath)}: {e}")
            continue

        if modified:
            with open(filepath, "wb") as f:
                writer = FlowWriter(f)
                for flow in flows:
                    writer.add(flow)
            count += 1

    print(f"\nRedacted API keys in {count} flow files")


def _system_prompt_hash(body_json, agent_name):
    """Return a short hash of the relevant system prompt, or None."""
    system = body_json.get("system")
    if not system:
        return None
    index = SYSTEM_PROMPT_INDEX.get(agent_name, 0)
    if index >= len(system):
        return None
    text = system[index].get("text", "")
    if not text:
        return None
    return hashlib.sha256(text.encode()).hexdigest()[:12]


def export_flows(input_dir: str, output_dir: str):
    """Export .flow files from input_dir into per-flow directories under output_dir.

    Directory structure: {agent}/{step_index}/{request_index}/
    Steps are delimited by end_turn stop_reason in responses.
    """
    flow_files = sorted(glob.glob(os.path.join(input_dir, "*.flow")))
    if not flow_files:
        print(f"No *.flow files found in {input_dir}")
        return

    total = 0
    for filepath in flow_files:
        filename = os.path.basename(filepath)
        name = os.path.splitext(filename)[0]
        file_dir = os.path.join(output_dir, name)
        if os.path.exists(file_dir):
            shutil.rmtree(file_dir)
        os.makedirs(file_dir)
        print(f"Reading {filename}...")
        try:
            step_index = 1
            request_index = 1
            prev_prompt_hash = None
            with open(filepath, "rb") as f:
                reader = FlowReader(f)
                for flow in reader.stream():
                    if not isinstance(flow, HTTPFlow):
                        continue

                    url = flow.request.pretty_url
                    if "/count_token" in url:
                        continue

                    # Parse body to check for quota requests
                    body_text = flow.request.get_text(strict=False)
                    body_json = None
                    if body_text:
                        try:
                            body_json = json.loads(body_text)
                            messages = body_json.get("messages", [])
                            system = body_json.get("system")
                            if messages and not system:
                                first_msg = messages[0]
                                content = first_msg.get("content", "")
                                if isinstance(content, str) and content == "quota":
                                    continue
                        except json.JSONDecodeError:
                            pass

                    # For cc: step boundary = system prompt change (checked before writing)
                    if name == "cc" and body_json is not None:
                        prompt_hash = _system_prompt_hash(body_json, name)
                        if prompt_hash is not None and prev_prompt_hash is not None and prompt_hash != prev_prompt_hash:
                            step_index += 1
                            request_index = 1
                        if prompt_hash is not None:
                            prev_prompt_hash = prompt_hash

                    flow_dir = os.path.join(file_dir, str(step_index), str(request_index))
                    os.makedirs(flow_dir, exist_ok=True)

                    write_request(flow, flow_dir)
                    write_response(flow, flow_dir)
                    # Write timing data inline
                    timing = {}
                    if flow.request:
                        timing["request_start"] = flow.request.timestamp_start
                    if flow.response:
                        timing["response_end"] = flow.response.timestamp_end
                    with open(os.path.join(flow_dir, "timing.json"), "w") as f:
                        json.dump(timing, f, indent=2)
                        f.write("\n")

                    # Extract stop_reason to determine step boundaries
                    response_raw = ""
                    resp = flow.response
                    if resp is not None:
                        response_raw = resp.get_text(strict=False) or ""
                    stop_reason = extract_stop_reason(response_raw)

                    print(f"  [step {step_index}, req {request_index}] {flow.request.method} {flow.request.pretty_url} ({stop_reason})")

                    # For vix: step boundary = end_turn stop_reason (checked after writing)
                    if name != "cc":
                        if stop_reason == "end_turn":
                            step_index += 1
                            request_index = 1
                        else:
                            request_index += 1
                    else:
                        request_index += 1

                    total += 1
        except FlowReadException as e:
            print(f"  Skipping {filename}: {e}")
        except Exception as e:
            print(f"  Skipping {filename}: {e}")

    print(f"\nExported {total} flows to {output_dir}")


def extract_usage(directory: str):
    """Walk output directory, extract token usage from SSE message_delta events into usage.json."""
    count = 0
    for dirpath, _dirnames, filenames in os.walk(directory):
        if "response_raw.txt" not in filenames:
            continue

        response_path = os.path.join(dirpath, "response_raw.txt")
        usage = None
        with open(response_path, "r") as f:
            content = f.read()

        # Try SSE streaming format: look for message_delta events
        for line in content.splitlines():
            line = line.strip()
            if not line.startswith("data: "):
                continue
            payload = line[len("data: "):].strip()
            if '"type":"message_delta"' not in payload and '"type": "message_delta"' not in payload:
                continue
            try:
                data = json.loads(payload)
                usage = data.get("usage")
            except json.JSONDecodeError:
                continue

        # Fallback: non-streaming JSON response (single message object)
        # The body comes after a blank line following the headers
        if usage is None:
            # Find the last {..."type":"message"...} JSON in the content
            for line in reversed(content.splitlines()):
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    msg = json.loads(line)
                    if msg.get("type") == "message" and "usage" in msg:
                        usage = msg["usage"]
                        break
                except (json.JSONDecodeError, ValueError):
                    continue

        if usage is None:
            print(f"  Warning: no usage found in {response_path}")
            continue

        # Keep only the token count fields we need
        TOKEN_FIELDS = ("input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens")
        usage = {k: v for k, v in usage.items() if k in TOKEN_FIELDS}

        # Merge timing data if available
        timing_path = os.path.join(dirpath, "timing.json")
        if os.path.exists(timing_path):
            with open(timing_path, "r") as f:
                timing = json.load(f)
            request_start = timing.get("request_start")
            response_end = timing.get("response_end")
            timing_block = {}
            if request_start:
                timing_block["request_start"] = request_start
            if response_end:
                timing_block["response_end"] = response_end
            if request_start and response_end:
                timing_block["duration_ms"] = round((response_end - request_start) * 1000)
            if timing_block:
                usage["timing"] = timing_block
            os.remove(timing_path)

        usage_path = os.path.join(dirpath, "usage.json")
        with open(usage_path, "w") as f:
            json.dump(usage, f, indent=2)
            f.write("\n")
        count += 1

    print(f"\nExtracted usage for {count} flows")



def extract_prompts(directory: str):
    """Extract system prompt and first user message from request.json files into per-request markdown files."""
    count = 0
    for dirpath, _dirnames, filenames in os.walk(directory):
        if "request.json" not in filenames:
            continue

        request_path = os.path.join(dirpath, "request.json")

        body = parse_request_body(request_path)
        if body is None:
            print(f"  Warning: no JSON body found in {request_path}")
            continue

        # System prompt: concatenate all text blocks from body["system"]
        system = body.get("system")
        if system and isinstance(system, list):
            texts = [block.get("text", "") for block in system if block.get("type") == "text"]
            system_text = "\n\n".join(t for t in texts if t)
            if system_text:
                with open(os.path.join(dirpath, "system_prompt.md"), "w") as f:
                    f.write(system_text)

        # First user message: find first message with role=="user", concatenate text blocks
        messages = body.get("messages", [])
        for msg in messages:
            if msg.get("role") == "user":
                content = msg.get("content", [])
                if isinstance(content, list):
                    texts = [block.get("text", "") for block in content if block.get("type") == "text"]
                    user_text = "\n\n".join(t for t in texts if t)
                    if user_text:
                        with open(os.path.join(dirpath, "first_user_message.md"), "w") as f:
                            f.write(user_text)
                break

        count += 1

    print(f"\nExtracted prompts for {count} requests")



def _resolve_read_file_name(name, tool_input):
    """Resolve read_file tool name to compressed/uncompressed variant."""
    if name == "read_file":
        mode = tool_input.get("mode", "original") if isinstance(tool_input, dict) else "original"
        return "read_file_compressed" if mode == "compress" else "read_file_uncompressed"
    return name


def extract_read_file_whitespace(body):
    """Extract whitespace stats from read_file/Read tool results in the request body."""
    ws = {"line_returns_count": 0, "unnecessary_space_count": 0, "total_chars": 0}

    # Build tool_use_id -> tool_name map from assistant messages
    tool_id_to_name = {}
    messages = body.get("messages", [])
    for msg in messages:
        if msg.get("role") != "assistant":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        for block in content:
            if block.get("type") == "tool_use":
                tool_id_to_name[block.get("id", "")] = _resolve_read_file_name(
                    block.get("name", "unknown"), block.get("input", {}))

    for msg in messages:
        if msg.get("role") != "user":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        for block in content:
            if block.get("type") != "tool_result":
                continue
            tool_use_id = block.get("tool_use_id", "")
            tool_name = tool_id_to_name.get(tool_use_id, "unknown")
            if not (tool_name.startswith("read_file") or tool_name == "Read"):
                continue
            result_content = block.get("content", "")
            if isinstance(result_content, str):
                text_for_ws = result_content
            elif isinstance(result_content, list):
                text_for_ws = "".join(b.get("text", "") for b in result_content)
            else:
                text_for_ws = ""
            stats = count_whitespace_stats(text_for_ws)
            for k in ("line_returns_count", "unnecessary_space_count", "total_chars"):
                ws[k] += stats[k]

    return ws


def categorize_input_sources(body):
    """Count input characters from tool results and tool use blocks, split by cache position.

    Returns: {
        "total_chars": int,  # total chars of entire request body
        "tool_results": {"Read": {"cache_write_chars": 0, "cache_read_chars": 1234}, ...},
        "tool_calls": {"Read": {"cache_write_chars": 0, "cache_read_chars": 567}, ...},
    }

    Position logic:
    - Last user message content → cache_write (new content being written to cache)
    - Second-to-last message (assistant before last user) → cache_write for tool_use blocks
    - Everything else → cache_read
    """
    total_chars = len(json.dumps(body))
    tool_results = {}
    tool_calls = {}

    messages = body.get("messages", [])

    # Find last user message index and second-to-last message index
    last_user_idx = -1
    for i in range(len(messages) - 1, -1, -1):
        if messages[i].get("role") == "user":
            last_user_idx = i
            break
    second_to_last_idx = last_user_idx - 1 if last_user_idx > 0 else -1

    # Build tool_use_id -> tool_name map from assistant messages
    tool_id_to_name = {}
    for msg in messages:
        if msg.get("role") != "assistant":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        for block in content:
            if block.get("type") == "tool_use":
                tool_id_to_name[block.get("id", "")] = _resolve_read_file_name(
                    block.get("name", "unknown"), block.get("input", {}))

    # Collect tool_result chars from user messages
    for i, msg in enumerate(messages):
        if msg.get("role") != "user":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        is_cache_write = (i == last_user_idx)
        for block in content:
            if block.get("type") != "tool_result":
                continue
            tool_use_id = block.get("tool_use_id", "")
            tool_name = tool_id_to_name.get(tool_use_id, "unknown")
            chars = len(json.dumps(block))
            if tool_name not in tool_results:
                tool_results[tool_name] = {"cache_write_chars": 0, "cache_read_chars": 0}
            if is_cache_write:
                tool_results[tool_name]["cache_write_chars"] += chars
            else:
                tool_results[tool_name]["cache_read_chars"] += chars

    # Collect tool_use chars from assistant messages
    for i, msg in enumerate(messages):
        if msg.get("role") != "assistant":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        is_cache_write = (i == second_to_last_idx)
        for block in content:
            if block.get("type") != "tool_use":
                continue
            tool_name = _resolve_read_file_name(
                block.get("name", "unknown"), block.get("input", {}))
            chars = len(json.dumps(block))
            if tool_name not in tool_calls:
                tool_calls[tool_name] = {"cache_write_chars": 0, "cache_read_chars": 0}
            if is_cache_write:
                tool_calls[tool_name]["cache_write_chars"] += chars
            else:
                tool_calls[tool_name]["cache_read_chars"] += chars

    return {"total_chars": total_chars, "tool_results": tool_results, "tool_calls": tool_calls}


def categorize_output_sources(response_path):
    """Count output characters from the actual response (SSE stream or single JSON).

    Parses content_block_start/delta/stop events from response_raw.txt.
    Returns: {"llm_text": int, "tool_calls": {"ToolName": int, ...}}
    """
    llm_text_chars = 0
    tool_calls = {}

    with open(response_path, "r") as f:
        content = f.read()

    # State machine for SSE content blocks
    # blocks[index] = {"type": "text"|"tool_use", "name": str, "text": str, "json": str}
    blocks = {}
    found_sse = False

    for line in content.splitlines():
        line = line.strip()
        if not line.startswith("data: "):
            continue
        payload = line[len("data: "):].strip()
        try:
            data = json.loads(payload)
        except json.JSONDecodeError:
            continue

        event_type = data.get("type", "")

        if event_type == "content_block_start":
            found_sse = True
            idx = data.get("index", 0)
            cb = data.get("content_block", {})
            blocks[idx] = {
                "type": cb.get("type", ""),
                "name": cb.get("name", "unknown"),
                "text": "",
                "json": "",
            }

        elif event_type == "content_block_delta":
            found_sse = True
            idx = data.get("index", 0)
            delta = data.get("delta", {})
            delta_type = delta.get("type", "")
            if idx in blocks:
                if delta_type == "text_delta":
                    blocks[idx]["text"] += delta.get("text", "")
                elif delta_type == "input_json_delta":
                    blocks[idx]["json"] += delta.get("partial_json", "")

        elif event_type == "content_block_stop":
            found_sse = True
            idx = data.get("index", 0)
            if idx in blocks:
                block = blocks[idx]
                if block["type"] == "text":
                    llm_text_chars += len(block["text"])
                elif block["type"] == "tool_use":
                    try:
                        input_data = json.loads(block["json"]) if block["json"] else {}
                    except json.JSONDecodeError:
                        input_data = {}
                    tool_name = _resolve_read_file_name(block["name"], input_data)
                    # Reconstruct full block to count all chars (id, name, type, input)
                    full_block = {"type": "tool_use", "name": block["name"], "input": input_data}
                    chars = len(json.dumps(full_block))
                    tool_calls[tool_name] = tool_calls.get(tool_name, 0) + chars
                del blocks[idx]

    # Non-streaming fallback: parse single JSON message
    if not found_sse:
        for line in reversed(content.splitlines()):
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                msg = json.loads(line)
                if msg.get("type") != "message":
                    continue
                for block in msg.get("content", []):
                    btype = block.get("type", "")
                    if btype == "text":
                        llm_text_chars += len(block.get("text", ""))
                    elif btype == "tool_use":
                        tool_name = _resolve_read_file_name(
                            block.get("name", "unknown"), block.get("input", {}))
                        chars = len(json.dumps(block))
                        tool_calls[tool_name] = tool_calls.get(tool_name, 0) + chars
                break
            except (json.JSONDecodeError, ValueError):
                continue

    return {"llm_text": llm_text_chars, "tool_calls": tool_calls}


def _format_tool_params(input_data):
    """Format tool input as compact param=value pairs."""
    parts = []
    for key, value in input_data.items():
        if isinstance(value, str):
            parts.append(f'{key}="{value}"')
        else:
            parts.append(f"{key}={json.dumps(value)}")
    return ", ".join(parts)


def parse_response_content(response_path):
    """Parse response_raw.txt into a list of content blocks.

    Returns: list of {"type": "text", "text": str} or {"type": "tool_use", "name": str, "input": dict}
    """
    with open(response_path, "r") as f:
        content = f.read()

    # SSE streaming parse
    blocks = {}
    finished = []
    found_sse = False

    for line in content.splitlines():
        line = line.strip()
        if not line.startswith("data: "):
            continue
        payload = line[len("data: "):].strip()
        try:
            data = json.loads(payload)
        except json.JSONDecodeError:
            continue

        event_type = data.get("type", "")

        if event_type == "content_block_start":
            found_sse = True
            idx = data.get("index", 0)
            cb = data.get("content_block", {})
            blocks[idx] = {
                "type": cb.get("type", ""),
                "name": cb.get("name", "unknown"),
                "text": "",
                "json": "",
            }

        elif event_type == "content_block_delta":
            idx = data.get("index", 0)
            delta = data.get("delta", {})
            delta_type = delta.get("type", "")
            if idx in blocks:
                if delta_type == "text_delta":
                    blocks[idx]["text"] += delta.get("text", "")
                elif delta_type == "input_json_delta":
                    blocks[idx]["json"] += delta.get("partial_json", "")

        elif event_type == "content_block_stop":
            idx = data.get("index", 0)
            if idx in blocks:
                block = blocks[idx]
                if block["type"] == "text":
                    finished.append({"type": "text", "text": block["text"]})
                elif block["type"] == "tool_use":
                    try:
                        input_data = json.loads(block["json"]) if block["json"] else {}
                    except json.JSONDecodeError:
                        input_data = {}
                    finished.append({"type": "tool_use", "name": block["name"], "input": input_data})
                del blocks[idx]

    # Non-streaming fallback
    if not found_sse:
        for line in reversed(content.splitlines()):
            line = line.strip()
            if not line.startswith("{"):
                continue
            try:
                msg = json.loads(line)
                if msg.get("type") != "message":
                    continue
                for block in msg.get("content", []):
                    btype = block.get("type", "")
                    if btype == "text":
                        finished.append({"type": "text", "text": block.get("text", "")})
                    elif btype == "tool_use":
                        finished.append({"type": "tool_use", "name": block.get("name", "unknown"), "input": block.get("input", {})})
                break
            except (json.JSONDecodeError, ValueError):
                continue

    return finished


def export_parsed_response(response_path, output_path):
    """Parse response_raw.txt and write a human-readable response_parsed.txt.

    Text blocks are output as-is. Tool calls are displayed inline with params.
    """
    blocks = parse_response_content(response_path)
    if not blocks:
        return False

    lines = []
    for block in blocks:
        if block["type"] == "text":
            lines.append(block["text"])
        elif block["type"] == "tool_use":
            params = _format_tool_params(block["input"])
            lines.append(f'\n[{block["name"]}({params})]\n')

    with open(output_path, "w") as f:
        f.write("".join(lines))
    return True


def export_parsed_responses(directory: str):
    """Walk output directory, export response_parsed.txt for each response_raw.txt."""
    count = 0
    for dirpath, _dirnames, filenames in os.walk(directory):
        if "response_raw.txt" not in filenames:
            continue

        response_path = os.path.join(dirpath, "response_raw.txt")
        output_path = os.path.join(dirpath, "response_parsed.txt")

        if export_parsed_response(response_path, output_path):
            count += 1

    print(f"\nExported parsed responses for {count} flows")


def attribute_tokens(input_sources, output_sources, usage, pricing):
    """Attribute tokens/cost to source categories.

    Input side: raw character counts per tool (no token/cost distribution — flawed due to caching).
    Output side: proportional token/cost distribution (valid since output isn't cached).
    """
    by_source = {"input": {}, "output": {}}

    # Input: distribute input tokens proportionally using total_chars as denominator
    cost_data = usage.get("cost", {})
    _ci = cost_data.get("input", {})
    _cw = cost_data.get("cache_write", {})
    _cr = cost_data.get("cache_read", {})
    input_tokens = _ci.get("tokens", 0) + _cw.get("tokens", 0) + _cr.get("tokens", 0)

    total_chars = input_sources.get("total_chars", 0)

    def _attribute_input_tool(tool_char_dict):
        """Compute tokens/dollars/chars for a tool given its {cache_write_chars, cache_read_chars}."""
        cw_chars = tool_char_dict.get("cache_write_chars", 0)
        cr_chars = tool_char_dict.get("cache_read_chars", 0)
        cw_tokens = round(input_tokens * (cw_chars / total_chars))
        cr_tokens = round(input_tokens * (cr_chars / total_chars))
        dollars = (
            round(cw_tokens * pricing["cache_write"], 6) +
            round(cr_tokens * pricing["cache_read"], 6)
        )
        return {"tokens": cw_tokens + cr_tokens, "dollars": dollars, "chars": cw_chars + cr_chars}

    if total_chars > 0 and input_tokens > 0 and pricing:
        # Tool results (from user messages)
        tool_results = input_sources.get("tool_results", {})
        if tool_results:
            tr_entry = {}
            for tool_name, char_dict in tool_results.items():
                tr_entry[tool_name] = _attribute_input_tool(char_dict)
            by_source["input"]["tool_results"] = tr_entry

        # Tool calls (from assistant messages)
        tool_calls_input = input_sources.get("tool_calls", {})
        if tool_calls_input:
            tc_entry = {}
            for tool_name, char_dict in tool_calls_input.items():
                tc_entry[tool_name] = _attribute_input_tool(char_dict)
            by_source["input"]["tool_calls"] = tc_entry

    # Output: distribute output tokens proportionally by character count
    output_cost_entry = cost_data.get("output", {})
    output_tokens = output_cost_entry.get("tokens", 0)
    output_dollars = output_cost_entry.get("dollars", 0.0)

    llm_chars = output_sources.get("llm_text", 0)
    tc = output_sources.get("tool_calls", {})
    tc_total_chars = sum(tc.values())
    total_output_chars = llm_chars + tc_total_chars

    if total_output_chars > 0 and output_tokens > 0:
        # LLM text
        llm_frac = llm_chars / total_output_chars
        by_source["output"]["llm_text"] = {
            "tokens": round(output_tokens * llm_frac),
            "dollars": round(output_dollars * llm_frac, 6),
            "chars": llm_chars,
        }

        # Tool calls
        tc_frac = tc_total_chars / total_output_chars
        tc_entry = {
            "__total": {
                "tokens": round(output_tokens * tc_frac),
                "dollars": round(output_dollars * tc_frac, 6),
                "chars": tc_total_chars,
            }
        }
        for tool_name, chars in tc.items():
            tool_frac = chars / total_output_chars
            tc_entry[tool_name] = {
                "tokens": round(output_tokens * tool_frac),
                "dollars": round(output_dollars * tool_frac, 6),
                "chars": chars,
            }
        by_source["output"]["tool_calls"] = tc_entry

    return by_source


READ_TOOLS = {"read_file", "Read", "read_file_compressed", "read_file_uncompressed"}
WRITE_TOOLS = {"write_file", "Write", "edit_file", "Edit"}


_PROJECT_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))


def _get_file_path(input_data):
    """Extract file path from tool input, checking common key names. Always returns an absolute path."""
    if not isinstance(input_data, dict):
        return None
    fp = input_data.get("file_path") or input_data.get("path") or None
    if fp and not os.path.isabs(fp):
        fp = os.path.join(_PROJECT_ROOT, fp)
    return fp


def extract_file_ops(body, response_path):
    """Extract file operation stats from request body (reads) and response (writes).

    Returns: {
        "input": {"unique_files_read": int, "total_read_chars": int, "per_file": {path: {"calls": int, "chars": int}}},
        "output": {"files_written": int, "total_write_chars": int, "per_file": {path: {"calls": int, "chars": int}}},
    }
    """
    # --- Input: files read (from conversation history in request body) ---
    read_per_file = {}  # path -> {"calls": int, "chars": int, "tool_ids": {id: chars}}

    messages = body.get("messages", [])

    # Build tool_use_id -> (resolved_name, file_path) map from assistant messages
    tool_id_info = {}
    for msg in messages:
        if msg.get("role") != "assistant":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        for block in content:
            if block.get("type") == "tool_use":
                name = _resolve_read_file_name(block.get("name", ""), block.get("input", {}))
                if name in READ_TOOLS:
                    fp = _get_file_path(block.get("input", {}))
                    tool_id_info[block.get("id", "")] = fp

    # Sum chars from tool_result blocks matching read tools
    for msg in messages:
        if msg.get("role") != "user":
            continue
        content = msg.get("content", [])
        if not isinstance(content, list):
            continue
        for block in content:
            if block.get("type") != "tool_result":
                continue
            tool_use_id = block.get("tool_use_id", "")
            if tool_use_id not in tool_id_info:
                continue
            fp = tool_id_info[tool_use_id]
            result_content = block.get("content", "")
            chars = 0
            if isinstance(result_content, str):
                chars = len(result_content)
            elif isinstance(result_content, list):
                chars = sum(len(b.get("text", "")) for b in result_content)
            if fp:
                if fp not in read_per_file:
                    read_per_file[fp] = {"calls": 0, "chars": 0, "tool_ids": {}}
                read_per_file[fp]["tool_ids"][tool_use_id] = chars
                read_per_file[fp]["calls"] = len(read_per_file[fp]["tool_ids"])
                read_per_file[fp]["chars"] = sum(read_per_file[fp]["tool_ids"].values())

    # --- Output: files written (from response) ---
    write_per_file = {}  # path -> {"calls": int, "chars": int, "tool_ids": {id: chars}}

    response_blocks = parse_response_content(response_path)
    for block in response_blocks:
        if block.get("type") != "tool_use":
            continue
        name = block.get("name", "")
        if name not in WRITE_TOOLS:
            continue
        inp = block.get("input", {})
        fp = _get_file_path(inp)
        # Sum content chars: "content" for Write/write_file, "new_string" for Edit/edit_file
        chars = 0
        if name in ("Write", "write_file"):
            chars = len(inp.get("content", ""))
        elif name in ("Edit", "edit_file"):
            chars = len(inp.get("new_string", ""))
        if fp:
            block_id = block.get("id", "")
            if fp not in write_per_file:
                write_per_file[fp] = {"calls": 0, "chars": 0, "tool_ids": {}}
            write_per_file[fp]["tool_ids"][block_id] = chars
            write_per_file[fp]["calls"] = len(write_per_file[fp]["tool_ids"])
            write_per_file[fp]["chars"] = sum(write_per_file[fp]["tool_ids"].values())

    # Add file_size for each file
    for pf in (read_per_file, write_per_file):
        for fp in pf:
            try:
                pf[fp]["file_size"] = os.path.getsize(fp)
            except OSError:
                pf[fp]["file_size"] = None

    # Derive aggregate scalars from deduplicated per_file data
    unique_files_read = len(read_per_file)
    total_read_chars = sum(v["chars"] for v in read_per_file.values())
    unique_files_written = len(write_per_file)
    total_write_chars_dedup = sum(v["chars"] for v in write_per_file.values())
    total_write_calls = sum(v["calls"] for v in write_per_file.values())

    return {
        "input": {"unique_files_read": unique_files_read, "total_read_chars": total_read_chars, "per_file": read_per_file},
        "output": {"files_written": total_write_calls, "unique_files_written": unique_files_written, "total_write_chars": total_write_chars_dedup, "per_file": write_per_file},
    }


def extract_source_attribution(directory: str):
    """Walk output directory, compute source attribution, read_file whitespace, and file ops."""
    count = 0
    for dirpath, _dirnames, filenames in os.walk(directory):
        if "request.json" not in filenames or "usage.json" not in filenames or "response_raw.txt" not in filenames:
            continue

        request_path = os.path.join(dirpath, "request.json")
        usage_path = os.path.join(dirpath, "usage.json")
        response_path = os.path.join(dirpath, "response_raw.txt")

        body = parse_request_body(request_path)
        if body is None:
            continue

        with open(usage_path, "r") as f:
            usage = json.load(f)

        # Get model pricing
        model = body.get("model", "")
        pricing = get_pricing(model) if model else None

        # Compute source attribution
        input_sources = categorize_input_sources(body)
        output_sources = categorize_output_sources(response_path)
        by_source = attribute_tokens(input_sources, output_sources, usage, pricing)

        usage["by_source"] = by_source

        # Compute read_file whitespace
        usage["read_file_whitespace"] = extract_read_file_whitespace(body)

        # Compute file operations
        usage["file_ops"] = extract_file_ops(body, response_path)

        with open(usage_path, "w") as f:
            json.dump(usage, f, indent=2)
            f.write("\n")
        count += 1

    print(f"\nExtracted source attribution for {count} flows")


def calculate_costs(directory: str):
    """Walk output directory, compute dollar costs from usage.json and request.json model info."""
    count = 0
    for dirpath, _dirnames, filenames in os.walk(directory):
        if "request.json" not in filenames or "usage.json" not in filenames:
            continue

        request_path = os.path.join(dirpath, "request.json")
        body = parse_request_body(request_path)
        if body is None:
            continue

        model = body.get("model")
        if not model:
            continue

        pricing = get_pricing(model)
        if pricing is None:
            print(f"  Warning: unknown model '{model}' in {request_path}, skipping cost calculation")
            continue

        usage_path = os.path.join(dirpath, "usage.json")
        with open(usage_path, "r") as f:
            usage = json.load(f)

        tokens_input = usage.get("input_tokens", 0)
        tokens_cache_write = usage.get("cache_creation_input_tokens", 0)
        tokens_cache_read = usage.get("cache_read_input_tokens", 0)
        tokens_output = usage.get("output_tokens", 0)
        tokens_total = tokens_input + tokens_cache_write + tokens_cache_read + tokens_output

        cost_input = tokens_input * pricing["input"]
        cost_cache_write = tokens_cache_write * pricing["cache_write"]
        cost_cache_read = tokens_cache_read * pricing["cache_read"]
        cost_output = tokens_output * pricing["output"]
        cost_total = cost_input + cost_cache_write + cost_cache_read + cost_output

        usage["cost"] = {
            "input": {"tokens": tokens_input, "dollars": round(cost_input, 6)},
            "cache_write": {"tokens": tokens_cache_write, "dollars": round(cost_cache_write, 6)},
            "cache_read": {"tokens": tokens_cache_read, "dollars": round(cost_cache_read, 6)},
            "output": {"tokens": tokens_output, "dollars": round(cost_output, 6)},
            "total": {"tokens": tokens_total, "dollars": round(cost_total, 6)},
        }

        # Remove top-level token keys (now redundant with cost entries)
        for key in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"):
            usage.pop(key, None)

        canonical = get_canonical_model(model)
        if canonical:
            usage["model"] = canonical

        with open(usage_path, "w") as f:
            json.dump(usage, f, indent=2)
            f.write("\n")
        count += 1

    print(f"\nCalculated costs for {count} flows")


def _aggregate_by_source(agg, flow_by_source):
    """Aggregate per-flow by_source into summary by_source.

    Input side: sum tool_results {tokens, dollars} dicts.
    Output side: sum token/dollars dicts.
    """
    # Input: tool_results and tool_calls are {tokens, dollars} dicts
    flow_input = flow_by_source.get("input", {})
    for input_key in ("tool_results", "tool_calls"):
        flow_section = flow_input.get(input_key, {})
        if not flow_section:
            continue
        if input_key not in agg["input"]:
            agg["input"][input_key] = {}
        for tool, entry in flow_section.items():
            if not isinstance(entry, dict):
                continue
            if tool not in agg["input"][input_key]:
                agg["input"][input_key][tool] = {"tokens": 0, "dollars": 0.0, "chars": 0}
            agg["input"][input_key][tool]["tokens"] += entry.get("tokens", 0)
            agg["input"][input_key][tool]["dollars"] += entry.get("dollars", 0.0)
            agg["input"][input_key][tool]["chars"] += entry.get("chars", 0)

    # Output: token/dollars dicts
    flow_output = flow_by_source.get("output", {})
    for key in ("llm_text",):
        entry = flow_output.get(key)
        if entry:
            if key not in agg["output"]:
                agg["output"][key] = {"tokens": 0, "dollars": 0.0, "chars": 0}
            agg["output"][key]["tokens"] += entry.get("tokens", 0)
            agg["output"][key]["dollars"] += entry.get("dollars", 0.0)
            agg["output"][key]["chars"] += entry.get("chars", 0)

    # Tool calls
    flow_tc = flow_output.get("tool_calls", {})
    if flow_tc:
        if "tool_calls" not in agg["output"]:
            agg["output"]["tool_calls"] = {}
        for tool_key, entry in flow_tc.items():
            if not isinstance(entry, dict):
                continue
            if tool_key not in agg["output"]["tool_calls"]:
                agg["output"]["tool_calls"][tool_key] = {"tokens": 0, "dollars": 0.0, "chars": 0}
            agg["output"]["tool_calls"][tool_key]["tokens"] += entry.get("tokens", 0)
            agg["output"]["tool_calls"][tool_key]["dollars"] += entry.get("dollars", 0.0)
            agg["output"]["tool_calls"][tool_key]["chars"] += entry.get("chars", 0)


def _round_by_source(agg):
    """Round dollar values in the by_source aggregate."""
    # Input: round dollars
    for input_key in ("tool_results", "tool_calls"):
        section = agg.get("input", {}).get(input_key, {})
        for entry in section.values():
            if isinstance(entry, dict) and "dollars" in entry:
                entry["dollars"] = round(entry["dollars"], 6)
    # Output: round dollars
    for key in ("llm_text",):
        entry = agg.get("output", {}).get(key)
        if entry:
            entry["dollars"] = round(entry["dollars"], 6)
    tc = agg.get("output", {}).get("tool_calls", {})
    for entry in tc.values():
        if isinstance(entry, dict) and "dollars" in entry:
            entry["dollars"] = round(entry["dollars"], 6)


AGENT_COLORS = {
    "vix": "#7B2FBE",
    "cc": "#D77656",
}


def _agent_color(name):
    """Return a deterministic hex color for an unknown agent name."""
    import hashlib
    h = int(hashlib.md5(name.encode()).hexdigest()[:6], 16)
    return f"#{h:06x}"


def summarize_usage(directory: str):
    """Aggregate per-request usage.json files into a summary usage.json per agent type."""
    COST_FIELDS = ["input", "cache_write", "cache_read", "output", "total"]

    def empty_bucket():
        bucket = {"request_count": 0}
        bucket["cost"] = {f: {"tokens": 0, "dollars": 0.0} for f in COST_FIELDS}
        bucket["timing"] = {
            "total_duration_ms": 0,
            "min_request_start": None, "max_response_end": None,
        }
        return bucket

    def add_to_bucket(bucket, usage):
        bucket["request_count"] += 1
        cost = usage.get("cost")
        if cost:
            for f in COST_FIELDS:
                entry = cost.get(f, {})
                if isinstance(entry, dict):
                    bucket["cost"][f]["tokens"] += entry.get("tokens", 0)
                    bucket["cost"][f]["dollars"] += entry.get("dollars", 0.0)
        timing = usage.get("timing")
        if timing:
            bucket["timing"]["total_duration_ms"] += timing.get("duration_ms", 0)
            rs = timing.get("request_start")
            re_ = timing.get("response_end")
            if rs is not None:
                cur = bucket["timing"]["min_request_start"]
                bucket["timing"]["min_request_start"] = rs if cur is None else min(cur, rs)
            if re_ is not None:
                cur = bucket["timing"]["max_response_end"]
                bucket["timing"]["max_response_end"] = re_ if cur is None else max(cur, re_)

    def round_costs(bucket):
        for f in COST_FIELDS:
            bucket["cost"][f]["dollars"] = round(bucket["cost"][f]["dollars"], 6)

    count = 0
    for entry in sorted(os.listdir(directory)):
        agent_dir = os.path.join(directory, entry)
        if not os.path.isdir(agent_dir):
            continue

        # Only process dirs that contain numbered step subdirs
        has_numbered = False
        for sub in os.listdir(agent_dir):
            if sub.isdigit() and os.path.isdir(os.path.join(agent_dir, sub)):
                has_numbered = True
                break
        if not has_numbered:
            continue

        by_model = {}
        by_step = {}
        total = empty_bucket()
        ws_agg = {"line_returns_count": 0, "unnecessary_space_count": 0, "total_chars": 0}
        by_source_agg = {"input": {}, "output": {}}
        file_ops_agg = {
            "input": {"unique_files_read": 0, "total_read_chars": 0, "per_file": {}},
            "output": {"files_written": 0, "unique_files_written": 0, "total_write_chars": 0, "per_file": {}},
        }

        # Iterate step dirs: {agent_dir}/{step_number}/
        for step_sub in sorted(os.listdir(agent_dir), key=lambda x: int(x) if x.isdigit() else float("inf")):
            if not step_sub.isdigit():
                continue
            step_dir = os.path.join(agent_dir, step_sub)
            if not os.path.isdir(step_dir):
                continue

            step_key = step_sub  # Use step directory number as key

            # Iterate request dirs: {step_dir}/{request_number}/usage.json
            for req_sub in sorted(os.listdir(step_dir), key=lambda x: int(x) if x.isdigit() else float("inf")):
                if not req_sub.isdigit():
                    continue
                usage_path = os.path.join(step_dir, req_sub, "usage.json")
                if not os.path.isfile(usage_path):
                    continue

                with open(usage_path, "r") as f:
                    usage = json.load(f)

                model_name = usage.get("model", "unknown")
                if model_name not in by_model:
                    by_model[model_name] = empty_bucket()
                add_to_bucket(by_model[model_name], usage)

                add_to_bucket(total, usage)

                # Aggregate by_source
                flow_by_source = usage.get("by_source")
                if flow_by_source:
                    _aggregate_by_source(by_source_agg, flow_by_source)

                # Aggregate read_file whitespace
                flow_ws = usage.get("read_file_whitespace")
                if flow_ws:
                    for k in ("line_returns_count", "unnecessary_space_count", "total_chars"):
                        ws_agg[k] += flow_ws.get(k, 0)

                # Aggregate file_ops
                flow_file_ops = usage.get("file_ops")
                if flow_file_ops:
                    fi = flow_file_ops.get("input", {})
                    fo = flow_file_ops.get("output", {})
                    # Scalar fields will be recomputed from deduplicated per_file after aggregation
                    # Aggregate per-file details (merge tool_ids maps for dedup)
                    for side in ("input", "output"):
                        for fp, stats in fi.get("per_file", {}).items() if side == "input" else fo.get("per_file", {}).items():
                            agg_pf = file_ops_agg[side]["per_file"]
                            if fp not in agg_pf:
                                agg_pf[fp] = {"tool_ids": {}, "file_size": stats.get("file_size")}
                            for tid, tc in stats.get("tool_ids", {}).items():
                                agg_pf[fp]["tool_ids"][tid] = tc

                # Aggregate per step
                if step_key not in by_step:
                    by_step[step_key] = empty_bucket()
                    by_step[step_key]["by_source"] = {"input": {}, "output": {}}
                    by_step[step_key]["read_file_whitespace"] = {"line_returns_count": 0, "unnecessary_space_count": 0, "total_chars": 0}
                    by_step[step_key]["file_ops"] = {
                        "input": {"unique_files_read": 0, "total_read_chars": 0, "per_file": {}},
                        "output": {"files_written": 0, "unique_files_written": 0, "total_write_chars": 0, "per_file": {}},
                    }
                add_to_bucket(by_step[step_key], usage)
                if flow_by_source:
                    _aggregate_by_source(by_step[step_key]["by_source"], flow_by_source)
                if flow_ws:
                    for k in ("line_returns_count", "unnecessary_space_count", "total_chars"):
                        by_step[step_key]["read_file_whitespace"][k] += flow_ws.get(k, 0)
                if flow_file_ops:
                    fi = flow_file_ops.get("input", {})
                    fo = flow_file_ops.get("output", {})
                    # Scalar fields will be recomputed from deduplicated per_file after aggregation
                    for side in ("input", "output"):
                        for fp, stats in fi.get("per_file", {}).items() if side == "input" else fo.get("per_file", {}).items():
                            agg_pf = by_step[step_key]["file_ops"][side]["per_file"]
                            if fp not in agg_pf:
                                agg_pf[fp] = {"tool_ids": {}, "file_size": stats.get("file_size")}
                            for tid, tc in stats.get("tool_ids", {}).items():
                                agg_pf[fp]["tool_ids"][tid] = tc

        round_costs(total)
        for bucket in by_model.values():
            round_costs(bucket)
        for bucket in by_step.values():
            round_costs(bucket)
            if "by_source" in bucket:
                _round_by_source(bucket["by_source"])
        _round_by_source(by_source_agg)

        # Finalize file_ops: convert tool_ids maps to calls/chars, recompute scalars
        def _finalize_file_ops(fops):
            for side in ("input", "output"):
                pf = fops[side]["per_file"]
                for fp in pf:
                    pf[fp]["calls"] = len(pf[fp].get("tool_ids", {}))
                    pf[fp]["chars"] = sum(pf[fp].get("tool_ids", {}).values())
                    pf[fp].pop("tool_ids", None)
            fops["input"]["unique_files_read"] = len(fops["input"]["per_file"])
            fops["input"]["total_read_chars"] = sum(v["chars"] for v in fops["input"]["per_file"].values())
            fops["output"]["unique_files_written"] = len(fops["output"]["per_file"])
            fops["output"]["files_written"] = sum(v["calls"] for v in fops["output"]["per_file"].values())
            fops["output"]["total_write_chars"] = sum(v["chars"] for v in fops["output"]["per_file"].values())

        _finalize_file_ops(file_ops_agg)
        for bucket in by_step.values():
            if "file_ops" in bucket:
                _finalize_file_ops(bucket["file_ops"])

        # Finalize timing for each bucket
        def _finalize_timing(bucket):
            t = bucket["timing"]
            req_count = bucket["request_count"]
            mn = t.pop("min_request_start", None)
            mx = t.pop("max_response_end", None)
            if mn is not None and mx is not None:
                t["wall_clock_ms"] = round((mx - mn) * 1000)
            else:
                t["wall_clock_ms"] = 0
            t["avg_duration_ms"] = round(t["total_duration_ms"] / req_count) if req_count > 0 else 0

        _finalize_timing(total)
        for bucket in by_model.values():
            _finalize_timing(bucket)
        for bucket in by_step.values():
            _finalize_timing(bucket)

        total["file_ops"] = file_ops_agg
        summary = {"title": entry, "color": AGENT_COLORS.get(entry, _agent_color(entry)), "by_model": by_model, "by_step": by_step, "by_source": by_source_agg, "read_file_whitespace": ws_agg, "total": total}
        summary_path = os.path.join(agent_dir, "usage.json")
        with open(summary_path, "w") as f:
            json.dump(summary, f, indent=2)
            f.write("\n")
        count += 1
        print(f"  Wrote summary: {summary_path} ({total['request_count']} requests)")

    print(f"\nSummarized usage for {count} agent types")


def main():
    script_dir = os.path.dirname(os.path.abspath(__file__))
    data_dir = os.path.join(script_dir, "data")

    parser = argparse.ArgumentParser(description="Export mitmproxy flow files to text.")
    parser.add_argument("--input-directory", default=data_dir, help="Directory containing *.flow files (default: data/ next to script)")
    parser.add_argument("--output-directory", default=data_dir, help="Output directory for exported flows (default: data/ next to script)")
    args = parser.parse_args()

    input_dir = args.input_directory
    output_dir = args.output_directory
    os.makedirs(output_dir, exist_ok=True)

    redact_flow_files(input_dir)
    export_flows(input_dir, output_dir)
    extract_usage(output_dir)
    export_parsed_responses(output_dir)
    extract_prompts(output_dir)
    calculate_costs(output_dir)
    extract_source_attribution(output_dir)
    summarize_usage(output_dir)


if __name__ == "__main__":
    main()
