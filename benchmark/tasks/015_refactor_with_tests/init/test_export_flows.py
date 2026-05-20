"""Comprehensive test suite for export_flows.py — targets 80%+ code coverage."""

import json
import os
from unittest.mock import MagicMock, mock_open, patch

import pytest

import export_flows as ef


# ---------------------------------------------------------------------------
# count_whitespace_stats
# ---------------------------------------------------------------------------
class TestCountWhitespaceStats:
    def test_empty_string(self):
        assert ef.count_whitespace_stats("") == {
            "line_returns_count": 0,
            "unnecessary_space_count": 0,
            "total_chars": 0,
        }

    def test_no_whitespace(self):
        assert ef.count_whitespace_stats("abc") == {
            "line_returns_count": 0,
            "unnecessary_space_count": 0,
            "total_chars": 3,
        }

    def test_line_returns(self):
        result = ef.count_whitespace_stats("a\nb\nc\n")
        assert result["line_returns_count"] == 3

    def test_unnecessary_spaces(self):
        # "a  b   c" -> run of 2 (excess 1) + run of 3 (excess 2) = 3
        result = ef.count_whitespace_stats("a  b   c")
        assert result["unnecessary_space_count"] == 3

    def test_single_spaces_not_counted(self):
        result = ef.count_whitespace_stats("a b c")
        assert result["unnecessary_space_count"] == 0

    def test_mixed(self):
        result = ef.count_whitespace_stats("hello  world\n  foo\n")
        assert result["line_returns_count"] == 2
        assert result["unnecessary_space_count"] == 1 + 1  # "  " twice
        assert result["total_chars"] == len("hello  world\n  foo\n")


# ---------------------------------------------------------------------------
# get_canonical_model / get_pricing
# ---------------------------------------------------------------------------
class TestGetCanonicalModel:
    def test_exact_match(self):
        assert ef.get_canonical_model("claude-sonnet-4") == "claude-sonnet-4"

    def test_with_date_suffix(self):
        assert ef.get_canonical_model("claude-opus-4-5-20250101") == "claude-opus-4-5"

    def test_longer_prefix_wins(self):
        # "claude-opus-4-5" should match before "claude-opus-4"
        assert ef.get_canonical_model("claude-opus-4-5") == "claude-opus-4-5"

    def test_unknown_model(self):
        assert ef.get_canonical_model("gpt-4") is None


class TestGetPricing:
    def test_known_model(self):
        p = ef.get_pricing("claude-sonnet-4-6")
        assert p is not None
        assert "input" in p and "output" in p

    def test_with_suffix(self):
        p = ef.get_pricing("claude-haiku-3-5-20250101")
        assert p is not None

    def test_unknown(self):
        assert ef.get_pricing("unknown-model") is None


# ---------------------------------------------------------------------------
# sanitize_path
# ---------------------------------------------------------------------------
class TestSanitizePath:
    def test_simple(self):
        assert ef.sanitize_path("/v1/messages") == "v1_messages"

    def test_strips_query_string(self):
        assert ef.sanitize_path("/api/foo?bar=1") == "api_foo"

    def test_removes_special_chars(self):
        assert ef.sanitize_path("/a/b.c!d") == "a_bcd"

    def test_truncates_long_path(self):
        long = "/a" * 100
        assert len(ef.sanitize_path(long)) <= 80

    def test_empty(self):
        assert ef.sanitize_path("") == ""


# ---------------------------------------------------------------------------
# extract_stop_reason
# ---------------------------------------------------------------------------
class TestExtractStopReason:
    def test_sse_message_delta(self):
        raw = 'data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}\n'
        assert ef.extract_stop_reason(raw) == "end_turn"

    def test_sse_message_delta_with_spaces(self):
        raw = 'data: {"type": "message_delta", "delta": {"stop_reason": "tool_use"}}\n'
        assert ef.extract_stop_reason(raw) == "tool_use"

    def test_non_streaming_fallback(self):
        raw = '{"type": "message", "stop_reason": "max_tokens"}\n'
        assert ef.extract_stop_reason(raw) == "max_tokens"

    def test_unknown_when_no_match(self):
        assert ef.extract_stop_reason("no data here") == "unknown"

    def test_ignores_non_data_lines(self):
        raw = "event: message_delta\nfoo bar\n"
        assert ef.extract_stop_reason(raw) == "unknown"

    def test_invalid_json_in_sse(self):
        raw = "data: {not json\ndata: also not\n"
        assert ef.extract_stop_reason(raw) == "unknown"


# ---------------------------------------------------------------------------
# _resolve_read_file_name
# ---------------------------------------------------------------------------
class TestResolveReadFileName:
    def test_non_read_file(self):
        assert ef._resolve_read_file_name("Write", {}) == "Write"

    def test_read_file_default(self):
        assert ef._resolve_read_file_name("read_file", {}) == "read_file_uncompressed"

    def test_read_file_compress(self):
        assert ef._resolve_read_file_name("read_file", {"mode": "compress"}) == "read_file_compressed"

    def test_read_file_original(self):
        assert ef._resolve_read_file_name("read_file", {"mode": "original"}) == "read_file_uncompressed"

    def test_read_file_non_dict_input(self):
        assert ef._resolve_read_file_name("read_file", "string") == "read_file_uncompressed"


# ---------------------------------------------------------------------------
# _format_tool_params
# ---------------------------------------------------------------------------
class TestFormatToolParams:
    def test_string_values(self):
        result = ef._format_tool_params({"path": "/tmp/foo"})
        assert result == 'path="/tmp/foo"'

    def test_non_string_values(self):
        result = ef._format_tool_params({"count": 5})
        assert result == "count=5"

    def test_mixed(self):
        result = ef._format_tool_params({"file": "a.py", "line": 10})
        assert 'file="a.py"' in result
        assert "line=10" in result

    def test_empty(self):
        assert ef._format_tool_params({}) == ""


# ---------------------------------------------------------------------------
# format_headers
# ---------------------------------------------------------------------------
class TestFormatHeaders:
    def test_normal_headers(self):
        headers = MagicMock()
        headers.fields = [
            (b"content-type", b"application/json"),
            (b"accept", b"*/*"),
        ]
        result = ef.format_headers(headers)
        assert "content-type" in result
        assert "application/json" in result

    def test_redacted_headers(self):
        headers = MagicMock()
        headers.fields = [
            (b"x-api-key", b"secret123"),
            (b"authorization", b"Bearer tok"),
            (b"host", b"example.com"),
        ]
        result = ef.format_headers(headers)
        assert "[REDACTED]" in result
        assert "secret123" not in result
        assert "Bearer tok" not in result
        assert "example.com" in result


# ---------------------------------------------------------------------------
# parse_request_body
# ---------------------------------------------------------------------------
class TestParseRequestBody:
    def test_valid_json(self, tmp_path):
        p = tmp_path / "req.json"
        p.write_text('{"model": "claude-sonnet-4"}')
        result = ef.parse_request_body(str(p))
        assert result == {"model": "claude-sonnet-4"}

    def test_invalid_json(self, tmp_path):
        p = tmp_path / "req.json"
        p.write_text("not json")
        assert ef.parse_request_body(str(p)) is None

    def test_empty_file(self, tmp_path):
        p = tmp_path / "req.json"
        p.write_text("")
        assert ef.parse_request_body(str(p)) is None


# ---------------------------------------------------------------------------
# _get_file_path
# ---------------------------------------------------------------------------
class TestGetFilePath:
    def test_file_path_key(self):
        result = ef._get_file_path({"file_path": "/abs/path.py"})
        assert result == "/abs/path.py"

    def test_path_key(self):
        result = ef._get_file_path({"path": "/abs/other.py"})
        assert result == "/abs/other.py"

    def test_relative_path(self):
        result = ef._get_file_path({"file_path": "relative/path.py"})
        assert os.path.isabs(result)

    def test_no_path(self):
        assert ef._get_file_path({"content": "foo"}) is None

    def test_non_dict(self):
        assert ef._get_file_path("string") is None


# ---------------------------------------------------------------------------
# _agent_color
# ---------------------------------------------------------------------------
class TestAgentColor:
    def test_returns_hex(self):
        c = ef._agent_color("test")
        assert c.startswith("#")
        assert len(c) == 7

    def test_deterministic(self):
        assert ef._agent_color("abc") == ef._agent_color("abc")

    def test_different_names_different_colors(self):
        # Not guaranteed but extremely likely
        assert ef._agent_color("agent1") != ef._agent_color("agent2")


# ---------------------------------------------------------------------------
# _system_prompt_hash
# ---------------------------------------------------------------------------
class TestSystemPromptHash:
    def test_no_system(self):
        assert ef._system_prompt_hash({"messages": []}, "vix") is None

    def test_index_out_of_range(self):
        assert ef._system_prompt_hash({"system": [{"text": "hi"}]}, "cc") is None  # cc index=2

    def test_empty_text(self):
        assert ef._system_prompt_hash({"system": [{"text": ""}]}, "vix") is None

    def test_valid(self):
        body = {"system": [{"text": "hello"}, {"text": "world"}, {"text": "third"}]}
        h = ef._system_prompt_hash(body, "cc")  # index 2
        assert h is not None
        assert len(h) == 12


# ---------------------------------------------------------------------------
# write_request / write_response
# ---------------------------------------------------------------------------
class TestWriteRequest:
    def test_writes_files(self, tmp_path):
        flow = MagicMock()
        flow.request.method = "POST"
        flow.request.pretty_url = "https://api.example.com/v1/messages"
        flow.request.headers.fields = [(b"content-type", b"application/json")]
        flow.request.get_text.return_value = '{"model": "test"}'

        ef.write_request(flow, str(tmp_path))

        headers_file = tmp_path / "request_headers.txt"
        assert headers_file.exists()
        assert "POST" in headers_file.read_text()

        body_file = tmp_path / "request.json"
        assert body_file.exists()
        content = json.loads(body_file.read_text())
        assert content["model"] == "test"

    def test_no_body(self, tmp_path):
        flow = MagicMock()
        flow.request.method = "GET"
        flow.request.pretty_url = "https://api.example.com/"
        flow.request.headers.fields = []
        flow.request.get_text.return_value = ""

        ef.write_request(flow, str(tmp_path))
        assert (tmp_path / "request_headers.txt").exists()
        assert not (tmp_path / "request.json").exists()

    def test_non_json_body(self, tmp_path):
        flow = MagicMock()
        flow.request.method = "POST"
        flow.request.pretty_url = "https://api.example.com/"
        flow.request.headers.fields = []
        flow.request.get_text.return_value = "not json body"

        ef.write_request(flow, str(tmp_path))
        body_file = tmp_path / "request.json"
        assert body_file.exists()
        assert body_file.read_text().strip() == "not json body"


class TestWriteResponse:
    def test_writes_response(self, tmp_path):
        flow = MagicMock()
        flow.response.status_code = 200
        flow.response.reason = "OK"
        flow.response.headers.fields = [(b"content-type", b"text/plain")]
        flow.response.get_text.return_value = "hello"

        ef.write_response(flow, str(tmp_path))
        resp_file = tmp_path / "response_raw.txt"
        assert resp_file.exists()
        text = resp_file.read_text()
        assert "200 OK" in text
        assert "hello" in text

    def test_no_response(self, tmp_path):
        flow = MagicMock()
        flow.response = None
        ef.write_response(flow, str(tmp_path))
        assert not (tmp_path / "response_raw.txt").exists()

    def test_no_body(self, tmp_path):
        flow = MagicMock()
        flow.response.status_code = 204
        flow.response.reason = "No Content"
        flow.response.headers.fields = []
        flow.response.get_text.return_value = ""

        ef.write_response(flow, str(tmp_path))
        text = (tmp_path / "response_raw.txt").read_text()
        assert "204" in text


# ---------------------------------------------------------------------------
# extract_stop_reason edge cases tested above
# ---------------------------------------------------------------------------


# ---------------------------------------------------------------------------
# parse_response_content
# ---------------------------------------------------------------------------
class TestParseResponseContent:
    def test_sse_text_block(self, tmp_path):
        lines = [
            'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}',
            'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}',
            'data: {"type":"content_block_stop","index":0}',
        ]
        p = tmp_path / "response_raw.txt"
        p.write_text("\n".join(lines))
        result = ef.parse_response_content(str(p))
        assert len(result) == 1
        assert result[0]["type"] == "text"
        assert result[0]["text"] == "Hello"

    def test_sse_tool_use_block(self, tmp_path):
        lines = [
            'data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Read"}}',
            'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"file_path\\":\\"/tmp/f\\"}"}}',
            'data: {"type":"content_block_stop","index":0}',
        ]
        p = tmp_path / "response_raw.txt"
        p.write_text("\n".join(lines))
        result = ef.parse_response_content(str(p))
        assert len(result) == 1
        assert result[0]["type"] == "tool_use"
        assert result[0]["name"] == "Read"

    def test_non_streaming_fallback(self, tmp_path):
        msg = {"type": "message", "content": [{"type": "text", "text": "Hi"}]}
        p = tmp_path / "response_raw.txt"
        p.write_text("200 OK\n\n" + json.dumps(msg))
        result = ef.parse_response_content(str(p))
        assert len(result) == 1
        assert result[0]["text"] == "Hi"

    def test_empty_response(self, tmp_path):
        p = tmp_path / "response_raw.txt"
        p.write_text("200 OK\n\nno json here")
        result = ef.parse_response_content(str(p))
        assert result == []

    def test_non_streaming_tool_use(self, tmp_path):
        msg = {"type": "message", "content": [{"type": "tool_use", "name": "Write", "input": {"content": "x"}}]}
        p = tmp_path / "response_raw.txt"
        p.write_text(json.dumps(msg))
        result = ef.parse_response_content(str(p))
        assert len(result) == 1
        assert result[0]["type"] == "tool_use"
        assert result[0]["name"] == "Write"


# ---------------------------------------------------------------------------
# export_parsed_response
# ---------------------------------------------------------------------------
class TestExportParsedResponse:
    def test_text_and_tool_blocks(self, tmp_path):
        lines = [
            'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}',
            'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Thinking..."}}',
            'data: {"type":"content_block_stop","index":0}',
            'data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Read"}}',
            'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"path\\": \\"/x\\"}"}}',
            'data: {"type":"content_block_stop","index":1}',
        ]
        resp = tmp_path / "response_raw.txt"
        resp.write_text("\n".join(lines))
        out = tmp_path / "response_parsed.txt"
        result = ef.export_parsed_response(str(resp), str(out))
        assert result is True
        text = out.read_text()
        assert "Thinking..." in text
        assert "[Read(" in text

    def test_empty_returns_false(self, tmp_path):
        resp = tmp_path / "response_raw.txt"
        resp.write_text("nothing useful")
        out = tmp_path / "parsed.txt"
        assert ef.export_parsed_response(str(resp), str(out)) is False


# ---------------------------------------------------------------------------
# categorize_output_sources
# ---------------------------------------------------------------------------
class TestCategorizeOutputSources:
    def test_sse_text_and_tool(self, tmp_path):
        lines = [
            'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}',
            'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello world"}}',
            'data: {"type":"content_block_stop","index":0}',
            'data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","name":"Write"}}',
            'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"content\\": \\"x\\"}"}}',
            'data: {"type":"content_block_stop","index":1}',
        ]
        p = tmp_path / "resp.txt"
        p.write_text("\n".join(lines))
        result = ef.categorize_output_sources(str(p))
        assert result["llm_text"] == len("Hello world")
        assert "Write" in result["tool_calls"]

    def test_non_streaming(self, tmp_path):
        msg = {"type": "message", "content": [{"type": "text", "text": "abc"}]}
        p = tmp_path / "resp.txt"
        p.write_text(json.dumps(msg))
        result = ef.categorize_output_sources(str(p))
        assert result["llm_text"] == 3

    def test_empty(self, tmp_path):
        p = tmp_path / "resp.txt"
        p.write_text("nothing")
        result = ef.categorize_output_sources(str(p))
        assert result["llm_text"] == 0
        assert result["tool_calls"] == {}


# ---------------------------------------------------------------------------
# extract_read_file_whitespace
# ---------------------------------------------------------------------------
class TestExtractReadFileWhitespace:
    def test_no_read_tools(self):
        body = {"messages": []}
        ws = ef.extract_read_file_whitespace(body)
        assert ws["total_chars"] == 0

    def test_with_read_tool_result(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Read", "input": {}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": "line1\nline2  extra"}
                ]},
            ]
        }
        ws = ef.extract_read_file_whitespace(body)
        assert ws["line_returns_count"] == 1
        assert ws["unnecessary_space_count"] == 1
        assert ws["total_chars"] == len("line1\nline2  extra")

    def test_list_content_in_tool_result(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "read_file", "input": {"mode": "compress"}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": [{"text": "abc  def"}]}
                ]},
            ]
        }
        ws = ef.extract_read_file_whitespace(body)
        assert ws["unnecessary_space_count"] == 1

    def test_skips_non_read_tools(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Write", "input": {}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": "lots  of  spaces"}
                ]},
            ]
        }
        ws = ef.extract_read_file_whitespace(body)
        assert ws["total_chars"] == 0

    def test_non_list_content_in_messages(self):
        body = {
            "messages": [
                {"role": "assistant", "content": "plain string"},
                {"role": "user", "content": "also plain"},
            ]
        }
        ws = ef.extract_read_file_whitespace(body)
        assert ws["total_chars"] == 0


# ---------------------------------------------------------------------------
# categorize_input_sources
# ---------------------------------------------------------------------------
class TestCategorizeInputSources:
    def test_empty_messages(self):
        body = {"messages": []}
        result = ef.categorize_input_sources(body)
        assert result["total_chars"] > 0  # json.dumps of body
        assert result["tool_results"] == {}
        assert result["tool_calls"] == {}

    def test_tool_result_in_last_user_msg_is_cache_write(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Read", "input": {}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": "file contents"}
                ]},
            ]
        }
        result = ef.categorize_input_sources(body)
        assert "Read" in result["tool_results"]
        assert result["tool_results"]["Read"]["cache_write_chars"] > 0

    def test_tool_result_in_earlier_user_msg_is_cache_read(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Read", "input": {}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": "old content"}
                ]},
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t2", "name": "Write", "input": {}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t2", "content": "ok"}
                ]},
            ]
        }
        result = ef.categorize_input_sources(body)
        assert result["tool_results"]["Read"]["cache_read_chars"] > 0
        assert result["tool_results"]["Read"]["cache_write_chars"] == 0

    def test_tool_use_in_second_to_last_is_cache_write(self):
        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "t1", "name": "Read", "input": {"file_path": "/x"}}
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "t1", "content": "data"}
                ]},
            ]
        }
        result = ef.categorize_input_sources(body)
        # The assistant message at index 0 is second_to_last (last_user_idx=1, so stl=0)
        assert "Read" in result["tool_calls"]
        assert result["tool_calls"]["Read"]["cache_write_chars"] > 0


# ---------------------------------------------------------------------------
# attribute_tokens
# ---------------------------------------------------------------------------
class TestAttributeTokens:
    def test_basic_attribution(self):
        input_sources = {
            "total_chars": 1000,
            "tool_results": {"Read": {"cache_write_chars": 500, "cache_read_chars": 200}},
            "tool_calls": {},
        }
        output_sources = {"llm_text": 80, "tool_calls": {"Write": 20}}
        usage = {
            "cost": {
                "input": {"tokens": 100, "dollars": 0.001},
                "cache_write": {"tokens": 200, "dollars": 0.002},
                "cache_read": {"tokens": 50, "dollars": 0.0005},
                "output": {"tokens": 50, "dollars": 0.01},
            }
        }
        pricing = ef.MODEL_PRICING["claude-sonnet-4"]
        result = ef.attribute_tokens(input_sources, output_sources, usage, pricing)
        assert "input" in result
        assert "output" in result
        assert result["output"]["llm_text"]["tokens"] > 0

    def test_zero_chars(self):
        result = ef.attribute_tokens(
            {"total_chars": 0, "tool_results": {}, "tool_calls": {}},
            {"llm_text": 0, "tool_calls": {}},
            {"cost": {}},
            None,
        )
        assert result["input"] == {}
        assert result["output"] == {}

    def test_no_output_tokens(self):
        result = ef.attribute_tokens(
            {"total_chars": 100, "tool_results": {}, "tool_calls": {}},
            {"llm_text": 50, "tool_calls": {}},
            {"cost": {"output": {"tokens": 0, "dollars": 0}}},
            None,
        )
        assert "llm_text" not in result["output"]


# ---------------------------------------------------------------------------
# _aggregate_by_source / _round_by_source
# ---------------------------------------------------------------------------
class TestAggregateBySource:
    def test_aggregates_input_tool_results(self):
        agg = {"input": {}, "output": {}}
        flow = {
            "input": {"tool_results": {"Read": {"tokens": 10, "dollars": 0.001, "chars": 100}}},
            "output": {},
        }
        ef._aggregate_by_source(agg, flow)
        assert agg["input"]["tool_results"]["Read"]["tokens"] == 10

        # Aggregate again
        ef._aggregate_by_source(agg, flow)
        assert agg["input"]["tool_results"]["Read"]["tokens"] == 20

    def test_aggregates_output(self):
        agg = {"input": {}, "output": {}}
        flow = {
            "input": {},
            "output": {
                "llm_text": {"tokens": 5, "dollars": 0.01, "chars": 50},
                "tool_calls": {"Write": {"tokens": 2, "dollars": 0.005, "chars": 20}},
            },
        }
        ef._aggregate_by_source(agg, flow)
        assert agg["output"]["llm_text"]["tokens"] == 5
        assert agg["output"]["tool_calls"]["Write"]["tokens"] == 2


class TestRoundBySource:
    def test_rounds_dollars(self):
        agg = {
            "input": {"tool_results": {"Read": {"tokens": 10, "dollars": 0.0011111111, "chars": 100}}},
            "output": {
                "llm_text": {"tokens": 5, "dollars": 0.0022222222, "chars": 50},
                "tool_calls": {"Write": {"tokens": 2, "dollars": 0.0033333333, "chars": 20}},
            },
        }
        ef._round_by_source(agg)
        assert agg["input"]["tool_results"]["Read"]["dollars"] == round(0.0011111111, 6)
        assert agg["output"]["llm_text"]["dollars"] == round(0.0022222222, 6)


# ---------------------------------------------------------------------------
# extract_usage (filesystem-based)
# ---------------------------------------------------------------------------
class TestExtractUsage:
    def test_sse_usage(self, tmp_path):
        flow_dir = tmp_path / "agent" / "1" / "1"
        flow_dir.mkdir(parents=True)

        # Write SSE response with message_delta usage
        sse = 'data: {"type":"message_delta","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":10,"cache_read_input_tokens":5,"extra_field":1}}\n'
        (flow_dir / "response_raw.txt").write_text("200 OK\n\n" + sse)

        # Write timing
        timing = {"request_start": 1000.0, "response_end": 1002.5}
        (flow_dir / "timing.json").write_text(json.dumps(timing))

        ef.extract_usage(str(tmp_path))

        usage = json.loads((flow_dir / "usage.json").read_text())
        assert usage["input_tokens"] == 100
        assert usage["output_tokens"] == 50
        assert "extra_field" not in usage
        assert usage["timing"]["duration_ms"] == 2500
        assert not (flow_dir / "timing.json").exists()

    def test_non_streaming_usage(self, tmp_path):
        flow_dir = tmp_path / "agent" / "1" / "1"
        flow_dir.mkdir(parents=True)

        msg = {"type": "message", "usage": {"input_tokens": 200, "output_tokens": 100}}
        (flow_dir / "response_raw.txt").write_text("200 OK\n\n" + json.dumps(msg))

        ef.extract_usage(str(tmp_path))

        usage = json.loads((flow_dir / "usage.json").read_text())
        assert usage["input_tokens"] == 200

    def test_no_usage_warns(self, tmp_path, capsys):
        flow_dir = tmp_path / "agent" / "1" / "1"
        flow_dir.mkdir(parents=True)
        (flow_dir / "response_raw.txt").write_text("200 OK\n\nno usage here")

        ef.extract_usage(str(tmp_path))
        assert not (flow_dir / "usage.json").exists()
        assert "Warning" in capsys.readouterr().out

    def test_timing_partial(self, tmp_path):
        flow_dir = tmp_path / "agent" / "1" / "1"
        flow_dir.mkdir(parents=True)

        sse = 'data: {"type":"message_delta","usage":{"input_tokens":10,"output_tokens":5}}\n'
        (flow_dir / "response_raw.txt").write_text(sse)
        (flow_dir / "timing.json").write_text(json.dumps({"request_start": 1000.0}))

        ef.extract_usage(str(tmp_path))
        usage = json.loads((flow_dir / "usage.json").read_text())
        assert "timing" in usage
        assert "duration_ms" not in usage["timing"]


# ---------------------------------------------------------------------------
# extract_prompts
# ---------------------------------------------------------------------------
class TestExtractPrompts:
    def test_extracts_system_and_user(self, tmp_path):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {
            "system": [{"type": "text", "text": "You are helpful."}],
            "messages": [
                {"role": "user", "content": [{"type": "text", "text": "Hello!"}]},
            ],
        }
        (flow_dir / "request.json").write_text(json.dumps(body))

        ef.extract_prompts(str(tmp_path))

        assert (flow_dir / "system_prompt.md").read_text() == "You are helpful."
        assert (flow_dir / "first_user_message.md").read_text() == "Hello!"

    def test_no_system(self, tmp_path):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {"messages": [{"role": "user", "content": [{"type": "text", "text": "Hi"}]}]}
        (flow_dir / "request.json").write_text(json.dumps(body))

        ef.extract_prompts(str(tmp_path))
        assert not (flow_dir / "system_prompt.md").exists()
        assert (flow_dir / "first_user_message.md").read_text() == "Hi"

    def test_invalid_json(self, tmp_path, capsys):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)
        (flow_dir / "request.json").write_text("bad json")

        ef.extract_prompts(str(tmp_path))
        assert "Warning" in capsys.readouterr().out

    def test_string_content_user_message(self, tmp_path):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {"messages": [{"role": "user", "content": "plain string"}]}
        (flow_dir / "request.json").write_text(json.dumps(body))

        ef.extract_prompts(str(tmp_path))
        # String content is not a list, so no first_user_message.md
        assert not (flow_dir / "first_user_message.md").exists()


# ---------------------------------------------------------------------------
# calculate_costs
# ---------------------------------------------------------------------------
class TestCalculateCosts:
    def test_calculates_costs(self, tmp_path):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {"model": "claude-sonnet-4"}
        (flow_dir / "request.json").write_text(json.dumps(body))

        usage = {
            "input_tokens": 1000,
            "output_tokens": 500,
            "cache_creation_input_tokens": 200,
            "cache_read_input_tokens": 100,
        }
        (flow_dir / "usage.json").write_text(json.dumps(usage))

        ef.calculate_costs(str(tmp_path))

        result = json.loads((flow_dir / "usage.json").read_text())
        assert "cost" in result
        assert "total" in result["cost"]
        assert result["cost"]["total"]["dollars"] > 0
        # Top-level token keys removed
        assert "input_tokens" not in result
        assert result["model"] == "claude-sonnet-4"

    def test_unknown_model_warns(self, tmp_path, capsys):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {"model": "unknown-model-xyz"}
        (flow_dir / "request.json").write_text(json.dumps(body))
        (flow_dir / "usage.json").write_text(json.dumps({"input_tokens": 10}))

        ef.calculate_costs(str(tmp_path))
        assert "Warning" in capsys.readouterr().out

    def test_no_model(self, tmp_path):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        body = {"messages": []}
        (flow_dir / "request.json").write_text(json.dumps(body))
        (flow_dir / "usage.json").write_text(json.dumps({"input_tokens": 10}))

        ef.calculate_costs(str(tmp_path))
        # usage.json should be unchanged
        result = json.loads((flow_dir / "usage.json").read_text())
        assert "cost" not in result


# ---------------------------------------------------------------------------
# extract_file_ops
# ---------------------------------------------------------------------------
class TestExtractFileOps:
    def test_reads_and_writes(self, tmp_path):
        # Response with a Write tool_use
        msg = {"type": "message", "content": [
            {"type": "tool_use", "name": "Write", "input": {"file_path": "/tmp/out.py", "content": "hello"}},
        ]}
        resp_path = tmp_path / "response_raw.txt"
        resp_path.write_text(json.dumps(msg))

        body = {
            "messages": [
                {"role": "assistant", "content": [
                    {"type": "tool_use", "id": "r1", "name": "Read", "input": {"file_path": "/tmp/in.py"}},
                ]},
                {"role": "user", "content": [
                    {"type": "tool_result", "tool_use_id": "r1", "content": "file content here"},
                ]},
            ]
        }

        with patch("os.path.getsize", return_value=100):
            result = ef.extract_file_ops(body, str(resp_path))

        assert result["input"]["unique_files_read"] == 1
        assert result["input"]["total_read_chars"] == len("file content here")
        assert result["output"]["unique_files_written"] == 1

    def test_no_ops(self, tmp_path):
        resp_path = tmp_path / "response_raw.txt"
        resp_path.write_text("200 OK\n\nplain text")

        body = {"messages": []}
        result = ef.extract_file_ops(body, str(resp_path))
        assert result["input"]["unique_files_read"] == 0
        assert result["output"]["files_written"] == 0


# ---------------------------------------------------------------------------
# export_parsed_responses
# ---------------------------------------------------------------------------
class TestExportParsedResponses:
    def test_walks_and_exports(self, tmp_path, capsys):
        flow_dir = tmp_path / "a" / "1" / "1"
        flow_dir.mkdir(parents=True)

        lines = [
            'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}',
            'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}',
            'data: {"type":"content_block_stop","index":0}',
        ]
        (flow_dir / "response_raw.txt").write_text("\n".join(lines))

        ef.export_parsed_responses(str(tmp_path))
        assert (flow_dir / "response_parsed.txt").exists()


# ---------------------------------------------------------------------------
# redact_flow_files (mock mitmproxy)
# ---------------------------------------------------------------------------
class TestRedactFlowFiles:
    @patch("export_flows.glob.glob")
    def test_no_flow_files(self, mock_glob):
        mock_glob.return_value = []
        ef.redact_flow_files("/some/dir")  # Should not raise

    @patch("export_flows.FlowWriter")
    @patch("export_flows.FlowReader")
    @patch("export_flows.glob.glob")
    def test_redacts_api_key(self, mock_glob, mock_reader_cls, mock_writer_cls, tmp_path):
        from mitmproxy.http import HTTPFlow

        flow_path = str(tmp_path / "test.flow")
        mock_glob.return_value = [flow_path]

        # Create a mock that passes isinstance(flow, HTTPFlow)
        mock_flow = MagicMock(spec=HTTPFlow)
        # spec restricts attrs, so set them explicitly
        mock_flow.request = MagicMock()
        mock_flow.request.headers = MagicMock()
        mock_flow.request.headers.get = MagicMock(
            side_effect=lambda h: "secret" if h == b"x-api-key" else None
        )

        mock_reader = MagicMock()
        mock_reader.stream.return_value = [mock_flow]
        mock_reader_cls.return_value = mock_reader

        # Create the file so open() works
        with open(flow_path, "wb") as f:
            f.write(b"dummy")

        ef.redact_flow_files(str(tmp_path))

        # Should have written back
        mock_writer_cls.assert_called_once()


# ---------------------------------------------------------------------------
# export_flows (mock mitmproxy)
# ---------------------------------------------------------------------------
class TestExportFlows:
    @patch("export_flows.glob.glob")
    def test_no_flow_files(self, mock_glob, capsys):
        mock_glob.return_value = []
        ef.export_flows("/in", "/out")
        assert "No *.flow files" in capsys.readouterr().out

    @patch("export_flows.FlowReader")
    @patch("export_flows.glob.glob")
    def test_exports_flows(self, mock_glob, mock_reader_cls, tmp_path, capsys):
        from mitmproxy.http import HTTPFlow

        flow_path = str(tmp_path / "vix.flow")
        mock_glob.return_value = [flow_path]

        # Create mock flow that passes isinstance check
        mock_flow = MagicMock(spec=HTTPFlow)
        mock_flow.request = MagicMock()
        mock_flow.response = MagicMock()
        mock_flow.request.pretty_url = "https://api.anthropic.com/v1/messages"
        mock_flow.request.method = "POST"
        mock_flow.request.headers.fields = []
        mock_flow.request.get_text.return_value = '{"model": "claude-sonnet-4", "system": [{"text": "hi"}], "messages": [{"role": "user", "content": "hello"}]}'
        mock_flow.request.timestamp_start = 1000.0
        mock_flow.response.status_code = 200
        mock_flow.response.reason = "OK"
        mock_flow.response.headers.fields = []
        mock_flow.response.get_text.return_value = 'data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}\n'
        mock_flow.response.timestamp_end = 1002.0

        mock_reader = MagicMock()
        mock_reader.stream.return_value = [mock_flow]
        mock_reader_cls.return_value = mock_reader

        # Need to create a real file for open()
        with open(flow_path, "wb") as f:
            f.write(b"dummy")

        out_dir = str(tmp_path / "out")
        os.makedirs(out_dir)

        ef.export_flows(str(tmp_path), out_dir)

        output = capsys.readouterr().out
        assert "Exported" in output

    @patch("export_flows.FlowReader")
    @patch("export_flows.glob.glob")
    def test_skips_count_token_and_quota(self, mock_glob, mock_reader_cls, tmp_path, capsys):
        from mitmproxy.http import HTTPFlow

        flow_path = str(tmp_path / "vix.flow")
        mock_glob.return_value = [flow_path]

        # count_token flow
        flow1 = MagicMock(spec=HTTPFlow)
        flow1.request = MagicMock()
        flow1.request.pretty_url = "https://api.anthropic.com/v1/count_tokens"
        flow1.request.get_text.return_value = ""

        # quota flow
        flow2 = MagicMock(spec=HTTPFlow)
        flow2.request = MagicMock()
        flow2.request.pretty_url = "https://api.anthropic.com/v1/messages"
        flow2.request.method = "POST"
        flow2.request.get_text.return_value = json.dumps({"messages": [{"role": "user", "content": "quota"}]})

        mock_reader = MagicMock()
        mock_reader.stream.return_value = [flow1, flow2]
        mock_reader_cls.return_value = mock_reader

        with open(flow_path, "wb") as f:
            f.write(b"dummy")

        out_dir = str(tmp_path / "out")
        os.makedirs(out_dir)

        ef.export_flows(str(tmp_path), out_dir)
        output = capsys.readouterr().out
        assert "Exported 0 flows" in output


# ---------------------------------------------------------------------------
# summarize_usage
# ---------------------------------------------------------------------------
class TestSummarizeUsage:
    def test_summarizes(self, tmp_path):
        agent_dir = tmp_path / "vix"
        step_dir = agent_dir / "1" / "1"
        step_dir.mkdir(parents=True)

        usage = {
            "model": "claude-sonnet-4",
            "cost": {
                "input": {"tokens": 100, "dollars": 0.001},
                "cache_write": {"tokens": 50, "dollars": 0.0005},
                "cache_read": {"tokens": 20, "dollars": 0.0001},
                "output": {"tokens": 30, "dollars": 0.003},
                "total": {"tokens": 200, "dollars": 0.0046},
            },
            "timing": {"request_start": 1000.0, "response_end": 1002.0, "duration_ms": 2000},
        }
        (step_dir / "usage.json").write_text(json.dumps(usage))

        ef.summarize_usage(str(tmp_path))

        summary = json.loads((agent_dir / "usage.json").read_text())
        assert summary["title"] == "vix"
        assert summary["total"]["request_count"] == 1
        assert summary["color"] == "#7B2FBE"

    def test_skips_non_agent_dirs(self, tmp_path):
        # Create a file, not a dir
        (tmp_path / "readme.txt").write_text("hi")
        # Create dir without numbered subdirs
        (tmp_path / "other").mkdir()
        (tmp_path / "other" / "notes.txt").write_text("hi")

        ef.summarize_usage(str(tmp_path))
        # No crash, no summary files
        assert not (tmp_path / "other" / "usage.json").exists()
