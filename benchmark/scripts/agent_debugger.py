#!/usr/bin/env python3
"""
Agent Debugger CLI

Analyzes agent execution traces using LLM (Debugger Agent).

Features:
- ask mode: Q&A analysis of traces (root cause, patterns)
- check mode: Automatic issue detection (tool errors, loops, hallucinations)
- Supports multi-trace comparison (PASS vs FAIL)

Usage:
    python3 agent_debugger.py ask \
        --traces trace1.json trace2.json \
        --question "为什么失败了？"

    python3 agent_debugger.py check \
        --traces trace1.json \
        --format json
"""

import json
import os
import re
import subprocess
import sys
import time
import uuid
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Optional, Union


# System prompt for the Debugger Agent
DEBUGGER_SYSTEM_PROMPT = """你是一个专业的 agent debugger，专门分析 agent 执行轨迹并回答问题。

## 任务
用户会给你一个或多个 agent 执行轨迹的本地文件路径（OpenAI messages 格式）。
每个文件包含 {"trace_id": "...", "messages": [...]}。
你不会在 system prompt 中看到完整的 trace 内容，必须通过工具读取。

## 工具
你有：read_file（读取文件）、search_file_content（搜索内容）、complete_task（完成任务）。

## 工作流程
严格按以下顺序执行，不要跳过：

1. **快速浏览**（≈ tool call 1-3）：对每个路径，用 small limit 读取文件头部，了解大致形状（system/user/assistant/tool 模式、错误标记、是否有 trace_id）。

2. **定位问题**（≈ tool call 4-10）：用 search_file_content 搜索工具名称、错误关键词、用户文本，找到与问题相关的范围。

3. **读取上下文**（≈ tool call 11-15）：用 offset/limit 完整读取每个命中的工具 I/O 上下文，然后得出结论。

4. **多 trace 对比**（≈ tool call 16-18，仅当多个 traces 时）：对比发现 —— 哪些点一致，哪些点分歧，哪个 trace 在每个争议点上更正确。

5. **完成**（≤ 20 calls）：调用 complete_task 恰好一次。

## 输出格式
调用 complete_task 恰好一次，result 字段匹配以下 schema：

### 对于 ask 模式
{
  "mode": "ask",
  "answer": "自由格式文本；引用确切的 message_index",
  "risks": {
    "description": "简短描述如果应用这些修复可能导致哪些任务回归",
    "affected_tasks": ["task_id_1", "task_id_2"],
    "confidence": "high|medium|low"
  }
}

### 对于 check 模式
{
  "mode": "check",
  "issues": [
    {
      "issue_type": "工具错误 | 幻觉 | 循环 | 不合规 | 截断",
      "summary": "一行摘要",
      "evidence": "引用的文本 / 确切原因",
      "trace_id": "trace ID",
      "message_index": 123
    }
  ],
  "response": "简短总段落"
}

issue_type 必须是上述 5 个枚举值之一。
message_index 是该 trace 的 messages 数组的 0-based 索引。
trace_id 必须匹配输入文件中的 trace_id；如果文件缺少，使用文件名。

## 风格要求
- 优先用具体证据 —— 精确的 message_index、引用 trace 中的字符串 —— 而不是模糊的声明。
- 每个证据都引用 trace_id + message_index（例如 trace_id=abc123 #42）。仅在 trace_id 缺失或重复时回退到文件名；永远不要用完整文件路径引用。
- 当给多个 traces 时，不要只是依次总结每个 —— 明确指出哪些点一致，哪些点分歧。
- 如果证据不足以回答，在 answer/response 中说清楚，并列出你检查了哪些 traces 和哪些 message_index 范围。不要编造。
- 保持答案简洁；读者是自动化的。

## 风险预测（重要）
在完成分析后，如果你识别出的修复策略可能影响其他任务，必须在 result 中包含 risks 字段：
- 如果你的建议强调"严格遵守约束"、"强制顺序"、"完整读取"，可能让某些任务变慢或失败
- 如果你的建议改变默认行为（如"不要用 grep 预过滤"、"不要提前停止"），可能影响依赖这些启发式的任务
- 如果修复涉及 task-specific 的代码（如"为 agent_005 专门修改"），谨慎声明只影响该任务
- 如果无法确定风险，设置为 null 或留空 affected_tasks
"""


# Tool definitions for the debugger agent (OpenAI function-calling schema)
DEBUGGER_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "read_file",
            "description": "Read a range of lines from a file. Returns file content with line numbers.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {
                        "type": "string",
                        "description": "Absolute file path to read"
                    },
                    "offset": {
                        "type": "integer",
                        "description": "0-based line offset to start reading from (default: 0)",
                        "default": 0
                    },
                    "limit": {
                        "type": "integer",
                        "description": "Maximum number of lines to read (default: 100, max: 500)",
                        "default": 100
                    }
                },
                "required": ["path"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "search_file_content",
            "description": "Search for a pattern in a file using regex. Returns matching lines with line numbers.",
            "parameters": {
                "type": "object",
                "properties": {
                    "path": {
                        "type": "string",
                        "description": "Absolute file path to search"
                    },
                    "pattern": {
                        "type": "string",
                        "description": "Regex pattern to search for"
                    }
                },
                "required": ["path", "pattern"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "complete_task",
            "description": "Complete the task and return the final result. Call exactly once when done.",
            "parameters": {
                "type": "object",
                "properties": {
                    "result": {
                        "type": "object",
                        "description": "Final result object matching the output schema",
                        "properties": {
                            "mode": {
                                "type": "string",
                                "enum": ["ask", "check"]
                            },
                            "answer": {
                                "type": "string",
                                "description": "Answer text (for ask mode)"
                            },
                            "issues": {
                                "type": "array",
                                "description": "List of issues (for check mode)",
                                "items": {
                                    "type": "object",
                                    "properties": {
                                        "issue_type": {"type": "string"},
                                        "summary": {"type": "string"},
                                        "evidence": {"type": "string"},
                                        "trace_id": {"type": "string"},
                                        "message_index": {"type": "integer"}
                                    }
                                }
                            },
                            "response": {
                                "type": "string",
                                "description": "Summary paragraph"
                            }
                        },
                        "required": ["mode"]
                    }
                },
                "required": ["result"]
            }
        }
    }
]

# Default config path
DEFAULT_CONFIG_PATH = os.path.join(os.path.dirname(__file__), "debugger_agent_config.yaml")


def _load_config(config_path: Optional[str] = None) -> Dict[str, Any]:
    """Load configuration from YAML file. Falls back to defaults if unavailable."""
    defaults = {
        "llm": {
            "model": "gpt-4.1",
            "ai_binary_path": "/Users/genius/project/ai/bin/ai",
            "max_tokens": 4096,
            "temperature": 0.3,
        },
        "debugger": {
            "max_iterations": 20,
            "timeout": 60,
            "max_retries": 3,
        },
        "traces": {
            "max_message_size": 30000,
        },
        "issues": {
            "loop_threshold": 5,
            "error_keywords": ["error", "failed", "exception"],
        },
    }

    path = config_path or DEFAULT_CONFIG_PATH
    if os.path.exists(path):
        try:
            import yaml
            with open(path, "r") as f:
                user_config = yaml.safe_load(f) or {}
            # Deep merge user_config into defaults
            for key in user_config:
                if key in defaults and isinstance(defaults[key], dict) and isinstance(user_config[key], dict):
                    defaults[key].update(user_config[key])
                else:
                    defaults[key] = user_config[key]
        except ImportError:
            # yaml not available, use defaults
            pass
        except Exception:
            pass

    return defaults


class DebuggerAgent:
    """Debugger Agent that uses LLM to analyze traces."""

    def __init__(
        self,
        model: str = "gpt-4.1",
        base_url: str = None,
        api_key: str = None,
        config_path: Optional[str] = None,
    ):
        """
        Initialize Debugger Agent.

        Args:
            model: LLM model name
            base_url: API base URL (optional, for custom endpoints)
            api_key: API key (optional)
            config_path: Path to config YAML file
        """
        self.model = model
        self.base_url = base_url or os.environ.get("OPENAI_BASE_URL")
        self.api_key = api_key or os.environ.get("OPENAI_API_KEY")
        self.request_id = str(uuid.uuid4())
        self.config = _load_config(config_path)

        # Resolve LLM backend
        self.ai_binary_path = self.config["llm"].get("ai_binary_path", "")
        self._use_openai = bool(self.api_key)

    # ------------------------------------------------------------------
    # LLM calling
    # ------------------------------------------------------------------

    def _call_llm(self, messages: List[Dict[str, str]], tools: Optional[List[Dict]] = None, **kwargs) -> Dict[str, Any]:
        """
        Call LLM API with retry logic.

        Priority:
        1. OpenAI API (if OPENAI_API_KEY is configured)
        2. Local ai binary (subprocess)

        Args:
            messages: Message list in OpenAI format
            tools: Optional tool definitions for function calling
            **kwargs: Override temperature, max_tokens, etc.

        Returns:
            Dict with keys: "content" (str), "tool_calls" (list | None)
        """
        max_retries = self.config["debugger"].get("max_retries", 3)
        timeout = self.config["debugger"].get("timeout", 60)

        last_error = None
        for attempt in range(1, max_retries + 1):
            try:
                if self._use_openai:
                    result = self._call_openai_api(messages, tools, timeout, **kwargs)
                else:
                    result = self._call_ai_binary(messages, tools, timeout, **kwargs)
                return result
            except Exception as e:
                last_error = e
                if attempt < max_retries:
                    time.sleep(2 ** attempt)  # exponential backoff

        raise RuntimeError(f"LLM call failed after {max_retries} retries: {last_error}")

    def _call_openai_api(
        self,
        messages: List[Dict[str, str]],
        tools: Optional[List[Dict]],
        timeout: int,
        **kwargs,
    ) -> Dict[str, Any]:
        """Call OpenAI-compatible API using http.client (no external deps)."""
        import http.client
        import ssl

        base_url = self.base_url or "https://api.openai.com"
        # Parse base_url into (host, base_path)
        parsed = re.match(r"(https?://)?([^/]+)(/.*)?", base_url)
        if not parsed:
            raise ValueError(f"Invalid base_url: {base_url}")
        scheme = parsed.group(1) or "https://"
        host = parsed.group(2)
        base_path = (parsed.group(3) or "").rstrip("/")
        use_https = scheme.startswith("https")

        model = kwargs.get("model", self.model)
        temperature = kwargs.get("temperature", self.config["llm"].get("temperature", 0.3))
        max_tokens = kwargs.get("max_tokens", self.config["llm"].get("max_tokens", 4096))

        body: Dict[str, Any] = {
            "model": model,
            "messages": messages,
            "temperature": temperature,
            "max_tokens": max_tokens,
        }
        if tools:
            body["tools"] = tools
            body["tool_choice"] = "auto"

        body_json = json.dumps(body, ensure_ascii=False)

        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}",
        }

        endpoint = f"{base_path}/v1/chat/completions"
        if not endpoint.startswith("/"):
            endpoint = "/" + endpoint

        ctx = ssl.create_default_context() if use_https else None
        conn_factory = http.client.HTTPSConnection if use_https else http.client.HTTPConnection
        conn = conn_factory(host, timeout=timeout, context=ctx)
        try:
            conn.request("POST", endpoint, body=body_json, headers=headers)
            resp = conn.getresponse()
            resp_body = resp.read().decode("utf-8")
            if resp.status != 200:
                raise RuntimeError(f"OpenAI API error {resp.status}: {resp_body[:500]}")
            data = json.loads(resp_body)
        finally:
            conn.close()

        choice = data["choices"][0]["message"]
        return {
            "risks": None,
            "content": choice.get("content") or "",
            "tool_calls": choice.get("tool_calls"),
        }

    def _call_ai_binary(
        self,
        messages: List[Dict[str, str]],
        tools: Optional[List[Dict]],
        timeout: int,
        **kwargs,
    ) -> Dict[str, Any]:
        """Call local ai binary via subprocess RPC (prompt mode).

        The ai binary runs its own agent loop. We send the combined prompt
        and extract the final text from the agent_end event.
        """
        ai_path = self.ai_binary_path
        if not ai_path or not os.path.isfile(ai_path):
            import shutil
            ai_path = shutil.which("ai")
            if not ai_path:
                raise FileNotFoundError(
                    f"ai binary not found at '{self.ai_binary_path}' or in PATH. "
                    "Set OPENAI_API_KEY or configure ai_binary_path."
                )

        # Combine messages into a single prompt string
        prompt_parts = []
        for msg in messages:
            role = msg.get("role", "user")
            content = msg.get("content", "")
            if isinstance(content, list):
                # Handle structured content (list of dicts)
                text_parts = []
                for item in content:
                    if isinstance(item, str):
                        text_parts.append(item)
                    elif isinstance(item, dict):
                        text_parts.append(item.get("text", item.get("thinking", "")))
                content = "\n".join(text_parts)
            if content:
                if role == "system":
                    prompt_parts.append(f"[System]\n{content}")
                elif role == "user":
                    prompt_parts.append(f"[User]\n{content}")
                elif role == "assistant":
                    prompt_parts.append(f"[Assistant]\n{content}")
        combined_prompt = "\n\n".join(prompt_parts)

        # Send prompt via RPC
        rpc_cmd = json.dumps({
            "id": "dbg-1",
            "type": "prompt",
            "message": combined_prompt,
        }, ensure_ascii=False)

        try:
            proc = subprocess.run(
                [ai_path, "--mode", "rpc"],
                input=rpc_cmd,
                capture_output=True,
                text=True,
                timeout=timeout,
            )
        except subprocess.TimeoutExpired:
            raise RuntimeError(f"ai binary timed out after {timeout}s")

        if proc.returncode != 0:
            raise RuntimeError(f"ai binary exited with code {proc.returncode}: {proc.stderr[:500]}")

        # Parse streaming output - find the agent_end event with final messages
        text_content = ""
        for line in proc.stdout.strip().splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError:
                continue

            # Extract text from message_update events (streaming text)
            if event.get("type") == "message_update":
                msg = event.get("message", {})
                assistant_event = event.get("assistantMessageEvent", {})
                if assistant_event.get("type") == "text_delta":
                    text_content += assistant_event.get("delta", "")

            # Also check agent_end for final content
            if event.get("type") == "agent_end":
                final_messages = event.get("messages", [])
                for msg in final_messages:
                    if msg.get("role") == "assistant":
                        content = msg.get("content", [])
                        if isinstance(content, list):
                            for item in content:
                                if isinstance(item, dict) and item.get("type") == "text":
                                    text_content = item.get("text", text_content)
                        elif isinstance(content, str) and content:
                            text_content = content

        if not text_content:
            raise RuntimeError("ai binary returned no text content")

        return {
            "risks": None,
            "content": text_content,
            "tool_calls": None,  # ai binary handles tools internally
        }


    def _read_file(self, path: str, offset: int = 0, limit: int = 100) -> str:
        """Read a range of lines from a file and return with line numbers."""
        try:
            file_path = Path(path)
            if not file_path.exists():
                return f"Error: File not found: {path}"
            with open(file_path, "r", encoding="utf-8", errors="replace") as f:
                lines = f.readlines()
            # Apply offset and limit
            selected = lines[offset: offset + limit]
            # Format with line numbers
            numbered = [f"{offset + i + 1}: {line.rstrip()}" for i, line in enumerate(selected)]
            return "\n".join(numbered) if numbered else "(empty range)"
        except Exception as e:
            return f"Error reading file: {e}"

    def _search_file_content(self, path: str, pattern: str) -> str:
        """Search for a regex pattern in a file, return matching lines with line numbers."""
        try:
            file_path = Path(path)
            if not file_path.exists():
                return f"Error: File not found: {path}"
            with open(file_path, "r", encoding="utf-8", errors="replace") as f:
                lines = f.readlines()
            regex = re.compile(pattern, re.IGNORECASE)
            matches = []
            for i, line in enumerate(lines):
                if regex.search(line):
                    matches.append(f"{i + 1}: {line.rstrip()}")
            if not matches:
                return "No matches found."
            # Limit output
            max_matches = 50
            if len(matches) > max_matches:
                matches = matches[:max_matches]
                matches.append(f"... ({len(matches)} more matches)")
            return "\n".join(matches)
        except re.error as e:
            return f"Invalid regex pattern: {e}"
        except Exception as e:
            return f"Error searching file: {e}"

    def _execute_tool_call(self, tool_call: Dict) -> Dict:
        """Execute a tool call and return the result in OpenAI tool result format."""
        func = tool_call.get("function", {})
        name = func.get("name", "")
        tool_call_id = tool_call.get("id", "")

        try:
            args = json.loads(func.get("arguments", "{}"))
        except json.JSONDecodeError:
            args = {}

        if name == "read_file":
            result_text = self._read_file(
                path=args.get("path", ""),
                offset=args.get("offset", 0),
                limit=args.get("limit", 100),
            )
        elif name == "search_file_content":
            result_text = self._search_file_content(
                path=args.get("path", ""),
                pattern=args.get("pattern", ""),
            )
        elif name == "complete_task":
            # Return marker so the caller knows to stop
            return {
            "risks": None,
                "tool_call_id": tool_call_id,
                "name": name,
                "content": json.dumps(args.get("result", {}), ensure_ascii=False),
                "_is_complete": True,
            }
        else:
            result_text = f"Unknown tool: {name}"

        # Truncate if too large
        max_size = self.config["traces"].get("max_message_size", 30000)
        if len(result_text) > max_size:
            result_text = result_text[:max_size] + "\n... (truncated)"

        return {
            "risks": None,
            "tool_call_id": tool_call_id,
            "name": name,
            "content": result_text,
            "_is_complete": False,
        }

    # ------------------------------------------------------------------
    # Agent loop
    # ------------------------------------------------------------------

    def _run_debugger_agent(self, user_message: str, mode: str = "ask") -> Dict[str, Any]:
        """
        Run the Debugger Agent loop with tool calling.

        Flow:
        1. system_prompt + user_message -> LLM
        2. LLM generates response (may include tool_calls)
        3. Execute tool_calls (read_file, search_file_content, complete_task)
        4. Append tool_results to message history
        5. Repeat until complete_task or max_iterations reached
        6. Return final result

        Args:
            user_message: The user prompt with trace paths and question
            mode: "ask" or "check"

        Returns:
            Parsed result dict from complete_task, or fallback dict
        """
        max_iterations = self.config["debugger"].get("max_iterations", 20)

        messages = [
            {"role": "system", "content": DEBUGGER_SYSTEM_PROMPT},
            {"role": "user", "content": user_message},
        ]

                # For ai binary backend: single-shot call (ai binary runs its own tool loop)
        if not self._use_openai:
            response = self._call_llm(messages)
            content = response.get("content", "")
            return self._parse_llm_text_result(content, mode)

        # For OpenAI backend: run tool-calling loop
        for iteration in range(max_iterations):
            # Call LLM
            response = self._call_llm(messages, tools=DEBUGGER_TOOLS)

            # Build assistant message for history
            assistant_msg: Dict[str, Any] = {"role": "assistant"}
            if response.get("content"):
                assistant_msg["content"] = response["content"]
            else:
                assistant_msg["content"] = None

            tool_calls = response.get("tool_calls")
            if tool_calls:
                # Normalize tool_calls for message history
                normalized_calls = []
                for tc in tool_calls:
                    normalized_calls.append({
                        "id": tc.get("id", f"call_{iteration}_{len(normalized_calls) if 'normalized_calls' in dir() else 0}"),
                        "type": "function",
                        "function": {
                            "name": tc["function"]["name"],
                            "arguments": tc["function"].get("arguments", "{}"),
                        },
                    })
                assistant_msg["tool_calls"] = normalized_calls

            messages.append(assistant_msg)

            # No tool calls -> LLM gave a text response, we're done
            if not tool_calls:
                # Try to extract JSON from the content
                content = response.get("content", "")
                return self._parse_llm_text_result(content, mode)

            # Execute each tool call
            completed = False
            final_result = None
            for tc in normalized_calls:
                tool_result = self._execute_tool_call(tc)

                # Add tool result to messages
                messages.append({
                    "role": "tool",
                    "tool_call_id": tool_result["tool_call_id"],
                    "content": tool_result["content"],
                })

                if tool_result.get("_is_complete"):
                    completed = True
                    try:
                        final_result = json.loads(tool_result["content"])
                    except json.JSONDecodeError:
                        final_result = {"mode": mode, "answer": tool_result["content"]}

            if completed:
                return final_result

        # Max iterations reached without complete_task
        # Build a fallback result from the last assistant message
        return {
            "risks": None,
            "mode": mode,
            "answer": "达到最大迭代次数，未能完成分析。" if mode == "ask" else "",
            "issues": [],
            "response": "Agent loop reached max iterations without completing.",
        }

    def _parse_llm_text_result(self, content: str, mode: str) -> Dict[str, Any]:
        """Try to extract JSON result from LLM text output."""
        # Try to find JSON in markdown code blocks
        json_match = re.search(r"```(?:json)?\s*\n?(.*?)\n?```", content, re.DOTALL)
        if json_match:
            try:
                return json.loads(json_match.group(1).strip())
            except json.JSONDecodeError:
                pass

        # Try to parse the entire content as JSON
        try:
            return json.loads(content)
        except json.JSONDecodeError:
            pass

        # Fallback: wrap as text answer
        result = {
            "mode": mode,
            "answer": content,
            "response": content[:200],
        }
        # Add risks if mode is ask
        if mode == "ask":
            result["risks"] = None
        return result

    # ------------------------------------------------------------------
    # Issue detection (rule-based, for check mode)
    # ------------------------------------------------------------------

    def _detect_issues(self, messages: List[Dict[str, Any]], trace_id: str) -> List[Dict[str, Any]]:
        """
        Detect 5 types of issues in trace messages:
        1. Tool errors: tool output contains error/failed/exception
        2. Hallucination: assistant claims success but tool failed
        3. Loop: same tool called >= threshold times
        4. Non-compliance: violates constraints (e.g., wrong first tool)
        5. Truncation: messages are incomplete or empty
        """
        issues = []
        loop_threshold = self.config["issues"].get("loop_threshold", 5)
        error_keywords = self.config["issues"].get("error_keywords", ["error", "failed", "exception"])

        # --- 1. Tool Errors ---
        for i, msg in enumerate(messages):
            if msg.get("role") != "tool":
                continue
            content = msg.get("content", "")
            if not content:
                continue
            content_lower = content.lower()
            for kw in error_keywords:
                if kw in content_lower:
                    issues.append({
                        "issue_type": "工具错误",
                        "summary": f"Tool output contains '{kw}' (message #{i})",
                        "evidence": content[:200],
                        "trace_id": trace_id,
                        "message_index": i,
                        "severity": "high" if kw == "error" else "medium",
                    })
                    break  # one report per message

        # --- 2. Hallucination ---
        # Look for assistant messages claiming success, preceded by tool errors
        for i, msg in enumerate(messages):
            if msg.get("role") != "assistant":
                continue
            text = ""
            if isinstance(msg.get("content"), str):
                text = msg["content"].lower()
            elif isinstance(msg.get("content"), list):
                # content may be list of dicts
                text = " ".join(
                    str(c) for c in msg["content"] if isinstance(c, (str, dict))
                ).lower()

            success_indicators = ["success", "completed", "done", "fixed", "resolved", "已修复", "成功"]
            has_success_claim = any(kw in text for kw in success_indicators)

            if not has_success_claim:
                continue

            # Check if the preceding tool message had an error
            if i > 0 and messages[i - 1].get("role") == "tool":
                prev_content = messages[i - 1].get("content", "").lower()
                has_error = any(kw in prev_content for kw in error_keywords)
                if has_error:
                    issues.append({
                        "issue_type": "幻觉",
                        "summary": f"Assistant claims success but previous tool had error (message #{i})",
                        "evidence": f"Assistant: '{text[:100]}...' | Tool: '{prev_content[:100]}...'",
                        "trace_id": trace_id,
                        "message_index": i,
                        "severity": "high",
                    })

        # --- 3. Loops ---
        tool_call_sequence: Dict[str, List[int]] = {}
        for i, msg in enumerate(messages):
            if msg.get("role") != "assistant":
                continue
            for tc in msg.get("tool_calls", []):
                tool_name = tc.get("function", {}).get("name", "")
                if tool_name:
                    tool_call_sequence.setdefault(tool_name, []).append(i)

        for tool_name, indices in tool_call_sequence.items():
            count = len(indices)
            if count >= loop_threshold:
                issues.append({
                    "issue_type": "循环",
                    "summary": f"Tool '{tool_name}' called {count} times (threshold: {loop_threshold})",
                    "evidence": f"Called at message indices: {indices}",
                    "trace_id": trace_id,
                    "message_index": indices[0],
                    "severity": "medium" if count < 10 else "high",
                })

        # --- 4. Non-compliance ---
        first_assistant_msg = next(
            (msg for msg in messages if msg.get("role") == "assistant"),
            None,
        )
        if first_assistant_msg:
            first_tool = ""
            for tc in first_assistant_msg.get("tool_calls", []):
                first_tool = tc.get("function", {}).get("name", "")
                break
            if first_tool and first_tool not in ("read", "grep", "bash"):
                idx = messages.index(first_assistant_msg)
                issues.append({
                    "issue_type": "不合规",
                    "summary": f"First tool call is '{first_tool}', should read task file first",
                    "evidence": f"Message {idx}: first tool call = {first_tool}",
                    "trace_id": trace_id,
                    "message_index": idx,
                    "severity": "medium",
                })

        # --- 5. Truncation ---
        for i, msg in enumerate(messages):
            content = msg.get("content", "")
            # Check for empty content in tool results (indicates truncation or missing data)
            if msg.get("role") == "tool" and not content:
                issues.append({
                    "issue_type": "截断",
                    "summary": f"Empty tool output at message #{i}",
                    "evidence": f"tool_call_id={msg.get('tool_call_id', 'N/A')}, content is empty",
                    "trace_id": trace_id,
                    "message_index": i,
                    "severity": "low",
                })
            # Check for assistant content that looks truncated
            if msg.get("role") == "assistant" and isinstance(content, str):
                if content and len(content) > 50 and not content.rstrip().endswith((".", "。", "!", "？", "```")):
                    # Might be truncated - check if it ends mid-word
                    if content.endswith(("…", "...", "[truncated]", "\n")):
                        issues.append({
                            "issue_type": "截断",
                            "summary": f"Assistant message appears truncated at message #{i}",
                            "evidence": f"Ends with: '...{content[-50:]}'",
                            "trace_id": trace_id,
                            "message_index": i,
                            "severity": "low",
                        })

        return issues

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    def ask(self, traces: List[str], question: str) -> Dict[str, Any]:
        """
        Analyze traces and answer a question using real LLM.

        Args:
            traces: List of trace file paths
            question: Question to answer

        Returns:
            JSON result with "answer" and "response" fields
        """
        start_time = time.time()

        # Build trace metadata
        trace_metadata = []
        for trace_path in traces:
            with open(trace_path, "r") as f:
                trace_data = json.load(f)
            trace_metadata.append({
                "path": trace_path,
                "trace_id": trace_data.get("trace_id", Path(trace_path).stem),
                "task_id": trace_data.get("task_id"),
                "passed": trace_data.get("passed"),
                "verifier_output": trace_data.get("verifier_output", ""),
            })

        # Build user prompt
        trace_labels = ", ".join([
            f"{m['trace_id']}={'PASS' if m['passed'] else 'FAIL'}"
            for m in trace_metadata
        ])

        if len(trace_metadata) > 1:
            prompt = self._build_multiple_trace_prompt(trace_metadata, trace_labels, question)
        else:
            m = trace_metadata[0]
            if m["passed"]:
                prompt = self._build_summary_prompt(m, trace_labels)
            else:
                prompt = self._build_single_fail_prompt(m, trace_labels)

        # Prepend trace file paths so the agent can read them
        paths_section = "Trace files to analyze:\n"
        for tm in trace_metadata:
            paths_section += f"- {tm['path']}\n"
        paths_section += "\n" + prompt

        # Run the debugger agent with real LLM
        result = self._run_debugger_agent(paths_section, mode="ask")

        duration = time.time() - start_time

        return {
            "risks": None,
            "status": "success",
            "command": "ask",
            "trace_ids": [m["trace_id"] for m in trace_metadata],
            "question": question or "自动生成的分析问题",
            "answer": result.get("answer", ""),
            "response": result.get("response", result.get("answer", "")),
            "request_id": self.request_id,
            "metadata": {
                "llm_model": self.model,
                "backend": "openai" if self._use_openai else "ai_binary",
                "duration": round(duration, 2),
            },
        }

    def check(self, traces: List[str]) -> Dict[str, Any]:
        """
        Automatically detect issues in traces using rule engine + LLM summary.

        Args:
            traces: List of trace file paths

        Returns:
            JSON result with "issues" list
        """
        start_time = time.time()
        issues = []

        for trace_path in traces:
            with open(trace_path, "r") as f:
                trace_data = json.load(f)

            trace_id = trace_data.get("trace_id", Path(trace_path).stem)
            messages = trace_data.get("messages", [])

            # Detect issues using rule engine
            detected_issues = self._detect_issues(messages, trace_id)
            issues.extend(detected_issues)

        # Use LLM to generate a summary of the detected issues
        if issues:
            issue_descriptions = []
            for issue in issues:
                issue_descriptions.append(
                    f"- [{issue['issue_type']}] {issue['summary']} "
                    f"(trace: {issue['trace_id']}, msg #{issue['message_index']})"
                )
            summary_prompt = (
                f"以下是自动检测出的 {len(issues)} 个问题：\n\n"
                + "\n".join(issue_descriptions)
                + "\n\n请用一段话总结这些问题的整体情况和严重程度（100字以内）。"
            )
            try:
                llm_result = self._call_llm([{"role": "user", "content": summary_prompt}])
                llm_summary = llm_result.get("content", f"发现 {len(issues)} 个问题")
            except Exception:
                llm_summary = f"发现 {len(issues)} 个问题（LLM 总结失败）"
        else:
            llm_summary = "未发现问题。"

        duration = time.time() - start_time

        # De-duplicate issue types for counting
        issue_types = set(i["issue_type"] for i in issues)

        return {
            "risks": None,
            "status": "success",
            "command": "check",
            "trace_ids": [Path(t).stem for t in traces],
            "issues_count": len(issues),
            "issue_types_detected": sorted(issue_types),
            "issues": issues,
            "response": llm_summary,
            "request_id": self.request_id,
            "metadata": {
                "llm_model": self.model,
                "backend": "openai" if self._use_openai else "ai_binary",
                "duration": round(duration, 2),
            },
        }

    # ------------------------------------------------------------------
    # Prompt builders
    # ------------------------------------------------------------------

    def _build_multiple_trace_prompt(
        self,
        metadata: List[Dict[str, Any]],
        trace_labels: str,
        question: str,
    ) -> str:
        """Build prompt for multiple traces."""
        n_pass = sum(1 for m in metadata if m["passed"])
        n_fail = sum(1 for m in metadata if not m["passed"])

        prompt = (
            f"这个任务有 {len(metadata)} 次 rollouts：{n_pass} 次通过，{n_fail} 次失败。\n"
            f"Traces: {trace_labels}\n\n"
        )

        if question:
            prompt += question
        else:
            prompt += (
                "重要：如果提供了验证器测试输出，那显示了决定 pass/fail 的真实外部测试结果。\n"
                "Agent 永远看不到这个输出。将验证器的实际失败信息与 agent 的 trace 交叉引用，\n"
                "以找到真正的根本原因 —— agent 可能认为自己成功了，但验证器显示不同。\n\n"
                "识别：\n"
                "1. **失败点**：失败的尝试在哪个具体步骤开始出错？交叉引用验证器输出（如果可用）。\n"
                "2. **根本原因**：失败的基本原因是什么？区分 'agent 认为成功但验证器不同意' vs 'agent 遇到错误'。\n"
                "3. **正确做法**：在失败点应该怎么做？\n"
                "4. **通用机制**：什么结构性机制（非任务特定知识）可以防止这类失败？\n\n"
                "保持简洁（300 字以内）。"
            )

        return prompt

    def _build_single_fail_prompt(
        self,
        metadata: Dict[str, Any],
        trace_labels: str,
    ) -> str:
        """Build prompt for single failed trace."""
        verifier_output = metadata.get("verifier_output", "N/A")

        return (
            f"这个任务有一次 rollout，结果为 **失败**。\n"
            f"Trace: {trace_labels}\n"
            f"验证器输出：{verifier_output}\n\n"
            "分析 trace，识别：\n"
            "1. **失败点**：Agent 在哪个步骤出错？\n"
            "2. **根本原因**：为什么失败？\n"
            "3. **正确做法**：应该怎么做？\n"
            "4. **通用机制**：什么结构性机制可以防止这类失败？\n\n"
            "保持简洁（300 字以内）。"
        )

    def _build_summary_prompt(
        self,
        metadata: Dict[str, Any],
        trace_labels: str,
    ) -> str:
        """Build prompt for successful trace summary."""
        return (
            f"这个任务有一次 rollout，结果为 **成功**。\n"
            f"Trace: {trace_labels}\n\n"
            "分析 trace。\n\n"
            "识别：\n"
            "1. **关键策略**：Agent 的方法是什么，为什么成功？\n"
            "2. **可重用模式**：哪些通用行为模式可以应用到其他任务？\n"
            "3. **脆弱性风险**：有什么看起来脆弱或幸运的吗？\n\n"
            "保持简洁（150 字以内）。"
        )


def main():
    import argparse

    parser = argparse.ArgumentParser(
        description="Agent Debugger CLI - Analyze agent traces"
    )
    subparsers = parser.add_subparsers(dest="command", help="Command")

    # ask subcommand
    ask_parser = subparsers.add_parser("ask", help="Ask a question about traces")
    ask_parser.add_argument(
        "--traces",
        nargs="+",
        required=True,
        help="Trace file paths",
    )
    ask_parser.add_argument(
        "--question",
        help="Question to ask (default: auto-generated)",
    )
    ask_parser.add_argument(
        "--output",
        help="Output JSON file (default: stdout)",
    )
    ask_parser.add_argument(
        "--model",
        default="gpt-4.1",
        help="LLM model to use",
    )
    ask_parser.add_argument(
        "--config",
        default=None,
        help="Config YAML file path",
    )

    # check subcommand
    check_parser = subparsers.add_parser("check", help="Auto-detect issues in traces")
    check_parser.add_argument(
        "--traces",
        nargs="+",
        required=True,
        help="Trace file paths",
    )
    check_parser.add_argument(
        "--output",
        help="Output JSON file (default: stdout)",
    )
    check_parser.add_argument(
        "--format",
        default="json",
        choices=["json", "text"],
        help="Output format",
    )
    check_parser.add_argument(
        "--model",
        default="gpt-4.1",
        help="LLM model to use",
    )
    check_parser.add_argument(
        "--config",
        default=None,
        help="Config YAML file path",
    )

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    # Initialize debugger
    debugger = DebuggerAgent(
        model=args.model,
        config_path=getattr(args, "config", None),
    )

    # Run command
    if args.command == "ask":
        result = debugger.ask(args.traces, args.question or "")
    elif args.command == "check":
        result = debugger.check(args.traces)
    else:
        parser.print_help()
        sys.exit(1)

    # Output result
    output = json.dumps(result, indent=2, ensure_ascii=False)

    if getattr(args, "output", None):
        with open(args.output, "w") as f:
            f.write(output)
        print(f"Result saved to {args.output}", file=sys.stderr)
    else:
        print(output)


if __name__ == "__main__":
    main()